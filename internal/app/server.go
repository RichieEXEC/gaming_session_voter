// Package app drží HTTP vrstvu: routy, šablony a handlery.
package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
	"github.com/RichieEXEC/gaming_session_voter/web"
)

const (
	maxOptions   = 30
	maxTitleLen  = 120
	maxNoteLen   = 500
	maxNickLen   = 18
	maxBodyBytes = 64 << 10

	// dayLayout je tvar data, jak ho posílá <input type="date">.
	dayLayout = "2006-01-02"
)

type Server struct {
	st     *store.Store
	log    *slog.Logger
	mux    *http.ServeMux
	pages  map[string]*template.Template
	static http.Handler

	// assetVer drží otisk obsahu každého statického souboru. Odkazy na ně
	// pak nesou ?v=<otisk>, takže po nasazení nové verze prohlížeč sáhne
	// na jinou adresu a nemůže si držet starý app.js.
	assetVer map[string]string
}

func New(st *store.Store, log *slog.Logger) (*Server, error) {
	s := &Server{st: st, log: log, mux: http.NewServeMux()}

	if err := s.hashAssets(); err != nil {
		return nil, err
	}
	if err := s.parsePages(); err != nil {
		return nil, err
	}
	sub, err := fs.Sub(web.Files, "static")
	if err != nil {
		return nil, err
	}
	s.static = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))

	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok"))
	})
	s.mux.Handle("GET /static/", s.cacheStatic(s.static))

	s.mux.HandleFunc("GET /{$}", s.handleNew)
	s.mux.HandleFunc("POST /polls", s.handleCreate)
	s.mux.HandleFunc("GET /p/{slug}", s.handlePoll)
	s.mux.HandleFunc("POST /p/{slug}/votes", s.handleVote)
	s.mux.HandleFunc("GET /lang/{code}", s.handleLang)
}

// hashAssets spočítá otisk každého souboru ve web/static. Obsah je
// zapečený v binárce, takže se otisk mění jen s novým buildem.
func (s *Server) hashAssets() error {
	s.assetVer = map[string]string{}
	entries, err := fs.ReadDir(web.Files, "static")
	if err != nil {
		return fmt.Errorf("read static dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(web.Files, "static/"+e.Name())
		if err != nil {
			return fmt.Errorf("read static file %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(b)
		s.assetVer[e.Name()] = hex.EncodeToString(sum[:])[:10]
	}
	return nil
}

func (s *Server) cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			// Adresa je vázaná na obsah, takže se držet dá klidně napořád.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Bez otisku se držet nesmí: přesně tím se stal starý app.js
			// na hodinu nesmrtelným, zatímco HTML už bylo nové.
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, r)
	})
}

// parsePages staví jednu sadu šablon na stránku, aby si stránky
// navzájem nepřepsaly blok "content".
func (s *Server) parsePages() error {
	s.pages = map[string]*template.Template{}
	for _, name := range []string{"new", "poll", "error"} {
		t, err := template.New("layout.html").Funcs(s.funcMap()).ParseFS(
			web.Files, "templates/layout.html", "templates/"+name+".html",
		)
		if err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return nil
}

// glyphs jsou stejné značky jako v prototypu. Jsou to konstanty, ne
// uživatelský vstup, takže template.HTML je tu bezpečné.
var glyphs = map[string]template.HTML{
	"yes":   `<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 6.4 4.6 9 10 3"/></svg>`,
	"maybe": `<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M1.5 7.2c1-2.2 2.2-2.2 3.2 0s2.2 2.2 3.2 0"/></svg>`,
	"no":    `<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3.5 6h5"/></svg>`,
}

func (s *Server) funcMap() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"glyph": func(v string) template.HTML {
			if g, ok := glyphs[v]; ok {
				return g
			}
			return glyphs["no"]
		},
		// asset vrátí adresu s otiskem obsahu. Vždycky přes tohle, jinak
		// se na nasazení nové verze zapomene v cache prohlížeče.
		"asset": func(name string) string {
			if v, ok := s.assetVer[name]; ok {
				return "/static/" + name + "?v=" + v
			}
			return "/static/" + name
		},
	}
}

type pageData struct {
	L       i18n.Printer
	Title   string
	Flash   string
	Board   Board
	Poll    *store.Poll
	Slug    string
	Mine    *store.Vote
	Editing bool
	Form    createForm
	Status  int
	Lead    string

	// DefaultDay je dnešek, jak ho vidí server. Prohlížeč podle něj
	// pozná, že do pole s datem nikdo nesáhl, a smí ho přepsat na svoje
	// dnes. Prázdné, když se formulář překresluje po chybě: tam už je
	// uvnitř to, co člověk zadal.
	DefaultDay string

	// Path je adresa, na kterou se má člověk vrátit po přepnutí jazyka.
	// Doplní ji render(). Handler ji nastaví sám jen tam, kde se stránka
	// kreslí jako odpověď na POST a r.URL.Path by ukazoval na endpoint,
	// který se nedá otevřít GETem.
	Path string
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, code int, data pageData) {
	t, ok := s.pages[page]
	if !ok {
		s.log.Error("unknown page", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if data.Path == "" {
		data.Path = r.URL.Path
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := t.Execute(w, data); err != nil {
		// Hlavička už je odeslaná, tak jen zaloguj.
		s.log.Error("render", "page", page, "err", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, titleKey, leadKey string) {
	pr := i18n.NewPrinter(i18n.FromRequest(r))
	s.render(w, r, "error", code, pageData{
		L:     pr,
		Title: pr.T(titleKey),
		Lead:  pr.T(leadKey),
	})
}

// --- cookie s vlastnictvím hlasu ---
//
// Hodnota je "<voteID>.<hmac>". Bez serverové session: jediné, co to
// tvrdí, je "tenhle prohlížeč založil tenhle hlas", což na úpravu
// vlastního hlasu stačí.

func (s *Server) voteCookieName(slug string) string { return "kh_vote_" + slug }

func (s *Server) signVote(voteID int64) string {
	raw := strconv.FormatInt(voteID, 10)
	mac := hmac.New(sha256.New, s.st.Secret())
	mac.Write([]byte(raw))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return raw + "." + sig
}

func (s *Server) verifyVote(value string) (int64, bool) {
	raw, sig, ok := strings.Cut(value, ".")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, s.st.Secret())
	mac.Write([]byte(raw))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return 0, false
	}
	return id, true
}

func (s *Server) setVoteCookie(w http.ResponseWriter, r *http.Request, slug string, voteID int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.voteCookieName(slug),
		Value:    s.signVote(voteID),
		Path:     "/p/" + slug,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   180 * 24 * 3600,
	})
}

// isHTTPS bere v potaz, že Coolify běží za reverzní proxy a TLS
// končí u ní, takže r.TLS je nil i na https adrese.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sameOrigin je lehká ochrana proti POSTu z cizí stránky. Formuláře
// tu nejsou za přihlášením, takže nic silnějšího nedává smysl.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Starší prohlížeče Origin u formulářů neposílají.
		return true
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return strings.HasSuffix(origin, "://"+host)
}

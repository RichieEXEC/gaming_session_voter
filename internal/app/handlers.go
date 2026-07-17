package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/igdb"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

// createForm drží rozepsané sezení, aby po chybě nezmizelo, co člověk napsal.
type createForm struct {
	Title string
	Note  string
	Dates []formDate
}

type formDate struct {
	Day   string
	Start string
	End   string
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	pr := i18n.NewPrinter(i18n.FromRequest(r))

	// Dnešek podle času serveru, tzn. podle proměnné TZ. Prohlížeč si to pak
	// srovná na svoje dnes, dokud do pole nikdo nesáhl.
	today := time.Now().Format(dayLayout)

	s.render(w, r, "new", http.StatusOK, pageData{
		L:          pr,
		Title:      pr.T("create.heading"),
		DefaultDay: today,
		Form:       createForm{Dates: []formDate{{Day: today, Start: "19:00", End: "22:00"}}},
	})
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	pr := i18n.NewPrinter(i18n.FromRequest(r))
	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "err.badRequest", "err.serverLead")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}

	form := createForm{
		Title: strings.TrimSpace(r.FormValue("title")),
		Note:  strings.TrimSpace(r.FormValue("note")),
	}
	days := r.Form["day"]
	starts := r.Form["start"]
	ends := r.Form["end"]
	for i := range days {
		fd := formDate{Day: strings.TrimSpace(days[i])}
		if i < len(starts) {
			fd.Start = strings.TrimSpace(starts[i])
		}
		if i < len(ends) {
			fd.End = strings.TrimSpace(ends[i])
		}
		form.Dates = append(form.Dates, fd)
	}
	if len(form.Dates) == 0 {
		form.Dates = []formDate{{Start: "19:00", End: "22:00"}}
	}

	reshow := func(msg string) {
		s.render(w, r, "new", http.StatusUnprocessableEntity, pageData{
			L: pr, Title: pr.T("create.heading"), Form: form, Flash: msg, Path: "/",
		})
	}

	if form.Title == "" {
		reshow(pr.T("err.titleRequired"))
		return
	}
	if utf8.RuneCountInString(form.Title) > maxTitleLen {
		reshow(pr.T("err.titleTooLong"))
		return
	}
	if utf8.RuneCountInString(form.Note) > maxNoteLen {
		form.Note = string([]rune(form.Note)[:maxNoteLen])
	}

	var dates []store.NewDate
	for _, fd := range form.Dates {
		if fd.Day == "" {
			continue // prázdné řádky se zahodí
		}
		if _, err := time.Parse(dayLayout, fd.Day); err != nil {
			reshow(pr.T("err.dateInvalid"))
			return
		}
		if !validTime(fd.Start) || !validTime(fd.End) {
			reshow(pr.T("err.dateInvalid"))
			return
		}
		dates = append(dates, store.NewDate{Day: fd.Day, StartAt: fd.Start, EndAt: fd.End})
	}
	if len(dates) == 0 {
		reshow(pr.T("err.dateRequired"))
		return
	}
	if len(dates) > maxDates {
		reshow(pr.T("err.tooManyDates", maxDates))
		return
	}

	slug, err := s.st.CreateSession(form.Title, form.Note, dates)
	if err != nil {
		s.log.Error("create session", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}
	http.Redirect(w, r, "/p/"+slug, http.StatusSeeOther)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	pr := i18n.NewPrinter(i18n.FromRequest(r))
	slug := r.PathValue("slug")

	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	// Sloupec "ty" je vidět vždycky. Kdo tu ještě nehlasoval, dostane prázdný
	// koncept, aby mohl klikat rovnou a přezdívku vyplnil až při odeslání.
	mine := s.currentVote(r, sess)
	editing := mine != nil
	if mine == nil {
		mine = blankDraft(sess)
	}

	s.render(w, r, "session", http.StatusOK, pageData{
		L:        pr,
		Title:    sess.Title,
		Session:  sess,
		Slug:     slug,
		View:     buildSessionView(sess, pr, mine),
		Mine:     mine,
		Editing:  editing,
		SearchOn: s.games.Enabled(),
		Flash:    flashText(pr, r.URL.Query().Get("flash")),
	})
}

func blankDraft(sess *store.Session) *store.Vote {
	v := &store.Vote{Choices: map[int64]string{}}
	for id := range sess.AllOptionIDs() {
		v.Choices[id] = "no"
	}
	return v
}

// currentVote vrátí hlas patřící tomuhle prohlížeči, pokud nějaký má.
func (s *Server) currentVote(r *http.Request, sess *store.Session) *store.Vote {
	c, err := r.Cookie(s.voteCookieName(sess.Slug))
	if err != nil {
		return nil
	}
	id, ok := s.verifyVote(c.Value)
	if !ok {
		return nil
	}
	v, err := s.st.VoteByID(sess.ID, id)
	if err != nil {
		return nil
	}
	return v
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	pr := i18n.NewPrinter(i18n.FromRequest(r))
	slug := r.PathValue("slug")

	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "err.badRequest", "err.serverLead")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}

	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	// Bere se jen to, co v sezení opravdu existuje, aby cizí option_id
	// z podvrženého formuláře neprošlo. Platí pro termíny i hry.
	choices := map[int64]string{}
	for id := range sess.AllOptionIDs() {
		switch r.FormValue("choice_" + strconv.FormatInt(id, 10)) {
		case "yes":
			choices[id] = "yes"
		case "maybe":
			choices[id] = "maybe"
		default:
			choices[id] = "no"
		}
	}

	existing := s.currentVote(r, sess)
	if existing != nil {
		if err := s.st.UpdateVote(sess.ID, existing.ID, choices); err != nil {
			s.log.Error("update vote", "err", err, "slug", slug)
			s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
			return
		}
		s.redirectFlash(w, r, slug, "saved", "")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.rerenderSession(w, r, pr, sess, choices, pr.T("err.nickRequired"))
		return
	}
	if utf8.RuneCountInString(name) > maxNickLen {
		s.rerenderSession(w, r, pr, sess, choices, pr.T("err.nickTooLong"))
		return
	}

	voteID, err := s.st.SaveVote(sess.ID, name, choices)
	if errors.Is(err, store.ErrDuplicateName) {
		s.rerenderSession(w, r, pr, sess, choices, pr.T("err.nickTaken"))
		return
	}
	if err != nil {
		s.log.Error("save vote", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	s.setVoteCookie(w, r, slug, voteID)
	s.redirectFlash(w, r, slug, "saved", "")
}

// handleClaim znovu naváže prohlížeč na už existující hlas podle přezdívky.
// Používá se, když někdo přijde o cookie (vymazal je, jiný prohlížeč). Bez
// účtů je přezdívka jediná identita, a hlasy jsou v sezení stejně veřejné,
// takže tohle sedí do modelu důvěry party. Hlas se nepřepisuje: jen se
// nastaví cookie a stránka se ukáže v režimu úprav s uloženými hlasy.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "err.badRequest", "err.serverLead")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}

	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	for _, v := range sess.Votes {
		if strings.EqualFold(v.Name, name) {
			s.setVoteCookie(w, r, slug, v.ID)
			s.redirectFlash(w, r, slug, "reclaimed", "")
			return
		}
	}
	s.redirectFlash(w, r, slug, "novote", "")
}

// rerenderSession ukáže sezení znovu i s tím, co člověk naklikal, aby se po
// chybě nemusel proklikávat znovu.
func (s *Server) rerenderSession(
	w http.ResponseWriter, r *http.Request, pr i18n.Printer,
	sess *store.Session, choices map[int64]string, msg string,
) {
	draft := &store.Vote{Name: strings.TrimSpace(r.FormValue("name")), Choices: choices}
	s.render(w, r, "session", http.StatusUnprocessableEntity, pageData{
		L:        pr,
		Title:    sess.Title,
		Session:  sess,
		Slug:     sess.Slug,
		Path:     "/p/" + sess.Slug,
		View:     buildSessionView(sess, pr, draft),
		Mine:     draft,
		Editing:  false,
		SearchOn: s.games.Enabled(),
		Flash:    msg,
	})
}

// --- hry ---

func (s *Server) handleGameSearch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, searchResp{Error: "not found"})
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		writeJSON(w, http.StatusInternalServerError, searchResp{Error: "server"})
		return
	}

	pr := i18n.NewPrinter(i18n.FromRequest(r))
	if !s.games.Enabled() {
		writeJSON(w, http.StatusOK, searchResp{Disabled: true})
		return
	}

	found, err := s.games.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil && !errors.Is(err, igdb.ErrDisabled) {
		s.log.Error("igdb search", "err", err)
		writeJSON(w, http.StatusBadGateway, searchResp{Error: pr.T("find.error")})
		return
	}

	inSession := map[int64]bool{}
	for _, g := range sess.Games {
		inSession[g.IGDBID] = true
	}

	resp := searchResp{Games: make([]searchGame, 0, len(found))}
	for _, g := range found {
		resp.Games = append(resp.Games, searchGame{
			IGDBID:  g.IGDBID,
			Name:    g.Name,
			Year:    g.Year,
			Genre:   g.Genre,
			Max:     g.MaxPlayers,
			Cover:   g.Cover,
			Slug:    g.Slug,
			IgdbURL: igdbGameURL(g.Slug),
			Meta:    gameMeta(pr, store.GameOption{Year: g.Year, Genre: g.Genre, MaxPlayers: g.MaxPlayers}),
			In:      inSession[g.IGDBID],
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAddGame(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "err.badRequest", "err.serverLead")
		return
	}
	if !s.games.Enabled() {
		s.redirectFlash(w, r, slug, "searchoff", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}

	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}
	if len(sess.Games) >= maxGames {
		s.redirectFlash(w, r, slug, "toomanygames", "co")
		return
	}

	g, ok := parseGameForm(r)
	if !ok {
		s.redirectFlash(w, r, slug, "gameinvalid", "co")
		return
	}

	_, err = s.st.AddGame(sess.ID, g)
	if errors.Is(err, store.ErrDuplicateGame) {
		s.redirectFlash(w, r, slug, "gameexists", "co")
		return
	}
	if err != nil {
		s.log.Error("add game", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}
	s.redirectFlash(w, r, slug, "gameadded", "co")
}

func (s *Server) handleRemoveGame(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "err.badRequest", "err.serverLead")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}

	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	if err := s.st.RemoveGame(sess.ID, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("remove game", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}
	s.redirectFlash(w, r, slug, "gameremoved", "co")
}

// handleSetMax ručně nastaví počet hráčů u hry, kterou IGDB nezná. Prázdné
// nebo neplatné číslo znamená "zpět na neznámé" (0).
func (s *Server) handleSetMax(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !sameOrigin(r) {
		s.fail(w, r, http.StatusForbidden, "err.badRequest", "err.serverLead")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.badRequest", "err.serverLead")
		return
	}

	sess, err := s.st.GetSession(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get session", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	max := 0 // mimo rozsah = neznámé
	if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("max"))); err == nil && n >= 1 && n <= 1000 {
		max = n
	}

	if err := s.st.UpdateGameMax(sess.ID, id, max); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("set game max", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}
	s.redirectFlash(w, r, slug, "maxset", "co")
}

// parseGameForm vytáhne a ořeže snímek hry z formuláře. Metadata posílá
// prohlížeč (z našeho hledání), tak je bereme s rezervou: ořízneme délky
// a rozsahy. V nejhorším bude u hry křivý rok, ne díra do aplikace.
func parseGameForm(r *http.Request) (store.NewGame, bool) {
	igdbID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("igdb_id")), 10, 64)
	if err != nil || igdbID <= 0 {
		return store.NewGame{}, false
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return store.NewGame{}, false
	}
	if utf8.RuneCountInString(name) > maxGameName {
		name = string([]rune(name)[:maxGameName])
	}
	g := store.NewGame{
		IGDBID: igdbID,
		Name:   name,
		Genre:  clampRunes(strings.TrimSpace(r.FormValue("genre")), 40),
		Cover:  cleanCover(r.FormValue("cover")),
		Slug:   cleanSlug(r.FormValue("slug")),
	}
	if y, err := strconv.Atoi(r.FormValue("year")); err == nil && y > 1950 && y < 2100 {
		g.Year = y
	}
	if m, err := strconv.Atoi(r.FormValue("max")); err == nil && m > 0 && m <= 1000 {
		g.MaxPlayers = m
	}
	return g, true
}

// cleanSlug pustí jen platný IGDB slug, jinak prázdno.
func cleanSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if validSlug(s) {
		return s
	}
	return ""
}

// cleanCover pustí jen tvar IGDB image_id (písmena, číslice, podtržítko).
func cleanCover(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return ""
		}
	}
	return s
}

func clampRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// --- JSON ---

type searchResp struct {
	Games    []searchGame `json:"games"`
	Disabled bool         `json:"disabled,omitempty"`
	Error    string       `json:"error,omitempty"`
}

type searchGame struct {
	IGDBID  int64  `json:"igdbId"`
	Name    string `json:"name"`
	Year    int    `json:"year"`
	Genre   string `json:"genre"`
	Max     int    `json:"max"`
	Cover   string `json:"cover"`
	Slug    string `json:"slug"`
	IgdbURL string `json:"igdbUrl"`
	Meta    string `json:"meta"`
	In      bool   `json:"in"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// --- flash / jazyk ---

// redirectFlash dělá POST/Redirect/GET. V URL je jen kód hlášky, ne text,
// jinak by šlo poslat odkaz, který v aplikaci vypíše cokoliv. anchor je
// nepovinná kotva (třeba "co" ke skoku na desku s hrami).
func (s *Server) redirectFlash(w http.ResponseWriter, r *http.Request, slug, code, anchor string) {
	u := "/p/" + slug
	if code != "" {
		u += "?flash=" + url.QueryEscape(code)
	}
	if anchor != "" {
		u += "#" + anchor
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

// flashText překládá jen kódy, které sama aplikace posílá.
func flashText(pr i18n.Printer, code string) string {
	switch code {
	case "saved":
		return pr.T("ok.saved")
	case "gameadded":
		return pr.T("ok.gameAdded")
	case "gameremoved":
		return pr.T("ok.gameRemoved")
	case "maxset":
		return pr.T("ok.maxSet")
	case "reclaimed":
		return pr.T("ok.reclaimed")
	case "novote":
		return pr.T("err.noVote")
	case "gameexists":
		return pr.T("err.gameExists")
	case "gameinvalid":
		return pr.T("err.gameInvalid")
	case "toomanygames":
		return pr.T("err.tooManyGames", maxGames)
	case "searchoff":
		return pr.T("err.searchOff")
	default:
		return ""
	}
}

func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	lang, ok := i18n.Parse(code)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     i18n.CookieName,
		Value:    string(lang),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   365 * 24 * 3600,
	})
	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

// safeNext propustí jen adresu na tomhle serveru. Cokoliv, co by mohlo
// odvést člověka jinam, spadne na domovskou stránku.
func safeNext(raw string) string {
	if raw == "" {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" {
		return "/"
	}
	p := u.Path
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\") {
		return "/"
	}
	return p
}

func validTime(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}

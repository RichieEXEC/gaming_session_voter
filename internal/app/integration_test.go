package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RichieEXEC/gaming_session_voter/internal/igdb"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

// igdbStub je falešné IGDB pro celý průchod: token + jeden nález na "hell".
func igdbStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok","expires_in":5000000}`)
	})
	mux.HandleFunc("/v4/games", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":17,"name":"Helldivers 2","slug":"helldivers-2","first_release_date":1707350400,
			"genres":[{"name":"Shooter"}],"multiplayer_modes":[{"onlinecoopmax":4}],
			"cover":{"image_id":"co6"}}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestServer(t *testing.T, enableIGDB bool) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "it.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var games *igdb.Client
	if enableIGDB {
		stub := igdbStub(t)
		games = igdb.New("cid", "secret", stub.URL+"/oauth2/token", stub.URL+"/v4")
	} else {
		games = igdb.New("", "", "", "")
	}

	srv, err := New(st, games, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func client(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func post(t *testing.T, c *http.Client, base, path string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", base+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

var reChoice = regexp.MustCompile(`name="choice_(\d+)"`)

func TestFullSessionFlow(t *testing.T) {
	ts := newTestServer(t, true)
	c := client(t)

	// 1. založení sezení
	resp := post(t, c, ts.URL, "/sessions", url.Values{
		"title": {"Herní večer"}, "day": {"2026-07-23"}, "start": {"19:00"}, "end": {"22:00"},
	})
	page := body(t, resp)
	slug := strings.TrimPrefix(resp.Request.URL.Path, "/p/")
	if slug == "" || slug == "/sessions" {
		t.Fatalf("po založení nečekaná adresa: %s", resp.Request.URL.Path)
	}
	if !strings.Contains(page, "Herní večer") {
		t.Fatal("stránka sezení nemá titulek")
	}

	// 2. hledání hry přes IGDB stub
	sresp, err := c.Get(ts.URL + "/p/" + slug + "/games/search?q=hell")
	if err != nil {
		t.Fatal(err)
	}
	sbody := body(t, sresp)
	if !strings.Contains(sbody, "Helldivers 2") || !strings.Contains(sbody, `"igdbId":17`) {
		t.Fatalf("hledání nevrátilo hru: %s", sbody)
	}

	// 3. přidání hry
	post(t, c, ts.URL, "/p/"+slug+"/games", url.Values{
		"igdb_id": {"17"}, "name": {"Helldivers 2"}, "year": {"2024"},
		"genre": {"Shooter"}, "max": {"4"}, "cover": {"co6"}, "slug": {"helldivers-2"},
	}).Body.Close()

	page = body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if !strings.Contains(page, "Helldivers 2") {
		t.Fatal("přidaná hra není na stránce")
	}
	if !strings.Contains(page, `href="https://www.igdb.com/games/helldivers-2"`) {
		t.Fatal("odkaz na IGDB stránku hry chybí")
	}

	// duplicitní přidání téže hry musí selhat (flash), ne přidat druhou
	post(t, c, ts.URL, "/p/"+slug+"/games", url.Values{
		"igdb_id": {"17"}, "name": {"Helldivers 2"},
	}).Body.Close()
	page = body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if strings.Count(page, `data-prefix="Helldivers 2"`) != 1 {
		t.Fatalf("hra je v tabulce vícekrát nebo vůbec")
	}

	// 4. hlas přes obě desky
	ids := reChoice.FindAllStringSubmatch(page, -1)
	if len(ids) != 2 {
		t.Fatalf("čekám 2 možnosti (termín + hra), mám %d", len(ids))
	}
	form := url.Values{"name": {"Vojta"}}
	for _, m := range ids {
		form.Set("choice_"+m[1], "yes")
	}
	post(t, c, ts.URL, "/p/"+slug+"/votes", form).Body.Close()

	page = body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if !strings.Contains(page, "1/1") {
		t.Fatal("hlas se nezapočítal do tabulky")
	}
	if !strings.Contains(page, "Vede termín") || !strings.Contains(page, "Vede hra") {
		t.Fatal("shrnutí nevede termín ani hru po hlasu")
	}
	// html/template umí zticha přepsat nebezpečně vypadající CSS/URL na
	// ZgotmplZ. Přesně tím zmizely sloupce mřížky. Ať to hlídá test.
	if strings.Contains(page, "ZgotmplZ") {
		t.Fatal("šablona něco sanitizovala na ZgotmplZ (nejspíš inline style mřížky)")
	}

	// Hra teď má hlas, takže mazání musí být označené k potvrzení.
	if !strings.Contains(page, `data-game="Helldivers 2" data-confirm="1"`) {
		t.Fatal("smazání hry s hlasem není označené data-confirm")
	}

	idGame := ids[1][1]

	// 5. ruční nastavení počtu hráčů
	post(t, c, ts.URL, "/p/"+slug+"/games/"+idGame+"/max", url.Values{"max": {"7"}}).Body.Close()
	page = body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if !strings.Contains(page, "až 7 hráčů") {
		t.Fatal("ruční počet hráčů se neuložil do metadat")
	}

	// 6. odebrání hry
	post(t, c, ts.URL, "/p/"+slug+"/games/"+idGame+"/delete", url.Values{}).Body.Close()
	page = body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if strings.Contains(page, "Helldivers 2") {
		t.Fatal("hra se neodebrala")
	}
}

// Kdo přijde o cookie, si má umět znovu navázat svůj hlas přezdívkou a
// upravit ho, aniž by se založil druhý.
func TestReclaimVoteByNickname(t *testing.T) {
	ts := newTestServer(t, false)

	// Voliči A založí sezení a zahlasuje jako Richie.
	a := client(t)
	resp := post(t, a, ts.URL, "/sessions", url.Values{"title": {"Večer"}, "day": {"2026-07-23"}})
	page := body(t, resp)
	slug := strings.TrimPrefix(resp.Request.URL.Path, "/p/")
	dateID := reChoice.FindStringSubmatch(page)[1]
	post(t, a, ts.URL, "/p/"+slug+"/votes", url.Values{"name": {"Richie"}, "choice_" + dateID: {"yes"}}).Body.Close()

	// Voliči B (čerstvý prohlížeč, žádná cookie) vidí sebe jako nového.
	b := client(t)
	page = body(t, mustGet(t, b, ts.URL+"/p/"+slug))
	if !strings.Contains(page, `id="nick"`) {
		t.Fatal("nový prohlížeč má vidět pole pro přezdívku")
	}

	// B se přihlásí jako Richie -> dostane cookie k existujícímu hlasu.
	post(t, b, ts.URL, "/p/"+slug+"/claim", url.Values{"name": {"richie"}}).Body.Close() // i jinak psané

	page = body(t, mustGet(t, b, ts.URL+"/p/"+slug))
	if strings.Contains(page, `id="nick"`) {
		t.Fatal("po přihlášení už se přezdívka nemá ptát (je v režimu úprav)")
	}
	if !strings.Contains(page, "1 hlas") {
		t.Fatal("po přihlášení má být pořád jen jeden hlas")
	}

	// B upraví hlas -> pořád jeden hlas, jen změněný.
	post(t, b, ts.URL, "/p/"+slug+"/votes", url.Values{"choice_" + dateID: {"maybe"}}).Body.Close()
	page = body(t, mustGet(t, b, ts.URL+"/p/"+slug))
	if !strings.Contains(page, "1 hlas") {
		t.Fatal("úprava po přihlášení nesmí založit druhý hlas")
	}

	// Neexistující jméno se navázat nedá.
	c := client(t)
	post(t, c, ts.URL, "/p/"+slug+"/claim", url.Values{"name": {"Nikdo"}}).Body.Close()
	page = body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if !strings.Contains(page, `id="nick"`) {
		t.Fatal("neplatné přihlášení nemá nikoho pustit do úprav")
	}
}

func TestSearchDisabledWithoutIGDB(t *testing.T) {
	ts := newTestServer(t, false)
	c := client(t)

	resp := post(t, c, ts.URL, "/sessions", url.Values{
		"title": {"Bez IGDB"}, "day": {"2026-07-23"},
	})
	slug := strings.TrimPrefix(resp.Request.URL.Path, "/p/")
	resp.Body.Close()

	sresp, err := c.Get(ts.URL + "/p/" + slug + "/games/search?q=hell")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body(t, sresp), `"disabled":true`) {
		t.Fatal("bez IGDB má hledání hlásit disabled")
	}

	// Přímý POST na přidání hry se bez IGDB nesmí uložit.
	post(t, c, ts.URL, "/p/"+slug+"/games", url.Values{"igdb_id": {"17"}, "name": {"X"}}).Body.Close()
	page := body(t, mustGet(t, c, ts.URL+"/p/"+slug))
	if strings.Contains(page, `data-prefix="X"`) {
		t.Fatal("hra se přidala i s vypnutým IGDB")
	}
}

func mustGet(t *testing.T, c *http.Client, u string) *http.Response {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	return resp
}

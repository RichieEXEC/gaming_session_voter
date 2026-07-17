package igdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubIGDB je falešné IGDB + Twitch: token na /oauth2/token, hry na /v4/games.
// Počítá volání, aby šlo ověřit, že cache a token opravdu šetří síť.
type stubIGDB struct {
	srv        *httptest.Server
	tokenCalls int
	gameCalls  int
	lastBody   string
}

func newStub(t *testing.T, gamesJSON string) *stubIGDB {
	t.Helper()
	s := &stubIGDB{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"stub-token","expires_in":5000000}`)
	})
	mux.HandleFunc("/v4/games", func(w http.ResponseWriter, r *http.Request) {
		s.gameCalls++
		b, _ := io.ReadAll(r.Body)
		s.lastBody = string(b)
		if r.Header.Get("Client-ID") != "cid" || r.Header.Get("Authorization") != "Bearer stub-token" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, gamesJSON)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubIGDB) client() *Client {
	return New("cid", "secret", s.srv.URL+"/oauth2/token", s.srv.URL+"/v4")
}

const sampleGames = `[
  {"id":1,"name":"Helldivers 2","slug":"helldivers-2","first_release_date":1707350400,
   "genres":[{"name":"Shooter"}],
   "multiplayer_modes":[{"onlinecoopmax":4,"onlinemax":0}],
   "cover":{"image_id":"co6"}},
  {"id":2,"name":"Factorio","slug":"factorio","first_release_date":1598313600,
   "genres":[{"name":"Strategy"}],
   "multiplayer_modes":[],
   "cover":{"image_id":"co7"}}
]`

func TestSearchParsesGames(t *testing.T) {
	stub := newStub(t, sampleGames)
	c := stub.client()

	games, err := c.Search(context.Background(), "helldivers")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("chci 2 hry, mám %d", len(games))
	}

	hd := games[0]
	if hd.Name != "Helldivers 2" {
		t.Errorf("name = %q", hd.Name)
	}
	if hd.Year != 2024 {
		t.Errorf("year = %d, chci 2024", hd.Year)
	}
	if hd.Genre != "Shooter" {
		t.Errorf("genre = %q", hd.Genre)
	}
	if hd.MaxPlayers != 4 {
		t.Errorf("max = %d, chci 4 (z onlinecoopmax)", hd.MaxPlayers)
	}
	if hd.Cover != "co6" {
		t.Errorf("cover = %q", hd.Cover)
	}
	if hd.Slug != "helldivers-2" {
		t.Errorf("slug = %q, chci helldivers-2", hd.Slug)
	}

	// Factorio: prázdné multiplayer_modes -> nevíme (0).
	if games[1].MaxPlayers != 0 {
		t.Errorf("Factorio max = %d, chci 0 (neznámé)", games[1].MaxPlayers)
	}
}

func TestSearchCachesAndReusesToken(t *testing.T) {
	stub := newStub(t, sampleGames)
	c := stub.client()
	ctx := context.Background()

	if _, err := c.Search(ctx, "helldivers"); err != nil {
		t.Fatal(err)
	}
	// Stejný dotaz podruhé jde z cache: žádné další volání.
	if _, err := c.Search(ctx, "helldivers"); err != nil {
		t.Fatal(err)
	}
	if stub.gameCalls != 1 {
		t.Errorf("game calls = %d, chci 1 (druhý dotaz z cache)", stub.gameCalls)
	}
	// Jiný dotaz síť použije, ale token se nesmí tahat znovu.
	if _, err := c.Search(ctx, "factorio"); err != nil {
		t.Fatal(err)
	}
	if stub.gameCalls != 2 {
		t.Errorf("game calls = %d, chci 2", stub.gameCalls)
	}
	if stub.tokenCalls != 1 {
		t.Errorf("token calls = %d, chci 1 (token se drží)", stub.tokenCalls)
	}
}

func TestDisabledClient(t *testing.T) {
	c := New("", "", "", "")
	if c.Enabled() {
		t.Fatal("bez credentials má být vypnutý")
	}
	if _, err := c.Search(context.Background(), "cokoliv"); err != ErrDisabled {
		t.Errorf("chci ErrDisabled, mám %v", err)
	}
}

func TestShortQueryNoNetwork(t *testing.T) {
	stub := newStub(t, sampleGames)
	c := stub.client()
	games, err := c.Search(context.Background(), "h")
	if err != nil || games != nil {
		t.Errorf("krátký dotaz: chci prázdno bez chyby, mám %v / %v", games, err)
	}
	if stub.gameCalls != 0 {
		t.Errorf("krátký dotaz nesmí sahat na síť, volání: %d", stub.gameCalls)
	}
}

func TestQuerySanitizedIntoApicalypse(t *testing.T) {
	stub := newStub(t, `[]`)
	c := stub.client()
	// Uvozovka a středník by rozbily dotaz; musí zmizet.
	if _, err := c.Search(context.Background(), `hell"; drop`); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stub.lastBody, `"; drop`) {
		t.Errorf("nebezpečné znaky prosákly do dotazu: %q", stub.lastBody)
	}
	if !strings.Contains(stub.lastBody, `search "hell drop"`) {
		t.Errorf("nečekaný tvar dotazu: %q", stub.lastBody)
	}
}

// IGDB opustilo pole "category" (nechalo ho prázdné), takže filtr na něj
// najednou nevracel nic. Filtrujeme přes game_type; ať se to nevrátí.
func TestSearchFiltersByGameType(t *testing.T) {
	stub := newStub(t, `[]`)
	c := stub.client()
	if _, err := c.Search(context.Background(), "cokoliv"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stub.lastBody, "game_type = 0") {
		t.Errorf("dotaz nefiltruje přes game_type: %q", stub.lastBody)
	}
	if strings.Contains(stub.lastBody, "category") {
		t.Errorf("dotaz pořád používá opuštěné pole category: %q", stub.lastBody)
	}
}

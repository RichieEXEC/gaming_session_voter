// Package igdb je tenký klient k IGDB (databáze her přes Twitch).
//
// Používá se jen při hledání, tzn. ve chvíli, kdy někdo přidává hru. Uložené
// hry jsou snímky, na stránce sezení se už nic z IGDB netahá. Token i výsledky
// hledání se drží v paměti, aby se nemlátil rate limit.
package igdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Game je to, co z IGDB potřebujeme. Year a MaxPlayers rovné nule znamenají
// "IGDB to o té hře neví".
type Game struct {
	IGDBID     int64  `json:"igdbId"`
	Name       string `json:"name"`
	Year       int    `json:"year"`
	Genre      string `json:"genre"`
	MaxPlayers int    `json:"max"`
	Cover      string `json:"cover"` // IGDB image_id
}

// Client mluví s IGDB. Bez client id a secret je vypnutý: hledání pak vrací
// ErrDisabled a aplikace jede dál jen s termíny.
type Client struct {
	id, secret string
	http       *http.Client

	tokenURL string
	apiURL   string

	mu       sync.Mutex
	token    string
	tokenExp time.Time

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	games []Game
	exp   time.Time
}

const (
	cacheTTL   = 10 * time.Minute
	cacheLimit = 256
)

// ErrDisabled znamená, že klient nemá nastavené credentials.
var ErrDisabled = fmt.Errorf("igdb: not configured")

// New vytvoří klienta. Prázdné id nebo secret = vypnuto. tokenURL a apiURL
// jsou volitelné (pro test); prázdné = ostré adresy.
func New(id, secret, tokenURL, apiURL string) *Client {
	if tokenURL == "" {
		tokenURL = "https://id.twitch.tv/oauth2/token"
	}
	if apiURL == "" {
		apiURL = "https://api.igdb.com/v4"
	}
	return &Client{
		id:       id,
		secret:   secret,
		http:     &http.Client{Timeout: 8 * time.Second},
		tokenURL: tokenURL,
		apiURL:   apiURL,
		cache:    map[string]cacheEntry{},
	}
}

// Enabled řekne, jestli má smysl hledat.
func (c *Client) Enabled() bool { return c.id != "" && c.secret != "" }

// Search vrátí hry odpovídající dotazu, max osm. Krátký dotaz vrací prázdno.
func (c *Client) Search(ctx context.Context, query string) ([]Game, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	q := sanitize(query)
	if len(q) < 2 {
		return nil, nil
	}
	key := strings.ToLower(q)
	if g, ok := c.cached(key); ok {
		return g, nil
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	// Apicalypse dotaz. game_type = 0 nechá jen hlavní hry (ne DLC/mody).
	// Pozor: dřív to bylo pole "category", které IGDB opustilo a nechalo
	// prázdné, takže "where category = 0" najednou nevracelo vůbec nic.
	body := fmt.Sprintf(
		`search "%s"; fields name, first_release_date, genres.name, `+
			`multiplayer_modes.onlinemax, multiplayer_modes.onlinecoopmax, `+
			`multiplayer_modes.offlinemax, cover.image_id; `+
			`where game_type = 0; limit 8;`, q)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/games", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-ID", c.id)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("igdb search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("igdb search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var raw []rawGame
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("igdb decode: %w", err)
	}

	games := make([]Game, 0, len(raw))
	for _, r := range raw {
		games = append(games, r.toGame())
	}
	c.store(key, games)
	return games, nil
}

func (c *Client) cached(key string) ([]Game, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.games, true
}

func (c *Client) store(key string, games []Game) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	// Když cache přeteče, prostě ji vyprázdníme. Je to jen odlehčení rate
	// limitu, ne zdroj pravdy, tak si nehrajeme na LRU.
	if len(c.cache) >= cacheLimit {
		c.cache = map[string]cacheEntry{}
	}
	c.cache[key] = cacheEntry{games: games, exp: time.Now().Add(cacheTTL)}
}

// accessToken vrátí platný token, případně si o nový řekne. Token z Twitche
// platí ~2 měsíce, takže se sahá ven výjimečně.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}

	u := c.tokenURL + "?" + url.Values{
		"client_id":     {c.id},
		"client_secret": {c.secret},
		"grant_type":    {"client_credentials"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("igdb token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("igdb token: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("igdb token decode: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("igdb token: empty access_token")
	}
	c.token = tok.AccessToken
	// Minuta rezervy, ať se netrefíme těsně za expiraci.
	life := time.Duration(tok.ExpiresIn) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	c.tokenExp = time.Now().Add(life - time.Minute)
	return c.token, nil
}

// sanitize vyčistí dotaz, aby nemohl rozbít Apicalypse dotaz. Uvozovka,
// středník a zpětné lomítko by z něj uměly utéct, tak pryč s nimi.
func sanitize(q string) string {
	q = strings.Map(func(r rune) rune {
		if r == '"' || r == ';' || r == '\\' || r < ' ' {
			return -1
		}
		return r
	}, q)
	return strings.TrimSpace(q)
}

// --- syrová podoba odpovědi z IGDB ---

type rawGame struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	FirstReleaseDate int64  `json:"first_release_date"`
	Genres           []struct {
		Name string `json:"name"`
	} `json:"genres"`
	MultiplayerModes []struct {
		OnlineMax     int `json:"onlinemax"`
		OnlineCoopMax int `json:"onlinecoopmax"`
		OfflineMax    int `json:"offlinemax"`
	} `json:"multiplayer_modes"`
	Cover struct {
		ImageID string `json:"image_id"`
	} `json:"cover"`
}

func (r rawGame) toGame() Game {
	g := Game{IGDBID: r.ID, Name: r.Name, Cover: r.Cover.ImageID}
	if r.FirstReleaseDate > 0 {
		g.Year = time.Unix(r.FirstReleaseDate, 0).UTC().Year()
	}
	if len(r.Genres) > 0 {
		g.Genre = r.Genres[0].Name
	}
	// Nejvyšší počet hráčů napříč režimy. IGDB to má roztroušené a často
	// prázdné, proto může vyjít nula = nevíme.
	for _, m := range r.MultiplayerModes {
		for _, n := range []int{m.OnlineMax, m.OnlineCoopMax, m.OfflineMax} {
			if n > g.MaxPlayers {
				g.MaxPlayers = n
			}
		}
	}
	return g
}

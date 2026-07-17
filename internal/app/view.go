package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

// SessionView je celé sezení připravené pro šablonu: dvě desky se sdílenými
// sloupci hlasujících a průběžně vedoucí termín, podle kterého se hlídá
// počet hráčů.
type SessionView struct {
	Names  []string // ostatní hlasující, sdílené sloupce obou desek
	Voters int      // kolik lidí celkem hlasovalo
	Dates  []DateRow
	Games  []GameRow
	Lead   *LeadDate // průběžně vedoucí termín; nil, když nikdo nehlasoval
	Best   *LeadGame // průběžně vedoucí hra; nil, když nikdo nehlasoval
}

// LeadDate je vedoucí termín pro shrnutí a pro hlídání počtu hráčů.
type LeadDate struct {
	Dow, Day, Month, Times string
	Yes                    int
}

// LeadGame je vedoucí hra pro shrnutí.
type LeadGame struct {
	Name, Meta, Cover string
	Hue               int
	Initials          string
	Yes               int
}

// Cell je hlas jednoho člověka pro jednu možnost.
type Cell struct {
	Value string // yes|maybe|no
	Name  string
	Title string
}

type DateRow struct {
	OptionID               int64
	Dow, Day, Month, Times string
	Mine                   string // hlas přihlášeného; "no" když nehlasuje
	Cells                  []Cell
	Yes, Total, Percent    int
	IsBest                 bool
}

type GameRow struct {
	OptionID                 int64
	Name, Genre, Meta, Cover string
	IgdbURL                  string // odkaz na stránku hry, prázdné u ručně přidaných
	Hue                      int    // barva náhradního obalu, když IGDB obal chybí
	Initials                 string
	Year, MaxPlayers         int
	MaxKnown                 bool
	HasVotes                 bool // někdo hru označil ano/možná; smazání by o to připravilo
	Mine                     string
	Cells                    []Cell
	Yes, Total, Percent      int
	IsBest                   bool
	Short                    int    // o kolik lidí se nevejde vs Lead.Yes; 0 = vejde/nevíme/nikdo nevede
	TightTitle               string // vysvětlení do title u chipu "max N"
}

// buildSessionView spočítá obě desky. Skóre: ano = 1, možná = 0.5, ne = 0.
// Vede možnost s nejvyšším skóre; při shodě ta dřívější (termíny jsou
// chronologicky, hry v pořadí přidání).
func buildSessionView(sess *store.Session, pr i18n.Printer, mine *store.Vote) SessionView {
	v := SessionView{Voters: len(sess.Votes)}

	// Kdo právě upravuje svůj hlas, má vlastní sloupec "Ty" a nesmí být
	// v tabulce podruhé. Do skóre se ale počítá pořád (jede přes sess.Votes).
	others := make([]store.Vote, 0, len(sess.Votes))
	for _, vote := range sess.Votes {
		if mine != nil && mine.ID != 0 && vote.ID == mine.ID {
			continue
		}
		others = append(others, vote)
	}
	for _, vote := range others {
		v.Names = append(v.Names, vote.Name)
	}

	// --- termíny ---
	bestDateScore, bestDateIdx := 0.0, -1
	for i, d := range sess.Dates {
		score, yes := scoreOf(sess.Votes, d.ID)
		row := DateRow{
			OptionID: d.ID,
			Times:    formatTimes(d.StartAt, d.EndAt),
			Mine:     mineOf(mine, d.ID),
			Cells:    cellsOf(others, d.ID, pr, "cell."),
			Yes:      yes,
			Total:    v.Voters,
			Percent:  pct(score, v.Voters),
		}
		if t, err := time.Parse(dayLayout, d.Day); err == nil {
			row.Dow, row.Day, row.Month = pr.Dow(t), t.Format("02"), pr.Month(t)
		} else {
			row.Day = d.Day
		}
		if score > bestDateScore {
			bestDateScore, bestDateIdx = score, i
		}
		v.Dates = append(v.Dates, row)
	}
	if bestDateIdx >= 0 {
		v.Dates[bestDateIdx].IsBest = true
		r := v.Dates[bestDateIdx]
		v.Lead = &LeadDate{Dow: r.Dow, Day: r.Day, Month: r.Month, Times: r.Times, Yes: r.Yes}
	}

	// --- hry ---
	leadYes := 0
	if v.Lead != nil {
		leadYes = v.Lead.Yes
	}
	bestGameScore, bestGameIdx := 0.0, -1
	for i, g := range sess.Games {
		score, yes := scoreOf(sess.Votes, g.ID)
		row := GameRow{
			OptionID:   g.ID,
			Name:       g.Name,
			Genre:      g.Genre,
			Year:       g.Year,
			MaxPlayers: g.MaxPlayers,
			MaxKnown:   g.MaxPlayers > 0,
			HasVotes:   score > 0, // score > 0 <=> aspoň jedno ano/možná
			Cover:      g.Cover,
			IgdbURL:    igdbGameURL(g.Slug),
			Hue:        gameHue(g.Name),
			Initials:   gameInitials(g.Name),
			Meta:       gameMeta(pr, g),
			Mine:       mineOf(mine, g.ID),
			Cells:      cellsOf(others, g.ID, pr, "gcell."),
			Yes:        yes,
			Total:      v.Voters,
			Percent:    pct(score, v.Voters),
		}
		// Vejde se vedoucí sestava do hry? Hlídá se jen když termín někdo
		// vede a IGDB u hry zná počet hráčů.
		if v.Lead != nil && g.MaxPlayers > 0 && g.MaxPlayers < leadYes {
			row.Short = leadYes - g.MaxPlayers
			row.TightTitle = pr.T("game.tightTitle", g.MaxPlayers, leadYes)
		}
		if score > bestGameScore {
			bestGameScore, bestGameIdx = score, i
		}
		v.Games = append(v.Games, row)
	}
	if bestGameIdx >= 0 {
		v.Games[bestGameIdx].IsBest = true
		r := v.Games[bestGameIdx]
		v.Best = &LeadGame{
			Name: r.Name, Meta: r.Meta, Cover: r.Cover,
			Hue: r.Hue, Initials: r.Initials, Yes: r.Yes,
		}
	}

	return v
}

// gameHue a gameInitials vyrábějí náhradní obal z názvu hry: barva podle
// jména, jedno nebo dvě písmena. Použije se, když IGDB obal nemá.
func gameHue(name string) int {
	h := 0
	for _, r := range name {
		h = (h*31 + int(r)) % 360
	}
	if h < 0 {
		h += 360
	}
	return h
}

func gameInitials(name string) string {
	var words []string
	for _, w := range strings.Fields(name) {
		cleaned := strings.TrimFunc(w, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		})
		if cleaned != "" {
			words = append(words, cleaned)
		}
	}
	if len(words) == 0 {
		return "?"
	}
	if len(words) == 1 {
		r := []rune(words[0])
		if len(r) == 1 {
			return strings.ToUpper(string(r))
		}
		return strings.ToUpper(string(r[:2]))
	}
	return strings.ToUpper(string([]rune(words[0])[:1]) + string([]rune(words[1])[:1]))
}

func scoreOf(votes []store.Vote, optID int64) (score float64, yes int) {
	for _, vote := range votes {
		switch vote.Choices[optID] {
		case "yes":
			score++
			yes++
		case "maybe":
			score += 0.5
		}
	}
	return score, yes
}

func cellsOf(others []store.Vote, optID int64, pr i18n.Printer, keyPrefix string) []Cell {
	cells := make([]Cell, 0, len(others))
	for _, vote := range others {
		val := vote.Choices[optID]
		if val == "" {
			val = "no"
		}
		cells = append(cells, Cell{
			Value: val,
			Name:  vote.Name,
			Title: vote.Name + ": " + pr.T(keyPrefix+val),
		})
	}
	return cells
}

func mineOf(mine *store.Vote, optID int64) string {
	if mine == nil {
		return "no"
	}
	if val := mine.Choices[optID]; val != "" {
		return val
	}
	return "no"
}

func pct(score float64, voters int) int {
	if voters == 0 {
		return 0
	}
	return int((score / float64(voters)) * 100)
}

// gameMeta složí "2024 · Střílečka · až 4 hráči". Neznámé části vynechá,
// u počtu hráčů to řekne narovinu místo hádání.
func gameMeta(pr i18n.Printer, g store.GameOption) string {
	var bits []string
	if g.Year > 0 {
		bits = append(bits, strconv.Itoa(g.Year))
	}
	if g.Genre != "" {
		bits = append(bits, g.Genre)
	}
	if g.MaxPlayers > 0 {
		bits = append(bits, pr.T("meta.upTo")+" "+pr.N(g.MaxPlayers, "player"))
	} else {
		bits = append(bits, pr.T("meta.playersUnknown"))
	}
	return strings.Join(bits, " · ")
}

// igdbGameURL složí odkaz na stránku hry z jejího slugu. Slug bereme s
// rezervou (posílá ho prohlížeč), tak pustíme jen [a-z0-9-]; jinak nic.
func igdbGameURL(slug string) string {
	if !validSlug(slug) {
		return ""
	}
	return "https://www.igdb.com/games/" + slug
}

func validSlug(s string) bool {
	if s == "" || len(s) > 120 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func formatTimes(start, end string) string {
	switch {
	case start == "" && end == "":
		return ""
	case end == "":
		return start
	case start == "":
		return end
	default:
		// Pomlčka pro rozsah, ne pro pauzu ve větě.
		return start + "–" + end
	}
}

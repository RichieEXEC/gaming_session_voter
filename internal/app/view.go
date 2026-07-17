package app

import (
	"strings"
	"time"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

// Row je jeden termín i s tím, jak pro něj kdo hlasoval.
type Row struct {
	OptionID int64
	Dow      string
	Day      string
	Month    string
	Times    string // "18:30–22:00", prázdné když se čas nezadal
	Cells    []Cell
	Mine     string // hlas přihlášeného, prázdné když nehlasuje
	Yes      int
	Total    int
	Percent  int
	IsBest   bool
}

// Cell je hlas jednoho člověka pro jeden termín.
type Cell struct {
	Value string // yes|maybe|no
	Name  string
	Title string // text do tooltipu
}

// Board je celá tabulka připravená pro šablonu.
type Board struct {
	Rows    []Row
	Names   []string
	Leader  *Leader
	Voters  int
	Options int
}

type Leader struct {
	Dow    string
	Day    string
	Month  string
	Times  string
	Yes    int
	Total  int
	Who    string
}

// buildBoard spočítá skóre a poskládá tabulku.
//
// Skóre: ano = 1, možná = 0.5, ne = 0. Vede termín s nejvyšším skóre;
// při shodě vyhraje ten dřívější, protože termíny jsou seřazené podle data.
func buildBoard(p *store.Poll, pr i18n.Printer, mine *store.Vote) Board {
	b := Board{Voters: len(p.Votes), Options: len(p.Options)}

	// Kdo svůj hlas právě upravuje, ten už má vlastní sloupec "Ty".
	// Bez tohohle by byl v tabulce dvakrát. Do skóre se ale počítá
	// pořád jednou, protože to jede přes p.Votes.
	others := make([]store.Vote, 0, len(p.Votes))
	for _, v := range p.Votes {
		if mine != nil && mine.ID != 0 && v.ID == mine.ID {
			continue
		}
		others = append(others, v)
	}

	for _, v := range others {
		b.Names = append(b.Names, v.Name)
	}

	bestScore := 0.0
	bestIdx := -1

	for i, o := range p.Options {
		row := Row{OptionID: o.ID, Total: len(p.Votes)}

		if t, err := time.Parse("2006-01-02", o.Day); err == nil {
			row.Dow = pr.Dow(t)
			row.Day = t.Format("02")
			row.Month = pr.Month(t)
		} else {
			row.Day = o.Day
		}
		row.Times = formatTimes(o.StartAt, o.EndAt)

		// Skóre jede přes všechny uložené hlasy.
		score := 0.0
		for _, v := range p.Votes {
			switch valueOf(v, o.ID) {
			case "yes":
				score++
				row.Yes++
			case "maybe":
				score += 0.5
			}
		}

		// Buňky jsou jen pro ostatní, můj sloupec se kreslí zvlášť.
		for _, v := range others {
			val := valueOf(v, o.ID)
			row.Cells = append(row.Cells, Cell{
				Value: val,
				Name:  v.Name,
				Title: v.Name + ": " + pr.T("cell."+val),
			})
		}

		if mine != nil {
			row.Mine = mine.Choices[o.ID]
			if row.Mine == "" {
				row.Mine = "no"
			}
		}

		if len(p.Votes) > 0 {
			row.Percent = int((score / float64(len(p.Votes))) * 100)
		}
		if score > bestScore {
			bestScore, bestIdx = score, i
		}
		b.Rows = append(b.Rows, row)
	}

	if bestIdx >= 0 && bestScore > 0 {
		b.Rows[bestIdx].IsBest = true
		r := b.Rows[bestIdx]
		b.Leader = &Leader{
			Dow: r.Dow, Day: r.Day, Month: r.Month, Times: r.Times,
			Yes: r.Yes, Total: r.Total,
			Who: namesFor(p, p.Options[bestIdx].ID, pr),
		}
	}
	return b
}

// valueOf bere chybějící hlas jako "nemůže".
func valueOf(v store.Vote, optID int64) string {
	if val := v.Choices[optID]; val != "" {
		return val
	}
	return "no"
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

func namesFor(p *store.Poll, optID int64, pr i18n.Printer) string {
	var yes []string
	for _, v := range p.Votes {
		if v.Choices[optID] == "yes" {
			yes = append(yes, v.Name)
		}
	}
	if len(yes) == 0 {
		return pr.T("leader.nobody")
	}
	if len(yes) <= 3 {
		return strings.Join(yes, ", ")
	}
	rest := len(yes) - 2
	return strings.Join(yes[:2], ", ") + " +" + pr.N(rest, "more")
}

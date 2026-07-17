package app

import (
	"testing"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

func testSession() *store.Session {
	return &store.Session{
		ID: 1, Slug: "abc", Title: "Herní večer",
		Dates: []store.DateOption{
			{ID: 10, Day: "2026-07-23", StartAt: "19:00", EndAt: "22:00"},
			{ID: 11, Day: "2026-07-28", StartAt: "19:00", EndAt: "22:00"},
		},
		Games: []store.GameOption{
			{ID: 20, IGDBID: 1, Name: "Helldivers 2", Year: 2024, Genre: "Střílečka", MaxPlayers: 4, Cover: "co6"},
			{ID: 21, IGDBID: 2, Name: "Among Us", Year: 2018, Genre: "Párty", MaxPlayers: 15},
		},
		Votes: []store.Vote{
			{ID: 100, Name: "Vojta", Choices: map[int64]string{10: "yes", 11: "no", 20: "yes", 21: "no"}},
			{ID: 101, Name: "Terka", Choices: map[int64]string{10: "yes", 11: "yes", 20: "maybe", 21: "yes"}},
		},
	}
}

func cs() i18n.Printer { return i18n.NewPrinter(i18n.CS) }

func TestTwoBoardsBuilt(t *testing.T) {
	v := buildSessionView(testSession(), cs(), nil)
	if len(v.Dates) != 2 {
		t.Errorf("chci 2 termíny, mám %d", len(v.Dates))
	}
	if len(v.Games) != 2 {
		t.Errorf("chci 2 hry, mám %d", len(v.Games))
	}
	if v.Voters != 2 {
		t.Errorf("Voters = %d, chci 2", v.Voters)
	}
}

func TestDateLead(t *testing.T) {
	v := buildSessionView(testSession(), cs(), nil)
	// 23. 7.: oba ano -> 2 jistá ano. 28. 7.: jedno ano. Vede 23.
	if !v.Dates[0].IsBest || v.Dates[1].IsBest {
		t.Errorf("vést má první termín")
	}
	if v.Lead == nil || v.Lead.Yes != 2 {
		t.Fatalf("Lead = %+v, chci Yes 2", v.Lead)
	}
}

func TestGameLead(t *testing.T) {
	v := buildSessionView(testSession(), cs(), nil)
	// Helldivers: yes + maybe = 1.5. Among Us: no + yes = 1.0. Vede Helldivers.
	if !v.Games[0].IsBest || v.Games[1].IsBest {
		t.Errorf("vést má Helldivers")
	}
	if v.Best == nil || v.Best.Name != "Helldivers 2" {
		t.Fatalf("Best = %+v, chci Helldivers", v.Best)
	}
}

// Jádro propojení desek: počet hráčů se hlídá proti průběžně vedoucímu termínu.
func TestPlayerCountAgainstLeadingDate(t *testing.T) {
	sess := &store.Session{
		ID: 1, Slug: "x", Title: "T",
		Dates: []store.DateOption{{ID: 10, Day: "2026-07-23"}},
		Games: []store.GameOption{
			{ID: 20, Name: "Malá hra", MaxPlayers: 4},
			{ID: 21, Name: "Velká hra", MaxPlayers: 64},
			{ID: 22, Name: "Neznámá", MaxPlayers: 0}, // IGDB neví
		},
		Votes: []store.Vote{
			{ID: 1, Name: "A", Choices: map[int64]string{10: "yes"}},
			{ID: 2, Name: "B", Choices: map[int64]string{10: "yes"}},
			{ID: 3, Name: "C", Choices: map[int64]string{10: "yes"}},
			{ID: 4, Name: "D", Choices: map[int64]string{10: "yes"}},
			{ID: 5, Name: "E", Choices: map[int64]string{10: "yes"}},
		},
	}
	v := buildSessionView(sess, cs(), nil)
	if v.Lead == nil || v.Lead.Yes != 5 {
		t.Fatalf("vedoucí termín má 5 jistých ano, mám %+v", v.Lead)
	}
	// Hra pro 4, dorazí 5 -> jeden se nevejde.
	if v.Games[0].Short != 1 {
		t.Errorf("malá hra Short = %d, chci 1", v.Games[0].Short)
	}
	// Hra pro 64 se vejde.
	if v.Games[1].Short != 0 {
		t.Errorf("velká hra Short = %d, chci 0", v.Games[1].Short)
	}
	// Neznámý počet hráčů se nehlídá.
	if v.Games[2].Short != 0 {
		t.Errorf("neznámá hra Short = %d, chci 0 (nehádat)", v.Games[2].Short)
	}
}

func TestNoLeadNoPlayerCheck(t *testing.T) {
	sess := &store.Session{
		ID: 1, Slug: "x", Title: "T",
		Dates: []store.DateOption{{ID: 10, Day: "2026-07-23"}},
		Games: []store.GameOption{{ID: 20, Name: "Hra", MaxPlayers: 2}},
		// žádné hlasy
	}
	v := buildSessionView(sess, cs(), nil)
	if v.Lead != nil {
		t.Errorf("bez hlasů nemá nic vést: %+v", v.Lead)
	}
	if v.Games[0].Short != 0 {
		t.Errorf("bez vedoucího termínu se počet hráčů nehlídá, Short = %d", v.Games[0].Short)
	}
}

// Kdo upravuje svůj hlas, má vlastní sloupec a nesmí být v tabulce podruhé,
// ale do skóre se pořád počítá. Platí pro obě desky.
func TestEditorNotDuplicated(t *testing.T) {
	sess := testSession()
	mine := &sess.Votes[0] // Vojta se vrátil upravit
	v := buildSessionView(sess, cs(), mine)

	if len(v.Names) != 1 || v.Names[0] != "Terka" {
		t.Errorf("Names = %v, chci jen [Terka]", v.Names)
	}
	if len(v.Dates[0].Cells) != 1 {
		t.Errorf("termín má %d cizích buněk, chci 1", len(v.Dates[0].Cells))
	}
	if len(v.Games[0].Cells) != 1 {
		t.Errorf("hra má %d cizích buněk, chci 1", len(v.Games[0].Cells))
	}
	// Vojtovo ano pro 23. 7. se pořád počítá (2 ano, ne 1).
	if v.Dates[0].Yes != 2 {
		t.Errorf("Yes = %d, chci 2: hlas editora se má počítat", v.Dates[0].Yes)
	}
	if v.Dates[0].Mine != "yes" {
		t.Errorf("Mine = %q, chci yes", v.Dates[0].Mine)
	}
}

// HasVotes rozhoduje, jestli se u mazání ptát. Musí být pravda jen když
// pro hru padlo aspoň jedno ano/možná; samé ne se maže bez ptaní.
func TestGameHasVotes(t *testing.T) {
	sess := &store.Session{
		ID: 1, Slug: "x", Title: "T",
		Dates: []store.DateOption{{ID: 10, Day: "2026-07-23"}},
		Games: []store.GameOption{
			{ID: 20, Name: "S hlasem", MaxPlayers: 4},
			{ID: 21, Name: "Jen maybe", MaxPlayers: 4},
			{ID: 22, Name: "Samé ne", MaxPlayers: 4},
			{ID: 23, Name: "Bez hlasů", MaxPlayers: 4},
		},
		Votes: []store.Vote{
			{ID: 1, Name: "A", Choices: map[int64]string{20: "yes", 21: "no", 22: "no"}},
			{ID: 2, Name: "B", Choices: map[int64]string{20: "no", 21: "maybe", 22: "no"}},
		},
	}
	v := buildSessionView(sess, cs(), nil)
	want := []bool{true, true, false, false}
	for i, w := range want {
		if v.Games[i].HasVotes != w {
			t.Errorf("%s HasVotes = %v, chci %v", v.Games[i].Name, v.Games[i].HasVotes, w)
		}
	}
}

func TestGameMeta(t *testing.T) {
	pr := cs()
	cases := []struct {
		g    store.GameOption
		want string
	}{
		{store.GameOption{Year: 2024, Genre: "Střílečka", MaxPlayers: 4}, "2024 · Střílečka · až 4 hráči"},
		{store.GameOption{Year: 2018, Genre: "Párty", MaxPlayers: 15}, "2018 · Párty · až 15 hráčů"},
		{store.GameOption{Year: 2011, Genre: "Přežití", MaxPlayers: 1}, "2011 · Přežití · až 1 hráč"},
		{store.GameOption{Year: 2020, Genre: "Strategie", MaxPlayers: 0}, "2020 · Strategie · počet hráčů neznámý"},
		{store.GameOption{Genre: "RPG", MaxPlayers: 2}, "RPG · až 2 hráči"},
	}
	for _, c := range cases {
		if got := gameMeta(pr, c.g); got != c.want {
			t.Errorf("gameMeta(%+v) = %q, chci %q", c.g, got, c.want)
		}
	}
}

func TestGameInitials(t *testing.T) {
	cases := map[string]string{
		"Helldivers 2":   "H2",
		"Valheim":        "VA",
		"It Takes Two":   "IT",
		"Counter-Strike": "CO",
		"":               "?",
	}
	for name, want := range cases {
		if got := gameInitials(name); got != want {
			t.Errorf("gameInitials(%q) = %q, chci %q", name, got, want)
		}
	}
}

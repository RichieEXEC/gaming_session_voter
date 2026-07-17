package app

import (
	"testing"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

func testPoll() *store.Poll {
	return &store.Poll{
		ID: 1, Slug: "abc", Title: "Herní večer",
		Options: []store.Option{
			{ID: 10, Day: "2026-07-23", StartAt: "19:00", EndAt: "22:00"},
			{ID: 11, Day: "2026-07-28", StartAt: "19:00", EndAt: "22:00"},
		},
		Votes: []store.Vote{
			{ID: 100, Name: "Vojta", Choices: map[int64]string{10: "yes", 11: "no"}},
			{ID: 101, Name: "Terka", Choices: map[int64]string{10: "maybe", 11: "yes"}},
		},
	}
}

func TestScoring(t *testing.T) {
	b := buildBoard(testPoll(), i18n.NewPrinter(i18n.CS), nil)

	// 23. 7.: Vojta ano, Terka možná -> jedno jisté ano ze dvou.
	if b.Rows[0].Yes != 1 || b.Rows[0].Total != 2 {
		t.Errorf("řádek 0 = %d/%d, chci 1/2", b.Rows[0].Yes, b.Rows[0].Total)
	}
	// Skóre 1.5 ze 2 = 75 %.
	if b.Rows[0].Percent != 75 {
		t.Errorf("řádek 0 procenta = %d, chci 75", b.Rows[0].Percent)
	}
	// 23. 7. (1.5) poráží 28. 7. (1.0).
	if !b.Rows[0].IsBest || b.Rows[1].IsBest {
		t.Errorf("vést má řádek 0, IsBest = %v, %v", b.Rows[0].IsBest, b.Rows[1].IsBest)
	}
}

// Kdo upravuje svůj hlas, má vlastní sloupec. Nesmí být v tabulce
// podruhé, ale do skóre se počítat musí.
func TestEditorNotDuplicated(t *testing.T) {
	p := testPoll()
	mine := &p.Votes[0] // Vojta se vrátil upravit hlas
	b := buildBoard(p, i18n.NewPrinter(i18n.CS), mine)

	if len(b.Names) != 1 || b.Names[0] != "Terka" {
		t.Errorf("Names = %v, chci jen [Terka]", b.Names)
	}
	if len(b.Rows[0].Cells) != 1 {
		t.Errorf("řádek 0 má %d buněk, chci 1", len(b.Rows[0].Cells))
	}
	// Vojtovo ano se pořád počítá.
	if b.Rows[0].Yes != 1 {
		t.Errorf("Yes = %d, chci 1: hlas editora se má počítat", b.Rows[0].Yes)
	}
	if b.Rows[0].Total != 2 {
		t.Errorf("Total = %d, chci 2", b.Rows[0].Total)
	}
	if b.Voters != 2 {
		t.Errorf("Voters = %d, chci 2", b.Voters)
	}
	if b.Rows[0].Mine != "yes" {
		t.Errorf("Mine = %q, chci yes", b.Rows[0].Mine)
	}
}

// Nový hlasující má koncept bez ID. Nikoho nesmí z tabulky vyhodit.
func TestNewVoterHidesNobody(t *testing.T) {
	p := testPoll()
	draft := &store.Vote{Choices: map[int64]string{10: "no", 11: "no"}}
	b := buildBoard(p, i18n.NewPrinter(i18n.CS), draft)

	if len(b.Names) != 2 {
		t.Errorf("Names = %v, chci oba hlasující", b.Names)
	}
	if b.Rows[0].Mine != "no" {
		t.Errorf("Mine = %q, chci no", b.Rows[0].Mine)
	}
}

func TestNoVotesNoLeader(t *testing.T) {
	p := testPoll()
	p.Votes = nil
	b := buildBoard(p, i18n.NewPrinter(i18n.CS), nil)
	if b.Leader != nil {
		t.Errorf("bez hlasů nemá nic vést, dostal jsem %+v", b.Leader)
	}
	for _, r := range b.Rows {
		if r.Percent != 0 {
			t.Errorf("bez hlasů má být 0 %%, je %d", r.Percent)
		}
	}
}

// Když všichni řeknou ne, není co vyhrát a nesmí se označit "nejlepší".
func TestAllNoHasNoLeader(t *testing.T) {
	p := testPoll()
	p.Votes = []store.Vote{
		{ID: 100, Name: "Vojta", Choices: map[int64]string{10: "no", 11: "no"}},
	}
	b := buildBoard(p, i18n.NewPrinter(i18n.CS), nil)
	if b.Leader != nil {
		t.Errorf("samé ne nemá mít vítěze, dostal jsem %+v", b.Leader)
	}
}

func TestFormatTimes(t *testing.T) {
	cases := []struct{ start, end, want string }{
		{"19:00", "22:00", "19:00–22:00"},
		{"19:00", "", "19:00"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := formatTimes(c.start, c.end); got != c.want {
			t.Errorf("formatTimes(%q, %q) = %q, chci %q", c.start, c.end, got, c.want)
		}
	}
}

func TestMissingChoiceCountsAsNo(t *testing.T) {
	p := testPoll()
	// Termín 11 Vojtovi v mapě chybí úplně.
	p.Votes[0].Choices = map[int64]string{10: "yes"}
	b := buildBoard(p, i18n.NewPrinter(i18n.CS), nil)
	if b.Rows[1].Cells[0].Value != "no" {
		t.Errorf("chybějící hlas = %q, chci no", b.Rows[1].Cells[0].Value)
	}
}

package i18n

import "testing"

// Čeština mění tvar na hranicích 1 a 5. Anglicky mluvící vývojář to
// nemá jak uvidět, tak ať to hlídá test.
func TestCzechPlurals(t *testing.T) {
	p := NewPrinter(CS)
	cases := []struct {
		n    int
		want string
	}{
		{0, "0 termínů"},
		{1, "1 termín"},
		{2, "2 termíny"},
		{4, "4 termíny"},
		{5, "5 termínů"},
		{11, "11 termínů"},
		{21, "21 termínů"},
	}
	for _, c := range cases {
		if got := p.N(c.n, "date"); got != c.want {
			t.Errorf("N(%d, date) = %q, chci %q", c.n, got, c.want)
		}
	}
}

func TestCzechVotePlurals(t *testing.T) {
	p := NewPrinter(CS)
	cases := map[int]string{0: "0 hlasů", 1: "1 hlas", 3: "3 hlasy", 5: "5 hlasů"}
	for n, want := range cases {
		if got := p.N(n, "vote"); got != want {
			t.Errorf("N(%d, vote) = %q, chci %q", n, got, want)
		}
	}
}

func TestEnglishPlurals(t *testing.T) {
	p := NewPrinter(EN)
	cases := map[int]string{0: "0 dates", 1: "1 date", 2: "2 dates", 5: "5 dates"}
	for n, want := range cases {
		if got := p.N(n, "date"); got != want {
			t.Errorf("N(%d, date) = %q, chci %q", n, got, want)
		}
	}
}

// Každý klíč z češtiny musí být i v angličtině, jinak by po přepnutí
// jazyka vykoukl holý klíč.
func TestCatalogsMatch(t *testing.T) {
	for key := range catalogs[CS] {
		// .few je jen české, angličtina ho nemá.
		if len(key) > 4 && key[len(key)-4:] == ".few" {
			continue
		}
		if _, ok := catalogs[EN][key]; !ok {
			t.Errorf("v anglickém katalogu chybí %q", key)
		}
	}
	for key := range catalogs[EN] {
		if _, ok := catalogs[CS][key]; !ok {
			t.Errorf("v českém katalogu chybí %q", key)
		}
	}
}

func TestMissingKeyIsVisible(t *testing.T) {
	p := NewPrinter(CS)
	if got := p.T("neexistuje.klic"); got != "neexistuje.klic" {
		t.Errorf("chybějící klíč má vrátit sám sebe, vrátil %q", got)
	}
}

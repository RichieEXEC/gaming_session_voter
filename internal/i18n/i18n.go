// Package i18n drží překlady a pravidla pro množné číslo.
package i18n

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Lang string

const (
	CS      Lang = "cs"
	EN      Lang = "en"
	Default      = CS
)

// CookieName drží zvolený jazyk mezi návštěvami.
const CookieName = "kh_lang"

var Supported = []Lang{CS, EN}

func Parse(s string) (Lang, bool) {
	switch Lang(strings.ToLower(strings.TrimSpace(s))) {
	case CS:
		return CS, true
	case EN:
		return EN, true
	}
	return Default, false
}

// FromRequest vybere jazyk: ?lang= má přednost, pak cookie, pak
// Accept-Language, jinak čeština.
func FromRequest(r *http.Request) Lang {
	if q := r.URL.Query().Get("lang"); q != "" {
		if l, ok := Parse(q); ok {
			return l
		}
	}
	if c, err := r.Cookie(CookieName); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" {
			continue
		}
		if l, ok := Parse(strings.SplitN(tag, "-", 2)[0]); ok {
			return l
		}
	}
	return Default
}

// Printer překládá pro jeden jazyk. Předává se do šablon.
type Printer struct{ Lang Lang }

func NewPrinter(l Lang) Printer { return Printer{Lang: l} }

func (p Printer) Code() string { return string(p.Lang) }

// T přeloží klíč. Chybějící klíč vrátí sám sebe, aby byl v UI vidět.
func (p Printer) T(key string, args ...any) string {
	cat, ok := catalogs[p.Lang]
	if !ok {
		cat = catalogs[Default]
	}
	s, ok := cat[key]
	if !ok {
		if s, ok = catalogs[Default][key]; !ok {
			return key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// N vrátí "5 termínů": číslo plus tvar podle jazyka.
//
// Čeština má tři tvary (1 / 2-4 / 5+), angličtina dva. Klíč je základ,
// ke kterému se přidá .one, .few nebo .many.
func (p Printer) N(n int, key string) string {
	return fmt.Sprintf("%d %s", n, p.PluralWord(n, key))
}

// PluralWord vrátí jen slovo ve správném tvaru, bez čísla.
func (p Printer) PluralWord(n int, key string) string {
	return p.T(key + "." + p.pluralSuffix(n))
}

func (p Printer) pluralSuffix(n int) string {
	if p.Lang == CS {
		switch {
		case n == 1:
			return "one"
		case n >= 2 && n <= 4:
			return "few"
		default:
			// Sem spadne i nula: "0 termínů".
			return "many"
		}
	}
	if n == 1 {
		return "one"
	}
	return "many"
}

var dows = map[Lang][7]string{
	CS: {"NE", "PO", "ÚT", "ST", "ČT", "PÁ", "SO"},
	EN: {"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"},
}

var months = map[Lang][12]string{
	CS: {"LED", "ÚNO", "BŘE", "DUB", "KVĚ", "ČVN", "ČVC", "SRP", "ZÁŘ", "ŘÍJ", "LIS", "PRO"},
	EN: {"JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"},
}

func (p Printer) Dow(t time.Time) string {
	d, ok := dows[p.Lang]
	if !ok {
		d = dows[Default]
	}
	return d[int(t.Weekday())]
}

func (p Printer) Month(t time.Time) string {
	m, ok := months[p.Lang]
	if !ok {
		m = months[Default]
	}
	return m[int(t.Month())-1]
}

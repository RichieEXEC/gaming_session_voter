package app

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RichieEXEC/gaming_session_voter/internal/i18n"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

// createForm drží rozepsané hlasování, aby po chybě nezmizelo, co člověk napsal.
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
	s.render(w, r, "new", http.StatusOK, pageData{
		L:     pr,
		Title: pr.T("create.heading"),
		Form:  createForm{Dates: []formDate{{Start: "19:00", End: "22:00"}}},
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
			L: pr, Title: pr.T("create.heading"), Form: form, Flash: msg,
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

	var opts []store.NewOption
	for _, fd := range form.Dates {
		if fd.Day == "" {
			continue // prázdné řádky se prostě zahodí
		}
		if _, err := time.Parse("2006-01-02", fd.Day); err != nil {
			reshow(pr.T("err.dateInvalid"))
			return
		}
		if !validTime(fd.Start) || !validTime(fd.End) {
			reshow(pr.T("err.dateInvalid"))
			return
		}
		opts = append(opts, store.NewOption{Day: fd.Day, StartAt: fd.Start, EndAt: fd.End})
	}
	if len(opts) == 0 {
		reshow(pr.T("err.dateRequired"))
		return
	}
	if len(opts) > maxOptions {
		reshow(pr.T("err.tooManyDates", maxOptions))
		return
	}

	slug, err := s.st.CreatePoll(form.Title, form.Note, opts)
	if err != nil {
		s.log.Error("create poll", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}
	http.Redirect(w, r, "/p/"+slug, http.StatusSeeOther)
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	pr := i18n.NewPrinter(i18n.FromRequest(r))
	slug := r.PathValue("slug")

	p, err := s.st.GetPoll(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get poll", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	// Sloupec "ty" je vidět vždycky. Kdo tu ještě nehlasoval, dostane
	// prázdný koncept, aby mohl klikat rovnou a přezdívku vyplnil až
	// při odeslání. O krok míň než v prototypu.
	mine := s.currentVote(r, p)
	editing := mine != nil
	if mine == nil {
		mine = blankDraft(p)
	}

	s.render(w, r, "poll", http.StatusOK, pageData{
		L:       pr,
		Title:   p.Title,
		Poll:    p,
		Slug:    slug,
		Board:   buildBoard(p, pr, mine),
		Mine:    mine,
		Editing: editing,
		Flash:   flashText(pr, r.URL.Query().Get("flash")),
	})
}

func blankDraft(p *store.Poll) *store.Vote {
	v := &store.Vote{Choices: make(map[int64]string, len(p.Options))}
	for _, o := range p.Options {
		v.Choices[o.ID] = "no"
	}
	return v
}

// currentVote vrátí hlas patřící tomuhle prohlížeči, pokud nějaký má.
func (s *Server) currentVote(r *http.Request, p *store.Poll) *store.Vote {
	c, err := r.Cookie(s.voteCookieName(p.Slug))
	if err != nil {
		return nil
	}
	id, ok := s.verifyVote(c.Value)
	if !ok {
		return nil
	}
	v, err := s.st.VoteByID(p.ID, id)
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

	p, err := s.st.GetPoll(slug)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "err.notFound", "err.notFoundLead")
		return
	}
	if err != nil {
		s.log.Error("get poll", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	// Bere se jen to, co v hlasování opravdu existuje, aby cizí
	// option_id z podvrženého formuláře neprošlo.
	choices := map[int64]string{}
	for _, o := range p.Options {
		val := r.FormValue("choice_" + strconv.FormatInt(o.ID, 10))
		switch val {
		case "yes", "maybe", "no":
			choices[o.ID] = val
		default:
			choices[o.ID] = "no"
		}
	}

	existing := s.currentVote(r, p)

	// Úprava hlasu, který tenhle prohlížeč založil.
	if existing != nil {
		if err := s.st.UpdateVote(p.ID, existing.ID, choices); err != nil {
			s.log.Error("update vote", "err", err, "slug", slug)
			s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
			return
		}
		s.redirectFlash(w, r, slug, "saved")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.rerenderPoll(w, r, pr, p, choices, pr.T("err.nickRequired"))
		return
	}
	if utf8.RuneCountInString(name) > maxNickLen {
		s.rerenderPoll(w, r, pr, p, choices, pr.T("err.nickTooLong"))
		return
	}

	voteID, err := s.st.SaveVote(p.ID, name, choices)
	if errors.Is(err, store.ErrDuplicateName) {
		s.rerenderPoll(w, r, pr, p, choices, pr.T("err.nickTaken"))
		return
	}
	if err != nil {
		s.log.Error("save vote", "err", err, "slug", slug)
		s.fail(w, r, http.StatusInternalServerError, "err.server", "err.serverLead")
		return
	}

	s.setVoteCookie(w, r, slug, voteID)
	s.redirectFlash(w, r, slug, "saved")
}

// rerenderPoll ukáže tabulku znovu i s tím, co člověk naklikal, aby
// se po chybě nemusel proklikávat znovu.
func (s *Server) rerenderPoll(
	w http.ResponseWriter, r *http.Request, pr i18n.Printer,
	p *store.Poll, choices map[int64]string, msg string,
) {
	draft := &store.Vote{Name: strings.TrimSpace(r.FormValue("name")), Choices: choices}
	s.render(w, r, "poll", http.StatusUnprocessableEntity, pageData{
		L:       pr,
		Title:   p.Title,
		Poll:    p,
		Slug:    p.Slug,
		Board:   buildBoard(p, pr, draft),
		Mine:    draft,
		Editing: false,
		Flash:   msg,
	})
}

// redirectFlash dělá POST/Redirect/GET, aby refresh znovu neodeslal formulář.
//
// V URL je jen kód hlášky, ne text. Jinak by šlo poslat odkaz, který
// v aplikaci vypíše cokoliv.
func (s *Server) redirectFlash(w http.ResponseWriter, r *http.Request, slug, code string) {
	u := "/p/" + slug
	if code != "" {
		u += "?flash=" + url.QueryEscape(code)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

// flashText překládá jen kódy, které sama aplikace posílá.
func flashText(pr i18n.Printer, code string) string {
	switch code {
	case "saved":
		return pr.T("ok.saved")
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
	// Zpět tam, odkud člověk přišel, ale jen na vlastní cestu.
	back := r.URL.Query().Get("next")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func validTime(s string) bool {
	if s == "" {
		return true // čas je nepovinný
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}

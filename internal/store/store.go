// Package store drží veškerý přístup k SQLite.
package store

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrDuplicateName znamená, že přezdívka už v tomto hlasování je.
var ErrDuplicateName = errors.New("store: nickname already used in this poll")

// ErrNotFound je vrácena, když hlasování neexistuje.
var ErrNotFound = errors.New("store: not found")

type Store struct {
	db     *sql.DB
	secret []byte
}

type Poll struct {
	ID        int64
	Slug      string
	Title     string
	Note      string
	CreatedAt time.Time
	Options   []Option
	Votes     []Vote
}

type Option struct {
	ID      int64
	Day     string // YYYY-MM-DD
	StartAt string // HH:MM, může být prázdné
	EndAt   string // HH:MM, může být prázdné
}

type Vote struct {
	ID      int64
	Name    string
	Choices map[int64]string // ID termínu -> yes|maybe|no
}

// NewOption je vstup pro zakládání hlasování.
type NewOption struct {
	Day     string
	StartAt string
	EndAt   string
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Zápisy do SQLite se stejně serializují a tahle aplikace má řádově
	// desítky uživatelů. Jedno spojení vyřadí celou třídu SQLITE_BUSY chyb.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	s := &Store{db: db}
	if s.secret, err = s.loadOrCreateSecret(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Secret je klíč pro podpis cookies. Drží se v databázi, aby restart
// kontejneru neodhlásil lidi z jejich vlastních hlasů.
func (s *Store) Secret() []byte { return s.secret }

func (s *Store) loadOrCreateSecret() ([]byte, error) {
	var enc string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'secret'`).Scan(&enc)
	if err == nil {
		return base64.StdEncoding.DecodeString(enc)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	enc = base64.StdEncoding.EncodeToString(buf)
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('secret', ?)`, enc); err != nil {
		return nil, fmt.Errorf("store secret: %w", err)
	}
	return buf, nil
}

func newSlug() (string, error) {
	buf := make([]byte, 9) // 12 znaků po base64, dost na neuhodnutelnost
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Store) CreatePoll(title, note string, opts []NewOption) (string, error) {
	slug, err := newSlug()
	if err != nil {
		return "", fmt.Errorf("slug: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO polls (slug, title, note, created_at) VALUES (?, ?, ?, ?)`,
		slug, title, note, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert poll: %w", err)
	}
	pollID, err := res.LastInsertId()
	if err != nil {
		return "", err
	}

	for i, o := range opts {
		if _, err := tx.Exec(
			`INSERT INTO options (poll_id, day, start_at, end_at, position) VALUES (?, ?, ?, ?, ?)`,
			pollID, o.Day, o.StartAt, o.EndAt, i,
		); err != nil {
			return "", fmt.Errorf("insert option: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return slug, nil
}

func (s *Store) GetPoll(slug string) (*Poll, error) {
	p := &Poll{}
	var created string
	err := s.db.QueryRow(
		`SELECT id, slug, title, note, created_at FROM polls WHERE slug = ?`, slug,
	).Scan(&p.ID, &p.Slug, &p.Title, &p.Note, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select poll: %w", err)
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, created)

	rows, err := s.db.Query(
		`SELECT id, day, start_at, end_at FROM options WHERE poll_id = ? ORDER BY day, position`, p.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("select options: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.Day, &o.StartAt, &o.EndAt); err != nil {
			return nil, err
		}
		p.Options = append(p.Options, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.loadVotes(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) loadVotes(p *Poll) error {
	rows, err := s.db.Query(
		`SELECT v.id, v.name, c.option_id, c.value
		   FROM votes v
		   LEFT JOIN choices c ON c.vote_id = v.id
		  WHERE v.poll_id = ?
		  ORDER BY v.created_at, v.id`, p.ID,
	)
	if err != nil {
		return fmt.Errorf("select votes: %w", err)
	}
	defer rows.Close()

	byID := map[int64]*Vote{}
	var order []int64
	for rows.Next() {
		var (
			id     int64
			name   string
			optID  sql.NullInt64
			choice sql.NullString
		)
		if err := rows.Scan(&id, &name, &optID, &choice); err != nil {
			return err
		}
		v, ok := byID[id]
		if !ok {
			v = &Vote{ID: id, Name: name, Choices: map[int64]string{}}
			byID[id] = v
			order = append(order, id)
		}
		if optID.Valid && choice.Valid {
			v.Choices[optID.Int64] = choice.String
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range order {
		p.Votes = append(p.Votes, *byID[id])
	}
	return nil
}

// SaveVote založí nový hlas. Vrací ErrDuplicateName, pokud přezdívka
// v tomhle hlasování už je.
func (s *Store) SaveVote(pollID int64, name string, choices map[int64]string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO votes (poll_id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		pollID, name, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrDuplicateName
		}
		return 0, fmt.Errorf("insert vote: %w", err)
	}
	voteID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := writeChoices(tx, voteID, choices); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return voteID, nil
}

// UpdateVote přepíše hlas, který už existuje. Volá se jen když je
// v cookie platný podpis pro tohle voteID.
func (s *Store) UpdateVote(pollID, voteID int64, choices map[int64]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`UPDATE votes SET updated_at = ? WHERE id = ? AND poll_id = ?`, now, voteID, pollID,
	)
	if err != nil {
		return fmt.Errorf("update vote: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM choices WHERE vote_id = ?`, voteID); err != nil {
		return fmt.Errorf("clear choices: %w", err)
	}
	if err := writeChoices(tx, voteID, choices); err != nil {
		return err
	}
	return tx.Commit()
}

// VoteByID vrátí hlas patřící do daného hlasování, jinak ErrNotFound.
func (s *Store) VoteByID(pollID, voteID int64) (*Vote, error) {
	v := &Vote{ID: voteID, Choices: map[int64]string{}}
	err := s.db.QueryRow(
		`SELECT name FROM votes WHERE id = ? AND poll_id = ?`, voteID, pollID,
	).Scan(&v.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT option_id, value FROM choices WHERE vote_id = ?`, voteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var val string
		if err := rows.Scan(&id, &val); err != nil {
			return nil, err
		}
		v.Choices[id] = val
	}
	return v, rows.Err()
}

func writeChoices(tx *sql.Tx, voteID int64, choices map[int64]string) error {
	stmt, err := tx.Prepare(`INSERT INTO choices (vote_id, option_id, value) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for optID, val := range choices {
		if _, err := stmt.Exec(voteID, optID, val); err != nil {
			return fmt.Errorf("insert choice: %w", err)
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	// Driver modernc.org/sqlite nedává typované chyby pro constrainty.
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

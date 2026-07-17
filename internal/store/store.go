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

// ErrDuplicateName znamená, že přezdívka už v tomto sezení je.
var ErrDuplicateName = errors.New("store: nickname already used in this session")

// ErrDuplicateGame znamená, že tahle hra už v sezení je.
var ErrDuplicateGame = errors.New("store: game already in this session")

// ErrNotFound je vrácena, když sezení (nebo možnost) neexistuje.
var ErrNotFound = errors.New("store: not found")

type Store struct {
	db     *sql.DB
	secret []byte
}

// Session je jedno sezení: termíny, hry a hlasy k obojímu.
type Session struct {
	ID        int64
	Slug      string
	Title     string
	Note      string
	CreatedAt time.Time
	Dates     []DateOption
	Games     []GameOption
	Votes     []Vote
}

// DateOption je jeden navržený termín.
type DateOption struct {
	ID      int64
	Day     string // YYYY-MM-DD
	StartAt string // HH:MM, může být prázdné
	EndAt   string // HH:MM, může být prázdné
}

// GameOption je jedna navržená hra. Data jsou snímek z IGDB pořízený při
// přidání, nečtou se znovu při každém zobrazení. Year a MaxPlayers rovné
// nule znamenají "nevíme".
type GameOption struct {
	ID         int64
	IGDBID     int64
	Name       string
	Year       int
	Genre      string
	MaxPlayers int
	Cover      string // IGDB image_id, prázdné = bez obalu
	Slug       string // IGDB slug pro odkaz na stránku hry, prázdné = ručně přidané
}

// Vote je hlas jednoho člověka. Choices mapuje ID možnosti (termínu i hry)
// na yes|maybe|no.
type Vote struct {
	ID      int64
	Name    string
	Choices map[int64]string
}

// AllOptionIDs vrátí ID všech možností v sezení, termínů i her. Používá se
// při ukládání hlasu, aby se braly jen možnosti, které opravdu existují.
func (s *Session) AllOptionIDs() map[int64]bool {
	ids := make(map[int64]bool, len(s.Dates)+len(s.Games))
	for _, d := range s.Dates {
		ids[d.ID] = true
	}
	for _, g := range s.Games {
		ids[g.ID] = true
	}
	return ids
}

// NewDate je vstup pro zakládání sezení.
type NewDate struct {
	Day     string
	StartAt string
	EndAt   string
}

// NewGame je snímek hry z IGDB, který se ukládá při přidání.
type NewGame struct {
	IGDBID     int64
	Name       string
	Year       int
	Genre      string
	MaxPlayers int
	Cover      string
	Slug       string
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
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

// CreateSession založí sezení s termíny. Hry se přidávají až na stránce
// sezení, kdokoliv z party.
func (s *Store) CreateSession(title, note string, dates []NewDate) (string, error) {
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
		`INSERT INTO sessions (slug, title, note, created_at) VALUES (?, ?, ?, ?)`,
		slug, title, note, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	sessionID, err := res.LastInsertId()
	if err != nil {
		return "", err
	}

	for i, d := range dates {
		if _, err := tx.Exec(
			`INSERT INTO options (session_id, kind, day, start_at, end_at, position)
			 VALUES (?, 'date', ?, ?, ?, ?)`,
			sessionID, d.Day, d.StartAt, d.EndAt, i,
		); err != nil {
			return "", fmt.Errorf("insert date: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return slug, nil
}

// AddGame přidá hru do sezení. Vrací ErrDuplicateGame, pokud tam ta hra
// (podle IGDB id) už je.
func (s *Store) AddGame(sessionID int64, g NewGame) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var pos int
	// Nová hra jde na konec seznamu her.
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM options WHERE session_id = ? AND kind = 'game'`,
		sessionID,
	).Scan(&pos); err != nil {
		return 0, fmt.Errorf("next position: %w", err)
	}

	res, err := tx.Exec(
		`INSERT INTO options (session_id, kind, igdb_id, name, year, genre, max_players, cover, slug, position, day)
		 VALUES (?, 'game', ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		sessionID, g.IGDBID, g.Name, g.Year, g.Genre, g.MaxPlayers, g.Cover, g.Slug, pos,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrDuplicateGame
		}
		return 0, fmt.Errorf("insert game: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateGameMax ručně nastaví (nebo vynuluje) počet hráčů u hry. Používá se
// tam, kde ho IGDB neznalo a ví ho ten, kdo hru přidal.
func (s *Store) UpdateGameMax(sessionID, optionID int64, max int) error {
	res, err := s.db.Exec(
		`UPDATE options SET max_players = ? WHERE id = ? AND session_id = ? AND kind = 'game'`,
		max, optionID, sessionID,
	)
	if err != nil {
		return fmt.Errorf("update game max: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveGame odebere hru ze sezení i s hlasy pro ni (přes ON DELETE CASCADE
// v choices). Termín odebrat nejde, jen hru.
func (s *Store) RemoveGame(sessionID, optionID int64) error {
	res, err := s.db.Exec(
		`DELETE FROM options WHERE id = ? AND session_id = ? AND kind = 'game'`,
		optionID, sessionID,
	)
	if err != nil {
		return fmt.Errorf("delete game: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetSession(slug string) (*Session, error) {
	sess := &Session{}
	var created string
	err := s.db.QueryRow(
		`SELECT id, slug, title, note, created_at FROM sessions WHERE slug = ?`, slug,
	).Scan(&sess.ID, &sess.Slug, &sess.Title, &sess.Note, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select session: %w", err)
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, created)

	if err := s.loadOptions(sess); err != nil {
		return nil, err
	}
	if err := s.loadVotes(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) loadOptions(sess *Session) error {
	rows, err := s.db.Query(
		`SELECT id, kind, day, start_at, end_at, igdb_id, name, year, genre, max_players, cover, slug
		   FROM options
		  WHERE session_id = ?
		  ORDER BY kind DESC, position, day`, // 'date' > 'game' abecedně, termíny se řadí přes ORDER BY day níž
		sess.ID,
	)
	if err != nil {
		return fmt.Errorf("select options: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id                        int64
			kind, day, start, end     string
			igdbID                    sql.NullInt64
			name, genre, cover, slug  string
			year, maxPlayers          int
		)
		if err := rows.Scan(&id, &kind, &day, &start, &end, &igdbID, &name, &year, &genre, &maxPlayers, &cover, &slug); err != nil {
			return err
		}
		if kind == "game" {
			sess.Games = append(sess.Games, GameOption{
				ID: id, IGDBID: igdbID.Int64, Name: name, Year: year,
				Genre: genre, MaxPlayers: maxPlayers, Cover: cover, Slug: slug,
			})
		} else {
			sess.Dates = append(sess.Dates, DateOption{ID: id, Day: day, StartAt: start, EndAt: end})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Termíny chronologicky (ORDER BY den + position výše řadí uvnitř kind,
	// ale kind DESC dá nejdřív termíny; den je hlavní klíč pro ně).
	sortDates(sess.Dates)
	return nil
}

func (s *Store) loadVotes(sess *Session) error {
	rows, err := s.db.Query(
		`SELECT v.id, v.name, c.option_id, c.value
		   FROM votes v
		   LEFT JOIN choices c ON c.vote_id = v.id
		  WHERE v.session_id = ?
		  ORDER BY v.created_at, v.id`, sess.ID,
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
		sess.Votes = append(sess.Votes, *byID[id])
	}
	return nil
}

// SaveVote založí nový hlas napříč oběma deskami. Vrací ErrDuplicateName,
// pokud přezdívka v sezení už je.
func (s *Store) SaveVote(sessionID int64, name string, choices map[int64]string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO votes (session_id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		sessionID, name, now, now,
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

// UpdateVote přepíše hlas, který už existuje. Volá se jen když je v cookie
// platný podpis pro tohle voteID.
func (s *Store) UpdateVote(sessionID, voteID int64, choices map[int64]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`UPDATE votes SET updated_at = ? WHERE id = ? AND session_id = ?`, now, voteID, sessionID,
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

// VoteByID vrátí hlas patřící do daného sezení, jinak ErrNotFound.
func (s *Store) VoteByID(sessionID, voteID int64) (*Vote, error) {
	v := &Vote{ID: voteID, Choices: map[int64]string{}}
	err := s.db.QueryRow(
		`SELECT name FROM votes WHERE id = ? AND session_id = ?`, voteID, sessionID,
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

// sortDates řadí termíny podle data vzestupně. Neplatné datum jde na konec.
func sortDates(dates []DateOption) {
	for i := 1; i < len(dates); i++ {
		for j := i; j > 0 && dates[j].Day < dates[j-1].Day; j-- {
			dates[j], dates[j-1] = dates[j-1], dates[j]
		}
	}
}

func isUniqueViolation(err error) bool {
	// Driver modernc.org/sqlite nedává typované chyby pro constrainty.
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

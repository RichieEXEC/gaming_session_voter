package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// seedOldDB vyrobí databázi ve stavu, v jakém je nasazená verze před tímhle
// buildem: aplikované jen původní schéma (migrations[0]), user_version 0 a
// pár řádků reálného tvaru. Přesně tohle najde migrace na produkci.
func seedOldDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatalf("apply original schema: %v", err)
	}
	// user_version zůstává 0, jako na nasazené databázi.

	if _, err := db.Exec(`
		INSERT INTO polls (id, slug, title, note, created_at)
		VALUES (1, 'oldslug', 'Starý večer', 'poznámka', '2026-07-01T10:00:00Z');
		INSERT INTO options (id, poll_id, day, start_at, end_at, position)
		VALUES (10, 1, '2026-07-23', '19:00', '22:00', 0),
		       (11, 1, '2026-07-24', '19:00', '22:00', 1);
		INSERT INTO votes (id, poll_id, name, created_at, updated_at)
		VALUES (100, 1, 'Vojta', '2026-07-01T11:00:00Z', '2026-07-01T11:00:00Z');
		INSERT INTO choices (vote_id, option_id, value)
		VALUES (100, 10, 'yes'), (100, 11, 'no');
	`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
}

func TestMigrationPreservesExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	seedOldDB(t, path)

	// Open spustí migraci.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	defer st.Close()

	sess, err := st.GetSession("oldslug")
	if err != nil {
		t.Fatalf("get migrated session: %v", err)
	}
	if sess.Title != "Starý večer" || sess.Note != "poznámka" {
		t.Errorf("session data lost: %+v", sess)
	}
	if len(sess.Dates) != 2 {
		t.Fatalf("chci 2 termíny, mám %d", len(sess.Dates))
	}
	if sess.Dates[0].Day != "2026-07-23" || sess.Dates[0].StartAt != "19:00" {
		t.Errorf("date fields lost: %+v", sess.Dates[0])
	}
	if len(sess.Games) != 0 {
		t.Errorf("stará data nemají žádnou hru, mám %d", len(sess.Games))
	}
	if len(sess.Votes) != 1 || sess.Votes[0].Name != "Vojta" {
		t.Fatalf("vote lost: %+v", sess.Votes)
	}
	if sess.Votes[0].Choices[10] != "yes" || sess.Votes[0].Choices[11] != "no" {
		t.Errorf("choices lost: %+v", sess.Votes[0].Choices)
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	seedOldDB(t, path)

	st1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	st1.Close()

	// Druhé otevření nesmí zkusit migraci znovu (jinak by RENAME na už
	// přejmenované tabulce spadl).
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second open must be a no-op, got: %v", err)
	}
	defer st2.Close()

	var v int
	if err := st2.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if v != len(migrations) {
		t.Errorf("user_version = %d, chci %d", v, len(migrations))
	}
}

func TestFreshDatabaseGetsFullSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	defer st.Close()

	slug, err := st.CreateSession("Nový večer", "", []NewDate{
		{Day: "2026-08-01", StartAt: "19:00", EndAt: "22:00"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	id, err := st.AddGame(sess(t, st, slug).ID, NewGame{
		IGDBID: 42, Name: "Helldivers 2", Year: 2024, Genre: "Střílečka", MaxPlayers: 4, Cover: "co1",
	})
	if err != nil {
		t.Fatalf("add game: %v", err)
	}
	if id == 0 {
		t.Fatal("add game vrátil id 0")
	}

	s := sess(t, st, slug)
	if len(s.Games) != 1 || s.Games[0].Name != "Helldivers 2" || s.Games[0].MaxPlayers != 4 {
		t.Fatalf("game not stored: %+v", s.Games)
	}
	// Stejnou hru podruhé nesmí vzít.
	if _, err := st.AddGame(s.ID, NewGame{IGDBID: 42, Name: "Helldivers 2"}); err != ErrDuplicateGame {
		t.Errorf("druhé přidání téže hry: chci ErrDuplicateGame, mám %v", err)
	}
}

func TestVoteSpansBothBoards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vote.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	slug, _ := st.CreateSession("Večer", "", []NewDate{{Day: "2026-08-01"}})
	s := sess(t, st, slug)
	dateID := s.Dates[0].ID
	gameID, _ := st.AddGame(s.ID, NewGame{IGDBID: 7, Name: "Valheim", MaxPlayers: 10})

	_, err = st.SaveVote(s.ID, "Terka", map[int64]string{dateID: "yes", gameID: "maybe"})
	if err != nil {
		t.Fatalf("save vote: %v", err)
	}

	s = sess(t, st, slug)
	if len(s.Votes) != 1 {
		t.Fatalf("chci 1 hlas, mám %d", len(s.Votes))
	}
	if s.Votes[0].Choices[dateID] != "yes" || s.Votes[0].Choices[gameID] != "maybe" {
		t.Errorf("hlas nepokryl obě desky: %+v", s.Votes[0].Choices)
	}

	// Odebrání hry smaže i hlas pro ni.
	if err := st.RemoveGame(s.ID, gameID); err != nil {
		t.Fatalf("remove game: %v", err)
	}
	s = sess(t, st, slug)
	if len(s.Games) != 0 {
		t.Errorf("hra se neodebrala: %+v", s.Games)
	}
	if _, ok := s.Votes[0].Choices[gameID]; ok {
		t.Errorf("hlas pro odebranou hru zůstal: %+v", s.Votes[0].Choices)
	}
	if s.Votes[0].Choices[dateID] != "yes" {
		t.Errorf("hlas pro termín se odebráním hry ztratil")
	}
}

func sess(t *testing.T, st *Store, slug string) *Session {
	t.Helper()
	s, err := st.GetSession(slug)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return s
}

package store

import (
	"database/sql"
	"fmt"
)

// migrations se aplikují po sobě podle PRAGMA user_version. Index 0 posune
// prázdnou databázi na verzi 1, index 1 na verzi 2 a tak dál. Jednou vydaný
// krok se už NIKDY nemění: úprava schématu = nový řetězec na konci.
//
// migrations[0] je původní schéma (schema.sql). Na čerstvé databázi ho
// vytvoří, na už nasazené je díky IF NOT EXISTS bez efektu, jen srovná
// user_version na 1, aby další kroky navázaly.
var migrations = []string{
	schema,

	// v1 -> v2: z "hlasování o termínu" se stává "sezení" se dvěma deskami.
	// Termín i hra jsou teď obojí "možnost" (option), liší se sloupcem kind.
	// Stávající možnosti jsou všechny termíny, takže kind s výchozím 'date'
	// sedí bez dalšího zásahu.
	`
	ALTER TABLE polls RENAME TO sessions;
	ALTER TABLE options RENAME COLUMN poll_id TO session_id;
	ALTER TABLE votes RENAME COLUMN poll_id TO session_id;

	ALTER TABLE options ADD COLUMN kind        TEXT    NOT NULL DEFAULT 'date';
	ALTER TABLE options ADD COLUMN igdb_id     INTEGER;
	ALTER TABLE options ADD COLUMN name        TEXT    NOT NULL DEFAULT '';
	ALTER TABLE options ADD COLUMN year        INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE options ADD COLUMN genre       TEXT    NOT NULL DEFAULT '';
	ALTER TABLE options ADD COLUMN max_players INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE options ADD COLUMN cover       TEXT    NOT NULL DEFAULT '';

	-- Stejnou hru nejde do jednoho sezení přidat dvakrát.
	CREATE UNIQUE INDEX IF NOT EXISTS idx_options_game
	    ON options(session_id, igdb_id) WHERE kind = 'game';
	`,

	// v2 -> v3: slug hry z IGDB, aby šlo odkázat na její stránku.
	`ALTER TABLE options ADD COLUMN slug TEXT NOT NULL DEFAULT '';`,
}

// migrate dožene databázi na poslední verzi. Každý krok běží ve vlastní
// transakci i s posunem user_version, takže se buď povede celý, nebo se
// vrátí zpátky a aplikace radši vůbec nenaběhne, než aby zůstala v půlce.
func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for ; v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", v+1, err)
		}
		// PRAGMA nebere parametr; v je index, ne vstup od uživatele.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("bump schema version to %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", v+1, err)
		}
	}
	return nil
}

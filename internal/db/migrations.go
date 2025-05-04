package db

import (
	"context"
	"database/sql"
)

// Migration represents a single database migration
// Each migration should have a unique ID and an Up function
// that applies the migration.
type Migration struct {
	ID int
	Up func(db *sql.DB) error
}

// migrations is a slice of all migrations to be applied in order.
// Add new migrations to this slice as needed.
//
// Migrations are used to update the database schema or data when
// the application is upgraded. Each migration should have a unique ID
// and will only be applied once.
//
// Example migration:
//
//	{
//	 ID: 1,
//	 Up: func(db *sql.DB) error {
//	   _, err := db.Exec(`ALTER TABLE transactions ADD COLUMN new_column TEXT;`)
//	   return err
//	 },
//	},
var migrations = []Migration{
	// Migrations will be added here as needed
}

// ApplyMigrations applies all pending migrations to the database.
func ApplyMigrations(ctx context.Context, db *sql.DB, logger func(msg string, args ...interface{})) error {
	// Ensure the migrations table exists
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			id INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Get already applied migration IDs
	rows, err := db.QueryContext(ctx, `SELECT id FROM migrations`)
	if err != nil {
		return err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return err
		}
		applied[id] = true
	}

	// Apply pending migrations
	for _, m := range migrations {
		if applied[m.ID] {
			continue
		}
		logger("Applying migration %d", m.ID)
		if err := m.Up(db); err != nil {
			return err
		}
		_, err := db.Exec(`INSERT INTO migrations (id) VALUES (?)`, m.ID)
		if err != nil {
			return err
		}
		logger("Migration %d applied", m.ID)
	}

	return nil
}

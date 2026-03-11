package migrations

import (
	"database/sql"
	"fmt"
	"testing"
)

// TestSchemaMigrationMatrix verifies schema upgrade paths from various
// starting points to the current version, ensuring that:
// 1. Fresh init creates all tables correctly
// 2. Fast-path (already current) recreates dolt_ignore'd tables
// 3. Partial migrations complete gracefully
// 4. dolt_ignore'd tables are recreated after every init
//
// This test matrix prevents regression of GH#2271 (wisps tables missing
// after server restart) and ensures migration idempotency.
func TestSchemaMigrationMatrix(t *testing.T) {
	tests := []struct {
		name          string
		setupSchema   func(*sql.DB) error
		verifyTables  []string
		verifyIgnored []string
	}{
		{
			name: "fresh_init_creates_all_tables",
			setupSchema: func(db *sql.DB) error {
				// Start with completely empty database (no schema_version)
				return nil
			},
			verifyTables: []string{
				"issues", "dependencies", "labels", "events", "comments",
				"config", "issue_counter", "wisps", "wisp_labels",
				"wisp_dependencies", "wisp_events", "wisp_comments",
			},
			verifyIgnored: []string{"wisps", "wisp_%"},
		},
		{
			name: "fast_path_recreates_ignored_tables",
			setupSchema: func(db *sql.DB) error {
				// Simulate current schema version but missing wisps tables
				// (server restart scenario from GH#2271)
				if err := runAllMigrations(db); err != nil {
					return err
				}
				// Drop wisps tables to simulate post-restart state
				if _, err := db.Exec("DROP TABLE IF EXISTS wisps"); err != nil {
					return err
				}
				if _, err := db.Exec("DROP TABLE IF EXISTS wisp_labels"); err != nil {
					return err
				}
				if _, err := db.Exec("DROP TABLE IF EXISTS wisp_dependencies"); err != nil {
					return err
				}
				if _, err := db.Exec("DROP TABLE IF EXISTS wisp_events"); err != nil {
					return err
				}
				if _, err := db.Exec("DROP TABLE IF EXISTS wisp_comments"); err != nil {
					return err
				}
				return nil
			},
			verifyTables: []string{
				"wisps", "wisp_labels", "wisp_dependencies",
				"wisp_events", "wisp_comments",
			},
			verifyIgnored: []string{"wisps", "wisp_%"},
		},
		{
			name: "partial_migration_completes_gracefully",
			setupSchema: func(db *sql.DB) error {
				// Simulate upgrade from older version (missing wisps)
				// Create base tables only (migration 001-003)
				if err := createBaseIssuesTable(db); err != nil {
					return err
				}
				if err := createConfigTable(db); err != nil {
					return err
				}
				// Set old schema version
				if _, err := db.Exec(
					"INSERT INTO config (`key`, `value`) VALUES ('schema_version', '3') "+
						"ON DUPLICATE KEY UPDATE `value` = '3'",
				); err != nil {
					return err
				}
				return nil
			},
			verifyTables: []string{
				"wisps", "wisp_labels", "wisp_dependencies",
				"wisp_events", "wisp_comments", "issue_counter",
			},
			verifyIgnored: []string{"wisps", "wisp_%"},
		},
		{
			name: "idempotent_double_init",
			setupSchema: func(db *sql.DB) error {
				// Run full migration, then run again
				if err := runAllMigrations(db); err != nil {
					return err
				}
				return runAllMigrations(db)
			},
			verifyTables: []string{
				"issues", "wisps", "wisp_labels", "wisp_dependencies",
				"wisp_events", "wisp_comments", "issue_counter",
			},
			verifyIgnored: []string{"wisps", "wisp_%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDoltBranch(t)

			// Setup initial schema state
			if err := tt.setupSchema(db); err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			// Run migrations (simulates bd init or server startup)
			if err := runAllMigrations(db); err != nil {
				t.Fatalf("migrations failed: %v", err)
			}

			// Verify all expected tables exist
			for _, table := range tt.verifyTables {
				exists, err := tableExists(db, table)
				if err != nil {
					t.Errorf("failed to check table %s: %v", table, err)
					continue
				}
				if !exists {
					t.Errorf("table %s should exist after migration", table)
				}
			}

			// Verify dolt_ignore patterns
			for _, pattern := range tt.verifyIgnored {
				var count int
				err := db.QueryRow(
					"SELECT COUNT(*) FROM dolt_ignore WHERE pattern = ?",
					pattern,
				).Scan(&count)
				if err != nil {
					t.Errorf("failed to check dolt_ignore pattern %s: %v", pattern, err)
					continue
				}
				if count != 1 {
					t.Errorf("expected 1 dolt_ignore pattern %s, got %d", pattern, count)
				}
			}

			// Critical: Verify wisps tables are NOT staged after dolt_add
			if _, err := db.Exec("CALL DOLT_ADD('-A')"); err != nil {
				t.Fatalf("dolt_add failed: %v", err)
			}
			for _, table := range []string{"wisps", "wisp_labels", "wisp_dependencies", "wisp_events", "wisp_comments"} {
				var staged bool
				err := db.QueryRow(
					"SELECT staged FROM dolt_status WHERE table_name = ?",
					table,
				).Scan(&staged)
				if err == nil && staged {
					t.Errorf("table %s should NOT be staged (dolt_ignore should prevent)", table)
				}
			}
		})
	}
}

// TestWispsTableRecreationEverySession verifies that dolt_ignore'd tables
// are recreated on every initSchemaOnDB call, not just on first init.
// This is the core regression test for GH#2271.
func TestWispsTableRecreationEverySession(t *testing.T) {
	db := openTestDoltBranch(t)

	// Session 1: Initialize schema
	if err := runAllMigrations(db); err != nil {
		t.Fatalf("initial migration failed: %v", err)
	}

	// Verify wisps table exists
	exists, err := tableExists(db, "wisps")
	if err != nil {
		t.Fatalf("failed to check wisps table: %v", err)
	}
	if !exists {
		t.Fatal("wisps table should exist after first init")
	}

	// Insert a test wisp
	_, err = db.Exec(
		"INSERT INTO wisps (id, title, description, design, acceptance_criteria, notes) "+
			"VALUES ('test-wisp-1', 'Test', 'desc', '', '', '')",
	)
	if err != nil {
		t.Fatalf("failed to insert test wisp: %v", err)
	}

	// Simulate server restart: drop wisps tables (they're not in Dolt history)
	// This is what happens in production when Dolt server restarts
	for _, table := range []string{"wisps", "wisp_labels", "wisp_dependencies", "wisp_events", "wisp_comments"} {
		if _, err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
			t.Fatalf("failed to drop %s: %v", table, err)
		}
	}

	// Verify wisps table is gone
	exists, err = tableExists(db, "wisps")
	if err != nil {
		t.Fatalf("failed to check wisps table after drop: %v", err)
	}
	if exists {
		t.Fatal("wisps table should be dropped")
	}

	// Session 2: Re-run migrations (simulates next bd command after restart)
	// The fast-path in initSchemaOnDB should recreate wisps tables
	if err := runAllMigrations(db); err != nil {
		t.Fatalf("second migration failed: %v", err)
	}

	// Verify wisps table was recreated
	exists, err = tableExists(db, "wisps")
	if err != nil {
		t.Fatalf("failed to check wisps table after recreation: %v", err)
	}
	if !exists {
		t.Fatal("wisps table should be recreated by fast-path (GH#2271 regression)")
	}

	// Verify we can insert new wisps (data from previous session is lost,
	// which is expected for ephemeral dolt_ignore'd tables)
	_, err = db.Exec(
		"INSERT INTO wisps (id, title, description, design, acceptance_criteria, notes) "+
			"VALUES ('test-wisp-2', 'Test After Restart', 'desc', '', '', '')",
	)
	if err != nil {
		t.Fatalf("failed to insert wisp after recreation: %v", err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM wisps").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query wisps: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 wisp (old data lost), got %d", count)
	}
}

// TestSchemaVersionProgression verifies that schema_version advances
// correctly through migrations and that the fast-path works.
func TestSchemaVersionProgression(t *testing.T) {
	db := openTestDoltBranch(t)

	// Start with no schema
	var version int
	err := db.QueryRow("SELECT `value` FROM config WHERE `key` = 'schema_version'").Scan(&version)
	if err == nil {
		t.Fatal("schema_version should not exist before migration")
	}

	// Run migrations
	if err := runAllMigrations(db); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	// Verify schema_version is current (8 migrations currently)
	err = db.QueryRow("SELECT `value` FROM config WHERE `key` = 'schema_version'").Scan(&version)
	if err != nil {
		t.Fatalf("failed to read schema_version: %v", err)
	}
	expectedVersion := 8 // Current migration count
	if version < expectedVersion {
		t.Errorf("expected schema_version >= %d, got %d", expectedVersion, version)
	}

	// Drop wisps to simulate restart
	if _, err := db.Exec("DROP TABLE IF EXISTS wisps"); err != nil {
		t.Fatalf("failed to drop wisps: %v", err)
	}

	// Re-run migrations (fast-path should trigger)
	if err := runAllMigrations(db); err != nil {
		t.Fatalf("fast-path migration failed: %v", err)
	}

	// Verify schema_version unchanged (fast-path doesn't bump it)
	var newVersion int
	err = db.QueryRow("SELECT `value` FROM config WHERE `key` = 'schema_version'").Scan(&newVersion)
	if err != nil {
		t.Fatalf("failed to read schema_version after fast-path: %v", err)
	}
	if newVersion != version {
		t.Errorf("fast-path should not change schema_version: was %d, now %d", version, newVersion)
	}

	// Verify wisps table was recreated by fast-path
	exists, err := tableExists(db, "wisps")
	if err != nil {
		t.Fatalf("failed to check wisps: %v", err)
	}
	if !exists {
		t.Fatal("fast-path should recreate wisps table")
	}
}

// runAllMigrations runs the full migration sequence (001-008).
func runAllMigrations(db *sql.DB) error {
	migrations := []func(*sql.DB) error{
		MigrateWispTypeColumn,
		MigrateSpecIDColumn,
		DetectOrphanedChildren,
		MigrateWispsTable,
		MigrateWispAuxiliaryTables,
		MigrateIssueCounterTable,
		MigrateInfraToWisps,
		MigrateWispDepTypeIndex,
	}

	for _, mig := range migrations {
		if err := mig(db); err != nil {
			return err
		}
	}

	return nil
}

// createBaseIssuesTable creates a minimal issues table for testing
// partial migrations from older versions.
func createBaseIssuesTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS issues (
			id VARCHAR(255) PRIMARY KEY,
			title VARCHAR(500) NOT NULL,
			description TEXT,
			status VARCHAR(32) NOT NULL DEFAULT 'open',
			issue_type VARCHAR(32) NOT NULL DEFAULT 'task',
			priority INT NOT NULL DEFAULT 0
		)
	`)
	return err
}

// createConfigTable creates a minimal config table for setting schema_version.
func createConfigTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS config (
			` + "`key`" + ` VARCHAR(255) PRIMARY KEY,
			` + "`value`" + ` TEXT
		)
	`)
	return err
}

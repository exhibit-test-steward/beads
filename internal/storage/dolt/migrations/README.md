# Dolt Schema Migrations

This package contains all schema migrations for the Beads Dolt backend.

## Migration Philosophy

Each migration is **idempotent** - safe to run multiple times. Migrations check whether their changes have already been applied before making modifications.

## Critical Invariant: dolt_ignore'd Tables

Tables matching patterns in `dolt_ignore` (wisps, wisp_*) are **stateless ephemeral tables** that only exist in the Dolt working set. They are NOT persisted in commit history.

**Consequence:** These tables MUST be recreated on every server session, even when schema is already at the current version.

See [GH#2271](https://github.com/steveyegge/beads/issues/2271) for the root cause: the schema fast-path was skipping `createIgnoredTables()`, causing "table not found: wisps" crashes after server restart.

## Test Coverage

### Unit Tests (`migrations_test.go`)
- Individual migration idempotency
- Table/column existence helpers
- Schema parity between issues/wisps

### Integration Tests (`schema_migration_test.go`)
- **Migration matrix**: Fresh init, fast-path, partial migrations, idempotency
- **Session recreation test**: Verifies wisps tables recreated on every init (GH#2271 regression)
- **Version progression**: Schema version tracking across upgrade paths

Run with Dolt server available:
```bash
go test -v ./internal/storage/dolt/migrations/...
```

Tests skip gracefully when Dolt is unavailable.

## Adding New Migrations

1. Create `00N_description.go` with idempotent migration function
2. Add to `migrationsList` in `../migrations.go`
3. Increment `currentSchemaVersion` in `../store.go`
4. Add test case to `migrations_test.go`
5. If adding dolt_ignore'd tables, update `createIgnoredTables()` in `../migrations.go`

# AGENTS.md

Instructions for LLM-based coding agents working on this project.

## Project Summary

XDAL is a Go library (not an application) providing a database abstraction layer with a fluent query builder and driver wrappers for MySQL, PostgreSQL, SQLite, and SQL Server.

- **Module**: `github.com/martinsuchenak/xdal`
- **Go version**: 1.26.2
- **No main.go, no cmd/ directory** — this is a library only

## Architecture

```
pkg/xdal/          Core package: types, query builder, dialect interface, logging
pkg/mysql/        MySQL driver (embeds BaseDB, configures BaseDialect)
pkg/postgres/     PostgreSQL driver
pkg/sqlite/       SQLite driver
pkg/mssql/        SQL Server driver
pkg/migrate/      Versioned schema migrations built on the schema builder and named locks
tests/integration/ Integration tests using Docker containers
```

### Key Types

| Type | File | Purpose |
|------|------|---------|
| `DBInterface` | `pkg/xdal/types.go` | Interface for all drivers (Exec, Query, QueryRow, BeginTx, Ping, Close + Select/Insert/Update/Delete factory methods) |
| `DBExecutor` | `pkg/xdal/logger.go` | Common interface for Exec/Query/QueryRow (satisfied by BaseDB and Tx) |
| `BaseDB` | `pkg/xdal/logger.go` | Shared implementation with structured logging; drivers embed this |
| `Tx` | `pkg/xdal/logger.go` | Transaction wrapper with logging |
| `Dialect` | `pkg/xdal/dialect.go` | Interface for SQL generation (returns error). Includes `TranslateSQL` for raw SQL placeholder translation |
| `BaseDialect` | `pkg/xdal/dialect.go` | Common implementation configured by function fields (Placeholder, AppendLimit, AppendReturning, AppendDeletedReturning, PrependReturning) + QuoteStyle |
| `QueryBuilder` | `pkg/xdal/query_builder.go` | Fluent API, delegates Build() to Dialect. SetMap/SetStruct for bulk column-value pairs |
| `SelectQuery` | `pkg/xdal/types.go` | Fluent SELECT builder |
| `InsertQuery` | `pkg/xdal/types.go` | Fluent INSERT builder (single-row and batch) |
| `UpdateQuery` | `pkg/xdal/types.go` | Fluent UPDATE builder |
| `DeleteQuery` | `pkg/xdal/types.go` | Fluent DELETE builder |
| `WhereGroup` | `pkg/xdal/query_builder.go` | Collects conditions for parenthesized WHERE groups |
| `Logger` | `pkg/xdal/logger.go` | Structured logging interface (compatible with fortix/go-libs/logger) |
| `Capabilities` | `pkg/xdal/capabilities.go` | Optional features per dialect (Returning, Upsert, RowLocks, SkipLocked, TransactionalDDL, SharedAppLocks) |
| `AppLocker`, `Lock` | `pkg/xdal/locking.go` | Named locks (`DB.Lock`/`TryLock`) and per-dialect implementations; row lock rendering |
| `Schema`, `CreateTable`, `AlterTable`, `CreateIndex`, ... | `pkg/xdal/schema.go` | Fluent schema statements; `Statement` interface and `ExecStatements` |
| `DDLOptions`, `TypeMap` | `pkg/xdal/schema_dialect.go` | Per-dialect DDL configuration and the `BaseDialect.Build*` DDL methods |
| `migrate.Migrator` | `pkg/migrate/migrate.go` | Applies, reverts and reports migrations for one namespace |

### How Drivers Work

Each driver is ~27 lines: a struct embedding `*xdal.BaseDB` (promoting all DBInterface methods), a constructor accepting `xdal.Logger`, and `NewQueryBuilder()` using the driver's dialect. No forwarding methods — Go embedding handles promotion.

### How Dialects Work

Query structs hold a `Dialect` reference. `Build()` delegates to `dialect.BuildXxx(q)` and returns `(string, []interface{}, error)`. `BaseDialect` is configured entirely via function fields (`Placeholder`, `AppendLimit`, `AppendReturning`, `PrependReturning`) and style flags (`QuoteStyle`, `BackslashEscapes`, `UpsertStyle`, `LockStyle`), plus `DialectName`, `Caps`, `AppLocker` and `DDL` (a `DDLOptions` with the `TypeMap`). Drivers configure hooks in their constructors — no need to override Build methods.

`BaseDialect` methods cannot see overrides on the embedding driver struct, so anything DDL generation needs from an override is passed in as a field (for example `DDL.BoolLiteral = d.BoolLiteral`).

## Build & Test Commands

```bash
# Unit tests (no dependencies)
go test ./pkg/... -v

# Integration tests (requires Docker containers running)
docker compose -f docker-compose.test.yml up -d
go test ./tests/integration/ -v -timeout 120s

# Vet
go vet ./...

# Build (library, no binary)
go build ./...
```

Or use Taskfile:

```bash
task test              # unit tests
task docker-up         # start test databases
task test-integration  # integration tests
task docker-down       # stop test databases
```

## Docker Containers

Integration tests use three Docker containers:

| Database | Port | Credentials |
|----------|------|-------------|
| MySQL 8.0 | 13306 | root:testpass |
| PostgreSQL 16 | 15432 | xdal:testpass |
| MSSQL 2022 | 11433 | sa:TestPass123! |

SQLite uses in-memory databases (no Docker).

## Coding Conventions

- GoDoc comments on exported types and methods only — no inline code comments unless requested
- Use Go struct embedding for shared behavior (never write forwarding methods)
- Every driver must have `var _ xdal.DBInterface = (*XxxDB)(nil)` for compile-time check
- Fluent API: all query builder methods return their query type pointer
- Quote-aware placeholder replacement: `?` inside single/double-quoted strings must be skipped
- Use `t.Run(subtest)` for multi-database integration tests
- Use ordered slices (not maps) for column/value pairs to ensure deterministic SQL
- `Build()` returns `(string, []interface{}, error)` — always handle the error
- `In()` returns `(InValues, error)` — always handle the error

## Common Tasks

### Adding a feature to the query builder

1. Add field(s) to the query struct in `pkg/xdal/types.go`
2. Add fluent method(s) in `pkg/xdal/query_builder.go`
3. Update `BaseDialect.BuildXxx()` in `pkg/xdal/dialect.go` — return errors for validation
4. Add unit tests in `pkg/xdal/query_builder_test.go`
5. Add integration tests if the feature interacts with real databases

### Adding a new database driver

1. Create `pkg/yourdb/yourdb.go` — struct embedding `*xdal.BaseDB`, constructor taking `xdal.Logger`, `NewQueryBuilder`
2. Create `pkg/yourdb/dialect.go` — `NewDialect()` returning configured `BaseDialect` with function fields
3. Add interface compliance test: `var _ xdal.DBInterface = (*YourDB)(nil)`
4. Add unit tests for the driver package
5. Add Docker service to `docker-compose.test.yml`
6. Add connection setup and tests in `tests/integration/`

See [docs/contributing.md](docs/contributing.md) for the full guide.

## Portability Gotchas

- PostgreSQL uses `$1, $2...`, MSSQL uses `@p1, @p2...`, MySQL/SQLite use `?`
- MSSQL uses `OFFSET X ROWS FETCH NEXT Y ROWS ONLY` instead of `LIMIT X OFFSET Y`
- PostgreSQL booleans: `TRUE`/`FALSE` in SQL; MSSQL: `1`/`0`; SQLite: `1`/`0`
- MSSQL requires creating the database explicitly after container startup
- `modernc.org/sqlite` registers as `"sqlite"` not `"sqlite3"`
- Identifier quoting: MySQL = backticks, PostgreSQL/SQLite = double quotes, MSSQL = brackets
- MySQL does not support RETURNING — use `LastInsertId()` instead
- MySQL upserts use the row alias syntax (8.0.19+); `DoNothing` assigns a table-qualified column to itself, because an unqualified name is ambiguous next to the alias
- MySQL commits DDL implicitly (`TransactionalDDL` is false); `pkg/migrate` records such migrations as dirty until they finish
- MySQL has no `CREATE INDEX IF NOT EXISTS` or `DROP INDEX IF EXISTS`; `CreateTable` indexes are declared inline instead
- SQL Server upserts use `MERGE ... WITH (HOLDLOCK)`, guards use `IF OBJECT_ID(...)`, renames use `sp_rename`, and Restrict renders as `NO ACTION`
- SQLite has no row locks (`ForUpdate` is ignored) and its named locks are process-local (`ProcessLocker`)
- SQLite `:memory:` gives each pooled connection its own database; tests that use transactions or locks call `SetMaxOpenConns(1)`
- Named locks on server databases hold a dedicated `*sql.Conn`; a failed release discards that connection rather than returning it to the pool

## File Map

| File | What to change for |
|------|-------------------|
| `pkg/xdal/types.go` | New query struct fields, interface changes, error vars |
| `pkg/xdal/query_builder.go` | New fluent methods, In() helper, WhereGroup |
| `pkg/xdal/dialect.go` | SQL generation, quoting, placeholder translation, SafeIdentifier |
| `pkg/xdal/expressions.go` | Portable SQL expression helpers (ConcatExpr, LengthExpr, etc.) — methods on BaseDialect + QueryBuilder wrappers |
| `pkg/xdal/logger.go` | Logging, Tx wrapper, BaseDB methods, DBExecutor, WithTx |
| `pkg/xdal/capabilities.go` | Dialect names and capability flags |
| `pkg/xdal/upsert.go` | OnConflict/DoUpdate/DoNothing and upsert rendering (ON CONFLICT, ON DUPLICATE KEY, MERGE) |
| `pkg/xdal/locking.go` | Row lock methods and rendering, AppLocker implementations, DB.Lock/TryLock |
| `pkg/xdal/schema.go` | Schema statement builders and column options |
| `pkg/xdal/schema_dialect.go` | DDL generation, TypeMap, DDLOptions |
| `pkg/xdal/dialects_test.go` | Exact SQL expected from each of the four dialects |
| `pkg/migrate/migrate.go` | Migration runner |
| `pkg/*/yourdb.go` | Driver constructor, NewQueryBuilder |
| `pkg/*/dialect.go` | Driver-specific dialect config (function fields) + expression overrides via embedded dialect struct |
| `tests/integration/helpers.go` | Database connection setup, schema, seed data |
| `tests/integration/*.go` | Integration test cases |
| `docker-compose.test.yml` | Test database containers |

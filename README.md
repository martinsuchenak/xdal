# XDAL

A lightweight, interface-driven database abstraction layer for Go.

Write database-agnostic SQL queries across **MySQL**, **PostgreSQL**, **SQLite**, and **SQL Server** with a fluent query builder, automatic placeholder translation, identifier quoting, and injectable structured logging.

## Install

```bash
go get github.com/martinsuchenak/xdal
```

## Quick Example

```go
import (
    "github.com/martinsuchenak/xdal/pkg/mysql"
)

db := mysql.NewMySQLDB(sqlDB, nil)
qb := mysql.NewQueryBuilder()

query, args, err := qb.Select("id", "name").
    From("users").
    Where("active = ?", true).
    OrWhere("role = ?", "admin").
    OrderBy("name").
    Limit(10).
    Build()
if err != nil {
    log.Fatal(err)
}

rows, _ := db.Query(ctx, query, args...)
```

## What It Does

- **Fluent query builder** — SELECT, INSERT, UPDATE, DELETE with JOINs, GROUP BY, HAVING, LIMIT/OFFSET, DISTINCT, OR, IN-clause expansion, batch INSERT, and RETURNING
- **Automatic dialect translation** — placeholders (`?` / `$1` / `@p1`), LIMIT/OFFSET vs FETCH NEXT, identifier quoting (backticks / double quotes / brackets)
- **Structured logging** — inject any logger matching the `Logger` interface, or run silently
- **Transaction wrapper** — `BeginTx` returns a `*Tx` with full logging
- **Upserts** — `OnConflict(...).DoUpdate()` / `DoNothing()` rendered as `ON CONFLICT`, `ON DUPLICATE KEY UPDATE` or `MERGE`
- **Locking** — row locks with `ForUpdate`, `SkipLocked` and `NoWait`, plus named locks (`Lock`, `TryLock`) backed by advisory locks, `GET_LOCK` or `sp_getapplock`
- **Schema builder** — `CREATE TABLE`, `ALTER TABLE`, indexes and foreign keys with portable column types
- **Migrations** — `pkg/migrate` runs versioned, namespaced migrations under a lock, with rollback and dirty-state handling for MySQL
- **Capabilities** — `Dialect().Capabilities()` reports optional features so code can branch on features, not database names
- **Extensible** — implement the `Dialect` interface to add new databases without touching shared code

## Supported Databases

| Database | Package | Placeholders | Quoting |
|----------|---------|-------------|---------|
| MySQL | `pkg/mysql` | `?` | `` `backticks` `` |
| PostgreSQL | `pkg/postgres` | `$1, $2, ...` | `"double quotes"` |
| SQLite | `pkg/sqlite` | `?` | `"double quotes"` |
| SQL Server | `pkg/mssql` | `@p1, @p2, ...` | `[brackets]` |

## Documentation

- **[Usage Guide](docs/usage.md)** — detailed API reference for the query builder, logging, transactions, and dialect system
- **[Examples](docs/examples.md)** — comprehensive code examples for every feature
- **[Contributing](docs/contributing.md)** — development setup, running tests, and adding new database drivers

## License

[MIT](LICENSE)

# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Changes before v0.1.0 are recorded in the [git history](https://github.com/martinsuchenak/xdal/commits/main).

## [Unreleased]

## [0.1.0] - 2026-10-13

### Added

- Upserts: `InsertQuery.OnConflict(cols...)` with `DoUpdate(cols...)` or `DoNothing()`,
  for single-row and batch inserts. Rendered as `ON CONFLICT` (PostgreSQL, SQLite 3.24+),
  `INSERT ... AS xdal_new ON DUPLICATE KEY UPDATE` (MySQL 8.0.19+) or
  `MERGE ... WITH (HOLDLOCK)` (SQL Server). `Returning()` works with upserts where the
  database supports it.
- Row locks: `SelectQuery.ForUpdate()`, `SkipLocked()` and `NoWait()`, rendered as
  `FOR UPDATE [SKIP LOCKED | NOWAIT]` (PostgreSQL, MySQL) or
  `WITH (UPDLOCK, ROWLOCK[, READPAST | NOWAIT])` (SQL Server). Ignored on SQLite.
- Named locks: `DB.Lock(ctx, name)` and `DB.TryLock(ctx, name)`, using advisory locks
  (PostgreSQL), `GET_LOCK` (MySQL), `sp_getapplock` (SQL Server) or an in-process lock
  (SQLite). `Lock.Alive(ctx)` checks that the holding session is still connected.
- Schema builder: `DB.Schema()` with `CreateTable`, `AlterTable`, `DropTable`,
  `CreateIndex` and `DropIndex`. It has portable column types (String, Text, Integer,
  BigInteger, Boolean, Float, Decimal, Timestamp, Date, UUID, JSON, Binary, Blob,
  AutoIncrement), defaults, primary and unique keys, and foreign keys with
  `ON DELETE`/`ON UPDATE` actions. `xdal.ExecStatements` builds and runs statements in order.
- `pkg/migrate`: a migration runner with versioned migrations and per-namespace
  histories in one tracking table.
  - A lock makes concurrent runs apply each migration exactly once.
  - Each migration runs in its own transaction.
  - On MySQL, where DDL commits implicitly, a failed migration is recorded as dirty
    until `Resolve` is called.
  - Validates checksums, migrations recorded in the database but not defined, and
    out-of-order migrations.
  - Provides `Up`, `UpTo`, `Down`, `DownTo`, `Pending` and `Status`.
- `Dialect.Name()` and `Dialect.Capabilities()`, which report `Returning`, `Upsert`,
  `RowLocks`, `SkipLocked`, `TransactionalDDL` and `SharedAppLocks`.
- Integration tests for the new features against SQLite, PostgreSQL, MySQL and SQL Server.

### Changed

- **Breaking:** the `Dialect` interface gains `Name`, `Capabilities`, `Locker` and the
  `Build*` schema methods. `DBInterface` gains `Schema`, `Lock` and `TryLock`. Custom
  dialects that embed `*xdal.BaseDialect`, and drivers that embed `*xdal.BaseDB`, get
  these automatically. Other implementations must add them.
- `BaseDialect` has new configuration fields: `DialectName`, `Caps`, `UpsertStyle`,
  `LockStyle`, `AppLocker` and `DDL`. The built-in drivers set them all.
- `BuildInsert` now builds its value groups in one shared path for single-row and batch
  inserts. The SQL it produces for existing queries is unchanged.

[Unreleased]: https://github.com/martinsuchenak/xdal/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/martinsuchenak/xdal/compare/v0.0.7...v0.1.0

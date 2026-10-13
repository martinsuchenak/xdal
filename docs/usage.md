# Usage Guide

## Creating a Database Connection

Each driver wraps a standard `*sql.DB` and accepts a `xdal.Logger` (pass `nil` to disable logging):

```go
import (
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
    "github.com/martinsuchenak/xdal/pkg/mysql"
)

sqlDB, _ := sql.Open("mysql", "user:pass@tcp(localhost:3306)/mydb")

// Silent (no logging)
db := mysql.NewMySQLDB(sqlDB, nil)

// With logging
db := mysql.NewMySQLDB(sqlDB, myLogger)
```

All drivers follow the same pattern:

```go
mysql.NewMySQLDB(sqlDB, logger)
postgres.NewPostgresDB(sqlDB, logger)
sqlite.NewSQLiteDB(sqlDB, logger)
mssql.NewMSSQLDB(sqlDB, logger)
```

### Health Check

```go
if err := db.Ping(ctx); err != nil {
    log.Fatal("database unreachable")
}
```

### Accessing the Underlying *sql.DB

```go
sqlDB := db.DB()
```

### Direct Execution

The `db` instance provides factory methods (`Select`, `Insert`, `Update`, `Delete`) that return query objects pre-wired for direct execution. No separate `QueryBuilder` needed:

```go
// INSERT — build + execute in one chain
result, err := db.Insert("users").
    Set("name", "Alice").
    Set("email", "alice@example.com").
    Exec(ctx)

// SELECT — build + query in one chain
rows, err := db.Select("id", "name").
    From("users").
    Where("active = ?", true).
    Query(ctx)

// SELECT single row
var name string
err := db.Select("name").
    From("users").
    Where("id = ?", 42).
    QueryRow(ctx).Scan(&name)

// UPDATE
result, err := db.Update("users").
    Set("email", "new@example.com").
    Where("id = ?", 42).
    Exec(ctx)

// DELETE
result, err := db.Delete("users").Where("id = ?", 42).Exec(ctx)

// INSERT with RETURNING (PostgreSQL)
var id int
err := db.Insert("users").
    Set("name", "Alice").
    Returning("id").
    QueryRow(ctx).Scan(&id)
```

You can still call `Build()` on any query for inspection, logging, or deferred execution:

```go
q := db.Select("name").From("users").Where("id = ?", 42)
query, args, err := q.Build()  // just build, don't execute
```

---

## Query Builder

Each driver provides `NewQueryBuilder()` pre-configured with the correct dialect:

```go
qb := mysql.NewQueryBuilder()
```

You can also construct one directly if needed:

```go
import "github.com/martinsuchenak/xdal/pkg/xdal"

d := &xdal.BaseDialect{
    Placeholder: xdal.QuestionMarkPlaceholder,
    AppendLimit: xdal.LimitOffset,
    QuoteStyle:  xdal.BacktickQuoting,
}
// Enable RETURNING (optional):
// d.AppendReturning = d.WriteReturning
qb := xdal.NewQueryBuilder(d)
```

### SELECT

```go
query, args, err := qb.Select("id", "name", "email").
    From("users").
    Where("active = ?", true).
    OrderBy("name").
    Limit(10).
    Offset(20).
    Build()
```

For `SELECT *`:

```go
query, args, err := qb.SelectAll().From("users").Build()
```

For `SELECT DISTINCT`:

```go
query, args, err := qb.Select("name").Distinct().From("users").Build()
```

### WHERE Clauses

Multiple `Where` calls are combined with AND:

```go
qb.Where("active = ?", true).Where("age > ?", 18)
// WHERE active = ? AND age > ?
```

Use `OrWhere` for OR conditions:

```go
qb.Where("active = ?", true).OrWhere("role = ?", "admin")
// WHERE active = ? OR role = ?
```

Mix AND and OR:

```go
qb.Where("a = ?", 1).OrWhere("b = ?", 2).Where("c = ?", 3)
// WHERE a = ? OR b = ? AND c = ?
```

### WHERE Groups

Use `WhereGroup` to create parenthesized groups of conditions:

```go
qb.Where("active = ?", true).
    WhereGroup(func(g *xdal.WhereGroup) {
        g.Where("role = ?", "admin").OrWhere("role = ?", "moderator")
    })
// WHERE active = ? AND (role = ? OR role = ?)
```

Use `OrWhereGroup` to combine a group with OR:

```go
qb.Where("a = ?", 1).
    OrWhereGroup(func(g *xdal.WhereGroup) {
        g.Where("b = ?", 2).Where("c = ?", 3)
    })
// WHERE a = ? OR (b = ? AND c = ?)
```

### WHERE Shortcuts

```go
// IS NULL
qb.WhereIsNull("deleted_at")
// WHERE deleted_at IS NULL

// IS NOT NULL
qb.WhereIsNotNull("email")
// WHERE email IS NOT NULL

// BETWEEN
qb.WhereBetween("age", 18, 65)
// WHERE age BETWEEN ? AND ?  with args [18, 65]
```

### IN Clause

Use `xdal.In()` to expand a single placeholder into multiple values:

```go
inVals, err := xdal.In(1, 2, 3)
if err != nil {
    return err
}
qb.Where("id IN (?)", inVals)
// WHERE id IN (?, ?, ?) with args [1, 2, 3]
```

`In()` returns an error if no values are provided or if the count exceeds 1000 (`xdal.MaxInValues`).

### JOINs

```go
query, args, err := qb.Select("u.name", "o.total").
    From("users u").
    Join("INNER JOIN orders o ON o.user_id = u.id").
    Where("o.total > ?", 100).
    Build()
```

Multiple joins:

```go
qb.Join("INNER JOIN orders o ON o.user_id = u.id").
    Join("INNER JOIN products p ON p.id = o.product_id")
```

### GROUP BY and HAVING

```go
query, args, err := qb.Select("active", "COUNT(*) as cnt").
    From("users").
    GroupBy("active").
    Having("COUNT(*) > ?", 1).
    Build()
```

Use `OrHaving` for OR conditions in the HAVING clause:

```go
qb.Having("COUNT(*) > ?", 10).OrHaving("AVG(score) < ?", 5)
// HAVING COUNT(*) > ? OR AVG(score) < ?
```

### INSERT

Single row using `Set`:

```go
query, args, err := qb.Insert("users").
    Set("name", "Alice").
    Set("email", "alice@example.com").
    Build()
// INSERT INTO `users` (`name`, `email`) VALUES (?, ?)
```

Single row using `SetMap` (keys are sorted for deterministic output):

```go
query, args, err := qb.Insert("users").
    SetMap(map[string]interface{}{
        "name":  "Alice",
        "email": "alice@example.com",
    }).
    Build()
// INSERT INTO `users` (`email`, `name`) VALUES (?, ?)
```

Single row using `SetStruct` (reads `db` struct tags, falls back to snake_case):

```go
type User struct {
    Name  string `db:"name"`
    Email string `db:"email"`
}

query, args, err := qb.Insert("users").
    SetStruct(User{Name: "Alice", Email: "alice@example.com"}).
    Build()
// INSERT INTO `users` (`name`, `email`) VALUES (?, ?)
```

Multi-row using `Columns` and `Values`:

```go
query, args, err := qb.Insert("users").
    Columns("name", "email").
    Values("Alice", "alice@example.com").
    Values("Bob", "bob@example.com").
    Values("Charlie", "charlie@example.com").
    Build()
// INSERT INTO `users` (`name`, `email`) VALUES (?, ?), (?, ?), (?, ?)
```

### INSERT RETURNING

For PostgreSQL (`RETURNING`), SQLite (`RETURNING`), and SQL Server (`OUTPUT INSERTED.*`):

```go
query, args, err := qb.Insert("users").
    Set("name", "Alice").
    Returning("id").
    Build()

// PostgreSQL: INSERT INTO "users" ("name") VALUES ($1) RETURNING "id"
// SQLite:     INSERT INTO "users" ("name") VALUES (?) RETURNING "id"
// MSSQL:      INSERT INTO [users] ([name]) OUTPUT INSERTED.[id] VALUES (@p1)
// MySQL:      returns ErrReturningNotSupported (use LastInsertId instead)
```

### UPDATE

```go
query, args, err := qb.Update("users").
    Set("email", "new@example.com").
    Set("active", false).
    Where("id = ?", 42).
    Build()
```

With `SetMap`:

```go
query, args, err := qb.Update("users").
    SetMap(map[string]interface{}{
        "email":  "new@example.com",
        "active": false,
    }).
    Where("id = ?", 42).
    Build()
```

With `SetStruct`:

```go
type UserUpdate struct {
    Email  string `db:"email"`
    Active bool   `db:"active"`
}

query, args, err := qb.Update("users").
    SetStruct(UserUpdate{Email: "new@example.com", Active: false}).
    Where("id = ?", 42).
    Build()
```

### UPDATE RETURNING

For PostgreSQL, SQLite, and SQL Server, use `Returning()` on UPDATE:

```go
query, args, err := qb.Update("users").
    Set("email", "new@example.com").
    Where("id = ?", 42).
    Returning("id", "email").
    Build()

// PostgreSQL: UPDATE "users" SET "email" = $1 WHERE "id" = $2 RETURNING "id", "email"
// MSSQL:      UPDATE [users] SET [email] = @p1 OUTPUT INSERTED.[id], INSERTED.[email] WHERE [id] = @p2
```

### DELETE

```go
query, args, err := qb.Delete("users").
    Where("active = ?", false).
    OrWhere("last_login < ?", "2020-01-01").
    Build()
```

Delete all rows (no WHERE):

```go
query, args, err := qb.Delete("users").Build()
// DELETE FROM `users`
```

### DELETE RETURNING

For PostgreSQL, SQLite, and SQL Server, use `Returning()` on DELETE:

```go
query, args, err := qb.Delete("users").
    Where("active = ?", false).
    Returning("id", "name").
    Build()

// PostgreSQL: DELETE FROM "users" WHERE "active" = $1 RETURNING "id", "name"
// MSSQL:      DELETE FROM [users] OUTPUT DELETED.[id], DELETED.[name] WHERE [active] = @p1
```

### Upsert

`OnConflict` turns an INSERT into an upsert keyed on a unique column set. Combine it with `DoUpdate` or `DoNothing`:

```go
// Update the listed columns of the existing row
db.Insert("devices").Set("mac", mac).Set("ip", ip).Set("last_seen", now).
    OnConflict("mac").DoUpdate("ip", "last_seen").Exec(ctx)

// DoUpdate() with no columns updates every inserted column except the conflict columns
db.Insert("devices").Set("mac", mac).Set("ip", ip).OnConflict("mac").DoUpdate().Exec(ctx)

// Keep existing rows; works with batch inserts
db.Insert("tags").Columns("device_id", "tag").
    Values("d1", "web").Values("d1", "prod").
    OnConflict("device_id", "tag").DoNothing().Exec(ctx)
```

| Database | Rendered as |
|----------|-------------|
| PostgreSQL, SQLite (3.24+) | `INSERT ... ON CONFLICT (cols) DO UPDATE SET c = excluded.c` / `DO NOTHING` |
| MySQL (8.0.19+) | `INSERT ... AS xdal_new ON DUPLICATE KEY UPDATE c = xdal_new.c` |
| SQL Server | `MERGE ... WITH (HOLDLOCK) ... WHEN MATCHED THEN UPDATE ... WHEN NOT MATCHED THEN INSERT ...;` |

Conflict and `DoUpdate` columns must be among the inserted columns (`xdal.ErrUpsertColumnNotInserted` otherwise), so an upsert never overwrites a column with its default. MySQL ignores the conflict column list and reacts to any unique key, so list the key you expect to conflict on. `Returning()` works with upserts on PostgreSQL, SQLite and SQL Server.

### Row Locks

Inside a transaction, `ForUpdate`, `SkipLocked` and `NoWait` lock the selected rows:

```go
// Each worker claims a different batch of rows from a shared queue
q, args, _ := qb.Select("id").From("jobs").Where("state = ?", "new").
    OrderBy("id").Limit(10).SkipLocked().Build()
rows, err := tx.Query(ctx, q, args...)
```

| Database | `ForUpdate` | `SkipLocked` | `NoWait` |
|----------|-------------|--------------|----------|
| PostgreSQL, MySQL 8+ | `FOR UPDATE` | `FOR UPDATE SKIP LOCKED` | `FOR UPDATE NOWAIT` |
| SQL Server | `WITH (UPDLOCK, ROWLOCK)` | `WITH (UPDLOCK, ROWLOCK, READPAST)` | `WITH (UPDLOCK, ROWLOCK, NOWAIT)` |
| SQLite | ignored | ignored | ignored |

SQLite has no row locks; it serializes writers instead. Check `Dialect().Capabilities().RowLocks` when the difference matters.

### SetStruct Tag Conventions

`SetStruct` reads struct fields using these rules:

| Tag | Behavior |
|-----|----------|
| `db:"column_name"` | Use `column_name` as the column |
| `db:"-"` | Skip this field |
| *(no tag)* | Use Go field name converted to `snake_case` |
| *(unexported)* | Always skipped |

Pointer fields with `nil` values are skipped (useful for optional UPDATE columns). Non-nil pointers are dereferenced automatically.

```go
type User struct {
    ID        string  `db:"id"`         // column: id
    FullName  string                     // column: full_name (snake_case)
    Email     string  `db:"email"`      // column: email
    Password  string  `db:"-"`          // skipped
    secret    string                    // skipped (unexported)
    LockedAt  *string `db:"locked_at"`  // skipped when nil, included when set
}
```

`SetMap` and `SetStruct` can be mixed with `Set` calls — they all append to the same column/value list.

### Executing Queries

```go
// Query builder produces (string, []interface{}, error)
query, args, err := qb.Select("name").From("users").Where("id = ?", 1).Build()
if err != nil {
    return err
}

// Execute
rows, err := db.Query(ctx, query, args...)
defer rows.Close()

var name string
var email string
for rows.Next() {
    rows.Scan(&name, &email)
}

// Single row
err = db.QueryRow(ctx, query, args...).Scan(&name)

// Write operations
result, err := db.Exec(ctx, query, args...)
```

### Using DBExecutor

Both `BaseDB` and `Tx` satisfy the `DBExecutor` interface, so you can write functions that work with either:

```go
func getUser(ctx context.Context, db xdal.DBExecutor, id int) (string, error) {
    qb := mysql.NewQueryBuilder()
    query, args, err := qb.Select("name").From("users").Where("id = ?", id).Build()
    if err != nil {
        return "", err
    }
    var name string
    err = db.QueryRow(ctx, query, args...).Scan(&name)
    return name, err
}
```

---

## Transactions

### Manual Transactions

`BeginTx` returns a `*xdal.Tx` wrapper with logging. Use `tx.Exec`, `tx.Query`, `tx.QueryRow` instead of the raw `*sql.Tx` methods:

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    log.Fatal(err)
}
defer tx.Rollback()

tx.Exec(ctx, "INSERT INTO users (name) VALUES (?)", "Alice")
tx.Exec(ctx, "INSERT INTO users (name) VALUES (?)", "Bob")

if err := tx.Commit(); err != nil {
    log.Fatal(err)
}
```

### WithTx Helper

`WithTx` handles begin/commit/rollback automatically:

```go
err := db.WithTx(ctx, nil, func(tx *xdal.Tx) error {
    if _, err := tx.Exec(ctx, "INSERT INTO users (name) VALUES (?)", "Alice"); err != nil {
        return err
    }
    if _, err := tx.Exec(ctx, "INSERT INTO users (name) VALUES (?)", "Bob"); err != nil {
        return err
    }
    return nil
})
```

If the callback returns an error, the transaction is rolled back. If it returns `nil`, the transaction is committed.

---

## Logging

### Logger Interface

```go
type Logger interface {
    Trace(msg string, keysAndValues ...any)
    Debug(msg string, keysAndValues ...any)
    Info(msg string, keysAndValues ...any)
    Warn(msg string, keysAndValues ...any)
    Error(msg string, keysAndValues ...any)
    Fatal(msg string, keysAndValues ...any)
}
```

This is structurally identical to `github.com/fortix/go-libs/logger` — pass it directly, no adapter needed.

### Setting a Logger

```go
// At creation
db := mysql.NewMySQLDB(sqlDB, myLogger)

// Change at runtime
db.SetLogger(anotherLogger)

// Disable logging
db.SetLogger(nil)
```

### Controlling Argument Logging

By default, query arguments are **redacted** in log output (shown as `<redacted>`). To include actual values:

```go
db.SetLogArgs(true)
```

### What Gets Logged

| Operation | Level | Fields |
|-----------|-------|--------|
| Exec start | Debug | `query`, `args` |
| Exec done | Debug | `query`, `duration` |
| Exec error | Error | `query`, `error`, `duration` |
| Query start | Debug | `query`, `args` |
| Query done | Debug | `query`, `duration` |
| Query error | Error | `query`, `error`, `duration` |
| QueryRow | Debug | `query`, `args` |
| BeginTx | Debug | — |
| Commit | Debug | — |
| Rollback | Debug | — |
| Ping | Debug | — |
| Close | Debug | — |

Transaction operations are prefixed with `tx ` (e.g., `tx exec`, `tx query`, `tx commit`).

---

## Dialect System

### How It Works

The query builder delegates SQL generation to a `Dialect` interface:

```go
type Dialect interface {
    BuildSelect(q *SelectQuery) (string, []interface{}, error)
    BuildInsert(q *InsertQuery) (string, []interface{}, error)
    BuildUpdate(q *UpdateQuery) (string, []interface{}, error)
    BuildDelete(q *DeleteQuery) (string, []interface{}, error)
    QuoteIdentifier(name string) string
    SupportsReturning() bool
    TranslateSQL(query string) string
    ConcatExpr(parts ...string) string
    LengthExpr(col string) string
    CurrentTimestamp() string
    BoolLiteral(v bool) string
    StringAggExpr(col, sep string) string
    RandExpr() string
}
```

Each query struct holds a `Dialect` reference. When you call `Build()`, it forwards to `dialect.BuildXxx(q)`.

### BaseDialect

`BaseDialect` handles the common case via configurable function fields and style flags:

```go
type BaseDialect struct {
    Placeholder       func(idx int) string                                           // e.g. QuestionMarkPlaceholder
    AppendLimit       func(b *strings.Builder, orderBy []string, limit, offset *int64) // e.g. LimitOffset
    AppendReturning   func(b *strings.Builder, columns []string)                     // e.g. d.WriteReturning
    PrependReturning  func(b *strings.Builder, columns []string)                     // e.g. d.WriteOutput
    QuoteStyle        QuoteStyle                                                     // BacktickQuoting, DoubleQuoteQuoting, etc.
    BackslashEscapes  bool                                                           // true for MySQL
}
```

Set RETURNING hooks after construction (can't reference methods in struct literal):

```go
// PostgreSQL/SQLite
d := &xdal.BaseDialect{...}
d.AppendReturning = d.WriteReturning

// MSSQL
d := &xdal.BaseDialect{...}
d.PrependReturning = d.WriteOutput   // INSERT: OUTPUT before VALUES
d.AppendReturning = d.WriteOutput    // UPDATE/DELETE: OUTPUT after WHERE

// MySQL — don't set any returning hooks
```

### Placeholder Functions

Write queries using `?` regardless of the database. The dialect translates:

| Function | Dialect | Input | Output |
|----------|---------|-------|--------|
| `QuestionMarkPlaceholder` | MySQL / SQLite | `WHERE id = ?` | `WHERE id = ?` |
| `DollarPlaceholder` | PostgreSQL | `WHERE id = ?` | `WHERE id = $1` |
| `AtPPlaceholder` | SQL Server | `WHERE id = ?` | `WHERE id = @p1` |

Placeholder numbering is automatic and correct across multiple WHERE/HAVING clauses.

The translation is **quote-aware** — `?` inside single-quoted or double-quoted string literals is left untouched:

```go
qb.Where("name = '?' AND id = ?", 42)
// PostgreSQL: WHERE name = '?' AND id = $1
```

### TranslateSQL

For SQL that can't be expressed through the query builder (e.g., column expressions like `col=col+1`, subqueries like `SELECT EXISTS(...)`), use `TranslateSQL` to translate `?` placeholders to the dialect's format:

```go
// Column expression — can't use Set() for col=col+1
query := qb.TranslateSQL("UPDATE users SET failed_login_attempts=failed_login_attempts+1, updated_at=" + qb.CurrentTimestamp() + " WHERE id = ?")
result, err := db.Exec(ctx, query, userID)

// Subquery
query := qb.TranslateSQL("SELECT EXISTS(SELECT 1 FROM users WHERE email = ? AND active = ?)")
var exists bool
db.QueryRow(ctx, query, email, true).Scan(&exists)
```

| Dialect | Input | Output |
|---------|-------|--------|
| MySQL / SQLite | `WHERE id = ?` | `WHERE id = ?` |
| PostgreSQL | `WHERE id = ?` | `WHERE id = $1` |
| SQL Server | `WHERE id = ?` | `WHERE id = @p1` |

Like the query builder, `TranslateSQL` is quote-aware and skips `?` inside string literals. It's available on both `Dialect` and `QueryBuilder`:

```go
dialect.TranslateSQL(query)  // on the dialect directly
qb.TranslateSQL(query)       // convenience wrapper
```

### Identifier Quoting

The dialect automatically quotes table and column names based on `QuoteStyle`:

| QuoteStyle | MySQL | PostgreSQL | MSSQL |
|------------|-------|------------|-------|
| BacktickQuoting | `` `users` `` | — | — |
| DoubleQuoteQuoting | — | `"users"` | — |
| BracketQuoting | — | — | `[users]` |

Quoting is skipped for expressions containing spaces, parentheses, commas, or `AS` (aliases, function calls, raw SQL).

### LIMIT/OFFSET Handling

| Function | Generated SQL |
|----------|---------------|
| `LimitOffset` | `LIMIT 10 OFFSET 20` |
| `FetchNextLimit` | `OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY` |

### RETURNING/OUTPUT Handling

| Hook | Dialects | INSERT | UPDATE | DELETE |
|------|----------|--------|--------|--------|
| none | MySQL | not supported | not supported | not supported |
| `AppendReturning = WriteReturning` | PostgreSQL, SQLite | `... VALUES (...) RETURNING col` | `... WHERE ... RETURNING col` | `... WHERE ... RETURNING col` |
| `PrependReturning = WriteOutput` | SQL Server | `... OUTPUT INSERTED.col VALUES (...)` | — | — |
| `AppendReturning = WriteOutput` | SQL Server | — | `... OUTPUT INSERTED.col WHERE ...` | — |
| `AppendDeletedReturning = WriteDeletedOutput` | SQL Server | — | — | `... OUTPUT DELETED.col WHERE ...` |

### SafeIdentifier

Use `xdal.SafeIdentifier()` to validate that a table or column name is safe (letters, digits, underscores, dots only):

```go
if err := xdal.SafeIdentifier(userInput); err != nil {
    return fmt.Errorf("invalid identifier: %w", err)
}
```

---

## Portability Helpers

SQL expression syntax varies across databases. Expression helpers are methods on the `Dialect` interface and are exposed through `QueryBuilder` convenience wrappers — no driver-specific imports needed:

```go
qb := mysql.NewQueryBuilder()

qb.Select(qb.ConcatExpr("first_name", "' '", "last_name"))
// MySQL/PostgreSQL/SQLite: CONCAT(first_name, ' ', last_name)
// MSSQL:                   first_name + ' ' + last_name

qb.Select(qb.LengthExpr("name"))
// MySQL/PostgreSQL/SQLite: LENGTH(name)
// MSSQL:                   LEN(name)

qb.Select(qb.CurrentTimestamp())
// MySQL/PostgreSQL: NOW()
// SQLite:           datetime('now')
// MSSQL:            GETDATE()

qb.Where("active = " + qb.BoolLiteral(true))
// MySQL/PostgreSQL: active = TRUE
// SQLite/MSSQL:     active = 1

qb.Select(qb.StringAggExpr("name", "', '")).GroupBy("team")
// MySQL:      GROUP_CONCAT(name SEPARATOR ', ')
// SQLite:     GROUP_CONCAT(name, ', ')
// PostgreSQL: STRING_AGG(name, ', ')
// MSSQL:      STRING_AGG(name, ', ')

qb.Select(qb.RandExpr())
// MySQL/MSSQL:      RAND()
// PostgreSQL/SQLite: RANDOM()
```

Each query builder is pre-configured with the correct dialect, so `qb.XxxExpr()` always generates the right SQL for your database.

### Still Portable (No Helper Needed)

These expressions work identically across all four databases:

- `COALESCE(a, b)`
- `UPPER(s)`, `LOWER(s)`
- `SUBSTRING(s, start, length)`
- `CAST(x AS type)`
- `COUNT(*)`, `SUM(x)`, `AVG(x)`, `MIN(x)`, `MAX(x)`

### Still Database-Specific

These vary too much for simple helpers and require database-specific SQL:

- Date/time arithmetic (`INTERVAL`, `DATEADD`, etc.)
- JSON operations
- Full-text search
- Window functions (syntax varies in edge cases)

## Capabilities

Each dialect reports its name and optional features, so code can branch on a feature rather than a database name:

```go
d := db.Dialect()
d.Name()                          // "sqlite", "postgres", "mysql" or "mssql"
d.Capabilities().TransactionalDDL // false on MySQL, which commits DDL implicitly
```

| Capability | SQLite | PostgreSQL | MySQL | SQL Server |
|------------|--------|------------|-------|------------|
| `Returning` | yes | yes | no | yes |
| `Upsert` | yes | yes | yes | yes |
| `RowLocks` | no | yes | yes | yes |
| `SkipLocked` | no | yes | yes | yes |
| `TransactionalDDL` | yes | yes | no | yes |
| `SharedAppLocks` | no (process-local) | yes | yes | yes |

## Named Locks

`Lock` and `TryLock` take an exclusive lock by name, for leader election or to keep a job from running twice:

```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()

lock, err := db.Lock(ctx, "nightly-report")   // waits until free or ctx is done
if err != nil {
    return err
}
defer lock.Release(context.Background())

lock, ok, err := db.TryLock(ctx, "nightly-report") // returns ok=false at once if held
```

| Database | Implementation |
|----------|----------------|
| PostgreSQL | `pg_try_advisory_lock` on a 64-bit hash of the name |
| MySQL | `GET_LOCK(name, 0)`; names over 64 characters are hashed |
| SQL Server | `sp_getapplock` with `@LockOwner = 'Session'` |
| SQLite | In-memory lock shared by users of the same `SQLiteDB` |

Server-side locks live on a dedicated connection taken from the pool and returned on `Release`. If the process dies, the database ends the session and frees the lock. If the connection drops, the lock is lost too; long-running holders call `lock.Alive(ctx)` before critical steps. Session locks do not work through PgBouncer in transaction pooling mode. SQLite locks only exclude callers in the same process, which suits SQLite's single-process use.

## Schema Builder

`db.Schema()` builds DDL with portable column types. Each statement's `Build()` returns one or more SQL strings; `xdal.ExecStatements` builds and runs them in order:

```go
s := db.Schema()

devices := s.CreateTable("devices").IfNotExists()
devices.String("id", 36).PrimaryKey()
devices.String("name", 255).NotNull()
devices.Boolean("active").NotNull().Default(true)
devices.Timestamp("created_at").NotNull().DefaultNow()
devices.String("site_id", 36).References("sites", "id").OnDelete(xdal.Cascade)
devices.Binary("ip_start", 16)
devices.Unique("name", "site_id")
devices.Index("idx_devices_ip_start", "ip_start")

err := xdal.ExecStatements(ctx, db, devices)
```

### Column Types

| Method | SQLite | PostgreSQL | MySQL | SQL Server |
|--------|--------|------------|-------|------------|
| `String(name, n)` | `TEXT` | `VARCHAR(n)` | `VARCHAR(n)` | `NVARCHAR(n)`, `NVARCHAR(MAX)` above 4000 |
| `Text` | `TEXT` | `TEXT` | `LONGTEXT` | `NVARCHAR(MAX)` |
| `Integer` | `INTEGER` | `INTEGER` | `INT` | `INT` |
| `BigInteger` | `INTEGER` | `BIGINT` | `BIGINT` | `BIGINT` |
| `Boolean` | `BOOLEAN` | `BOOLEAN` | `BOOLEAN` | `BIT` |
| `Float` | `REAL` | `DOUBLE PRECISION` | `DOUBLE` | `FLOAT(53)` |
| `Decimal(name, p, s)` | `NUMERIC(p,s)` | `NUMERIC(p,s)` | `DECIMAL(p,s)` | `DECIMAL(p,s)` |
| `Timestamp` | `TIMESTAMP` | `TIMESTAMPTZ` | `DATETIME(6)` | `DATETIME2` |
| `Date` | `DATE` | `DATE` | `DATE` | `DATE` |
| `UUID` | `TEXT` | `UUID` | `CHAR(36)` | `CHAR(36)` |
| `JSON` | `TEXT` | `JSONB` | `JSON` | `NVARCHAR(MAX)` |
| `Binary(name, n)` | `BLOB` | `BYTEA` | `VARBINARY(n)` | `VARBINARY(n)`, `VARBINARY(MAX)` above 8000 |
| `Blob` | `BLOB` | `BYTEA` | `LONGBLOB` | `VARBINARY(MAX)` |
| `AutoIncrement` | `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT GENERATED BY DEFAULT AS IDENTITY` | `BIGINT AUTO_INCREMENT` | `BIGINT IDENTITY(1,1)` |

Binary values compare bytewise on every database, so fixed-width keys (for example IP addresses stored as 16 bytes) support portable range queries and ordering. PostgreSQL rejects non-UUID values in `UUID` columns; use `String(name, 36)` for free-form IDs. `DefaultNow` renders `CURRENT_TIMESTAMP` (MySQL: `CURRENT_TIMESTAMP(6)`, SQL Server: `SYSUTCDATETIME()`); MySQL evaluates it in the session time zone.

### Column Options

`NotNull()`, `PrimaryKey()` (on several columns for a composite key, or `CreateTable.PrimaryKey(cols...)`), `Unique()`, `Default(v)` for string, bool and numeric literals, `DefaultExpr(sql)` for trusted expressions, `DefaultNow()`, and `References(table, col)` with `OnDelete`/`OnUpdate` (`xdal.Restrict`, `xdal.Cascade`, `xdal.SetNull`). Constraint names are generated as `uq_<table>_<cols>_<hash>` and `fk_<table>_<col>_<hash>`; the hash keeps names unique when underscores make them ambiguous, and long names are shortened to 63 characters. Every table, column and index name, including column lists, is checked with `SafeIdentifier`.

### Other Statements

```go
alter := s.AlterTable("devices")
alter.String("serial", 64).NotNull().Default("")
alter.String("rack_id", 36).References("racks", "id").OnDelete(xdal.SetNull)
alter.DropColumn("old").RenameColumn("name", "hostname").RenameTo("hosts")

s.CreateIndex("idx_hosts_serial", "hosts", "serial").Unique().IfNotExists()
s.DropIndex("idx_hosts_serial", "hosts").IfExists()
s.DropTable("hosts").IfExists()
```

`AlterTable` renders one statement per operation, in order, because SQLite accepts only one change per `ALTER TABLE`. It cannot add primary key, unique or `AutoIncrement` columns; add the column, then a unique index. Foreign keys added with a column are inline on SQLite and a separate `ADD CONSTRAINT` elsewhere.

MySQL has no `IF NOT EXISTS` for indexes or `IF EXISTS` for `DROP INDEX`; those return `xdal.ErrNotSupported`. Indexes declared with `CreateTable.Index` are created inside `CREATE TABLE` on MySQL, so `CreateTable.IfNotExists` still works there. SQL Server guards use `IF OBJECT_ID(...)` and `sys.indexes`, string defaults use `N'...'` literals, and `DropColumn` first drops the column's system-named default constraint.

## Migrations

Package `migrate` runs versioned migrations on any dialect:

```go
import "github.com/martinsuchenak/xdal/pkg/migrate"

migrations := []migrate.Migration{
    {
        Version: "20260101120000",
        Name:    "create_devices",
        Up: func(ctx context.Context, tx *migrate.Tx) error {
            t := tx.Schema().CreateTable("devices")
            t.String("id", 36).PrimaryKey()
            t.String("name", 255).NotNull()
            return tx.Apply(ctx, t)
        },
        Down: func(ctx context.Context, tx *migrate.Tx) error {
            return tx.Apply(ctx, tx.Schema().DropTable("devices"))
        },
    },
    {
        Version: "20260102090000",
        Name:    "add_devices_fts",
        Up: func(ctx context.Context, tx *migrate.Tx) error {
            switch tx.Dialect().Name() {
            case xdal.DialectSQLite:
                return tx.ExecSQL(ctx, `CREATE VIRTUAL TABLE devices_fts USING fts5(name)`)
            case xdal.DialectPostgres:
                return tx.ExecSQL(ctx, `CREATE INDEX idx_devices_fts ON devices USING gin (to_tsvector('simple', name))`)
            default:
                return nil
            }
        },
    },
}

m, err := migrate.New(db, migrations, migrate.Options{Namespace: "core"})
applied, err := m.Up(ctx)
```

| Method | Effect |
|--------|--------|
| `Up`, `UpTo(version)` | Apply pending migrations in version order |
| `Down(steps)`, `DownTo(version)` | Revert applied migrations, newest first (`DownTo("")` reverts all) |
| `Pending` | Validate and list what `Up` would apply |
| `Status` | Every migration with applied time, duration and dirty flag |
| `Resolve(version, applied)` | Clear a dirty record after fixing the schema by hand |

Behaviour:

- **Tracking table:** `xdal_migrations` by default (`Options.Table`), with one history per `Options.Namespace`. Independent modules can share one table.
- **Locking:** a named lock on the tracking table serializes concurrent runs, including first-time table creation, and is checked before each migration. On PostgreSQL, MySQL and SQL Server the lock holds its own connection, so the pool must allow at least two (`migrate.ErrSingleConnection` otherwise).
- **Transactions:** each migration runs in its own transaction and is recorded in the same transaction. On MySQL, where DDL commits implicitly, the record is written as dirty first and marked clean on success; a failure leaves it dirty and later runs return `migrate.ErrDirty` until `Resolve` is called.
- **Validation:** renamed or changed migrations (`ErrChecksumMismatch`), migrations recorded but not defined (`ErrUnknownApplied`, or allow with `IgnoreUnknown`), and pending migrations older than the latest applied one (`ErrOutOfOrder`, or allow with `AllowOutOfOrder`).
- **Checksums:** derived from version and name. Set `Migration.Checksum` to also detect content changes.

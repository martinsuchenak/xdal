// Package xdaltest runs one test against every database xdal supports.
//
// Run calls the test once per dialect as a subtest. SQLite always runs, on a
// file in the test's temporary directory. PostgreSQL, MySQL and SQL Server run
// when a server is configured through an environment variable and are skipped
// otherwise:
//
//	XDAL_TEST_POSTGRES_DSN  postgres://user:pass@localhost:15432/db?sslmode=disable
//	XDAL_TEST_MYSQL_DSN     user:pass@tcp(localhost:13306)/db?parseTime=true
//	XDAL_TEST_MSSQL_DSN     sqlserver://user:pass@localhost:11433?encrypt=disable
//
// Set XDAL_TEST_REQUIRE to a comma-separated list of dialect names (or "all")
// to fail instead of skip when one of them isn't configured, so CI notices a
// missing service.
//
// Every subtest gets a database of its own: a new schema on PostgreSQL and a
// new database on MySQL and SQL Server, dropped when the test ends. Tests can
// create tables freely and run in parallel.
package xdaltest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"

	"github.com/martinsuchenak/xdal/pkg/mssql"
	"github.com/martinsuchenak/xdal/pkg/mysql"
	"github.com/martinsuchenak/xdal/pkg/postgres"
	"github.com/martinsuchenak/xdal/pkg/sqlite"
	"github.com/martinsuchenak/xdal/pkg/xdal"
)

// Environment variables that configure the servers.
const (
	EnvPostgresDSN = "XDAL_TEST_POSTGRES_DSN"
	EnvMySQLDSN    = "XDAL_TEST_MYSQL_DSN"
	EnvMSSQLDSN    = "XDAL_TEST_MSSQL_DSN"
	EnvRequire     = "XDAL_TEST_REQUIRE"
)

// Dialects lists every dialect Run tries, in the order it runs them.
var Dialects = []string{xdal.DialectSQLite, xdal.DialectPostgres, xdal.DialectMySQL, xdal.DialectMSSQL}

// DB is one isolated database for a test.
type DB struct {
	// Name is the dialect name, one of the xdal.Dialect* constants.
	Name string
	// SQL is the underlying connection pool.
	SQL *sql.DB
	// DB is the xdal handle on SQL.
	DB xdal.DBInterface
	// Dialect is the dialect DB builds queries for.
	Dialect xdal.Dialect
}

// QueryBuilder returns a query builder for the database's dialect.
func (d *DB) QueryBuilder() *xdal.QueryBuilder {
	return xdal.NewQueryBuilder(d.Dialect)
}

// Run runs fn once for every available dialect, each as a subtest named after
// the dialect with a fresh, empty database.
func Run(t *testing.T, fn func(t *testing.T, db *DB)) {
	t.Helper()
	for _, name := range Dialects {
		t.Run(name, func(t *testing.T) {
			fn(t, Open(t, name))
		})
	}
}

// Open returns a fresh, empty database of the named dialect, dropped when the
// test ends. It skips the test when the dialect's server isn't configured,
// or fails it when XDAL_TEST_REQUIRE names the dialect.
func Open(t testing.TB, name string) *DB {
	t.Helper()
	switch name {
	case xdal.DialectSQLite:
		return openSQLite(t)
	case xdal.DialectPostgres:
		return openPostgres(t, configured(t, name, EnvPostgresDSN))
	case xdal.DialectMySQL:
		return openMySQL(t, configured(t, name, EnvMySQLDSN))
	case xdal.DialectMSSQL:
		return openMSSQL(t, configured(t, name, EnvMSSQLDSN))
	default:
		t.Fatalf("xdaltest: unknown dialect %q", name)
		return nil
	}
}

// configured returns the dialect's DSN, or skips (or fails) the test when
// there is none.
func configured(t testing.TB, name, env string) string {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn != "" {
		return dsn
	}
	if required(name) {
		t.Fatalf("xdaltest: %s is required by %s but %s is not set", name, EnvRequire, env)
	}
	t.Skipf("xdaltest: %s not set; skipping %s", env, name)
	return ""
}

func required(name string) bool {
	for _, r := range strings.Split(os.Getenv(EnvRequire), ",") {
		r = strings.TrimSpace(r)
		if r == "all" || r == name {
			return true
		}
	}
	return false
}

// uniqueName returns a database or schema name no other test is using.
func uniqueName() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "xdaltest_" + hex.EncodeToString(b)
}

func openSQLite(t testing.TB) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	// Foreign keys are off by default in SQLite; turn them on so the dialects agree.
	// The path goes in a URI, where '#' (in subtest names like "sqlite#01") and
	// '?' would end it early.
	uriPath := strings.NewReplacer("%", "%25", "#", "%23", "?", "%3f").Replace(path)
	db, err := sql.Open("sqlite", "file:"+uriPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("xdaltest: open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &DB{Name: xdal.DialectSQLite, SQL: db, DB: sqlite.NewSQLiteDB(db, nil), Dialect: sqlite.NewDialect()}
}

func openPostgres(t testing.TB, dsn string) *DB {
	t.Helper()
	admin := connect(t, "pgx", dsn)
	schema := uniqueName()
	mustExec(t, admin, "CREATE SCHEMA "+schema)
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		_ = admin.Close()
	})

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("xdaltest: parse %s: %v", EnvPostgresDSN, err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	ping(t, db)
	t.Cleanup(func() { _ = db.Close() })
	return &DB{Name: xdal.DialectPostgres, SQL: db, DB: postgres.NewPostgresDB(db, nil), Dialect: postgres.NewDialect()}
}

func openMySQL(t testing.TB, dsn string) *DB {
	t.Helper()
	admin := connect(t, "mysql", dsn)
	name := uniqueName()
	mustExec(t, admin, "CREATE DATABASE "+name)
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE " + name)
		_ = admin.Close()
	})

	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("xdaltest: parse %s: %v", EnvMySQLDSN, err)
	}
	cfg.DBName = name
	db := connect(t, "mysql", cfg.FormatDSN())
	t.Cleanup(func() { _ = db.Close() })
	return &DB{Name: xdal.DialectMySQL, SQL: db, DB: mysql.NewMySQLDB(db, nil), Dialect: mysql.NewDialect()}
}

func openMSSQL(t testing.TB, dsn string) *DB {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "sqlserver" {
		t.Fatalf("xdaltest: %s must be a sqlserver:// URL", EnvMSSQLDSN)
	}
	admin := connect(t, "sqlserver", dsn)
	name := uniqueName()
	mustExec(t, admin, "CREATE DATABASE "+name)

	var db *sql.DB
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
		// Close any connection still open (a pooled one from another goroutine) before dropping.
		_, _ = admin.Exec("ALTER DATABASE " + name + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		_, _ = admin.Exec("DROP DATABASE " + name)
		_ = admin.Close()
	})

	q := u.Query()
	q.Set("database", name)
	u.RawQuery = q.Encode()
	db = connect(t, "sqlserver", u.String())
	return &DB{Name: xdal.DialectMSSQL, SQL: db, DB: mssql.NewMSSQLDB(db, nil), Dialect: mssql.NewDialect()}
}

// connect opens and pings a pool, retrying for a while so a server that is
// still starting doesn't fail the test.
func connect(t testing.TB, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("xdaltest: open %s: %v", driver, err)
	}
	ping(t, db)
	return db
}

func ping(t testing.TB, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			_ = db.Close()
			t.Fatalf("xdaltest: database not reachable: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func mustExec(t testing.TB, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("xdaltest: %s: %v", query, err)
	}
}

// Available reports which dialects Run would run rather than skip.
func Available() []string {
	out := []string{xdal.DialectSQLite}
	for _, d := range []struct{ name, env string }{
		{xdal.DialectPostgres, EnvPostgresDSN},
		{xdal.DialectMySQL, EnvMySQLDSN},
		{xdal.DialectMSSQL, EnvMSSQLDSN},
	} {
		if os.Getenv(d.env) != "" {
			out = append(out, d.name)
		}
	}
	return out
}

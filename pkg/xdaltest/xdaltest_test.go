package xdaltest_test

import (
	"context"
	"testing"

	"github.com/martinsuchenak/xdal/pkg/xdal"
	"github.com/martinsuchenak/xdal/pkg/xdaltest"
)

// Each subtest gets an empty database it can build a schema in and query.
func TestRunGivesEachDialectAWorkingDatabase(t *testing.T) {
	xdaltest.Run(t, func(t *testing.T, db *xdaltest.DB) {
		ctx := context.Background()
		if got := db.Dialect.Name(); got != db.Name {
			t.Fatalf("dialect %q for database %q", got, db.Name)
		}

		s := db.DB.Schema()
		items := s.CreateTable("items")
		items.String("id", 36).PrimaryKey()
		items.String("name", 100).NotNull()
		if err := xdal.ExecStatements(ctx, db.DB, items); err != nil {
			t.Fatalf("create table: %v", err)
		}

		q, args, err := db.QueryBuilder().Insert("items").Set("id", "a").Set("name", "first").Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB.Exec(ctx, q, args...); err != nil {
			t.Fatalf("insert: %v", err)
		}
		q, args, err = db.QueryBuilder().Select("name").From("items").Where("id = ?", "a").Build()
		if err != nil {
			t.Fatal(err)
		}
		var name string
		if err := db.DB.QueryRow(ctx, q, args...).Scan(&name); err != nil || name != "first" {
			t.Fatalf("select: %q, %v", name, err)
		}
	})
}

// Databases are isolated: a table one test creates is absent in the next.
func TestRunIsolatesTests(t *testing.T) {
	for i := 0; i < 2; i++ {
		xdaltest.Run(t, func(t *testing.T, db *xdaltest.DB) {
			t.Parallel()
			s := db.DB.Schema()
			// IfNotExists isn't used: a leftover table from another run must fail this.
			tbl := s.CreateTable("isolated")
			tbl.Integer("n")
			if err := xdal.ExecStatements(context.Background(), db.DB, tbl); err != nil {
				t.Fatalf("create table in a fresh database: %v", err)
			}
		})
	}
}

func TestAvailableAlwaysIncludesSQLite(t *testing.T) {
	t.Setenv(xdaltest.EnvPostgresDSN, "")
	t.Setenv(xdaltest.EnvMySQLDSN, "")
	t.Setenv(xdaltest.EnvMSSQLDSN, "")
	if got := xdaltest.Available(); len(got) != 1 || got[0] != xdal.DialectSQLite {
		t.Fatalf("Available() = %v, want [sqlite]", got)
	}
	t.Setenv(xdaltest.EnvMySQLDSN, "x")
	if got := xdaltest.Available(); len(got) != 2 || got[1] != xdal.DialectMySQL {
		t.Fatalf("Available() = %v, want [sqlite mysql]", got)
	}
}

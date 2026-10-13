package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/martinsuchenak/xdal/pkg/migrate"
	"github.com/martinsuchenak/xdal/pkg/sqlite"
	"github.com/martinsuchenak/xdal/pkg/xdal"

	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) xdal.DBInterface {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewSQLiteDB(db, nil)
}

func createTable(name string) func(context.Context, *migrate.Tx) error {
	return func(ctx context.Context, tx *migrate.Tx) error {
		t := tx.Schema().CreateTable(name)
		t.String("id", 36).PrimaryKey()
		return tx.Apply(ctx, t)
	}
}

func dropTable(name string) func(context.Context, *migrate.Tx) error {
	return func(ctx context.Context, tx *migrate.Tx) error {
		return tx.Apply(ctx, tx.Schema().DropTable(name))
	}
}

func step(version, table string) migrate.Migration {
	return migrate.Migration{Version: version, Name: "create_" + table, Up: createTable(table), Down: dropTable(table)}
}

func tableExists(t *testing.T, db xdal.DBInterface, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func newMigrator(t *testing.T, db xdal.DBInterface, ms []migrate.Migration, opts migrate.Options) *migrate.Migrator {
	t.Helper()
	m, err := migrate.New(db, ms, opts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpAppliesInOrderOnce(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	m := newMigrator(t, db, []migrate.Migration{step("002", "b"), step("001", "a")}, migrate.Options{})

	applied, err := m.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, []string{"001", "002"}) {
		t.Errorf("applied = %v", applied)
	}
	if !tableExists(t, db, "a") || !tableExists(t, db, "b") {
		t.Error("tables not created")
	}

	applied, err = m.Up(ctx)
	if err != nil || len(applied) != 0 {
		t.Errorf("second Up = %v, %v", applied, err)
	}

	status, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range status {
		if !s.Applied || s.Dirty || s.AppliedAt.IsZero() {
			t.Errorf("status %+v", s)
		}
	}
}

func TestUpToAndPending(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	m := newMigrator(t, db, []migrate.Migration{step("001", "a"), step("002", "b"), step("003", "c")}, migrate.Options{})

	if _, err := m.UpTo(ctx, "002"); err != nil {
		t.Fatal(err)
	}
	pending, err := m.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Version != "003" {
		t.Errorf("pending = %+v", pending)
	}
	if _, err := m.UpTo(ctx, "999"); !errors.Is(err, migrate.ErrUnknownVersion) {
		t.Errorf("err = %v", err)
	}
}

func TestDownAndDownTo(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	m := newMigrator(t, db, []migrate.Migration{step("001", "a"), step("002", "b"), step("003", "c")}, migrate.Options{})
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	reverted, err := m.Down(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reverted, []string{"003"}) || tableExists(t, db, "c") {
		t.Errorf("Down(1) reverted %v", reverted)
	}

	reverted, err = m.DownTo(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reverted, []string{"002", "001"}) || tableExists(t, db, "a") {
		t.Errorf("DownTo(\"\") reverted %v", reverted)
	}
}

func TestDownRejectsNegativeSteps(t *testing.T) {
	m := newMigrator(t, openDB(t), []migrate.Migration{step("001", "a")}, migrate.Options{})
	if _, err := m.Down(context.Background(), -1); !errors.Is(err, migrate.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestDownWithIgnoreUnknown(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := newMigrator(t, db, []migrate.Migration{step("001", "a"), step("002", "b"), step("003", "c")}, migrate.Options{}).Up(ctx); err != nil {
		t.Fatal(err)
	}

	older := newMigrator(t, db, []migrate.Migration{step("002", "b"), step("003", "c")}, migrate.Options{IgnoreUnknown: true})
	reverted, err := older.DownTo(ctx, "")
	if err != nil || !reflect.DeepEqual(reverted, []string{"003", "002"}) {
		t.Errorf("DownTo with an older unknown record = %v, %v", reverted, err)
	}
	if !tableExists(t, db, "a") {
		t.Error("reverted below the unknown record")
	}

	if _, err := newMigrator(t, db, []migrate.Migration{step("002", "b")}, migrate.Options{IgnoreUnknown: true}).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := newMigrator(t, db, []migrate.Migration{step("000", "z")}, migrate.Options{IgnoreUnknown: true}).Down(ctx, 1); !errors.Is(err, migrate.ErrUnknownApplied) {
		t.Errorf("Down beneath a newer unknown record: err = %v", err)
	}
}

func TestDownIrreversible(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	m := newMigrator(t, db, []migrate.Migration{{Version: "001", Name: "a", Up: createTable("a")}}, migrate.Options{})
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Down(ctx, 1); !errors.Is(err, migrate.ErrIrreversible) {
		t.Errorf("err = %v", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	boom := errors.New("boom")
	m := newMigrator(t, db, []migrate.Migration{
		step("001", "a"),
		{Version: "002", Name: "half", Up: func(ctx context.Context, tx *migrate.Tx) error {
			if err := createTable("b")(ctx, tx); err != nil {
				return err
			}
			return boom
		}},
	}, migrate.Options{})

	applied, err := m.Up(ctx)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(applied, []string{"001"}) {
		t.Errorf("applied = %v", applied)
	}
	if tableExists(t, db, "b") {
		t.Error("failed migration was not rolled back")
	}
	status, _ := m.Status(ctx)
	if status[1].Applied || status[1].Dirty {
		t.Errorf("status = %+v", status[1])
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	m := newMigrator(t, db, []migrate.Migration{step("001", "a"), step("003", "c")}, migrate.Options{})
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	renamed := step("001", "a")
	renamed.Name = "renamed"
	if _, err := newMigrator(t, db, []migrate.Migration{renamed, step("003", "c")}, migrate.Options{}).Up(ctx); !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Errorf("rename: err = %v", err)
	}

	if _, err := newMigrator(t, db, []migrate.Migration{step("001", "a")}, migrate.Options{}).Up(ctx); !errors.Is(err, migrate.ErrUnknownApplied) {
		t.Errorf("unknown: err = %v", err)
	}
	if _, err := newMigrator(t, db, []migrate.Migration{step("001", "a")}, migrate.Options{IgnoreUnknown: true}).Up(ctx); err != nil {
		t.Errorf("IgnoreUnknown: err = %v", err)
	}

	withGap := []migrate.Migration{step("001", "a"), step("002", "b"), step("003", "c")}
	if _, err := newMigrator(t, db, withGap, migrate.Options{}).Up(ctx); !errors.Is(err, migrate.ErrOutOfOrder) {
		t.Errorf("out of order: err = %v", err)
	}
	applied, err := newMigrator(t, db, withGap, migrate.Options{AllowOutOfOrder: true}).Up(ctx)
	if err != nil || !reflect.DeepEqual(applied, []string{"002"}) {
		t.Errorf("AllowOutOfOrder: %v, %v", applied, err)
	}
}

func TestNewRejectsInvalidMigrations(t *testing.T) {
	db := openDB(t)
	cases := map[string][]migrate.Migration{
		"duplicate":  {step("001", "a"), step("001", "b")},
		"no version": {{Name: "a", Up: createTable("a")}},
		"no name":    {{Version: "001", Up: createTable("a")}},
		"no up":      {{Version: "001", Name: "a"}},
	}
	for name, ms := range cases {
		if _, err := migrate.New(db, ms, migrate.Options{}); !errors.Is(err, migrate.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := migrate.New(db, nil, migrate.Options{Table: "bad table"}); err == nil {
		t.Error("bad table name accepted")
	}
}

func TestNamespacesAreIndependent(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	core := newMigrator(t, db, []migrate.Migration{step("001", "core_a")}, migrate.Options{Namespace: "core"})
	pro := newMigrator(t, db, []migrate.Migration{step("001", "pro_a")}, migrate.Options{Namespace: "pro"})
	if _, err := core.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pro.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := core.DownTo(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, db, "pro_a") || tableExists(t, db, "core_a") {
		t.Error("namespaces interfered")
	}
}

func TestDirtyRecordAndResolve(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	ms := []migrate.Migration{step("001", "a")}
	m := newMigrator(t, db, ms, migrate.Options{})
	if err := m.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	insert := func() {
		_, err := db.Insert(migrate.DefaultTable).
			Set("namespace", migrate.DefaultNamespace).Set("version", "001").Set("name", "create_a").
			Set("checksum", "x").Set("applied_at", "2026-01-01 00:00:00").Set("duration_ms", 0).Set("dirty", true).
			Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert()

	if _, err := m.Up(ctx); !errors.Is(err, migrate.ErrDirty) {
		t.Errorf("Up err = %v", err)
	}
	if _, err := m.Down(ctx, 1); !errors.Is(err, migrate.ErrDirty) {
		t.Errorf("Down err = %v", err)
	}
	status, _ := m.Status(ctx)
	if !status[0].Dirty {
		t.Errorf("status = %+v", status[0])
	}

	if err := m.Resolve(ctx, "001", false); err != nil {
		t.Fatal(err)
	}
	if applied, err := m.Up(ctx); err != nil || len(applied) != 1 {
		t.Errorf("Up after Resolve(false) = %v, %v", applied, err)
	}
	if err := m.Resolve(ctx, "001", true); !errors.Is(err, migrate.ErrNotDirty) {
		t.Errorf("Resolve on clean record: err = %v", err)
	}
}

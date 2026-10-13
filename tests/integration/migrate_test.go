package integration

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/martinsuchenak/xdal/pkg/migrate"
	"github.com/martinsuchenak/xdal/pkg/xdal"
)

const itMigrationsTable = "xt_migrations"

func itMigrations() []migrate.Migration {
	return []migrate.Migration{
		{
			Version: "20260101000000",
			Name:    "create_sites_and_devices",
			Up: func(ctx context.Context, tx *migrate.Tx) error {
				s := tx.Schema()
				sites := s.CreateTable("xm_sites")
				sites.String("id", 36).PrimaryKey()
				sites.String("name", 100).NotNull()
				devices := s.CreateTable("xm_devices")
				devices.String("id", 36).PrimaryKey()
				devices.String("site_id", 36).NotNull().References("xm_sites", "id").OnDelete(xdal.Cascade)
				devices.Index("idx_xm_devices_site", "site_id")
				return tx.Apply(ctx, sites, devices)
			},
			Down: func(ctx context.Context, tx *migrate.Tx) error {
				s := tx.Schema()
				return tx.Apply(ctx, s.DropTable("xm_devices"), s.DropTable("xm_sites"))
			},
		},
		{
			Version: "20260102000000",
			Name:    "add_device_serial",
			Up: func(ctx context.Context, tx *migrate.Tx) error {
				a := tx.Schema().AlterTable("xm_devices")
				a.String("serial", 64)
				return tx.Apply(ctx, a)
			},
			Down: func(ctx context.Context, tx *migrate.Tx) error {
				return tx.Apply(ctx, tx.Schema().AlterTable("xm_devices").DropColumn("serial"))
			},
		},
		{
			Version: "20260103000000",
			Name:    "seed_default_site",
			Up: func(ctx context.Context, tx *migrate.Tx) error {
				q, args, err := xdal.NewQueryBuilder(tx.Dialect()).Insert("xm_sites").
					Set("id", "default").Set("name", "Default").
					OnConflict("id").DoNothing().Build()
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, q, args...)
				return err
			},
			Down: func(ctx context.Context, tx *migrate.Tx) error {
				q, args, err := xdal.NewQueryBuilder(tx.Dialect()).Delete("xm_sites").
					Where(tx.Dialect().QuoteIdentifier("id")+" = ?", "default").Build()
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, q, args...)
				return err
			},
		},
	}
}

func resetMigrationTables(t *testing.T, td *testDB) {
	t.Helper()
	dropTables(t, td, "xm_devices", "xm_broken", "xm_sites", itMigrationsTable)
}

func countRows(t *testing.T, td *testDB, table string) int {
	t.Helper()
	var n int
	if err := td.dalDB.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+td.dialect.QuoteIdentifier(table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestMigrateUpDownAllDialects(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		resetMigrationTables(t, td)

		m, err := migrate.New(td.dalDB, itMigrations(), migrate.Options{Table: itMigrationsTable, Namespace: "core"})
		if err != nil {
			t.Fatal(err)
		}
		applied, err := m.Up(ctx)
		if err != nil {
			t.Fatalf("Up: %v", err)
		}
		if len(applied) != 3 {
			t.Errorf("applied = %v", applied)
		}
		if countRows(t, td, "xm_sites") != 1 {
			t.Error("seed migration did not run")
		}
		if _, err := td.dalDB.Insert("xm_devices").Set("id", "d1").Set("site_id", "default").Set("serial", "S1").Exec(ctx); err != nil {
			t.Fatalf("insert into migrated schema: %v", err)
		}

		status, err := m.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range status {
			if !s.Applied || s.AppliedAt.IsZero() {
				t.Errorf("status %+v", s)
			}
		}

		if _, err := td.dalDB.Delete("xm_devices").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		reverted, err := m.DownTo(ctx, "")
		if err != nil {
			t.Fatalf("DownTo: %v", err)
		}
		if !reflect.DeepEqual(reverted, []string{"20260103000000", "20260102000000", "20260101000000"}) {
			t.Errorf("reverted = %v", reverted)
		}
		if _, err := m.Up(ctx); err != nil {
			t.Fatalf("Up after full rollback: %v", err)
		}
		resetMigrationTables(t, td)
	})
}

func TestMigrateFailureHandling(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		resetMigrationTables(t, td)

		boom := errors.New("boom")
		broken := migrate.Migration{
			Version: "20260101000000",
			Name:    "create_then_fail",
			Up: func(ctx context.Context, tx *migrate.Tx) error {
				tb := tx.Schema().CreateTable("xm_broken")
				tb.Integer("id").PrimaryKey()
				if err := tx.Apply(ctx, tb); err != nil {
					return err
				}
				return boom
			},
		}
		m, err := migrate.New(td.dalDB, []migrate.Migration{broken}, migrate.Options{Table: itMigrationsTable})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Up(ctx); !errors.Is(err, boom) {
			t.Fatalf("Up err = %v", err)
		}
		status, err := m.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if td.dialect.Capabilities().TransactionalDDL {
			if status[0].Applied || status[0].Dirty {
				t.Errorf("transactional failure left a record: %+v", status[0])
			}
			return
		}

		if !status[0].Dirty {
			t.Fatalf("non-transactional failure should leave a dirty record: %+v", status[0])
		}
		if _, err := m.Up(ctx); !errors.Is(err, migrate.ErrDirty) {
			t.Errorf("Up on dirty = %v", err)
		}
		dropTables(t, td, "xm_broken")
		if err := m.Resolve(ctx, broken.Version, false); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if _, err := m.Up(ctx); !errors.Is(err, boom) {
			t.Errorf("Up after Resolve should run the migration again, got %v", err)
		}
		resetMigrationTables(t, td)
	})
}

func TestMigrateConcurrentRunsApplyOnce(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		resetMigrationTables(t, td)

		var wg sync.WaitGroup
		results := make([][]string, 3)
		errs := make([]error, 3)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				m, err := migrate.New(td.dalDB, itMigrations(), migrate.Options{Table: itMigrationsTable})
				if err != nil {
					errs[i] = err
					return
				}
				results[i], errs[i] = m.Up(ctx)
			}(i)
		}
		wg.Wait()

		total := 0
		for i := range results {
			if errs[i] != nil {
				t.Errorf("run %d: %v", i, errs[i])
			}
			total += len(results[i])
		}
		if total != 3 {
			t.Errorf("migrations applied %d times in total, want 3: %v", total, results)
		}
		resetMigrationTables(t, td)
	})
}

func TestMigrateRejectsSingleConnectionPool(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		if !td.dialect.Locker().SessionBound() {
			t.Skip("locks do not hold a connection")
		}
		td.db.SetMaxOpenConns(1)
		defer td.db.SetMaxOpenConns(0)
		m, err := migrate.New(td.dalDB, itMigrations(), migrate.Options{Table: itMigrationsTable})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Up(context.Background()); !errors.Is(err, migrate.ErrSingleConnection) {
			t.Errorf("err = %v, want ErrSingleConnection", err)
		}
	})
}

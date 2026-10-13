package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/martinsuchenak/xdal/pkg/xdal"
)

func TestNamedLocks(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		name := "xdal-test-lock-" + td.name

		l1, err := td.dalDB.Lock(ctx, name)
		if err != nil {
			t.Fatalf("Lock: %v", err)
		}
		if _, ok, err := td.dalDB.TryLock(ctx, name); err != nil || ok {
			t.Fatalf("TryLock while held = %v, %v", ok, err)
		}

		wctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if _, err := td.dalDB.Lock(wctx, name); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Lock while held = %v, want deadline exceeded", err)
		}

		if err := l1.Release(ctx); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if err := l1.Release(ctx); err != nil {
			t.Errorf("second Release: %v", err)
		}

		l2, ok, err := td.dalDB.TryLock(ctx, name)
		if err != nil || !ok {
			t.Fatalf("TryLock after release = %v, %v", ok, err)
		}
		_ = l2.Release(ctx)

		long := make([]byte, 300)
		for i := range long {
			long[i] = 'x'
		}
		l3, err := td.dalDB.Lock(ctx, string(long))
		if err != nil {
			t.Fatalf("long lock name: %v", err)
		}
		_ = l3.Release(ctx)
	})
}

func TestSkipLockedClaimsDisjointRows(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		if !td.dialect.Capabilities().SkipLocked {
			t.Skip("dialect has no SKIP LOCKED")
		}
		ctx := context.Background()
		dropTables(t, td, "xt_jobs")
		tb := td.dalDB.Schema().CreateTable("xt_jobs")
		tb.Integer("id").PrimaryKey()
		tb.String("state", 10).NotNull()
		applyAll(t, td, tb)
		for i := 1; i <= 4; i++ {
			if _, err := td.dalDB.Insert("xt_jobs").Set("id", i).Set("state", "new").Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}

		claim := func(tx *xdal.Tx) []int {
			t.Helper()
			q, args, err := td.builder().Select("id").From("xt_jobs").
				Where(td.dialect.QuoteIdentifier("state")+" = ?", "new").
				OrderBy(td.dialect.QuoteIdentifier("id")).Limit(2).SkipLocked().Build()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := tx.Query(ctx, q, args...)
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			defer func() { _ = rows.Close() }()
			var ids []int
			for rows.Next() {
				var id int
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			return ids
		}

		tx1, err := td.dalDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx1.Rollback() }()
		tx2, err := td.dalDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx2.Rollback() }()

		first := claim(tx1)
		second := claim(tx2)
		if !reflect.DeepEqual(first, []int{1, 2}) || !reflect.DeepEqual(second, []int{3, 4}) {
			t.Errorf("claims = %v and %v, want [1 2] and [3 4]", first, second)
		}
	})
}

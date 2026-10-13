package integration

import (
	"context"
	"testing"
)

func createUpsertTable(t *testing.T, td *testDB) {
	t.Helper()
	dropTables(t, td, "xt_seen")
	tb := td.dalDB.Schema().CreateTable("xt_seen")
	tb.String("mac", 17).PrimaryKey()
	tb.String("ip", 45).NotNull()
	tb.Integer("hits").NotNull().Default(1)
	applyAll(t, td, tb)
}

func seenRow(t *testing.T, td *testDB, mac string) (string, int) {
	t.Helper()
	var ip string
	var hits int
	err := td.dalDB.Select("ip", "hits").From("xt_seen").
		Where(td.dialect.QuoteIdentifier("mac")+" = ?", mac).QueryRow(context.Background()).Scan(&ip, &hits)
	if err != nil {
		t.Fatalf("select %s: %v", mac, err)
	}
	return ip, hits
}

func TestUpsertUpdateAndNothing(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		createUpsertTable(t, td)

		upsert := func(ip string, hits int) {
			t.Helper()
			_, err := td.dalDB.Insert("xt_seen").Set("mac", "aa:bb").Set("ip", ip).Set("hits", hits).
				OnConflict("mac").DoUpdate("ip").Exec(ctx)
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
		}
		upsert("10.0.0.1", 1)
		upsert("10.0.0.2", 5)
		if ip, hits := seenRow(t, td, "aa:bb"); ip != "10.0.0.2" || hits != 1 {
			t.Errorf("after DoUpdate(ip): ip=%s hits=%d", ip, hits)
		}

		_, err := td.dalDB.Insert("xt_seen").Set("mac", "aa:bb").Set("ip", "10.0.0.3").Set("hits", 9).
			OnConflict("mac").DoUpdate().Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ip, hits := seenRow(t, td, "aa:bb"); ip != "10.0.0.3" || hits != 9 {
			t.Errorf("after DoUpdate(): ip=%s hits=%d", ip, hits)
		}

		_, err = td.dalDB.Insert("xt_seen").Columns("mac", "ip").
			Values("aa:bb", "10.9.9.9").Values("cc:dd", "10.0.0.4").
			OnConflict("mac").DoNothing().Exec(ctx)
		if err != nil {
			t.Fatalf("batch DoNothing: %v", err)
		}
		if ip, _ := seenRow(t, td, "aa:bb"); ip != "10.0.0.3" {
			t.Errorf("DoNothing changed existing row: %s", ip)
		}
		if ip, hits := seenRow(t, td, "cc:dd"); ip != "10.0.0.4" || hits != 1 {
			t.Errorf("DoNothing new row: ip=%s hits=%d", ip, hits)
		}
	})
}

func TestUpsertReturning(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		if !td.dialect.Capabilities().Returning {
			t.Skip("dialect has no RETURNING")
		}
		ctx := context.Background()
		singleConnSQLite(td)
		createUpsertTable(t, td)

		var ip string
		err := td.dalDB.Insert("xt_seen").Set("mac", "ee:ff").Set("ip", "10.1.1.1").
			OnConflict("mac").DoUpdate("ip").Returning("ip").QueryRow(ctx).Scan(&ip)
		if err != nil || ip != "10.1.1.1" {
			t.Fatalf("insert returning: %q, %v", ip, err)
		}
		err = td.dalDB.Insert("xt_seen").Set("mac", "ee:ff").Set("ip", "10.1.1.2").
			OnConflict("mac").DoUpdate("ip").Returning("ip").QueryRow(ctx).Scan(&ip)
		if err != nil || ip != "10.1.1.2" {
			t.Fatalf("update returning: %q, %v", ip, err)
		}
	})
}

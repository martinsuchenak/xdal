package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/martinsuchenak/xdal/pkg/xdal"
)

func singleConnSQLite(td *testDB) {
	if td.name == "sqlite" {
		td.db.SetMaxOpenConns(1)
	}
}

func dropTables(t *testing.T, td *testDB, names ...string) {
	t.Helper()
	ctx := context.Background()
	s := td.dalDB.Schema()
	for _, n := range names {
		if err := xdal.ExecStatements(ctx, td.dalDB, s.DropTable(n).IfExists()); err != nil {
			t.Fatalf("drop %s: %v", n, err)
		}
	}
}

func applyAll(t *testing.T, td *testDB, stmts ...xdal.Statement) {
	t.Helper()
	if err := xdal.ExecStatements(context.Background(), td.dalDB, stmts...); err != nil {
		t.Fatalf("schema: %v", err)
	}
}

func TestSchemaTypesRoundTrip(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		dropTables(t, td, "xt_items", "xt_sites")
		s := td.dalDB.Schema()

		sites := s.CreateTable("xt_sites").IfNotExists()
		sites.String("id", 36).PrimaryKey()
		sites.String("name", 100).NotNull()

		items := s.CreateTable("xt_items").IfNotExists()
		items.AutoIncrement("id")
		items.String("site_id", 36).NotNull().References("xt_sites", "id").OnDelete(xdal.Cascade)
		items.String("s", 50).NotNull().Default("none")
		items.Text("t")
		items.Integer("i").NotNull().Default(0)
		items.BigInteger("bi")
		items.Boolean("b").NotNull().Default(false)
		items.Float("f")
		items.Decimal("d", 10, 2)
		items.Timestamp("ts")
		items.Timestamp("created_at").NotNull().DefaultNow()
		items.Date("dt")
		items.UUID("u")
		items.JSON("j")
		items.Binary("bin", 16)
		items.Blob("blob")
		items.Index("idx_xt_items_bin", "bin")
		items.Unique("site_id", "s")

		applyAll(t, td, sites, items)
		applyAll(t, td, sites, items)

		if _, err := td.dalDB.Insert("xt_sites").Set("id", "site-1").Set("name", "Sydney").Exec(ctx); err != nil {
			t.Fatal(err)
		}

		ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		dt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		bin := []byte{0x0a, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 10, 0, 0, 1}
		blob := bytes.Repeat([]byte{0xab}, 10000)
		_, err := td.dalDB.Insert("xt_items").
			Set("site_id", "site-1").Set("s", "hello").Set("t", "long text").Set("i", 42).
			Set("bi", int64(1)<<40).Set("b", true).Set("f", 1.5).Set("d", 12.34).
			Set("ts", ts).Set("dt", dt).Set("u", "550e8400-e29b-41d4-a716-446655440000").
			Set("j", `{"a":1}`).Set("bin", bin).Set("blob", blob).
			Exec(ctx)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}

		var (
			id                 int64
			sv, tv, uv, jv     string
			iv                 int
			biv                int64
			bv                 bool
			fv, dv             float64
			tsv, createdv, dtv time.Time
			binv, blobv        []byte
		)
		err = td.dalDB.Select("id", "s", "t", "i", "bi", "b", "f", "d", "ts", "created_at", "dt", "u", "j", "bin", "blob").
			From("xt_items").QueryRow(ctx).
			Scan(&id, &sv, &tv, &iv, &biv, &bv, &fv, &dv, &tsv, &createdv, &dtv, &uv, &jv, &binv, &blobv)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if id <= 0 || sv != "hello" || tv != "long text" || iv != 42 || biv != 1<<40 || !bv || fv != 1.5 || dv != 12.34 {
			t.Errorf("scalars: id=%d s=%q t=%q i=%d bi=%d b=%v f=%v d=%v", id, sv, tv, iv, biv, bv, fv, dv)
		}
		if !tsv.Equal(ts) {
			t.Errorf("timestamp = %v, want %v", tsv, ts)
		}
		if createdv.IsZero() {
			t.Error("created_at default not set")
		}
		if y, m, d := dtv.Date(); y != 2026 || m != 1 || d != 2 {
			t.Errorf("date = %v", dtv)
		}
		if uv != "550e8400-e29b-41d4-a716-446655440000" {
			t.Errorf("uuid = %q", uv)
		}
		var jm map[string]int
		if err := json.Unmarshal([]byte(jv), &jm); err != nil || jm["a"] != 1 {
			t.Errorf("json = %q (%v)", jv, err)
		}
		if !bytes.Equal(binv, bin) || !bytes.Equal(blobv, blob) {
			t.Errorf("binary mismatch: bin=%x blob len=%d", binv, len(blobv))
		}

		var def string
		if _, err := td.dalDB.Insert("xt_items").Set("site_id", "site-1").Set("s", "second").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := td.dalDB.Insert("xt_items").Set("site_id", "site-1").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := td.dalDB.Select("s").From("xt_items").WhereIsNull("t").Where(td.dialect.QuoteIdentifier("s")+" <> ?", "second").QueryRow(ctx).Scan(&def); err != nil || def != "none" {
			t.Errorf("default s = %q, %v", def, err)
		}
		if _, err := td.dalDB.Insert("xt_items").Set("site_id", "site-1").Set("s", "hello").Exec(ctx); err == nil {
			t.Error("unique (site_id, s) not enforced")
		}
		if _, err := td.dalDB.Insert("xt_items").Set("site_id", "missing").Set("s", "x").Exec(ctx); err == nil && td.name != "sqlite" {
			t.Error("foreign key not enforced")
		}
	})
}

func TestSchemaBinaryOrdering(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		dropTables(t, td, "xt_ips")
		tb := td.dalDB.Schema().CreateTable("xt_ips")
		tb.Binary("ip", 16).PrimaryKey()
		applyAll(t, td, tb)

		ip := func(last ...byte) []byte {
			b := make([]byte, 16)
			copy(b[16-len(last):], last)
			return b
		}
		values := [][]byte{ip(0xff, 0xff, 10, 0, 0, 200), ip(0xff, 0xff, 10, 0, 0, 2), ip(0xff, 0xff, 10, 0, 1, 0), ip(0xff, 0xff, 9, 255, 255, 255)}
		for _, v := range values {
			if _, err := td.dalDB.Insert("xt_ips").Set("ip", v).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}

		rows, err := td.dalDB.Select("ip").From("xt_ips").
			WhereBetween("ip", ip(0xff, 0xff, 10, 0, 0, 0), ip(0xff, 0xff, 10, 0, 0, 255)).
			OrderBy(td.dialect.QuoteIdentifier("ip")).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var got [][]byte
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				t.Fatal(err)
			}
			got = append(got, b)
		}
		want := [][]byte{values[1], values[0]}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("range query = %x, want %x", got, want)
		}
	})
}

func TestSchemaAlterAndIndexes(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		dropTables(t, td, "xt_hosts", "xt_devices", "xt_racks")
		s := td.dalDB.Schema()

		racks := s.CreateTable("xt_racks")
		racks.String("id", 36).PrimaryKey()
		devices := s.CreateTable("xt_devices")
		devices.String("id", 36).PrimaryKey()
		devices.String("name", 100).NotNull()
		devices.String("old", 10).NotNull().Default("x")
		applyAll(t, td, racks, devices)

		alter := s.AlterTable("xt_devices")
		alter.String("serial", 64).NotNull().Default("")
		alter.String("rack_id", 36).References("xt_racks", "id").OnDelete(xdal.SetNull)
		alter.DropColumn("old").RenameColumn("name", "hostname").RenameTo("xt_hosts")
		applyAll(t, td, alter,
			s.CreateIndex("idx_xt_hosts_serial", "xt_hosts", "serial").Unique(),
		)

		if _, err := td.dalDB.Insert("xt_hosts").Set("id", "h1").Set("hostname", "web-01").Set("serial", "S1").Exec(ctx); err != nil {
			t.Fatalf("insert into altered table: %v", err)
		}
		if _, err := td.dalDB.Insert("xt_hosts").Set("id", "h2").Set("hostname", "web-02").Set("serial", "S1").Exec(ctx); err == nil {
			t.Error("unique index not enforced")
		}

		applyAll(t, td, s.DropIndex("idx_xt_hosts_serial", "xt_hosts"))
		if _, err := td.dalDB.Insert("xt_hosts").Set("id", "h2").Set("hostname", "web-02").Set("serial", "S1").Exec(ctx); err != nil {
			t.Errorf("insert after dropping index: %v", err)
		}
		if td.name != "mysql" {
			applyAll(t, td, s.DropIndex("idx_xt_hosts_serial", "xt_hosts").IfExists())
		}
	})
}

func TestSchemaUnicodeDefault(t *testing.T) {
	runForEachDB(t, func(t *testing.T, td *testDB) {
		ctx := context.Background()
		singleConnSQLite(td)
		dropTables(t, td, "xt_names")
		tb := td.dalDB.Schema().CreateTable("xt_names")
		tb.Integer("id").PrimaryKey()
		tb.String("label", 20).NotNull().Default("日本 – ü")
		applyAll(t, td, tb)
		if _, err := td.dalDB.Insert("xt_names").Set("id", 1).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		var label string
		if err := td.dalDB.Select("label").From("xt_names").QueryRow(ctx).Scan(&label); err != nil || label != "日本 – ü" {
			t.Errorf("label = %q, %v", label, err)
		}
	})
}

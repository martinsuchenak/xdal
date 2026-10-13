package xdal_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/martinsuchenak/xdal/pkg/mssql"
	"github.com/martinsuchenak/xdal/pkg/mysql"
	"github.com/martinsuchenak/xdal/pkg/postgres"
	"github.com/martinsuchenak/xdal/pkg/sqlite"
	"github.com/martinsuchenak/xdal/pkg/xdal"
)

type namedDialect struct {
	name    string
	dialect xdal.Dialect
}

func allDialects() []namedDialect {
	return []namedDialect{
		{xdal.DialectSQLite, sqlite.NewDialect()},
		{xdal.DialectPostgres, postgres.NewDialect()},
		{xdal.DialectMySQL, mysql.NewDialect()},
		{xdal.DialectMSSQL, mssql.NewDialect()},
	}
}

func dialectFor(t *testing.T, name string) xdal.Dialect {
	t.Helper()
	for _, d := range allDialects() {
		if d.name == name {
			return d.dialect
		}
	}
	t.Fatalf("unknown dialect %s", name)
	return nil
}

func assertSQL(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SQL mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestDialectNamesAndCapabilities(t *testing.T) {
	want := map[string]xdal.Capabilities{
		xdal.DialectSQLite:   {Returning: true, Upsert: true, TransactionalDDL: true},
		xdal.DialectPostgres: {Returning: true, Upsert: true, RowLocks: true, SkipLocked: true, TransactionalDDL: true, SharedAppLocks: true},
		xdal.DialectMySQL:    {Upsert: true, RowLocks: true, SkipLocked: true, SharedAppLocks: true},
		xdal.DialectMSSQL:    {Returning: true, Upsert: true, RowLocks: true, SkipLocked: true, TransactionalDDL: true, SharedAppLocks: true},
	}
	for _, d := range allDialects() {
		if got := d.dialect.Name(); got != d.name {
			t.Errorf("Name() = %q, want %q", got, d.name)
		}
		if got := d.dialect.Capabilities(); got != want[d.name] {
			t.Errorf("%s: Capabilities() = %+v, want %+v", d.name, got, want[d.name])
		}
		if d.dialect.Locker() == nil {
			t.Errorf("%s: Locker() is nil", d.name)
		}
	}
}

func TestUpsertDoUpdate(t *testing.T) {
	want := map[string]string{
		xdal.DialectSQLite:   `INSERT INTO "devices" ("id", "name", "seen") VALUES (?, ?, ?) ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name", "seen" = excluded."seen"`,
		xdal.DialectPostgres: `INSERT INTO "devices" ("id", "name", "seen") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name", "seen" = excluded."seen"`,
		xdal.DialectMySQL:    "INSERT INTO `devices` (`id`, `name`, `seen`) VALUES (?, ?, ?) AS xdal_new ON DUPLICATE KEY UPDATE `name` = `xdal_new`.`name`, `seen` = `xdal_new`.`seen`",
		xdal.DialectMSSQL:    `MERGE INTO [devices] WITH (HOLDLOCK) AS [xdal_target] USING (VALUES (@p1, @p2, @p3)) AS [xdal_source] ([id], [name], [seen]) ON [xdal_target].[id] = [xdal_source].[id] WHEN MATCHED THEN UPDATE SET [name] = [xdal_source].[name], [seen] = [xdal_source].[seen] WHEN NOT MATCHED THEN INSERT ([id], [name], [seen]) VALUES ([xdal_source].[id], [xdal_source].[name], [xdal_source].[seen]);`,
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			q, args, err := xdal.NewQueryBuilder(d.dialect).Insert("devices").
				Set("id", 1).Set("name", "a").Set("seen", 2).
				OnConflict("id").DoUpdate().
				Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, []string{q}, []string{want[d.name]})
			if !reflect.DeepEqual(args, []interface{}{1, "a", 2}) {
				t.Errorf("args = %v", args)
			}
		})
	}
}

func TestUpsertDoNothingBatch(t *testing.T) {
	want := map[string]string{
		xdal.DialectSQLite:   `INSERT INTO "tags" ("device_id", "tag") VALUES (?, ?), (?, ?) ON CONFLICT ("device_id", "tag") DO NOTHING`,
		xdal.DialectPostgres: `INSERT INTO "tags" ("device_id", "tag") VALUES ($1, $2), ($3, $4) ON CONFLICT ("device_id", "tag") DO NOTHING`,
		xdal.DialectMySQL:    "INSERT INTO `tags` (`device_id`, `tag`) VALUES (?, ?), (?, ?) AS xdal_new ON DUPLICATE KEY UPDATE `tags`.`device_id` = `tags`.`device_id`",
		xdal.DialectMSSQL:    `MERGE INTO [tags] WITH (HOLDLOCK) AS [xdal_target] USING (VALUES (@p1, @p2), (@p3, @p4)) AS [xdal_source] ([device_id], [tag]) ON [xdal_target].[device_id] = [xdal_source].[device_id] AND [xdal_target].[tag] = [xdal_source].[tag] WHEN NOT MATCHED THEN INSERT ([device_id], [tag]) VALUES ([xdal_source].[device_id], [xdal_source].[tag]);`,
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			q, args, err := xdal.NewQueryBuilder(d.dialect).Insert("tags").
				Columns("device_id", "tag").
				Values("d1", "web").Values("d1", "prod").
				OnConflict("device_id", "tag").DoNothing().
				Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, []string{q}, []string{want[d.name]})
			if len(args) != 4 {
				t.Errorf("args = %v", args)
			}
		})
	}
}

func TestUpsertReturning(t *testing.T) {
	want := map[string]string{
		xdal.DialectPostgres: `INSERT INTO "devices" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name" RETURNING "id"`,
		xdal.DialectMSSQL:    `MERGE INTO [devices] WITH (HOLDLOCK) AS [xdal_target] USING (VALUES (@p1, @p2)) AS [xdal_source] ([id], [name]) ON [xdal_target].[id] = [xdal_source].[id] WHEN MATCHED THEN UPDATE SET [name] = [xdal_source].[name] WHEN NOT MATCHED THEN INSERT ([id], [name]) VALUES ([xdal_source].[id], [xdal_source].[name]) OUTPUT INSERTED.[id];`,
	}
	for name, sql := range want {
		q, _, err := xdal.NewQueryBuilder(dialectFor(t, name)).Insert("devices").
			Set("id", 1).Set("name", "a").
			OnConflict("id").DoUpdate("name").Returning("id").
			Build()
		if err != nil {
			t.Fatal(err)
		}
		assertSQL(t, []string{q}, []string{sql})
	}
}

func TestUpsertErrors(t *testing.T) {
	qb := xdal.NewQueryBuilder(postgres.NewDialect())
	cases := []struct {
		name string
		q    *xdal.InsertQuery
		want error
	}{
		{"no conflict columns", qb.Insert("t").Set("a", 1).OnConflict().DoNothing(), xdal.ErrUpsertConflictColumns},
		{"action without OnConflict", qb.Insert("t").Set("a", 1).DoNothing(), xdal.ErrUpsertConflictColumns},
		{"no action", qb.Insert("t").Set("a", 1).OnConflict("a"), xdal.ErrUpsertAction},
		{"nothing to update", qb.Insert("t").Set("a", 1).OnConflict("a").DoUpdate(), xdal.ErrUpsertNothingToUpdate},
		{"dialect without upsert", xdal.NewQueryBuilder(&xdal.BaseDialect{Placeholder: xdal.QuestionMarkPlaceholder}).Insert("t").Set("a", 1).OnConflict("a").DoNothing(), xdal.ErrUpsertNotSupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := c.q.Build(); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestRowLocks(t *testing.T) {
	want := map[string][3]string{
		xdal.DialectSQLite: {
			`SELECT "id" FROM "jobs" WHERE state = ? LIMIT 5`,
			`SELECT "id" FROM "jobs" WHERE state = ? LIMIT 5`,
			`SELECT "id" FROM "jobs" WHERE state = ? LIMIT 5`,
		},
		xdal.DialectPostgres: {
			`SELECT "id" FROM "jobs" WHERE state = $1 LIMIT 5 FOR UPDATE`,
			`SELECT "id" FROM "jobs" WHERE state = $1 LIMIT 5 FOR UPDATE SKIP LOCKED`,
			`SELECT "id" FROM "jobs" WHERE state = $1 LIMIT 5 FOR UPDATE NOWAIT`,
		},
		xdal.DialectMySQL: {
			"SELECT `id` FROM `jobs` WHERE state = ? LIMIT 5 FOR UPDATE",
			"SELECT `id` FROM `jobs` WHERE state = ? LIMIT 5 FOR UPDATE SKIP LOCKED",
			"SELECT `id` FROM `jobs` WHERE state = ? LIMIT 5 FOR UPDATE NOWAIT",
		},
		xdal.DialectMSSQL: {
			`SELECT [id] FROM [jobs] WITH (UPDLOCK, ROWLOCK) WHERE state = @p1 ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY`,
			`SELECT [id] FROM [jobs] WITH (UPDLOCK, ROWLOCK, READPAST) WHERE state = @p1 ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY`,
			`SELECT [id] FROM [jobs] WITH (UPDLOCK, ROWLOCK, NOWAIT) WHERE state = @p1 ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY`,
		},
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			qb := xdal.NewQueryBuilder(d.dialect)
			base := func() *xdal.SelectQuery { return qb.Select("id").From("jobs").Where("state = ?", "new").Limit(5) }
			for i, q := range []*xdal.SelectQuery{base().ForUpdate(), base().SkipLocked(), base().NoWait()} {
				sql, _, err := q.Build()
				if err != nil {
					t.Fatal(err)
				}
				assertSQL(t, []string{sql}, []string{want[d.name][i]})
			}
		})
	}
}

func devicesTable(s *xdal.Schema) *xdal.CreateTable {
	t := s.CreateTable("devices").IfNotExists()
	t.String("id", 36).PrimaryKey()
	t.String("name", 100).NotNull()
	t.Boolean("active").NotNull().Default(true)
	t.Timestamp("created_at").NotNull().DefaultNow()
	t.String("site_id", 36).References("sites", "id").OnDelete(xdal.Cascade)
	t.Index("idx_devices_name", "name")
	return t
}

func TestCreateTable(t *testing.T) {
	want := map[string][]string{
		xdal.DialectSQLite: {
			`CREATE TABLE IF NOT EXISTS "devices" ("id" TEXT NOT NULL, "name" TEXT NOT NULL, "active" BOOLEAN NOT NULL DEFAULT 1, "created_at" TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, "site_id" TEXT, PRIMARY KEY ("id"), CONSTRAINT "fk_devices_site_id_af95f51d" FOREIGN KEY ("site_id") REFERENCES "sites" ("id") ON DELETE CASCADE)`,
			`CREATE INDEX IF NOT EXISTS "idx_devices_name" ON "devices" ("name")`,
		},
		xdal.DialectPostgres: {
			`CREATE TABLE IF NOT EXISTS "devices" ("id" VARCHAR(36) NOT NULL, "name" VARCHAR(100) NOT NULL, "active" BOOLEAN NOT NULL DEFAULT TRUE, "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, "site_id" VARCHAR(36), PRIMARY KEY ("id"), CONSTRAINT "fk_devices_site_id_af95f51d" FOREIGN KEY ("site_id") REFERENCES "sites" ("id") ON DELETE CASCADE)`,
			`CREATE INDEX IF NOT EXISTS "idx_devices_name" ON "devices" ("name")`,
		},
		xdal.DialectMySQL: {
			"CREATE TABLE IF NOT EXISTS `devices` (`id` VARCHAR(36) NOT NULL, `name` VARCHAR(100) NOT NULL, `active` BOOLEAN NOT NULL DEFAULT TRUE, `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), `site_id` VARCHAR(36), PRIMARY KEY (`id`), CONSTRAINT `fk_devices_site_id_af95f51d` FOREIGN KEY (`site_id`) REFERENCES `sites` (`id`) ON DELETE CASCADE, INDEX `idx_devices_name` (`name`))",
		},
		xdal.DialectMSSQL: {
			`IF OBJECT_ID(N'devices', N'U') IS NULL CREATE TABLE [devices] ([id] NVARCHAR(36) NOT NULL, [name] NVARCHAR(100) NOT NULL, [active] BIT NOT NULL DEFAULT 1, [created_at] DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(), [site_id] NVARCHAR(36), PRIMARY KEY ([id]), CONSTRAINT [fk_devices_site_id_af95f51d] FOREIGN KEY ([site_id]) REFERENCES [sites] ([id]) ON DELETE CASCADE)`,
			`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'idx_devices_name' AND object_id = OBJECT_ID(N'devices')) CREATE INDEX [idx_devices_name] ON [devices] ([name])`,
		},
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			got, err := devicesTable(xdal.NewSchema(d.dialect)).Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, got, want[d.name])
		})
	}
}

func TestCreateTableTypes(t *testing.T) {
	want := map[string]string{
		xdal.DialectSQLite:   `CREATE TABLE "all_types" ("id" INTEGER PRIMARY KEY AUTOINCREMENT, "s" TEXT, "t" TEXT, "i" INTEGER, "bi" INTEGER, "f" REAL, "d" NUMERIC(10,2), "dt" DATE, "u" TEXT, "j" TEXT, "bin" BLOB, "blob" BLOB)`,
		xdal.DialectPostgres: `CREATE TABLE "all_types" ("id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, "s" VARCHAR(255), "t" TEXT, "i" INTEGER, "bi" BIGINT, "f" DOUBLE PRECISION, "d" NUMERIC(10,2), "dt" DATE, "u" UUID, "j" JSONB, "bin" BYTEA, "blob" BYTEA)`,
		xdal.DialectMySQL:    "CREATE TABLE `all_types` (`id` BIGINT AUTO_INCREMENT PRIMARY KEY, `s` VARCHAR(255), `t` LONGTEXT, `i` INT, `bi` BIGINT, `f` DOUBLE, `d` DECIMAL(10,2), `dt` DATE, `u` CHAR(36), `j` JSON, `bin` VARBINARY(16), `blob` LONGBLOB)",
		xdal.DialectMSSQL:    `CREATE TABLE [all_types] ([id] BIGINT IDENTITY(1,1) PRIMARY KEY, [s] NVARCHAR(255), [t] NVARCHAR(MAX), [i] INT, [bi] BIGINT, [f] FLOAT(53), [d] DECIMAL(10,2), [dt] DATE, [u] CHAR(36), [j] NVARCHAR(MAX), [bin] VARBINARY(16), [blob] VARBINARY(MAX))`,
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			tb := xdal.NewSchema(d.dialect).CreateTable("all_types")
			tb.AutoIncrement("id")
			tb.String("s", 0)
			tb.Text("t")
			tb.Integer("i")
			tb.BigInteger("bi")
			tb.Float("f")
			tb.Decimal("d", 10, 2)
			tb.Date("dt")
			tb.UUID("u")
			tb.JSON("j")
			tb.Binary("bin", 16)
			tb.Blob("blob")
			got, err := tb.Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, got, []string{want[d.name]})
		})
	}
}

func TestCreateTableConstraints(t *testing.T) {
	s := xdal.NewSchema(postgres.NewDialect())
	tb := s.CreateTable("role_permissions")
	tb.String("role_id", 36).NotNull().References("roles", "id").OnDelete(xdal.Cascade).OnUpdate(xdal.Restrict)
	tb.String("permission", 100).NotNull()
	tb.String("label", 50).Unique().Default("it's")
	tb.PrimaryKey("role_id", "permission")
	tb.Unique("permission", "label")
	tb.Index("idx_rp_perm", "permission").Unique()
	got, err := tb.Build()
	if err != nil {
		t.Fatal(err)
	}
	assertSQL(t, got, []string{
		`CREATE TABLE "role_permissions" ("role_id" VARCHAR(36) NOT NULL, "permission" VARCHAR(100) NOT NULL, "label" VARCHAR(50) DEFAULT 'it''s', PRIMARY KEY ("role_id", "permission"), CONSTRAINT "uq_role_permissions_label_a7402184" UNIQUE ("label"), CONSTRAINT "uq_role_permissions_permission_label_0d7d65d3" UNIQUE ("permission", "label"), CONSTRAINT "fk_role_permissions_role_id_021e2524" FOREIGN KEY ("role_id") REFERENCES "roles" ("id") ON DELETE CASCADE ON UPDATE RESTRICT)`,
		`CREATE UNIQUE INDEX "idx_rp_perm" ON "role_permissions" ("permission")`,
	})

	tb = xdal.NewSchema(mssql.NewDialect()).CreateTable("x")
	tb.String("a", 10).References("y", "id").OnDelete(xdal.Restrict)
	got, err = tb.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0], "ON DELETE NO ACTION") {
		t.Errorf("mssql Restrict should render NO ACTION: %s", got[0])
	}
}

func TestCreateTableDefaults(t *testing.T) {
	my := xdal.NewSchema(mysql.NewDialect()).CreateTable("notes")
	my.Text("body").Default(`a\b`)
	my.JSON("meta").DefaultExpr("(JSON_OBJECT())")
	my.Integer("n").Default(0)
	got, err := my.Build()
	if err != nil {
		t.Fatal(err)
	}
	assertSQL(t, got, []string{"CREATE TABLE `notes` (`body` LONGTEXT DEFAULT ('a\\\\b'), `meta` JSON DEFAULT (JSON_OBJECT()), `n` INT DEFAULT 0)"})

	ms := xdal.NewSchema(mssql.NewDialect()).CreateTable("big")
	ms.String("s", 5000)
	ms.Binary("b", 9000)
	got, err = ms.Build()
	if err != nil {
		t.Fatal(err)
	}
	assertSQL(t, got, []string{`CREATE TABLE [big] ([s] NVARCHAR(MAX), [b] VARBINARY(MAX))`})
}

func TestCreateTableErrors(t *testing.T) {
	s := xdal.NewSchema(sqlite.NewDialect())
	cases := map[string]func() *xdal.CreateTable{
		"no columns": func() *xdal.CreateTable { return s.CreateTable("t") },
		"bad table name": func() *xdal.CreateTable {
			t := s.CreateTable("bad name")
			t.Integer("a")
			return t
		},
		"bad column name": func() *xdal.CreateTable {
			t := s.CreateTable("t")
			t.Integer("a;b")
			return t
		},
		"auto increment with primary key": func() *xdal.CreateTable {
			t := s.CreateTable("t")
			t.AutoIncrement("id")
			t.String("code", 10).PrimaryKey()
			return t
		},
		"mixed primary keys": func() *xdal.CreateTable {
			t := s.CreateTable("t")
			t.String("a", 10).PrimaryKey()
			t.String("b", 10)
			t.PrimaryKey("a", "b")
			return t
		},
		"decimal without precision": func() *xdal.CreateTable {
			t := s.CreateTable("t")
			t.Decimal("d", 0, 0)
			return t
		},
		"unsupported default": func() *xdal.CreateTable {
			t := s.CreateTable("t")
			t.String("a", 10).Default([]int{1})
			return t
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := build().Build(); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestAlterTable(t *testing.T) {
	want := map[string][]string{
		xdal.DialectSQLite: {
			`ALTER TABLE "devices" ADD COLUMN "serial" TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE "devices" ADD COLUMN "rack_id" TEXT REFERENCES "racks" ("id") ON DELETE SET NULL`,
			`ALTER TABLE "devices" DROP COLUMN "old"`,
			`ALTER TABLE "devices" RENAME COLUMN "a" TO "b"`,
			`ALTER TABLE "devices" RENAME TO "hosts"`,
			`ALTER TABLE "hosts" DROP COLUMN "c"`,
		},
		xdal.DialectPostgres: {
			`ALTER TABLE "devices" ADD COLUMN "serial" VARCHAR(64) NOT NULL DEFAULT ''`,
			`ALTER TABLE "devices" ADD COLUMN "rack_id" VARCHAR(36)`,
			`ALTER TABLE "devices" ADD CONSTRAINT "fk_devices_rack_id_c0a16f2e" FOREIGN KEY ("rack_id") REFERENCES "racks" ("id") ON DELETE SET NULL`,
			`ALTER TABLE "devices" DROP COLUMN "old"`,
			`ALTER TABLE "devices" RENAME COLUMN "a" TO "b"`,
			`ALTER TABLE "devices" RENAME TO "hosts"`,
			`ALTER TABLE "hosts" DROP COLUMN "c"`,
		},
		xdal.DialectMySQL: {
			"ALTER TABLE `devices` ADD COLUMN `serial` VARCHAR(64) NOT NULL DEFAULT ''",
			"ALTER TABLE `devices` ADD COLUMN `rack_id` VARCHAR(36)",
			"ALTER TABLE `devices` ADD CONSTRAINT `fk_devices_rack_id_c0a16f2e` FOREIGN KEY (`rack_id`) REFERENCES `racks` (`id`) ON DELETE SET NULL",
			"ALTER TABLE `devices` DROP COLUMN `old`",
			"ALTER TABLE `devices` RENAME COLUMN `a` TO `b`",
			"ALTER TABLE `devices` RENAME TO `hosts`",
			"ALTER TABLE `hosts` DROP COLUMN `c`",
		},
		xdal.DialectMSSQL: {
			`ALTER TABLE [devices] ADD [serial] NVARCHAR(64) NOT NULL DEFAULT N''`,
			`ALTER TABLE [devices] ADD [rack_id] NVARCHAR(36)`,
			`ALTER TABLE [devices] ADD CONSTRAINT [fk_devices_rack_id_c0a16f2e] FOREIGN KEY ([rack_id]) REFERENCES [racks] ([id]) ON DELETE SET NULL`,
			mssqlDropDefault("devices", "old"),
			`ALTER TABLE [devices] DROP COLUMN [old]`,
			`EXEC sp_rename N'devices.a', N'b', N'COLUMN'`,
			`EXEC sp_rename N'devices', N'hosts'`,
			mssqlDropDefault("hosts", "c"),
			`ALTER TABLE [hosts] DROP COLUMN [c]`,
		},
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			a := xdal.NewSchema(d.dialect).AlterTable("devices")
			a.String("serial", 64).NotNull().Default("")
			a.String("rack_id", 36).References("racks", "id").OnDelete(xdal.SetNull)
			a.DropColumn("old").RenameColumn("a", "b").RenameTo("hosts").DropColumn("c")
			got, err := a.Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, got, want[d.name])
		})
	}
}

func mssqlDropDefault(table, column string) string {
	return "DECLARE @xdal_df NVARCHAR(256), @xdal_sql NVARCHAR(1024); SELECT @xdal_df = dc.name FROM sys.default_constraints dc " +
		"JOIN sys.columns c ON c.default_object_id = dc.object_id WHERE dc.parent_object_id = OBJECT_ID(N'" + table +
		"') AND c.name = N'" + column + "'; IF @xdal_df IS NOT NULL BEGIN SET @xdal_sql = N'ALTER TABLE [" + table +
		"] DROP CONSTRAINT ' + QUOTENAME(@xdal_df); EXEC sp_executesql @xdal_sql; END"
}

func TestUpsertColumnsMustBeInserted(t *testing.T) {
	for _, d := range allDialects() {
		qb := xdal.NewQueryBuilder(d.dialect)
		_, _, err := qb.Insert("t").Set("k", 1).Set("v", "y").OnConflict("k").DoUpdate("hits").Build()
		if !errors.Is(err, xdal.ErrUpsertColumnNotInserted) {
			t.Errorf("%s: update column not inserted: err = %v", d.name, err)
		}
		_, _, err = qb.Insert("t").Set("v", "y").OnConflict("k").DoNothing().Build()
		if !errors.Is(err, xdal.ErrUpsertColumnNotInserted) {
			t.Errorf("%s: conflict column not inserted: err = %v", d.name, err)
		}
		_, _, err = qb.Insert("t").Set("k", 1).OnConflict("k; DROP TABLE t").DoNothing().Build()
		if err == nil {
			t.Errorf("%s: unsafe conflict column accepted", d.name)
		}
	}
}

func TestSchemaValidatesColumnLists(t *testing.T) {
	s := xdal.NewSchema(postgres.NewDialect())
	if _, err := s.CreateIndex("idx", "t", "a); DROP TABLE t; --").Build(); err == nil {
		t.Error("unsafe index column accepted")
	}
	tb := s.CreateTable("t")
	tb.Integer("a")
	tb.Unique("a, b")
	if _, err := tb.Build(); err == nil {
		t.Error("unsafe unique column accepted")
	}
	tb = s.CreateTable("t")
	tb.Integer("a")
	tb.PrimaryKey("a (x)")
	if _, err := tb.Build(); err == nil {
		t.Error("unsafe primary key column accepted")
	}
	a := xdal.NewSchema(sqlite.NewDialect()).AlterTable("t")
	a.String("r", 10).References("other", "id) ON DELETE CASCADE --")
	if _, err := a.Build(); err == nil {
		t.Error("unsafe referenced column accepted")
	}
}

func TestMSSQLUnicodeDefaults(t *testing.T) {
	tb := xdal.NewSchema(mssql.NewDialect()).CreateTable("t")
	tb.String("name", 10).Default("日本")
	got, err := tb.Build()
	if err != nil {
		t.Fatal(err)
	}
	assertSQL(t, got, []string{`CREATE TABLE [t] ([name] NVARCHAR(10) DEFAULT N'日本')`})
}

func TestConstraintNamesDoNotCollide(t *testing.T) {
	s := xdal.NewSchema(postgres.NewDialect())
	a := s.CreateTable("user")
	a.String("profile_id", 36).References("profiles", "id")
	b := s.CreateTable("user_profile")
	b.String("id", 36).References("users", "id")
	sa, _ := a.Build()
	sb, _ := b.Build()
	name := func(sql string) string {
		start := strings.Index(sql, `CONSTRAINT "`) + len(`CONSTRAINT "`)
		return sql[start : start+strings.Index(sql[start:], `"`)]
	}
	if name(sa[0]) == name(sb[0]) {
		t.Errorf("both tables got constraint %q", name(sa[0]))
	}
}

func TestAlterTableRejectsKeys(t *testing.T) {
	a := xdal.NewSchema(postgres.NewDialect()).AlterTable("t")
	a.String("code", 10).Unique()
	if _, err := a.Build(); !errors.Is(err, xdal.ErrNotSupported) {
		t.Errorf("err = %v, want ErrNotSupported", err)
	}
}

func TestIndexesAndDrops(t *testing.T) {
	type out struct {
		create, createIfNotExists, drop, dropIfExists []string
		createErr, dropErr                            bool
	}
	want := map[string]out{
		xdal.DialectSQLite: {
			create:            []string{`CREATE UNIQUE INDEX "idx_a" ON "t" ("a", "b")`},
			createIfNotExists: []string{`CREATE UNIQUE INDEX IF NOT EXISTS "idx_a" ON "t" ("a", "b")`},
			drop:              []string{`DROP INDEX "idx_a"`},
			dropIfExists:      []string{`DROP INDEX IF EXISTS "idx_a"`},
		},
		xdal.DialectPostgres: {
			create:            []string{`CREATE UNIQUE INDEX "idx_a" ON "t" ("a", "b")`},
			createIfNotExists: []string{`CREATE UNIQUE INDEX IF NOT EXISTS "idx_a" ON "t" ("a", "b")`},
			drop:              []string{`DROP INDEX "idx_a"`},
			dropIfExists:      []string{`DROP INDEX IF EXISTS "idx_a"`},
		},
		xdal.DialectMySQL: {
			create:    []string{"CREATE UNIQUE INDEX `idx_a` ON `t` (`a`, `b`)"},
			drop:      []string{"DROP INDEX `idx_a` ON `t`"},
			createErr: true,
			dropErr:   true,
		},
		xdal.DialectMSSQL: {
			create:            []string{`CREATE UNIQUE INDEX [idx_a] ON [t] ([a], [b])`},
			createIfNotExists: []string{`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'idx_a' AND object_id = OBJECT_ID(N't')) CREATE UNIQUE INDEX [idx_a] ON [t] ([a], [b])`},
			drop:              []string{`DROP INDEX [idx_a] ON [t]`},
			dropIfExists:      []string{`DROP INDEX IF EXISTS [idx_a] ON [t]`},
		},
	}
	for _, d := range allDialects() {
		t.Run(d.name, func(t *testing.T) {
			s := xdal.NewSchema(d.dialect)
			w := want[d.name]
			got, err := s.CreateIndex("idx_a", "t", "a", "b").Unique().Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, got, w.create)

			got, err = s.CreateIndex("idx_a", "t", "a", "b").Unique().IfNotExists().Build()
			if w.createErr {
				if !errors.Is(err, xdal.ErrNotSupported) {
					t.Errorf("IfNotExists err = %v, want ErrNotSupported", err)
				}
			} else {
				assertSQL(t, got, w.createIfNotExists)
			}

			got, err = s.DropIndex("idx_a", "t").Build()
			if err != nil {
				t.Fatal(err)
			}
			assertSQL(t, got, w.drop)

			got, err = s.DropIndex("idx_a", "t").IfExists().Build()
			if w.dropErr {
				if !errors.Is(err, xdal.ErrNotSupported) {
					t.Errorf("IfExists err = %v, want ErrNotSupported", err)
				}
			} else {
				assertSQL(t, got, w.dropIfExists)
			}

			got, err = s.DropTable("t").IfExists().Build()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got[0], "DROP TABLE IF EXISTS ") {
				t.Errorf("drop table = %q", got[0])
			}
		})
	}
}

func TestConstraintNameShortened(t *testing.T) {
	tb := xdal.NewSchema(postgres.NewDialect()).CreateTable("a_really_long_table_name_for_testing_identifier_limits")
	tb.String("another_rather_long_column_name", 10).Unique()
	got, err := tb.Build()
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(got[0], `CONSTRAINT "`) + len(`CONSTRAINT "`)
	name := got[0][start : start+strings.Index(got[0][start:], `"`)]
	if len(name) > 63 {
		t.Errorf("constraint name %q is %d characters", name, len(name))
	}
}

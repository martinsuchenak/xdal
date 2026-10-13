package xdal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// TypeMap maps portable column types to native type names. Entries may contain
// %d verbs: String and Binary take the size, Decimal takes precision and scale.
type TypeMap struct {
	String        string
	Text          string
	Integer       string
	BigInteger    string
	Boolean       string
	Float         string
	Decimal       string
	Timestamp     string
	Date          string
	UUID          string
	JSON          string
	Binary        string
	Blob          string
	AutoIncrement string // full column definition, including PRIMARY KEY

	// MaxStringSize is the largest String size rendered with String; larger sizes use StringMax. 0 means no limit.
	MaxStringSize int
	StringMax     string
	// MaxBinarySize is the largest Binary size rendered with Binary; larger sizes use Blob. 0 means no limit.
	MaxBinarySize int
}

// IfNotExistsStyle determines how IF NOT EXISTS / IF EXISTS guards are rendered.
type IfNotExistsStyle int

const (
	IfNotExistsUnsupported IfNotExistsStyle = iota // the guard returns ErrNotSupported
	IfNotExistsClause                              // CREATE ... IF NOT EXISTS
	IfNotExistsObjectGuard                         // IF OBJECT_ID(...) IS NULL CREATE ... — SQL Server
)

// RenameStyle determines how column and table renames are rendered.
type RenameStyle int

const (
	RenameAlterTable RenameStyle = iota // ALTER TABLE t RENAME [COLUMN a] TO b
	RenameSPRename                      // EXEC sp_rename — SQL Server
)

// DDLOptions configures schema statement generation for a dialect.
type DDLOptions struct {
	Types TypeMap
	// DefaultNow is the expression used by Column.DefaultNow.
	DefaultNow string
	// BoolLiteral renders boolean defaults. Set it to the outer dialect's BoolLiteral.
	BoolLiteral func(bool) string
	// AddColumnKeyword is "ADD COLUMN", or "ADD" for SQL Server.
	AddColumnKeyword string
	// TableIfNotExists renders CreateTable.IfNotExists.
	TableIfNotExists IfNotExistsStyle
	// IndexIfNotExists renders CreateIndex.IfNotExists.
	IndexIfNotExists IfNotExistsStyle
	// InlineIndexes declares CreateTable indexes inside CREATE TABLE (MySQL).
	InlineIndexes bool
	// DropIndexOnTable renders DROP INDEX name ON table (MySQL, SQL Server).
	DropIndexOnTable bool
	// DropIndexIfExists allows DropIndex.IfExists.
	DropIndexIfExists bool
	// Rename selects the rename syntax.
	Rename RenameStyle
	// InlineAddColumnReferences adds foreign keys inside ADD COLUMN (SQLite) instead of a separate ADD CONSTRAINT.
	InlineAddColumnReferences bool
	// ParenthesizeLOBDefaults wraps literal defaults of Text, JSON and Blob columns in parentheses (MySQL 8.0.13+).
	ParenthesizeLOBDefaults bool
	// RestrictKeyword renders FKAction Restrict ("RESTRICT", or "NO ACTION" for SQL Server).
	RestrictKeyword string
	// UnicodeStringPrefix is prepended to string literal defaults ("N" for SQL Server NVARCHAR columns).
	UnicodeStringPrefix string
	// DropColumnDefaultConstraint drops a column's default constraint before dropping the column (SQL Server).
	DropColumnDefaultConstraint bool
}

// BuildCreateTable renders CREATE TABLE plus any indexes declared on the table.
func (d *BaseDialect) BuildCreateTable(t *CreateTable) ([]string, error) {
	if t.name == "" {
		return nil, ErrEmptyTable
	}
	if len(t.columns) == 0 {
		return nil, ErrEmptyColumns
	}
	if err := SafeIdentifier(t.name); err != nil {
		return nil, err
	}

	pk, err := d.tablePrimaryKey(t)
	if err != nil {
		return nil, err
	}
	if err := safeIdentifiers(pk); err != nil {
		return nil, err
	}
	for _, cols := range t.uniques {
		if len(cols) == 0 {
			return nil, ErrEmptyColumns
		}
		if err := safeIdentifiers(cols); err != nil {
			return nil, err
		}
	}

	var defs []string
	for _, c := range t.columns {
		def, err := d.columnDefinition(c)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	if len(pk) > 0 {
		defs = append(defs, "PRIMARY KEY ("+d.quoteColumns(pk)+")")
	}
	for _, c := range t.columns {
		if c.unique {
			defs = append(defs, d.uniqueConstraint(t.name, []string{c.name}))
		}
	}
	for _, cols := range t.uniques {
		defs = append(defs, d.uniqueConstraint(t.name, cols))
	}
	for _, c := range t.columns {
		if c.ref != nil {
			fk, err := d.foreignKeyConstraint(t.name, c)
			if err != nil {
				return nil, err
			}
			defs = append(defs, fk)
		}
	}
	if d.DDL.InlineIndexes {
		for _, i := range t.indexes {
			if err := i.validate(); err != nil {
				return nil, err
			}
			kw := "INDEX "
			if i.unique {
				kw = "UNIQUE INDEX "
			}
			defs = append(defs, kw+d.QuoteIdentifier(i.name)+" ("+d.quoteColumns(i.columns)+")")
		}
	}

	stmt := "CREATE TABLE " + d.QuoteIdentifier(t.name) + " (" + strings.Join(defs, ", ") + ")"
	if t.ifNotExists {
		stmt, err = d.guardCreate(d.DDL.TableIfNotExists, stmt, "IF OBJECT_ID(N"+d.ddlString(t.name)+", N'U') IS NULL ")
		if err != nil {
			return nil, err
		}
	}
	out := []string{stmt}

	if !d.DDL.InlineIndexes {
		for _, i := range t.indexes {
			idx := *i
			idx.ifNotExists = t.ifNotExists
			sqls, err := d.BuildCreateIndex(&idx)
			if err != nil {
				return nil, err
			}
			out = append(out, sqls...)
		}
	}
	return out, nil
}

func (d *BaseDialect) tablePrimaryKey(t *CreateTable) ([]string, error) {
	var colPK []string
	auto := 0
	for _, c := range t.columns {
		if c.typ == TypeAutoIncrement {
			auto++
			continue
		}
		if c.primary {
			colPK = append(colPK, c.name)
		}
	}
	if auto > 1 {
		return nil, fmt.Errorf("xdal: table %q has more than one AutoIncrement column", t.name)
	}
	if len(t.primaryKey) > 0 && len(colPK) > 0 {
		return nil, fmt.Errorf("xdal: table %q mixes CreateTable.PrimaryKey with Column.PrimaryKey", t.name)
	}
	pk := colPK
	if len(t.primaryKey) > 0 {
		pk = t.primaryKey
	}
	if auto > 0 && len(pk) > 0 {
		return nil, fmt.Errorf("xdal: table %q combines an AutoIncrement column with another primary key", t.name)
	}
	return pk, nil
}

// BuildAlterTable renders one statement per operation.
func (d *BaseDialect) BuildAlterTable(a *AlterTable) ([]string, error) {
	if a.name == "" {
		return nil, ErrEmptyTable
	}
	if len(a.ops) == 0 {
		return nil, ErrEmptyColumns
	}
	if err := SafeIdentifier(a.name); err != nil {
		return nil, err
	}

	table := a.name
	var out []string
	for _, op := range a.ops {
		qt := d.QuoteIdentifier(table)
		switch op.kind {
		case alterAddColumn:
			sqls, err := d.addColumn(table, op.column)
			if err != nil {
				return nil, err
			}
			out = append(out, sqls...)
		case alterDropColumn:
			if err := SafeIdentifier(op.name); err != nil {
				return nil, err
			}
			if d.DDL.DropColumnDefaultConstraint {
				out = append(out, d.dropDefaultConstraint(table, op.name))
			}
			out = append(out, "ALTER TABLE "+qt+" DROP COLUMN "+d.QuoteIdentifier(op.name))
		case alterRenameColumn:
			if err := SafeIdentifier(op.name); err != nil {
				return nil, err
			}
			if err := SafeIdentifier(op.newName); err != nil {
				return nil, err
			}
			if d.DDL.Rename == RenameSPRename {
				out = append(out, "EXEC sp_rename N"+d.ddlString(table+"."+op.name)+", N"+d.ddlString(op.newName)+", N'COLUMN'")
			} else {
				out = append(out, "ALTER TABLE "+qt+" RENAME COLUMN "+d.QuoteIdentifier(op.name)+" TO "+d.QuoteIdentifier(op.newName))
			}
		case alterRenameTable:
			if err := SafeIdentifier(op.newName); err != nil {
				return nil, err
			}
			if d.DDL.Rename == RenameSPRename {
				out = append(out, "EXEC sp_rename N"+d.ddlString(table)+", N"+d.ddlString(op.newName))
			} else {
				out = append(out, "ALTER TABLE "+qt+" RENAME TO "+d.QuoteIdentifier(op.newName))
			}
			table = op.newName
		}
	}
	return out, nil
}

func (d *BaseDialect) addColumn(table string, c *Column) ([]string, error) {
	if c.typ == TypeAutoIncrement || c.primary || c.unique {
		return nil, fmt.Errorf("%w: AlterTable cannot add primary key, unique or AutoIncrement columns; add the column, then CreateIndex", ErrNotSupported)
	}
	def, err := d.columnDefinition(c)
	if err != nil {
		return nil, err
	}
	qt := d.QuoteIdentifier(table)
	if c.ref == nil {
		return []string{"ALTER TABLE " + qt + " " + d.DDL.AddColumnKeyword + " " + def}, nil
	}
	if err := SafeIdentifier(c.ref.table); err != nil {
		return nil, err
	}
	if err := SafeIdentifier(c.ref.column); err != nil {
		return nil, err
	}
	if d.DDL.InlineAddColumnReferences {
		def += " REFERENCES " + d.QuoteIdentifier(c.ref.table) + " (" + d.QuoteIdentifier(c.ref.column) + ")" + d.fkActions(c.ref)
		return []string{"ALTER TABLE " + qt + " " + d.DDL.AddColumnKeyword + " " + def}, nil
	}
	fk, err := d.foreignKeyConstraint(table, c)
	if err != nil {
		return nil, err
	}
	return []string{
		"ALTER TABLE " + qt + " " + d.DDL.AddColumnKeyword + " " + def,
		"ALTER TABLE " + qt + " ADD " + fk,
	}, nil
}

// BuildDropTable renders DROP TABLE.
func (d *BaseDialect) BuildDropTable(t *DropTable) ([]string, error) {
	if t.name == "" {
		return nil, ErrEmptyTable
	}
	if err := SafeIdentifier(t.name); err != nil {
		return nil, err
	}
	stmt := "DROP TABLE "
	if t.ifExists {
		stmt += "IF EXISTS "
	}
	return []string{stmt + d.QuoteIdentifier(t.name)}, nil
}

func (i *CreateIndex) validate() error {
	if i.name == "" {
		return fmt.Errorf("xdal: index name is required")
	}
	if len(i.columns) == 0 {
		return ErrEmptyColumns
	}
	if err := safeIdentifiers(i.columns); err != nil {
		return err
	}
	return SafeIdentifier(i.name)
}

// BuildCreateIndex renders CREATE INDEX.
func (d *BaseDialect) BuildCreateIndex(i *CreateIndex) ([]string, error) {
	if i.table == "" {
		return nil, ErrEmptyTable
	}
	if err := i.validate(); err != nil {
		return nil, err
	}
	if err := SafeIdentifier(i.table); err != nil {
		return nil, err
	}
	kw := "CREATE INDEX "
	if i.unique {
		kw = "CREATE UNIQUE INDEX "
	}
	body := d.QuoteIdentifier(i.name) + " ON " + d.QuoteIdentifier(i.table) + " (" + d.quoteColumns(i.columns) + ")"
	if !i.ifNotExists {
		return []string{kw + body}, nil
	}
	switch d.DDL.IndexIfNotExists {
	case IfNotExistsClause:
		return []string{kw + "IF NOT EXISTS " + body}, nil
	case IfNotExistsObjectGuard:
		guard := "IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N" + d.ddlString(i.name) +
			" AND object_id = OBJECT_ID(N" + d.ddlString(i.table) + ")) "
		return []string{guard + kw + body}, nil
	default:
		return nil, fmt.Errorf("%w: CREATE INDEX IF NOT EXISTS", ErrNotSupported)
	}
}

// BuildDropIndex renders DROP INDEX.
func (d *BaseDialect) BuildDropIndex(i *DropIndex) ([]string, error) {
	if i.name == "" {
		return nil, fmt.Errorf("xdal: index name is required")
	}
	if err := SafeIdentifier(i.name); err != nil {
		return nil, err
	}
	if i.ifExists && !d.DDL.DropIndexIfExists {
		return nil, fmt.Errorf("%w: DROP INDEX IF EXISTS", ErrNotSupported)
	}
	stmt := "DROP INDEX "
	if i.ifExists {
		stmt += "IF EXISTS "
	}
	stmt += d.QuoteIdentifier(i.name)
	if d.DDL.DropIndexOnTable {
		if i.table == "" {
			return nil, ErrEmptyTable
		}
		if err := SafeIdentifier(i.table); err != nil {
			return nil, err
		}
		stmt += " ON " + d.QuoteIdentifier(i.table)
	}
	return []string{stmt}, nil
}

func (d *BaseDialect) columnDefinition(c *Column) (string, error) {
	if c.name == "" {
		return "", fmt.Errorf("xdal: column name is required")
	}
	if err := SafeIdentifier(c.name); err != nil {
		return "", err
	}
	if c.typ == TypeAutoIncrement {
		if d.DDL.Types.AutoIncrement == "" {
			return "", fmt.Errorf("%w: AutoIncrement", ErrNotSupported)
		}
		return d.QuoteIdentifier(c.name) + " " + d.DDL.Types.AutoIncrement, nil
	}
	typ, err := d.columnType(c)
	if err != nil {
		return "", err
	}
	def := d.QuoteIdentifier(c.name) + " " + typ
	if c.notNull || c.primary {
		def += " NOT NULL"
	}
	if c.def != nil {
		lit, err := d.defaultClause(c)
		if err != nil {
			return "", err
		}
		def += " DEFAULT " + lit
	}
	return def, nil
}

func (d *BaseDialect) columnType(c *Column) (string, error) {
	t := d.DDL.Types
	var format string
	switch c.typ {
	case TypeString:
		size := c.size
		if size <= 0 {
			size = DefaultStringSize
		}
		if t.MaxStringSize > 0 && size > t.MaxStringSize && t.StringMax != "" {
			return t.StringMax, nil
		}
		return sized(t.String, size), nil
	case TypeText:
		format = t.Text
	case TypeInteger:
		format = t.Integer
	case TypeBigInteger:
		format = t.BigInteger
	case TypeBoolean:
		format = t.Boolean
	case TypeFloat:
		format = t.Float
	case TypeDecimal:
		if c.precision <= 0 {
			return "", fmt.Errorf("xdal: decimal column %q needs a precision", c.name)
		}
		if strings.Count(t.Decimal, "%d") == 2 {
			return fmt.Sprintf(t.Decimal, c.precision, c.scale), nil
		}
		format = t.Decimal
	case TypeTimestamp:
		format = t.Timestamp
	case TypeDate:
		format = t.Date
	case TypeUUID:
		format = t.UUID
	case TypeJSON:
		format = t.JSON
	case TypeBinary:
		size := c.size
		if size <= 0 {
			size = DefaultStringSize
		}
		if t.MaxBinarySize > 0 && size > t.MaxBinarySize {
			return t.Blob, nil
		}
		return sized(t.Binary, size), nil
	case TypeBlob:
		format = t.Blob
	default:
		return "", fmt.Errorf("xdal: unknown column type %d for %q", c.typ, c.name)
	}
	if format == "" {
		return "", fmt.Errorf("%w: column type %d", ErrNotSupported, c.typ)
	}
	return format, nil
}

func sized(format string, size int) string {
	if strings.Contains(format, "%d") {
		return fmt.Sprintf(format, size)
	}
	return format
}

func (d *BaseDialect) defaultClause(c *Column) (string, error) {
	switch {
	case c.def.now:
		return d.DDL.DefaultNow, nil
	case c.def.expr != "":
		return c.def.expr, nil
	}
	lit, err := d.ddlLiteral(c.def.value)
	if err != nil {
		return "", fmt.Errorf("xdal: default for column %q: %w", c.name, err)
	}
	if d.DDL.ParenthesizeLOBDefaults && (c.typ == TypeText || c.typ == TypeJSON || c.typ == TypeBlob) {
		return "(" + lit + ")", nil
	}
	return lit, nil
}

func (d *BaseDialect) ddlLiteral(v interface{}) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case string:
		return d.DDL.UnicodeStringPrefix + d.ddlString(x), nil
	case bool:
		if d.DDL.BoolLiteral != nil {
			return d.DDL.BoolLiteral(x), nil
		}
		return d.BoolLiteral(x), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", x), nil
	default:
		return "", fmt.Errorf("unsupported literal type %T", v)
	}
}

// ddlString renders a string literal, escaping quotes (and backslashes when the dialect treats them as escapes).
func (d *BaseDialect) ddlString(s string) string {
	if d.BackslashEscapes {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (d *BaseDialect) guardCreate(style IfNotExistsStyle, stmt, objectGuard string) (string, error) {
	switch style {
	case IfNotExistsClause:
		return strings.Replace(stmt, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1), nil
	case IfNotExistsObjectGuard:
		return objectGuard + stmt, nil
	default:
		return "", fmt.Errorf("%w: CREATE TABLE IF NOT EXISTS", ErrNotSupported)
	}
}

func (d *BaseDialect) uniqueConstraint(table string, columns []string) string {
	return "CONSTRAINT " + d.QuoteIdentifier(constraintName("uq", table, columns)) + " UNIQUE (" + d.quoteColumns(columns) + ")"
}

func (d *BaseDialect) foreignKeyConstraint(table string, c *Column) (string, error) {
	if err := SafeIdentifier(c.ref.table); err != nil {
		return "", err
	}
	if err := SafeIdentifier(c.ref.column); err != nil {
		return "", err
	}
	return "CONSTRAINT " + d.QuoteIdentifier(constraintName("fk", table, []string{c.name})) +
		" FOREIGN KEY (" + d.QuoteIdentifier(c.name) + ") REFERENCES " + d.QuoteIdentifier(c.ref.table) +
		" (" + d.QuoteIdentifier(c.ref.column) + ")" + d.fkActions(c.ref), nil
}

func (d *BaseDialect) fkActions(r *reference) string {
	var s string
	if a := d.fkAction(r.onDelete); a != "" {
		s += " ON DELETE " + a
	}
	if a := d.fkAction(r.onUpdate); a != "" {
		s += " ON UPDATE " + a
	}
	return s
}

func (d *BaseDialect) fkAction(a FKAction) string {
	switch a {
	case Restrict:
		if d.DDL.RestrictKeyword != "" {
			return d.DDL.RestrictKeyword
		}
		return "RESTRICT"
	case Cascade:
		return "CASCADE"
	case SetNull:
		return "SET NULL"
	default:
		return ""
	}
}

// dropDefaultConstraint finds the system-named default constraint of a SQL Server
// column and drops it, so the column itself can be dropped.
func (d *BaseDialect) dropDefaultConstraint(table, column string) string {
	alter := "ALTER TABLE " + d.QuoteIdentifier(table) + " DROP CONSTRAINT "
	return "DECLARE @xdal_df NVARCHAR(256), @xdal_sql NVARCHAR(1024); " +
		"SELECT @xdal_df = dc.name FROM sys.default_constraints dc " +
		"JOIN sys.columns c ON c.default_object_id = dc.object_id " +
		"WHERE dc.parent_object_id = OBJECT_ID(N" + d.ddlString(table) + ") AND c.name = N" + d.ddlString(column) + "; " +
		"IF @xdal_df IS NOT NULL BEGIN SET @xdal_sql = N" + d.ddlString(alter) + " + QUOTENAME(@xdal_df); " +
		"EXEC sp_executesql @xdal_sql; END"
}

// constraintName builds prefix_table_cols plus a hash of the table and columns,
// so names stay unique when underscores make them ambiguous (table "user" with
// column "profile_id" versus table "user_profile" with column "id"). Long names
// are shortened to fit PostgreSQL's 63-character identifier limit.
func constraintName(prefix, table string, columns []string) string {
	sum := sha256.Sum256([]byte(table + "\x00" + strings.Join(columns, "\x00")))
	hash := hex.EncodeToString(sum[:])[:8]
	name := prefix + "_" + strings.ReplaceAll(table, ".", "_") + "_" + strings.Join(columns, "_")
	if len(name) > 54 {
		name = name[:54]
	}
	return name + "_" + hash
}

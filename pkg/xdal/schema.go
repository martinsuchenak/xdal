package xdal

import (
	"context"
)

// ColumnType identifies a portable column type. Each dialect maps it to a native
// type through its TypeMap.
type ColumnType int

const (
	TypeString        ColumnType = iota + 1 // variable-length string with a maximum size
	TypeText                                // unbounded text
	TypeInteger                             // 32-bit integer
	TypeBigInteger                          // 64-bit integer
	TypeBoolean                             // boolean
	TypeFloat                               // double-precision floating point
	TypeDecimal                             // exact decimal with precision and scale
	TypeTimestamp                           // date and time
	TypeDate                                // calendar date
	TypeUUID                                // UUID stored natively or as 36-character text
	TypeJSON                                // JSON document
	TypeBinary                              // variable-length binary with a maximum size
	TypeBlob                                // unbounded binary
	TypeAutoIncrement                       // 64-bit auto-incrementing primary key
)

// FKAction is a foreign key ON DELETE / ON UPDATE action.
type FKAction int

const (
	NoAction FKAction = iota // database default; no clause is emitted
	Restrict                 // RESTRICT, or NO ACTION where RESTRICT is unavailable
	Cascade                  // CASCADE
	SetNull                  // SET NULL
)

// DefaultStringSize is used by String and Binary columns created with size 0.
const DefaultStringSize = 255

// Statement is a schema change that renders to one or more SQL statements.
type Statement interface {
	Build() ([]string, error)
}

// ExecStatements builds each statement and executes the resulting SQL in order.
func ExecStatements(ctx context.Context, exec DBExecutor, stmts ...Statement) error {
	for _, s := range stmts {
		sqls, err := s.Build()
		if err != nil {
			return err
		}
		for _, q := range sqls {
			if _, err := exec.Exec(ctx, q); err != nil {
				return err
			}
		}
	}
	return nil
}

// Schema creates schema statements for a dialect.
type Schema struct {
	dialect Dialect
}

// NewSchema returns a Schema that renders statements for the given dialect.
func NewSchema(d Dialect) *Schema {
	return &Schema{dialect: d}
}

// Schema returns a Schema for this connection's dialect.
func (b *BaseDB) Schema() *Schema {
	return NewSchema(b.dialect)
}

// Schema returns a Schema for this builder's dialect.
func (qb *QueryBuilder) Schema() *Schema {
	return NewSchema(qb.dialect)
}

// Column describes a column for CreateTable or AlterTable.
type Column struct {
	name      string
	typ       ColumnType
	size      int
	precision int
	scale     int
	notNull   bool
	primary   bool
	unique    bool
	def       *columnDefault
	ref       *reference
}

type columnDefault struct {
	value interface{}
	expr  string
	now   bool
}

type reference struct {
	table    string
	column   string
	onDelete FKAction
	onUpdate FKAction
}

// NewColumn returns a column of the given portable type.
func NewColumn(name string, typ ColumnType) *Column {
	return &Column{name: name, typ: typ}
}

// Size sets the maximum size of a String or Binary column.
func (c *Column) Size(n int) *Column {
	c.size = n
	return c
}

// Precision sets the precision and scale of a Decimal column.
func (c *Column) Precision(precision, scale int) *Column {
	c.precision = precision
	c.scale = scale
	return c
}

// NotNull marks the column NOT NULL.
func (c *Column) NotNull() *Column {
	c.notNull = true
	return c
}

// PrimaryKey makes the column part of the table's primary key. Calling it on
// several columns creates a composite key.
func (c *Column) PrimaryKey() *Column {
	c.primary = true
	return c
}

// Unique adds a single-column UNIQUE constraint.
func (c *Column) Unique() *Column {
	c.unique = true
	return c
}

// Default sets a literal default value (string, bool, integer or float).
func (c *Column) Default(v interface{}) *Column {
	c.def = &columnDefault{value: v}
	return c
}

// DefaultExpr sets a raw SQL default expression. Only pass trusted, hardcoded strings.
func (c *Column) DefaultExpr(expr string) *Column {
	c.def = &columnDefault{expr: expr}
	return c
}

// DefaultNow defaults the column to the current timestamp.
func (c *Column) DefaultNow() *Column {
	c.def = &columnDefault{now: true}
	return c
}

// References adds a foreign key to table(column).
func (c *Column) References(table, column string) *Column {
	c.ref = &reference{table: table, column: column}
	return c
}

// OnDelete sets the foreign key ON DELETE action. Requires References.
func (c *Column) OnDelete(a FKAction) *Column {
	if c.ref != nil {
		c.ref.onDelete = a
	}
	return c
}

// OnUpdate sets the foreign key ON UPDATE action. Requires References.
func (c *Column) OnUpdate(a FKAction) *Column {
	if c.ref != nil {
		c.ref.onUpdate = a
	}
	return c
}

// columnSet provides typed column constructors shared by CreateTable and AlterTable.
type columnSet struct {
	add func(*Column)
}

func (s columnSet) column(name string, typ ColumnType) *Column {
	c := NewColumn(name, typ)
	s.add(c)
	return c
}

// String adds a variable-length string column. Size 0 uses DefaultStringSize.
func (s columnSet) String(name string, size int) *Column {
	return s.column(name, TypeString).Size(size)
}

// Text adds an unbounded text column.
func (s columnSet) Text(name string) *Column { return s.column(name, TypeText) }

// Integer adds a 32-bit integer column.
func (s columnSet) Integer(name string) *Column { return s.column(name, TypeInteger) }

// BigInteger adds a 64-bit integer column.
func (s columnSet) BigInteger(name string) *Column { return s.column(name, TypeBigInteger) }

// Boolean adds a boolean column.
func (s columnSet) Boolean(name string) *Column { return s.column(name, TypeBoolean) }

// Float adds a double-precision floating point column.
func (s columnSet) Float(name string) *Column { return s.column(name, TypeFloat) }

// Decimal adds an exact decimal column.
func (s columnSet) Decimal(name string, precision, scale int) *Column {
	return s.column(name, TypeDecimal).Precision(precision, scale)
}

// Timestamp adds a date-and-time column.
func (s columnSet) Timestamp(name string) *Column { return s.column(name, TypeTimestamp) }

// Date adds a calendar date column.
func (s columnSet) Date(name string) *Column { return s.column(name, TypeDate) }

// UUID adds a UUID column (native on PostgreSQL, 36-character text elsewhere).
// PostgreSQL rejects values that are not valid UUIDs; use String for free-form IDs.
func (s columnSet) UUID(name string) *Column { return s.column(name, TypeUUID) }

// JSON adds a JSON column (JSONB on PostgreSQL, JSON on MySQL, text elsewhere).
func (s columnSet) JSON(name string) *Column { return s.column(name, TypeJSON) }

// Binary adds a variable-length binary column. Size 0 uses DefaultStringSize.
// Binary values compare bytewise on every dialect, so fixed-width keys sort correctly.
func (s columnSet) Binary(name string, size int) *Column {
	return s.column(name, TypeBinary).Size(size)
}

// Blob adds an unbounded binary column.
func (s columnSet) Blob(name string) *Column { return s.column(name, TypeBlob) }

// AutoIncrement adds a 64-bit auto-incrementing primary key column.
func (s columnSet) AutoIncrement(name string) *Column {
	return s.column(name, TypeAutoIncrement)
}

// CreateTable builds a CREATE TABLE statement and the indexes declared with it.
type CreateTable struct {
	columnSet
	name        string
	ifNotExists bool
	columns     []*Column
	primaryKey  []string
	uniques     [][]string
	indexes     []*CreateIndex
	dialect     Dialect
}

// CreateTable starts a CREATE TABLE statement. Add columns with the typed methods.
func (s *Schema) CreateTable(name string) *CreateTable {
	t := &CreateTable{name: name, dialect: s.dialect}
	t.add = func(c *Column) { t.columns = append(t.columns, c) }
	return t
}

// IfNotExists skips creation when the table already exists. Indexes declared on
// the table are guarded the same way where the dialect allows it.
func (t *CreateTable) IfNotExists() *CreateTable {
	t.ifNotExists = true
	return t
}

// Column adds a column built with NewColumn.
func (t *CreateTable) Column(c *Column) *CreateTable {
	t.add(c)
	return t
}

// PrimaryKey sets a composite primary key. Do not combine with Column.PrimaryKey.
func (t *CreateTable) PrimaryKey(columns ...string) *CreateTable {
	t.primaryKey = columns
	return t
}

// Unique adds a multi-column UNIQUE constraint.
func (t *CreateTable) Unique(columns ...string) *CreateTable {
	t.uniques = append(t.uniques, columns)
	return t
}

// Index declares an index created together with the table.
func (t *CreateTable) Index(name string, columns ...string) *CreateIndex {
	i := &CreateIndex{name: name, table: t.name, columns: columns, dialect: t.dialect}
	t.indexes = append(t.indexes, i)
	return i
}

// Build renders the statements.
func (t *CreateTable) Build() ([]string, error) {
	return t.dialect.BuildCreateTable(t)
}

type alterKind int

const (
	alterAddColumn alterKind = iota
	alterDropColumn
	alterRenameColumn
	alterRenameTable
)

type alterOp struct {
	kind    alterKind
	column  *Column
	name    string
	newName string
}

// AlterTable builds ALTER TABLE statements. Operations render in the order they
// are added, one statement each, which every dialect accepts.
type AlterTable struct {
	columnSet
	name    string
	ops     []alterOp
	dialect Dialect
}

// AlterTable starts a set of changes to an existing table. The typed column
// methods add columns.
func (s *Schema) AlterTable(name string) *AlterTable {
	a := &AlterTable{name: name, dialect: s.dialect}
	a.add = func(c *Column) { a.ops = append(a.ops, alterOp{kind: alterAddColumn, column: c}) }
	return a
}

// AddColumn adds a column built with NewColumn.
func (a *AlterTable) AddColumn(c *Column) *AlterTable {
	a.add(c)
	return a
}

// DropColumn removes a column.
func (a *AlterTable) DropColumn(name string) *AlterTable {
	a.ops = append(a.ops, alterOp{kind: alterDropColumn, name: name})
	return a
}

// RenameColumn renames a column.
func (a *AlterTable) RenameColumn(from, to string) *AlterTable {
	a.ops = append(a.ops, alterOp{kind: alterRenameColumn, name: from, newName: to})
	return a
}

// RenameTo renames the table. Later operations apply to the new name.
func (a *AlterTable) RenameTo(name string) *AlterTable {
	a.ops = append(a.ops, alterOp{kind: alterRenameTable, newName: name})
	return a
}

// Build renders the statements.
func (a *AlterTable) Build() ([]string, error) {
	return a.dialect.BuildAlterTable(a)
}

// DropTable builds a DROP TABLE statement.
type DropTable struct {
	name     string
	ifExists bool
	dialect  Dialect
}

// DropTable starts a DROP TABLE statement.
func (s *Schema) DropTable(name string) *DropTable {
	return &DropTable{name: name, dialect: s.dialect}
}

// IfExists skips the drop when the table does not exist.
func (t *DropTable) IfExists() *DropTable {
	t.ifExists = true
	return t
}

// Build renders the statement.
func (t *DropTable) Build() ([]string, error) {
	return t.dialect.BuildDropTable(t)
}

// CreateIndex builds a CREATE INDEX statement.
type CreateIndex struct {
	name        string
	table       string
	columns     []string
	unique      bool
	ifNotExists bool
	dialect     Dialect
}

// CreateIndex starts a CREATE INDEX statement.
func (s *Schema) CreateIndex(name, table string, columns ...string) *CreateIndex {
	return &CreateIndex{name: name, table: table, columns: columns, dialect: s.dialect}
}

// Unique makes the index unique.
func (i *CreateIndex) Unique() *CreateIndex {
	i.unique = true
	return i
}

// IfNotExists skips creation when the index exists. MySQL has no such clause and
// returns ErrNotSupported.
func (i *CreateIndex) IfNotExists() *CreateIndex {
	i.ifNotExists = true
	return i
}

// Build renders the statement.
func (i *CreateIndex) Build() ([]string, error) {
	return i.dialect.BuildCreateIndex(i)
}

// DropIndex builds a DROP INDEX statement. The table is required by MySQL and SQL Server.
type DropIndex struct {
	name     string
	table    string
	ifExists bool
	dialect  Dialect
}

// DropIndex starts a DROP INDEX statement.
func (s *Schema) DropIndex(name, table string) *DropIndex {
	return &DropIndex{name: name, table: table, dialect: s.dialect}
}

// IfExists skips the drop when the index does not exist. MySQL has no such clause
// and returns ErrNotSupported.
func (i *DropIndex) IfExists() *DropIndex {
	i.ifExists = true
	return i
}

// Build renders the statement.
func (i *DropIndex) Build() ([]string, error) {
	return i.dialect.BuildDropIndex(i)
}

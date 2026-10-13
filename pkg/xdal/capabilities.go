package xdal

// Dialect names returned by Dialect.Name().
const (
	DialectSQLite   = "sqlite"
	DialectPostgres = "postgres"
	DialectMySQL    = "mysql"
	DialectMSSQL    = "mssql"
)

// Capabilities describes optional SQL features supported by a dialect, so callers
// can choose a code path without comparing dialect names.
type Capabilities struct {
	// Returning is true when INSERT/UPDATE/DELETE can return rows (RETURNING or OUTPUT).
	Returning bool
	// Upsert is true when InsertQuery.OnConflict is supported.
	Upsert bool
	// RowLocks is true when SelectQuery.ForUpdate takes row locks. When false,
	// ForUpdate is accepted and ignored (SQLite serializes writers instead).
	RowLocks bool
	// SkipLocked is true when SelectQuery.SkipLocked skips rows locked by others.
	SkipLocked bool
	// TransactionalDDL is true when DDL statements can be rolled back as part of
	// a transaction. MySQL commits implicitly on DDL.
	TransactionalDDL bool
	// SharedAppLocks is true when named locks from DB.Lock exclude other processes
	// and connections. When false, locks exclude only within the current DB instance.
	SharedAppLocks bool
}

// Name returns the dialect name configured in DialectName.
func (d *BaseDialect) Name() string {
	return d.DialectName
}

// Capabilities returns the configured Caps, with Returning derived from the
// configured RETURNING hooks.
func (d *BaseDialect) Capabilities() Capabilities {
	c := d.Caps
	c.Returning = d.SupportsReturning()
	return c
}

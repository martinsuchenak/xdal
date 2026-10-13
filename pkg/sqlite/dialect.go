package sqlite

import (
	"fmt"

	"github.com/martinsuchenak/xdal/pkg/xdal"
)

type sqliteDialect struct {
	*xdal.BaseDialect
}

func (d *sqliteDialect) CurrentTimestamp() string { return "datetime('now')" }

func (d *sqliteDialect) BoolLiteral(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func (d *sqliteDialect) StringAggExpr(col, sep string) string {
	return fmt.Sprintf("GROUP_CONCAT(%s, %s)", col, sep)
}

func (d *sqliteDialect) RandExpr() string { return "RANDOM()" }

// NewDialect returns a Dialect configured for SQLite 3.35 or later.
// Named locks are process-local; SQLite serializes writers across processes itself.
func NewDialect() xdal.Dialect {
	b := &xdal.BaseDialect{
		DialectName: xdal.DialectSQLite,
		Caps: xdal.Capabilities{
			Upsert:           true,
			TransactionalDDL: true,
		},
		UpsertStyle: xdal.OnConflictUpsert,
		LockStyle:   xdal.NoRowLocks,
		AppLocker:   xdal.NewProcessLocker(),
		Placeholder: xdal.QuestionMarkPlaceholder,
		AppendLimit: xdal.LimitOffset,
		QuoteStyle:  xdal.DoubleQuoteQuoting,
	}
	b.AppendReturning = b.WriteReturning
	d := &sqliteDialect{BaseDialect: b}
	b.DDL = xdal.DDLOptions{
		Types: xdal.TypeMap{
			String:        "TEXT",
			Text:          "TEXT",
			Integer:       "INTEGER",
			BigInteger:    "INTEGER",
			Boolean:       "BOOLEAN",
			Float:         "REAL",
			Decimal:       "NUMERIC(%d,%d)",
			Timestamp:     "TIMESTAMP",
			Date:          "DATE",
			UUID:          "TEXT",
			JSON:          "TEXT",
			Binary:        "BLOB",
			Blob:          "BLOB",
			AutoIncrement: "INTEGER PRIMARY KEY AUTOINCREMENT",
		},
		DefaultNow:                "CURRENT_TIMESTAMP",
		BoolLiteral:               d.BoolLiteral,
		AddColumnKeyword:          "ADD COLUMN",
		TableIfNotExists:          xdal.IfNotExistsClause,
		IndexIfNotExists:          xdal.IfNotExistsClause,
		DropIndexIfExists:         true,
		Rename:                    xdal.RenameAlterTable,
		InlineAddColumnReferences: true,
	}
	return d
}

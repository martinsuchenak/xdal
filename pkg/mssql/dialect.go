package mssql

import (
	"fmt"
	"strings"

	"github.com/martinsuchenak/xdal/pkg/xdal"
)

type mssqlDialect struct {
	*xdal.BaseDialect
}

func (d *mssqlDialect) ConcatExpr(parts ...string) string {
	return strings.Join(parts, " + ")
}

func (d *mssqlDialect) LengthExpr(col string) string {
	return "LEN(" + col + ")"
}

func (d *mssqlDialect) CurrentTimestamp() string { return "GETDATE()" }

func (d *mssqlDialect) BoolLiteral(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func (d *mssqlDialect) StringAggExpr(col, sep string) string {
	return fmt.Sprintf("STRING_AGG(%s, %s)", col, sep)
}

func (d *mssqlDialect) RandExpr() string { return "RAND()" }

// NewDialect returns a Dialect configured for SQL Server 2016 or later.
func NewDialect() xdal.Dialect {
	b := &xdal.BaseDialect{
		DialectName: xdal.DialectMSSQL,
		Caps: xdal.Capabilities{
			Upsert:           true,
			RowLocks:         true,
			SkipLocked:       true,
			TransactionalDDL: true,
			SharedAppLocks:   true,
		},
		UpsertStyle: xdal.MergeUpsert,
		LockStyle:   xdal.TableHintLocks,
		AppLocker:   xdal.MSSQLAppLocker{},
		Placeholder: xdal.AtPPlaceholder,
		AppendLimit: xdal.FetchNextLimit,
		QuoteStyle:  xdal.BracketQuoting,
	}
	b.PrependReturning = b.WriteOutput
	b.AppendReturning = b.WriteOutput
	b.AppendDeletedReturning = b.WriteDeletedOutput
	d := &mssqlDialect{BaseDialect: b}
	b.DDL = xdal.DDLOptions{
		Types: xdal.TypeMap{
			String:        "NVARCHAR(%d)",
			Text:          "NVARCHAR(MAX)",
			Integer:       "INT",
			BigInteger:    "BIGINT",
			Boolean:       "BIT",
			Float:         "FLOAT(53)",
			Decimal:       "DECIMAL(%d,%d)",
			Timestamp:     "DATETIME2",
			Date:          "DATE",
			UUID:          "CHAR(36)",
			JSON:          "NVARCHAR(MAX)",
			Binary:        "VARBINARY(%d)",
			Blob:          "VARBINARY(MAX)",
			AutoIncrement: "BIGINT IDENTITY(1,1) PRIMARY KEY",
			MaxStringSize: 4000,
			StringMax:     "NVARCHAR(MAX)",
			MaxBinarySize: 8000,
		},
		DefaultNow:                  "SYSUTCDATETIME()",
		BoolLiteral:                 d.BoolLiteral,
		AddColumnKeyword:            "ADD",
		TableIfNotExists:            xdal.IfNotExistsObjectGuard,
		IndexIfNotExists:            xdal.IfNotExistsObjectGuard,
		DropIndexOnTable:            true,
		DropIndexIfExists:           true,
		Rename:                      xdal.RenameSPRename,
		RestrictKeyword:             "NO ACTION",
		UnicodeStringPrefix:         "N",
		DropColumnDefaultConstraint: true,
	}
	return d
}

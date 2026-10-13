package mysql

import "github.com/martinsuchenak/xdal/pkg/xdal"

// NewDialect returns a Dialect configured for MySQL 8.0.19 or later.
// MySQL does not support RETURNING — neither hook is set — and commits DDL implicitly.
func NewDialect() xdal.Dialect {
	b := &xdal.BaseDialect{
		DialectName: xdal.DialectMySQL,
		Caps: xdal.Capabilities{
			Upsert:         true,
			RowLocks:       true,
			SkipLocked:     true,
			SharedAppLocks: true,
		},
		UpsertStyle:      xdal.OnDuplicateKeyUpsert,
		LockStyle:        xdal.ForUpdateLocks,
		AppLocker:        xdal.MySQLNamedLocker{},
		Placeholder:      xdal.QuestionMarkPlaceholder,
		AppendLimit:      xdal.LimitOffset,
		QuoteStyle:       xdal.BacktickQuoting,
		BackslashEscapes: true,
	}
	b.DDL = xdal.DDLOptions{
		Types: xdal.TypeMap{
			String:        "VARCHAR(%d)",
			Text:          "LONGTEXT",
			Integer:       "INT",
			BigInteger:    "BIGINT",
			Boolean:       "BOOLEAN",
			Float:         "DOUBLE",
			Decimal:       "DECIMAL(%d,%d)",
			Timestamp:     "DATETIME(6)",
			Date:          "DATE",
			UUID:          "CHAR(36)",
			JSON:          "JSON",
			Binary:        "VARBINARY(%d)",
			Blob:          "LONGBLOB",
			AutoIncrement: "BIGINT AUTO_INCREMENT PRIMARY KEY",
		},
		DefaultNow:              "CURRENT_TIMESTAMP(6)",
		BoolLiteral:             b.BoolLiteral,
		AddColumnKeyword:        "ADD COLUMN",
		TableIfNotExists:        xdal.IfNotExistsClause,
		IndexIfNotExists:        xdal.IfNotExistsUnsupported,
		InlineIndexes:           true,
		DropIndexOnTable:        true,
		Rename:                  xdal.RenameAlterTable,
		ParenthesizeLOBDefaults: true,
	}
	return b
}

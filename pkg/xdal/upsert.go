package xdal

import (
	"fmt"
	"strings"
)

// UpsertStyle determines how InsertQuery.OnConflict is rendered.
type UpsertStyle int

const (
	NoUpsert             UpsertStyle = iota
	OnConflictUpsert                 // INSERT ... ON CONFLICT (...) DO UPDATE — PostgreSQL, SQLite
	OnDuplicateKeyUpsert             // INSERT ... AS alias ON DUPLICATE KEY UPDATE — MySQL 8.0.19+
	MergeUpsert                      // MERGE ... WHEN MATCHED / WHEN NOT MATCHED — SQL Server
)

const (
	upsertNewAlias    = "xdal_new"
	upsertTargetAlias = "xdal_target"
	upsertSourceAlias = "xdal_source"
)

type conflictAction int

const (
	conflictNone conflictAction = iota
	conflictUpdate
	conflictNothing
)

type conflictClause struct {
	columns []string
	update  []string
	action  conflictAction
}

func (q *InsertQuery) conflictClause() *conflictClause {
	if q.conflict == nil {
		q.conflict = &conflictClause{}
	}
	return q.conflict
}

// OnConflict turns the INSERT into an upsert keyed on the given unique columns.
// Combine with DoUpdate or DoNothing. MySQL ignores the column list and reacts to
// any unique key, so list the columns of the key you expect to conflict on.
func (q *InsertQuery) OnConflict(columns ...string) *InsertQuery {
	q.conflictClause().columns = columns
	return q
}

// DoUpdate updates the given columns of the existing row with the values being
// inserted; each must be an inserted column. With no columns, every inserted
// column except the conflict columns is updated.
func (q *InsertQuery) DoUpdate(columns ...string) *InsertQuery {
	c := q.conflictClause()
	c.action = conflictUpdate
	c.update = columns
	return q
}

// DoNothing keeps the existing row unchanged when a conflict occurs.
func (q *InsertQuery) DoNothing() *InsertQuery {
	c := q.conflictClause()
	c.action = conflictNothing
	c.update = nil
	return q
}

func (d *BaseDialect) resolveConflict(q *InsertQuery) ([]string, error) {
	c := q.conflict
	if d.UpsertStyle == NoUpsert {
		return nil, ErrUpsertNotSupported
	}
	if len(c.columns) == 0 {
		return nil, ErrUpsertConflictColumns
	}
	if err := safeIdentifiers(c.columns); err != nil {
		return nil, err
	}
	for _, col := range c.columns {
		if !containsString(q.keys, col) {
			return nil, fmt.Errorf("%w: conflict column %q", ErrUpsertColumnNotInserted, col)
		}
	}
	switch c.action {
	case conflictNothing:
		return nil, nil
	case conflictUpdate:
		if len(c.update) > 0 {
			if err := safeIdentifiers(c.update); err != nil {
				return nil, err
			}
			for _, col := range c.update {
				if !containsString(q.keys, col) {
					return nil, fmt.Errorf("%w: update column %q", ErrUpsertColumnNotInserted, col)
				}
			}
			return c.update, nil
		}
		var cols []string
		for _, k := range q.keys {
			if !containsString(c.columns, k) {
				cols = append(cols, k)
			}
		}
		if len(cols) == 0 {
			return nil, ErrUpsertNothingToUpdate
		}
		return cols, nil
	default:
		return nil, ErrUpsertAction
	}
}

func (d *BaseDialect) appendConflict(b *strings.Builder, q *InsertQuery, update []string) {
	c := q.conflict
	switch d.UpsertStyle {
	case OnConflictUpsert:
		b.WriteString(" ON CONFLICT (")
		b.WriteString(d.quoteColumns(c.columns))
		b.WriteString(")")
		if c.action == conflictNothing {
			b.WriteString(" DO NOTHING")
			return
		}
		b.WriteString(" DO UPDATE SET ")
		b.WriteString(d.assignFrom(update, "excluded"))
	case OnDuplicateKeyUpsert:
		b.WriteString(" AS ")
		b.WriteString(upsertNewAlias)
		b.WriteString(" ON DUPLICATE KEY UPDATE ")
		if c.action == conflictNothing {
			col := d.QuoteIdentifier(q.table) + "." + d.QuoteIdentifier(c.columns[0])
			b.WriteString(col + " = " + col)
			return
		}
		b.WriteString(d.assignFrom(update, upsertNewAlias))
	}
}

func (d *BaseDialect) buildMerge(q *InsertQuery, groups []string, update []string) string {
	c := q.conflict
	target := d.QuoteIdentifier(upsertTargetAlias)
	source := d.QuoteIdentifier(upsertSourceAlias)

	var b strings.Builder
	b.WriteString("MERGE INTO ")
	b.WriteString(d.QuoteIdentifier(q.table))
	b.WriteString(" WITH (HOLDLOCK) AS ")
	b.WriteString(target)
	b.WriteString(" USING (VALUES ")
	b.WriteString(strings.Join(groups, ", "))
	b.WriteString(") AS ")
	b.WriteString(source)
	b.WriteString(" (")
	b.WriteString(d.quoteColumns(q.keys))
	b.WriteString(") ON ")
	match := make([]string, len(c.columns))
	for i, col := range c.columns {
		qc := d.QuoteIdentifier(col)
		match[i] = target + "." + qc + " = " + source + "." + qc
	}
	b.WriteString(strings.Join(match, " AND "))

	if c.action == conflictUpdate {
		b.WriteString(" WHEN MATCHED THEN UPDATE SET ")
		b.WriteString(d.assignFrom(update, upsertSourceAlias))
	}

	sourceCols := make([]string, len(q.keys))
	for i, k := range q.keys {
		sourceCols[i] = source + "." + d.QuoteIdentifier(k)
	}
	b.WriteString(" WHEN NOT MATCHED THEN INSERT (")
	b.WriteString(d.quoteColumns(q.keys))
	b.WriteString(") VALUES (")
	b.WriteString(strings.Join(sourceCols, ", "))
	b.WriteString(")")

	if len(q.returning) > 0 {
		d.WriteOutput(&b, q.returning)
	}
	b.WriteString(";")
	return b.String()
}

func (d *BaseDialect) assignFrom(columns []string, alias string) string {
	qa := alias
	if alias != "excluded" {
		qa = d.QuoteIdentifier(alias)
	}
	parts := make([]string, len(columns))
	for i, col := range columns {
		qc := d.QuoteIdentifier(col)
		parts[i] = qc + " = " + qa + "." + qc
	}
	return strings.Join(parts, ", ")
}

func safeIdentifiers(names []string) error {
	for _, n := range names {
		if err := SafeIdentifier(n); err != nil {
			return err
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

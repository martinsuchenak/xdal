// Package migrate runs versioned schema migrations on any xdal dialect.
//
// Migrations are Go functions that receive a transaction, so they can use the
// schema builder, raw SQL, or per-dialect SQL chosen with Tx.Dialect().Name().
// Applied migrations are recorded per namespace, which lets independent modules
// (for example an application core and its plugins) keep separate histories in
// one tracking table. A named lock serializes concurrent runs; on server databases
// it holds its own connection, so the pool must allow at least two, and session
// locks do not work through PgBouncer in transaction pooling mode.
//
// On dialects without transactional DDL (MySQL), a migration is recorded as dirty
// before it runs and marked clean after it succeeds. A failure leaves the record
// dirty, and further runs stop with ErrDirty until Resolve is called.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/martinsuchenak/xdal/pkg/xdal"
)

// DefaultTable is the tracking table used when Options.Table is empty.
const DefaultTable = "xdal_migrations"

// DefaultNamespace is the namespace used when Options.Namespace is empty.
const DefaultNamespace = "default"

var (
	// ErrInvalid is returned by New for an invalid migration list.
	ErrInvalid = errors.New("migrate: invalid migration")
	// ErrDirty is returned when a previous migration failed part-way. Fix the schema by hand, then call Resolve.
	ErrDirty = errors.New("migrate: a migration failed part-way and must be resolved")
	// ErrChecksumMismatch is returned when an applied migration's name or checksum differs from its definition.
	ErrChecksumMismatch = errors.New("migrate: applied migration does not match its definition")
	// ErrUnknownApplied is returned when the database records a migration that is not defined.
	ErrUnknownApplied = errors.New("migrate: database contains a migration that is not defined")
	// ErrOutOfOrder is returned when a pending migration is older than the latest applied one.
	ErrOutOfOrder = errors.New("migrate: pending migration is older than the latest applied migration")
	// ErrIrreversible is returned when rolling back a migration without a Down step.
	ErrIrreversible = errors.New("migrate: migration has no Down step")
	// ErrUnknownVersion is returned when a target version is not defined.
	ErrUnknownVersion = errors.New("migrate: unknown version")
	// ErrNotDirty is returned by Resolve when the version is not recorded as dirty.
	ErrNotDirty = errors.New("migrate: version is not dirty")
	// ErrSingleConnection is returned when the pool allows one connection but the
	// migration lock needs its own, which would deadlock.
	ErrSingleConnection = errors.New("migrate: connection pool must allow at least 2 connections")
)

// Migration is one versioned schema change.
type Migration struct {
	// Version orders migrations by string comparison. Use fixed-width values such as 20260101120000.
	Version string
	// Name describes the change. Renaming an applied migration is reported as ErrChecksumMismatch.
	Name string
	// Up applies the change.
	Up func(ctx context.Context, tx *Tx) error
	// Down reverts the change. Leave nil for irreversible migrations.
	Down func(ctx context.Context, tx *Tx) error
	// Checksum optionally fingerprints the migration content. When empty, it is derived from Version and Name.
	Checksum string
}

func (m Migration) checksum() string {
	if m.Checksum != "" {
		return m.Checksum
	}
	sum := sha256.Sum256([]byte(m.Version + "\x00" + m.Name))
	return hex.EncodeToString(sum[:])
}

// Tx is the transaction passed to Up and Down.
type Tx struct {
	*xdal.Tx
	dialect xdal.Dialect
}

// Dialect returns the database dialect, for per-dialect SQL.
func (t *Tx) Dialect() xdal.Dialect {
	return t.dialect
}

// Schema returns a schema builder for the database dialect.
func (t *Tx) Schema() *xdal.Schema {
	return xdal.NewSchema(t.dialect)
}

// Apply builds and executes schema statements in order.
func (t *Tx) Apply(ctx context.Context, stmts ...xdal.Statement) error {
	return xdal.ExecStatements(ctx, t.Tx, stmts...)
}

// ExecSQL executes raw SQL statements in order.
func (t *Tx) ExecSQL(ctx context.Context, queries ...string) error {
	for _, q := range queries {
		if _, err := t.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Options configures a Migrator.
type Options struct {
	// Table is the tracking table. Defaults to DefaultTable.
	Table string
	// Namespace separates histories that share a table. Defaults to DefaultNamespace.
	Namespace string
	// AllowOutOfOrder applies pending migrations older than the latest applied one instead of failing.
	AllowOutOfOrder bool
	// IgnoreUnknown tolerates applied migrations that are not defined, such as after a downgrade.
	IgnoreUnknown bool
	// Logger receives progress messages. Defaults to no logging.
	Logger xdal.Logger
}

// Status describes one migration.
type Status struct {
	Version   string
	Name      string
	Applied   bool
	Dirty     bool
	AppliedAt time.Time
	Duration  time.Duration
	// Unknown is true when the migration is recorded in the database but not defined.
	Unknown bool
}

type record struct {
	version   string
	name      string
	checksum  string
	appliedAt time.Time
	duration  time.Duration
	dirty     bool
}

// Migrator applies and reverts migrations for one namespace.
type Migrator struct {
	db         xdal.DBInterface
	dialect    xdal.Dialect
	migrations []Migration
	byVersion  map[string]Migration
	opts       Options
	log        xdal.Logger
}

// New validates the migrations and returns a Migrator. Migrations may be given in
// any order; they run sorted by Version.
func New(db xdal.DBInterface, migrations []Migration, opts Options) (*Migrator, error) {
	if opts.Table == "" {
		opts.Table = DefaultTable
	}
	if opts.Namespace == "" {
		opts.Namespace = DefaultNamespace
	}
	if err := xdal.SafeIdentifier(opts.Table); err != nil {
		return nil, err
	}
	log := opts.Logger
	if log == nil {
		log = xdal.NoopLoggerInstance()
	}

	sorted := make([]Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

	byVersion := make(map[string]Migration, len(sorted))
	for _, m := range sorted {
		switch {
		case m.Version == "":
			return nil, fmt.Errorf("%w: empty version (name %q)", ErrInvalid, m.Name)
		case m.Name == "":
			return nil, fmt.Errorf("%w: version %s has no name", ErrInvalid, m.Version)
		case m.Up == nil:
			return nil, fmt.Errorf("%w: version %s has no Up step", ErrInvalid, m.Version)
		}
		if _, dup := byVersion[m.Version]; dup {
			return nil, fmt.Errorf("%w: duplicate version %s", ErrInvalid, m.Version)
		}
		byVersion[m.Version] = m
	}

	return &Migrator{
		db:         db,
		dialect:    db.Dialect(),
		migrations: sorted,
		byVersion:  byVersion,
		opts:       opts,
		log:        log,
	}, nil
}

// EnsureTable creates the tracking table if it does not exist.
func (m *Migrator) EnsureTable(ctx context.Context) error {
	t := xdal.NewSchema(m.dialect).CreateTable(m.opts.Table).IfNotExists()
	t.String("namespace", 100).PrimaryKey()
	t.String("version", 100).PrimaryKey()
	t.String("name", 255).NotNull()
	t.String("checksum", 64).NotNull()
	t.Timestamp("applied_at").NotNull()
	t.BigInteger("duration_ms").NotNull()
	t.Boolean("dirty").NotNull()
	return xdal.ExecStatements(ctx, m.db, t)
}

// Status lists every defined migration plus any applied migration that is not defined.
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	var records map[string]record
	err := m.locked(ctx, func(r map[string]record, _ *xdal.Lock) error {
		records = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Status
	for _, mig := range m.migrations {
		s := Status{Version: mig.Version, Name: mig.Name}
		if r, ok := records[mig.Version]; ok {
			s.Applied = !r.dirty
			s.Dirty = r.dirty
			s.AppliedAt = r.appliedAt
			s.Duration = r.duration
		}
		out = append(out, s)
	}
	for _, r := range records {
		if _, ok := m.byVersion[r.version]; !ok {
			out = append(out, Status{Version: r.version, Name: r.name, Applied: !r.dirty, Dirty: r.dirty,
				AppliedAt: r.appliedAt, Duration: r.duration, Unknown: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Pending returns the migrations that Up would apply, without applying them.
func (m *Migrator) Pending(ctx context.Context) ([]Migration, error) {
	var out []Migration
	err := m.locked(ctx, func(records map[string]record, _ *xdal.Lock) error {
		if err := m.validate(records); err != nil {
			return err
		}
		out = m.pending(records, "")
		return nil
	})
	return out, err
}

// Up applies every pending migration and returns the versions applied.
func (m *Migrator) Up(ctx context.Context) ([]string, error) {
	return m.UpTo(ctx, "")
}

// UpTo applies pending migrations up to and including version. An empty version applies all.
func (m *Migrator) UpTo(ctx context.Context, version string) ([]string, error) {
	if version != "" {
		if _, ok := m.byVersion[version]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownVersion, version)
		}
	}
	var applied []string
	err := m.locked(ctx, func(records map[string]record, lock *xdal.Lock) error {
		if err := m.validate(records); err != nil {
			return err
		}
		for _, mig := range m.pending(records, version) {
			if err := lockAlive(ctx, lock); err != nil {
				return err
			}
			if err := m.apply(ctx, mig); err != nil {
				return err
			}
			applied = append(applied, mig.Version)
		}
		return nil
	})
	return applied, err
}

// Down reverts the latest steps applied migrations, newest first, and returns the versions reverted.
func (m *Migrator) Down(ctx context.Context, steps int) ([]string, error) {
	if steps < 0 {
		return nil, fmt.Errorf("%w: negative step count %d", ErrInvalid, steps)
	}
	var reverted []string
	err := m.locked(ctx, func(records map[string]record, lock *xdal.Lock) error {
		targets, err := m.revertible(records)
		if err != nil {
			return err
		}
		if steps < len(targets) {
			targets = targets[:steps]
		}
		for _, mig := range targets {
			if err := lockAlive(ctx, lock); err != nil {
				return err
			}
			if err := m.revert(ctx, mig); err != nil {
				return err
			}
			reverted = append(reverted, mig.Version)
		}
		return nil
	})
	return reverted, err
}

// DownTo reverts applied migrations newer than version, newest first. An empty version reverts all.
func (m *Migrator) DownTo(ctx context.Context, version string) ([]string, error) {
	if version != "" {
		if _, ok := m.byVersion[version]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownVersion, version)
		}
	}
	var reverted []string
	err := m.locked(ctx, func(records map[string]record, lock *xdal.Lock) error {
		targets, err := m.revertible(records)
		if err != nil {
			return err
		}
		for _, mig := range targets {
			if mig.Version <= version {
				break
			}
			if err := lockAlive(ctx, lock); err != nil {
				return err
			}
			if err := m.revert(ctx, mig); err != nil {
				return err
			}
			reverted = append(reverted, mig.Version)
		}
		return nil
	})
	return reverted, err
}

// Resolve clears a dirty record left by a failed run on a dialect without
// transactional DDL. Pass applied=true after completing the change by hand, or
// applied=false after undoing it, so the migration runs again.
func (m *Migrator) Resolve(ctx context.Context, version string, applied bool) error {
	return m.locked(ctx, func(records map[string]record, _ *xdal.Lock) error {
		r, ok := records[version]
		if !ok || !r.dirty {
			return fmt.Errorf("%w: %s", ErrNotDirty, version)
		}
		if applied {
			return m.markClean(ctx, m.db, version, r.duration)
		}
		return m.deleteRecord(ctx, m.db, version)
	})
}

// locked runs fn while holding the table's migration lock, so concurrent runs
// (including ones creating the tracking table) are serialized. Session-bound locks
// hold one pooled connection, so the pool must allow at least two.
func (m *Migrator) locked(ctx context.Context, fn func(map[string]record, *xdal.Lock) error) error {
	if locker := m.dialect.Locker(); locker != nil && locker.SessionBound() {
		if p, ok := m.db.(interface{ DB() *sql.DB }); ok && p.DB().Stats().MaxOpenConnections == 1 {
			return ErrSingleConnection
		}
	}
	lock, err := m.db.Lock(ctx, "xdal_migrate:"+m.opts.Table)
	if err != nil && !errors.Is(err, xdal.ErrNotSupported) {
		return err
	}
	if lock != nil {
		defer func() {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = lock.Release(rctx)
		}()
	}
	if err := m.EnsureTable(ctx); err != nil {
		return err
	}
	records, err := m.records(ctx)
	if err != nil {
		return err
	}
	return fn(records, lock)
}

func lockAlive(ctx context.Context, lock *xdal.Lock) error {
	if lock == nil {
		return nil
	}
	return lock.Alive(ctx)
}

// detached returns a context for bookkeeping that must finish after a migration
// has committed, even if the caller's context is cancelled.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

func (m *Migrator) validate(records map[string]record) error {
	var latest string
	for _, r := range records {
		if r.dirty {
			return fmt.Errorf("%w: %s %s", ErrDirty, r.version, r.name)
		}
		if mig, ok := m.byVersion[r.version]; ok {
			if mig.Name != r.name || mig.checksum() != r.checksum {
				return fmt.Errorf("%w: %s (recorded %q, defined %q)", ErrChecksumMismatch, r.version, r.name, mig.Name)
			}
		} else if !m.opts.IgnoreUnknown {
			return fmt.Errorf("%w: %s %s", ErrUnknownApplied, r.version, r.name)
		}
		if r.version > latest {
			latest = r.version
		}
	}
	if !m.opts.AllowOutOfOrder {
		for _, mig := range m.pending(records, "") {
			if mig.Version < latest {
				return fmt.Errorf("%w: %s %s is older than %s", ErrOutOfOrder, mig.Version, mig.Name, latest)
			}
		}
	}
	return nil
}

func (m *Migrator) pending(records map[string]record, upTo string) []Migration {
	var out []Migration
	for _, mig := range m.migrations {
		if upTo != "" && mig.Version > upTo {
			break
		}
		if _, ok := records[mig.Version]; !ok {
			out = append(out, mig)
		}
	}
	return out
}

// revertible returns applied migrations newest first, failing on dirty records.
// An unknown record newer than every defined one always fails, since reverting
// beneath it could break the newer schema. With IgnoreUnknown, an older unknown
// record ends the list instead, so nothing below it is reverted.
func (m *Migrator) revertible(records map[string]record) ([]Migration, error) {
	versions := make([]string, 0, len(records))
	for v, r := range records {
		if r.dirty {
			return nil, fmt.Errorf("%w: %s %s", ErrDirty, r.version, r.name)
		}
		versions = append(versions, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	out := make([]Migration, 0, len(versions))
	for _, v := range versions {
		mig, ok := m.byVersion[v]
		if !ok {
			if m.opts.IgnoreUnknown && len(out) > 0 {
				break
			}
			return nil, fmt.Errorf("%w: %s %s", ErrUnknownApplied, v, records[v].name)
		}
		out = append(out, mig)
	}
	return out, nil
}

func (m *Migrator) apply(ctx context.Context, mig Migration) error {
	start := time.Now()
	transactional := m.dialect.Capabilities().TransactionalDDL
	if !transactional {
		if err := m.insertRecord(ctx, m.db, mig, 0, true); err != nil {
			return err
		}
	}
	err := m.db.WithTx(ctx, nil, func(tx *xdal.Tx) error {
		if err := mig.Up(ctx, &Tx{Tx: tx, dialect: m.dialect}); err != nil {
			return err
		}
		if transactional {
			return m.insertRecord(ctx, tx, mig, time.Since(start), false)
		}
		return nil
	})
	if err != nil {
		m.log.Error("migration failed", "namespace", m.opts.Namespace, "version", mig.Version, "name", mig.Name, "error", err)
		return fmt.Errorf("migrate: up %s %s: %w", mig.Version, mig.Name, err)
	}
	if !transactional {
		bctx, cancel := detached(ctx)
		defer cancel()
		if err := m.markClean(bctx, m.db, mig.Version, time.Since(start)); err != nil {
			return err
		}
	}
	m.log.Info("migration applied", "namespace", m.opts.Namespace, "version", mig.Version, "name", mig.Name, "duration", time.Since(start))
	return nil
}

func (m *Migrator) revert(ctx context.Context, mig Migration) error {
	if mig.Down == nil {
		return fmt.Errorf("%w: %s %s", ErrIrreversible, mig.Version, mig.Name)
	}
	start := time.Now()
	transactional := m.dialect.Capabilities().TransactionalDDL
	if !transactional {
		if err := m.markDirty(ctx, mig.Version); err != nil {
			return err
		}
	}
	err := m.db.WithTx(ctx, nil, func(tx *xdal.Tx) error {
		if err := mig.Down(ctx, &Tx{Tx: tx, dialect: m.dialect}); err != nil {
			return err
		}
		if transactional {
			return m.deleteRecord(ctx, tx, mig.Version)
		}
		return nil
	})
	if err != nil {
		m.log.Error("migration rollback failed", "namespace", m.opts.Namespace, "version", mig.Version, "name", mig.Name, "error", err)
		return fmt.Errorf("migrate: down %s %s: %w", mig.Version, mig.Name, err)
	}
	if !transactional {
		bctx, cancel := detached(ctx)
		defer cancel()
		if err := m.deleteRecord(bctx, m.db, mig.Version); err != nil {
			return err
		}
	}
	m.log.Info("migration reverted", "namespace", m.opts.Namespace, "version", mig.Version, "name", mig.Name, "duration", time.Since(start))
	return nil
}

func (m *Migrator) qb() *xdal.QueryBuilder {
	return xdal.NewQueryBuilder(m.dialect)
}

func (m *Migrator) col(name string) string {
	return m.dialect.QuoteIdentifier(name)
}

func (m *Migrator) records(ctx context.Context) (map[string]record, error) {
	query, args, err := m.qb().Select("version", "name", "checksum", "applied_at", "duration_ms", "dirty").
		From(m.opts.Table).
		Where(m.col("namespace")+" = ?", m.opts.Namespace).
		Build()
	if err != nil {
		return nil, err
	}
	rows, err := m.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]record{}
	for rows.Next() {
		var r record
		var appliedAt interface{}
		var durationMS int64
		if err := rows.Scan(&r.version, &r.name, &r.checksum, &appliedAt, &durationMS, &r.dirty); err != nil {
			return nil, err
		}
		r.appliedAt = parseTime(appliedAt)
		r.duration = time.Duration(durationMS) * time.Millisecond
		out[r.version] = r
	}
	return out, rows.Err()
}

func (m *Migrator) insertRecord(ctx context.Context, exec xdal.DBExecutor, mig Migration, d time.Duration, dirty bool) error {
	query, args, err := m.qb().Insert(m.opts.Table).
		Set("namespace", m.opts.Namespace).
		Set("version", mig.Version).
		Set("name", mig.Name).
		Set("checksum", mig.checksum()).
		Set("applied_at", time.Now().UTC()).
		Set("duration_ms", d.Milliseconds()).
		Set("dirty", dirty).
		Build()
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, query, args...)
	return err
}

func (m *Migrator) markClean(ctx context.Context, exec xdal.DBExecutor, version string, d time.Duration) error {
	query, args, err := m.qb().Update(m.opts.Table).
		Set("dirty", false).
		Set("duration_ms", d.Milliseconds()).
		Set("applied_at", time.Now().UTC()).
		Where(m.col("namespace")+" = ?", m.opts.Namespace).
		Where(m.col("version")+" = ?", version).
		Build()
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, query, args...)
	return err
}

func (m *Migrator) markDirty(ctx context.Context, version string) error {
	query, args, err := m.qb().Update(m.opts.Table).
		Set("dirty", true).
		Where(m.col("namespace")+" = ?", m.opts.Namespace).
		Where(m.col("version")+" = ?", version).
		Build()
	if err != nil {
		return err
	}
	_, err = m.db.Exec(ctx, query, args...)
	return err
}

func (m *Migrator) deleteRecord(ctx context.Context, exec xdal.DBExecutor, version string) error {
	query, args, err := m.qb().Delete(m.opts.Table).
		Where(m.col("namespace")+" = ?", m.opts.Namespace).
		Where(m.col("version")+" = ?", version).
		Build()
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, query, args...)
	return err
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// parseTime accepts the representations drivers return for timestamp columns,
// including MySQL without parseTime=true.
func parseTime(v interface{}) time.Time {
	var s string
	switch x := v.(type) {
	case time.Time:
		return x
	case []byte:
		s = string(x)
	case string:
		s = x
	default:
		return time.Time{}
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

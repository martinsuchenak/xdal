package xdal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

// LockStyle determines how SelectQuery row locks are rendered.
type LockStyle int

const (
	NoRowLocks     LockStyle = iota // row lock requests are ignored — SQLite
	ForUpdateLocks                  // SELECT ... FOR UPDATE [SKIP LOCKED | NOWAIT] — PostgreSQL, MySQL 8+
	TableHintLocks                  // FROM t WITH (UPDLOCK, ROWLOCK[, READPAST | NOWAIT]) — SQL Server
)

type rowLockMode int

const (
	lockNone rowLockMode = iota
	lockForUpdate
)

type rowLockWait int

const (
	waitBlock rowLockWait = iota
	waitSkipLocked
	waitNoWait
)

type rowLock struct {
	mode rowLockMode
	wait rowLockWait
}

// ForUpdate locks the selected rows for update until the surrounding transaction ends.
// Ignored by dialects without row locks (see Capabilities.RowLocks).
func (q *SelectQuery) ForUpdate() *SelectQuery {
	q.lock.mode = lockForUpdate
	return q
}

// SkipLocked locks the selected rows for update and skips rows already locked by
// other transactions. Use it to let several workers claim rows from a shared queue.
func (q *SelectQuery) SkipLocked() *SelectQuery {
	q.lock = rowLock{mode: lockForUpdate, wait: waitSkipLocked}
	return q
}

// NoWait locks the selected rows for update and fails immediately instead of
// waiting when a row is already locked.
func (q *SelectQuery) NoWait() *SelectQuery {
	q.lock = rowLock{mode: lockForUpdate, wait: waitNoWait}
	return q
}

func (d *BaseDialect) writeTableLockHint(b *strings.Builder, l rowLock) {
	if d.LockStyle != TableHintLocks || l.mode == lockNone {
		return
	}
	b.WriteString(" WITH (UPDLOCK, ROWLOCK")
	switch l.wait {
	case waitSkipLocked:
		b.WriteString(", READPAST")
	case waitNoWait:
		b.WriteString(", NOWAIT")
	}
	b.WriteString(")")
}

func (d *BaseDialect) writeLockClause(b *strings.Builder, l rowLock) {
	if d.LockStyle != ForUpdateLocks || l.mode == lockNone {
		return
	}
	b.WriteString(" FOR UPDATE")
	switch l.wait {
	case waitSkipLocked:
		b.WriteString(" SKIP LOCKED")
	case waitNoWait:
		b.WriteString(" NOWAIT")
	}
}

// ErrLockNotHeld is returned by Lock.Release when the database reports that the
// lock was not held by this session.
var ErrLockNotHeld = errors.New("xdal: lock not held")

// AppLocker implements exclusive named locks for a dialect. Session-bound lockers
// hold the lock on a dedicated connection, which is released when the lock is.
type AppLocker interface {
	// SessionBound reports whether the lock lives on a database session and needs a dedicated connection.
	SessionBound() bool
	// TryAcquire attempts to take the lock without waiting. conn is nil when SessionBound is false.
	TryAcquire(ctx context.Context, conn *sql.Conn, name string) (bool, error)
	// Release frees a lock taken by TryAcquire on the same connection.
	Release(ctx context.Context, conn *sql.Conn, name string) error
}

// Locker returns the configured AppLocker, or nil when named locks are unsupported.
func (d *BaseDialect) Locker() AppLocker {
	return d.AppLocker
}

// Lock is a held named lock. Release it when done; a session-bound lock is also
// released if the process exits, because the database closes the session.
type Lock struct {
	name     string
	locker   AppLocker
	conn     *sql.Conn
	log      Logger
	mu       sync.Mutex
	released bool
}

// Name returns the lock name.
func (l *Lock) Name() string {
	return l.name
}

// Release frees the lock. Releasing twice is a no-op. If the database cannot
// confirm the release, the dedicated connection is discarded so the lock cannot
// stay held by a pooled connection.
func (l *Lock) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	err := l.locker.Release(ctx, l.conn, l.name)
	if err != nil {
		discardConn(l.conn)
	} else if l.conn != nil {
		_ = l.conn.Close()
	}
	if err != nil {
		l.log.Error("lock release error", "lock", l.name, "error", err)
		return err
	}
	l.log.Debug("lock released", "lock", l.name)
	return nil
}

// TryLock attempts to take the named lock without waiting. It returns false when
// another holder has it.
func (b *BaseDB) TryLock(ctx context.Context, name string) (*Lock, bool, error) {
	locker := b.dialect.Locker()
	if locker == nil {
		return nil, false, ErrNotSupported
	}
	conn, err := b.lockConn(ctx, locker)
	if err != nil {
		return nil, false, err
	}
	l, ok, err := b.tryLockOn(ctx, locker, conn, name)
	if err != nil {
		discardConn(conn)
	} else if !ok && conn != nil {
		_ = conn.Close()
	}
	return l, ok, err
}

// discardConn closes a lock connection without returning it to the pool, in case
// the server granted the lock before an error was reported.
func discardConn(conn *sql.Conn) {
	if conn == nil {
		return
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// Alive reports whether the session holding the lock is still connected. A
// session-bound lock is lost when its connection drops, so long-running holders
// should check before each critical step.
func (l *Lock) Alive(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return ErrLockNotHeld
	}
	if l.conn == nil {
		return nil
	}
	if err := l.conn.PingContext(ctx); err != nil {
		return fmt.Errorf("xdal: lock %q lost: %w", l.name, err)
	}
	return nil
}

// Lock takes the named lock, waiting until it is free or ctx is done. Use a
// context deadline to bound the wait.
func (b *BaseDB) Lock(ctx context.Context, name string) (*Lock, error) {
	locker := b.dialect.Locker()
	if locker == nil {
		return nil, ErrNotSupported
	}
	conn, err := b.lockConn(ctx, locker)
	if err != nil {
		return nil, err
	}
	delay := 25 * time.Millisecond
	for {
		l, ok, err := b.tryLockOn(ctx, locker, conn, name)
		if err != nil {
			discardConn(conn)
			return nil, err
		}
		if ok {
			return l, nil
		}
		select {
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < time.Second {
			delay *= 2
		}
	}
}

func (b *BaseDB) lockConn(ctx context.Context, locker AppLocker) (*sql.Conn, error) {
	if !locker.SessionBound() {
		return nil, nil
	}
	return b.db.Conn(ctx)
}

func (b *BaseDB) tryLockOn(ctx context.Context, locker AppLocker, conn *sql.Conn, name string) (*Lock, bool, error) {
	log := b.getLogger()
	ok, err := locker.TryAcquire(ctx, conn, name)
	if err != nil {
		log.Error("lock acquire error", "lock", name, "error", err)
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	log.Debug("lock acquired", "lock", name)
	return &Lock{name: name, locker: locker, conn: conn, log: log}, true, nil
}

// PostgresAdvisoryLocker uses session-level advisory locks keyed by a 64-bit hash of the name.
type PostgresAdvisoryLocker struct{}

func (PostgresAdvisoryLocker) SessionBound() bool { return true }

func (PostgresAdvisoryLocker) TryAcquire(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var ok bool
	err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryKey(name)).Scan(&ok)
	return ok, err
}

func (PostgresAdvisoryLocker) Release(ctx context.Context, conn *sql.Conn, name string) error {
	var ok bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryKey(name)).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrLockNotHeld
	}
	return nil
}

func advisoryKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

// MySQLNamedLocker uses GET_LOCK/RELEASE_LOCK. Names longer than 64 characters
// are replaced by a hash.
type MySQLNamedLocker struct{}

func (MySQLNamedLocker) SessionBound() bool { return true }

func (MySQLNamedLocker) TryAcquire(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var r sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", mysqlLockName(name)).Scan(&r); err != nil {
		return false, err
	}
	if !r.Valid {
		return false, fmt.Errorf("xdal: GET_LOCK failed for %q", name)
	}
	return r.Int64 == 1, nil
}

func (MySQLNamedLocker) Release(ctx context.Context, conn *sql.Conn, name string) error {
	var r sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", mysqlLockName(name)).Scan(&r); err != nil {
		return err
	}
	if !r.Valid || r.Int64 != 1 {
		return ErrLockNotHeld
	}
	return nil
}

func mysqlLockName(name string) string {
	if len(name) <= 64 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return "xdal_" + hex.EncodeToString(sum[:])[:59]
}

// MSSQLAppLocker uses session-owned sp_getapplock/sp_releaseapplock. Names longer
// than 255 characters are replaced by a hash.
type MSSQLAppLocker struct{}

func (MSSQLAppLocker) SessionBound() bool { return true }

func (MSSQLAppLocker) TryAcquire(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var r int
	err := conn.QueryRowContext(ctx,
		"DECLARE @r INT; EXEC @r = sp_getapplock @Resource = @p1, @LockMode = 'Exclusive', @LockOwner = 'Session', @LockTimeout = 0; SELECT @r",
		mssqlLockName(name)).Scan(&r)
	if err != nil {
		return false, err
	}
	switch {
	case r >= 0:
		return true, nil
	case r == -1:
		return false, nil
	default:
		return false, fmt.Errorf("xdal: sp_getapplock returned %d for %q", r, name)
	}
}

func (MSSQLAppLocker) Release(ctx context.Context, conn *sql.Conn, name string) error {
	var r int
	err := conn.QueryRowContext(ctx,
		"DECLARE @r INT; EXEC @r = sp_releaseapplock @Resource = @p1, @LockOwner = 'Session'; SELECT @r",
		mssqlLockName(name)).Scan(&r)
	if err != nil {
		return err
	}
	if r != 0 {
		return ErrLockNotHeld
	}
	return nil
}

func mssqlLockName(name string) string {
	if len(name) <= 255 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return "xdal_" + hex.EncodeToString(sum[:])
}

// ProcessLocker keeps named locks in memory. It excludes holders that share the
// same ProcessLocker instance only, which suits single-process databases such as
// SQLite, where the database itself serializes writers across processes.
type ProcessLocker struct {
	mu   sync.Mutex
	held map[string]struct{}
}

// NewProcessLocker returns an empty ProcessLocker.
func NewProcessLocker() *ProcessLocker {
	return &ProcessLocker{held: map[string]struct{}{}}
}

func (*ProcessLocker) SessionBound() bool { return false }

func (p *ProcessLocker) TryAcquire(_ context.Context, _ *sql.Conn, name string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.held[name]; ok {
		return false, nil
	}
	p.held[name] = struct{}{}
	return true, nil
}

func (p *ProcessLocker) Release(_ context.Context, _ *sql.Conn, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.held[name]; !ok {
		return ErrLockNotHeld
	}
	delete(p.held, name)
	return nil
}

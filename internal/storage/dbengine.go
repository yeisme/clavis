package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	defaultPoolBusyTimeout    = 5 * time.Second
	defaultPoolCacheSizeKB    = 16384
	defaultPoolMmapBytes      = int64(64 << 20)
	defaultSlowQueryThreshold = 15 * time.Millisecond
	slowQueryRingSize         = 32
)

// CheckpointMode selects the SQLite WAL checkpoint aggressiveness.
type CheckpointMode string

const (
	CheckpointPassive  CheckpointMode = "PASSIVE"
	CheckpointTruncate CheckpointMode = "TRUNCATE"
)

// QueryEvent reports sanitized execution timings for one database operation.
// Raw SQL arguments and user content are never captured.
type QueryEvent struct {
	Op           string
	IsWrite      bool
	WaitDuration time.Duration
	ExecDuration time.Duration
	Rows         int
	Err          error
}

// QueryObserver receives QueryEvent telemetry outside internal DBPool locks.
type QueryObserver func(QueryEvent)

// SlowQueryRecord stores a sanitized entry in DBPool's bounded slow-query ring.
type SlowQueryRecord struct {
	At      time.Time `json:"at"`
	Op      string    `json:"op"`
	IsWrite bool      `json:"isWrite"`
	WaitMs  float64   `json:"waitMs"`
	ExecMs  float64   `json:"execMs"`
	Rows    int       `json:"rows"`
	Err     string    `json:"err,omitempty"`
}

// DBStatsSnapshot reports structural, storage-size, and latency counters for a DBPool.
type DBStatsSnapshot struct {
	Path          string            `json:"path"`
	IsMemory      bool              `json:"isMemory"`
	SchemaVersion int               `json:"schemaVersion"`
	DBSizeBytes   int64             `json:"dbSizeBytes"`
	WALSizeBytes  int64             `json:"walSizeBytes"`
	PageSize      int64             `json:"pageSize"`
	PageCount     int64             `json:"pageCount"`
	FreelistCount int64             `json:"freelistCount"`
	ReaderCount   int               `json:"readerCount"`
	TotalReads    uint64            `json:"totalReads"`
	TotalWrites   uint64            `json:"totalWrites"`
	SlowQueries   uint64            `json:"slowQueries"`
	TableCounts   map[string]int64  `json:"tableCounts,omitempty"`
	RecentSlow    []SlowQueryRecord `json:"recentSlow,omitempty"`
}

// DBPoolOptions configures unified writer + concurrent WAL reader pooling.
type DBPoolOptions struct {
	Path               string
	MaxReaders         int
	BusyTimeout        time.Duration
	CacheSizeKB        int
	MmapSizeBytes      int64
	SlowQueryThreshold time.Duration
	Observer           QueryObserver
}

// DBPool manages one serialized SQLite writer connection and a bounded pool of
// concurrent ReadOnly WAL reader connections for high-throughput, low-latency reads.
type DBPool struct {
	opts DBPoolOptions

	closeMu sync.RWMutex
	closed  bool

	writer  *sqlite.DB
	writeMu sync.Mutex

	readersMu  sync.Mutex
	readers    chan *sqlite.DB
	allReaders []*sqlite.DB

	isMemory bool

	obsMu    sync.RWMutex
	observer QueryObserver

	slowMu    sync.Mutex
	slowRing  [slowQueryRingSize]SlowQueryRecord
	slowHead  int
	slowCount int

	totalReads  atomic.Uint64
	totalWrites atomic.Uint64
	totalSlow   atomic.Uint64
}

// OpenDBPool opens the primary writer connection for opts.Path. Call TuneAfterMigrate
// and InitReaders once schema migration succeeds.
func OpenDBPool(ctx context.Context, opts DBPoolOptions) (*DBPool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = ":memory:"
	}
	isMem := path == ":memory:" || strings.HasPrefix(path, "file::memory:")
	if !isMem {
		dir := filepath.Dir(path)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create sqlite db dir: %w", err)
			}
		}
	}

	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = defaultPoolBusyTimeout
	}
	if opts.CacheSizeKB <= 0 {
		opts.CacheSizeKB = defaultPoolCacheSizeKB
	}
	if opts.MmapSizeBytes == 0 && !isMem {
		opts.MmapSizeBytes = defaultPoolMmapBytes
	}
	if opts.SlowQueryThreshold <= 0 {
		opts.SlowQueryThreshold = defaultSlowQueryThreshold
	}
	if opts.MaxReaders <= 0 {
		n := runtime.NumCPU()
		if n < 2 {
			n = 2
		}
		if n > 4 {
			n = 4
		}
		opts.MaxReaders = n
	}
	opts.Path = path

	writer, err := sqlite.Open(ctx, path, sqlite.OpenOptions{BusyTimeout: opts.BusyTimeout})
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	p := &DBPool{
		opts:     opts,
		writer:   writer,
		isMemory: isMem,
		observer: opts.Observer,
	}
	return p, nil
}

// Writer exposes the primary serialized writer connection for sibling stores and migrations.
func (p *DBPool) Writer() *sqlite.DB {
	if p == nil {
		return nil
	}
	return p.writer
}

// SetObserver updates the QueryObserver callback at runtime.
func (p *DBPool) SetObserver(obs QueryObserver) {
	if p == nil {
		return
	}
	p.obsMu.Lock()
	p.observer = obs
	p.obsMu.Unlock()
}

// TuneAfterMigrate enables WAL (for file-backed DBs) and applies memory/cache PRAGMAs
// after schema checks ensure the database is not newer than supported.
func (p *DBPool) TuneAfterMigrate(ctx context.Context) error {
	if p == nil {
		return sqlite.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()
	if p.closed {
		return sqlite.ErrClosed
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	if !p.isMemory {
		if _, err := p.writer.Execute(ctx, `PRAGMA journal_mode=WAL`); err != nil {
			return fmt.Errorf("enable WAL mode: %w", err)
		}
		// Best-effort tuning PRAGMAs (may be no-ops on some platforms/settings).
		_, _ = p.writer.Execute(ctx, `PRAGMA synchronous=NORMAL`)
		_, _ = p.writer.Execute(ctx, `PRAGMA wal_autocheckpoint=1000`)
		if p.opts.MmapSizeBytes > 0 {
			_, _ = p.writer.Execute(ctx, fmt.Sprintf(`PRAGMA mmap_size=%d`, p.opts.MmapSizeBytes))
		}
	}
	_, _ = p.writer.Execute(ctx, `PRAGMA temp_store=MEMORY`)
	_, _ = p.writer.Execute(ctx, fmt.Sprintf(`PRAGMA cache_size=-%d`, p.opts.CacheSizeKB))
	return nil
}

// InitReaders opens the ReadOnly connection pool for file-backed WAL databases.
func (p *DBPool) InitReaders(ctx context.Context) error {
	if p == nil {
		return sqlite.ErrClosed
	}
	if p.isMemory {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()
	if p.closed {
		return sqlite.ErrClosed
	}

	p.readersMu.Lock()
	defer p.readersMu.Unlock()
	if p.readers != nil {
		return nil
	}

	count := p.opts.MaxReaders
	if count < 1 {
		count = 1
	}
	ch := make(chan *sqlite.DB, count)
	opened := make([]*sqlite.DB, 0, count)
	for i := 0; i < count; i++ {
		rdb, err := sqlite.Open(ctx, p.opts.Path, sqlite.OpenOptions{
			ReadOnly:    true,
			BusyTimeout: p.opts.BusyTimeout,
		})
		if err != nil {
			for _, existing := range opened {
				_ = existing.Close()
			}
			return fmt.Errorf("open sqlite reader[%d]: %w", i, err)
		}
		_, _ = rdb.Execute(ctx, `PRAGMA temp_store=MEMORY`)
		readerCacheKB := p.opts.CacheSizeKB / 2
		if readerCacheKB < 2048 {
			readerCacheKB = 2048
		}
		_, _ = rdb.Execute(ctx, fmt.Sprintf(`PRAGMA cache_size=-%d`, readerCacheKB))
		opened = append(opened, rdb)
		ch <- rdb
	}
	p.allReaders = opened
	p.readers = ch
	return nil
}

func (p *DBPool) readerChannel() chan *sqlite.DB {
	p.readersMu.Lock()
	ch := p.readers
	p.readersMu.Unlock()
	return ch
}

// QueryRead executes a read-only SQL query using a pooled WAL reader (or the writer for :memory:).
func (p *DBPool) QueryRead(ctx context.Context, op string, sql string, args ...any) (sqlite.Rows, error) {
	if p == nil {
		return sqlite.Rows{}, sqlite.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return sqlite.Rows{}, err
	}

	p.closeMu.RLock()
	if p.closed {
		p.closeMu.RUnlock()
		return sqlite.Rows{}, sqlite.ErrClosed
	}

	waitStart := time.Now()
	ch := p.readerChannel()
	var (
		db       *sqlite.DB
		waitDur  time.Duration
		execRows sqlite.Rows
		err      error
	)

	if ch != nil {
		select {
		case db = <-ch:
		case <-ctx.Done():
			p.closeMu.RUnlock()
			return sqlite.Rows{}, ctx.Err()
		}
		waitDur = time.Since(waitStart)
		execStart := time.Now()
		execRows, err = db.Query(ctx, sql, args...)
		execDur := time.Since(execStart)
		ch <- db
		p.closeMu.RUnlock()

		p.totalReads.Add(1)
		p.recordOp(QueryEvent{
			Op:           op,
			IsWrite:      false,
			WaitDuration: waitDur,
			ExecDuration: execDur,
			Rows:         len(execRows.Values),
			Err:          err,
		})
		return execRows, err
	}

	// Fallback for :memory: databases or before InitReaders is invoked.
	p.writeMu.Lock()
	waitDur = time.Since(waitStart)
	execStart := time.Now()
	execRows, err = p.writer.Query(ctx, sql, args...)
	execDur := time.Since(execStart)
	p.writeMu.Unlock()
	p.closeMu.RUnlock()

	p.totalReads.Add(1)
	p.recordOp(QueryEvent{
		Op:           op,
		IsWrite:      false,
		WaitDuration: waitDur,
		ExecDuration: execDur,
		Rows:         len(execRows.Values),
		Err:          err,
	})
	return execRows, err
}

// ExecWrite executes a serialized write statement through the single writer connection.
func (p *DBPool) ExecWrite(ctx context.Context, op string, sql string, args ...any) (sqlite.Result, error) {
	if p == nil {
		return sqlite.Result{}, sqlite.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return sqlite.Result{}, err
	}

	p.closeMu.RLock()
	if p.closed {
		p.closeMu.RUnlock()
		return sqlite.Result{}, sqlite.ErrClosed
	}

	waitStart := time.Now()
	p.writeMu.Lock()
	waitDur := time.Since(waitStart)
	execStart := time.Now()
	res, err := p.writer.Execute(ctx, sql, args...)
	execDur := time.Since(execStart)
	p.writeMu.Unlock()
	p.closeMu.RUnlock()

	p.totalWrites.Add(1)
	p.recordOp(QueryEvent{
		Op:           op,
		IsWrite:      true,
		WaitDuration: waitDur,
		ExecDuration: execDur,
		Rows:         int(res.Changes),
		Err:          err,
	})
	return res, err
}

// TxWrite executes a batch of statements atomically through the single writer connection.
func (p *DBPool) TxWrite(ctx context.Context, op string, stmts []sqlite.Statement) ([]sqlite.Result, error) {
	if p == nil {
		return nil, sqlite.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.closeMu.RLock()
	if p.closed {
		p.closeMu.RUnlock()
		return nil, sqlite.ErrClosed
	}

	waitStart := time.Now()
	p.writeMu.Lock()
	waitDur := time.Since(waitStart)
	execStart := time.Now()
	res, err := p.writer.Transaction(ctx, stmts)
	execDur := time.Since(execStart)
	p.writeMu.Unlock()
	p.closeMu.RUnlock()

	p.totalWrites.Add(1)
	p.recordOp(QueryEvent{
		Op:           op,
		IsWrite:      true,
		WaitDuration: waitDur,
		ExecDuration: execDur,
		Rows:         len(stmts),
		Err:          err,
	})
	return res, err
}

func (p *DBPool) recordOp(ev QueryEvent) {
	totalDur := ev.WaitDuration + ev.ExecDuration
	if totalDur >= p.opts.SlowQueryThreshold {
		p.totalSlow.Add(1)
		rec := SlowQueryRecord{
			At:      time.Now().UTC(),
			Op:      ev.Op,
			IsWrite: ev.IsWrite,
			WaitMs:  float64(ev.WaitDuration.Microseconds()) / 1000.0,
			ExecMs:  float64(ev.ExecDuration.Microseconds()) / 1000.0,
			Rows:    ev.Rows,
		}
		if ev.Err != nil {
			rec.Err = ev.Err.Error()
		}
		p.slowMu.Lock()
		p.slowRing[p.slowHead] = rec
		p.slowHead = (p.slowHead + 1) % slowQueryRingSize
		if p.slowCount < slowQueryRingSize {
			p.slowCount++
		}
		p.slowMu.Unlock()
	}

	p.obsMu.RLock()
	obs := p.observer
	p.obsMu.RUnlock()
	if obs != nil {
		obs(ev)
	}
}

// RecentSlowQueries returns a newest-first copy of the bounded slow-query ring.
func (p *DBPool) RecentSlowQueries() []SlowQueryRecord {
	if p == nil {
		return nil
	}
	p.slowMu.Lock()
	defer p.slowMu.Unlock()
	if p.slowCount == 0 {
		return nil
	}
	out := make([]SlowQueryRecord, 0, p.slowCount)
	for i := 0; i < p.slowCount; i++ {
		idx := (p.slowHead - 1 - i + slowQueryRingSize) % slowQueryRingSize
		out = append(out, p.slowRing[idx])
	}
	return out
}

// Checkpoint runs PRAGMA wal_checkpoint(PASSIVE|TRUNCATE) on the writer connection.
func (p *DBPool) Checkpoint(ctx context.Context, mode CheckpointMode) (busy, logFrames, checkpointed int64, err error) {
	if p == nil {
		return 0, 0, 0, sqlite.ErrClosed
	}
	if p.isMemory {
		return 0, 0, 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pragmaSQL := `PRAGMA wal_checkpoint(PASSIVE)`
	if mode == CheckpointTruncate {
		pragmaSQL = `PRAGMA wal_checkpoint(TRUNCATE)`
	}

	p.closeMu.RLock()
	if p.closed {
		p.closeMu.RUnlock()
		return 0, 0, 0, sqlite.ErrClosed
	}
	p.writeMu.Lock()
	rows, qErr := p.writer.Query(ctx, pragmaSQL)
	p.writeMu.Unlock()
	p.closeMu.RUnlock()
	if qErr != nil {
		return 0, 0, 0, qErr
	}
	if len(rows.Values) > 0 && len(rows.Values[0]) >= 3 {
		busy = int64val(rows.Values[0], 0)
		logFrames = int64val(rows.Values[0], 1)
		checkpointed = int64val(rows.Values[0], 2)
	}
	return busy, logFrames, checkpointed, nil
}

// Optimize runs PRAGMA optimize on the writer connection.
func (p *DBPool) Optimize(ctx context.Context) error {
	_, err := p.ExecWrite(ctx, "db.optimize", `PRAGMA optimize`)
	return err
}

// IncrementalVacuum reclaims freelist pages when auto_vacuum=INCREMENTAL is active.
func (p *DBPool) IncrementalVacuum(ctx context.Context, pages int) error {
	sql := `PRAGMA incremental_vacuum`
	if pages > 0 {
		sql = fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, pages)
	}
	_, err := p.ExecWrite(ctx, "db.incremental_vacuum", sql)
	return err
}

// QuickCheck runs PRAGMA quick_check(1) and returns an error unless SQLite reports "ok".
func (p *DBPool) QuickCheck(ctx context.Context) error {
	rows, err := p.QueryRead(ctx, "db.quick_check", `PRAGMA quick_check(1)`)
	if err != nil {
		return err
	}
	if len(rows.Values) == 0 || !strings.EqualFold(strings.TrimSpace(str(rows.Values[0], 0)), "ok") {
		return fmt.Errorf("%w: quick_check returned %v", ErrIntegrityFailed, rows.Values)
	}
	return nil
}

// InspectStats collects structural sizes, pragma page metrics, whitelisted row counts,
// and pool counters for Console/CLI diagnostics.
func (p *DBPool) InspectStats(ctx context.Context) (DBStatsSnapshot, error) {
	if p == nil {
		return DBStatsSnapshot{}, sqlite.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.readersMu.Lock()
	readerCount := len(p.allReaders)
	p.readersMu.Unlock()

	snap := DBStatsSnapshot{
		Path:        p.opts.Path,
		IsMemory:    p.isMemory,
		ReaderCount: readerCount,
		TotalReads:  p.totalReads.Load(),
		TotalWrites: p.totalWrites.Load(),
		SlowQueries: p.totalSlow.Load(),
		TableCounts: make(map[string]int64),
		RecentSlow:  p.RecentSlowQueries(),
	}
	if !p.isMemory {
		if fi, err := os.Stat(p.opts.Path); err == nil {
			snap.DBSizeBytes = fi.Size()
		}
		if fi, err := os.Stat(p.opts.Path + "-wal"); err == nil {
			snap.WALSizeBytes = fi.Size()
		}
	}

	readScalar := func(op, q string) int64 {
		rows, err := p.QueryRead(ctx, op, q)
		if err != nil || len(rows.Values) == 0 || len(rows.Values[0]) == 0 {
			return 0
		}
		return int64val(rows.Values[0], 0)
	}
	snap.SchemaVersion = int(readScalar("db.stats.user_version", `PRAGMA user_version`))
	snap.PageSize = readScalar("db.stats.page_size", `PRAGMA page_size`)
	snap.PageCount = readScalar("db.stats.page_count", `PRAGMA page_count`)
	snap.FreelistCount = readScalar("db.stats.freelist_count", `PRAGMA freelist_count`)

	for _, tbl := range []string{
		"clipboard_items",
		"capture_assets",
		"entity_annotations",
		"ai_conversations",
		"ai_conversation_turns",
	} {
		if rows, err := p.QueryRead(ctx, "db.stats.count."+tbl, `SELECT COUNT(*) FROM `+tbl); err == nil && len(rows.Values) > 0 && len(rows.Values[0]) > 0 {
			snap.TableCounts[tbl] = int64val(rows.Values[0], 0)
		}
	}
	return snap, nil
}

// Close closes all pooled reader connections and the primary writer connection.
func (p *DBPool) Close() error {
	if p == nil {
		return nil
	}
	p.closeMu.Lock()
	if p.closed {
		p.closeMu.Unlock()
		return nil
	}
	p.closed = true
	p.closeMu.Unlock()

	var errs []error
	p.readersMu.Lock()
	readers := p.allReaders
	p.allReaders = nil
	p.readers = nil
	p.readersMu.Unlock()

	for _, rdb := range readers {
		if rdb != nil {
			if err := rdb.Close(); err != nil && !errors.Is(err, sqlite.ErrClosed) {
				errs = append(errs, err)
			}
		}
	}
	if p.writer != nil {
		if err := p.writer.Close(); err != nil && !errors.Is(err, sqlite.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

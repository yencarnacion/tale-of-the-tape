package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"tale-of-the-tape/internal/positions"
)

type Store struct {
	DB   *sql.DB
	Path string
}
type Bar struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}
type Excursion struct {
	MFE          int64  `json:"mfe"`
	MAE          int64  `json:"mae"`
	MFEAt        int64  `json:"mfe_at"`
	MAEAt        int64  `json:"mae_at"`
	Source       string `json:"source"`
	Completeness string `json:"completeness"`
	Warnings     string `json:"warnings"`
	Events       int    `json:"event_count"`
	CalculatedAt int64  `json:"calculated_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS import_batches(id INTEGER PRIMARY KEY, sha256 TEXT NOT NULL UNIQUE, filename TEXT NOT NULL, imported_at INTEGER NOT NULL, accepted_rows INTEGER NOT NULL, rejected_rows INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS broker_pnl_snapshots(statement_date TEXT PRIMARY KEY,day INTEGER NOT NULL,ytd INTEGER NOT NULL,fees_ytd INTEGER NOT NULL,batch_id INTEGER REFERENCES import_batches(id));
CREATE TABLE IF NOT EXISTS raw_import_rows(id INTEGER PRIMARY KEY, batch_id INTEGER NOT NULL REFERENCES import_batches(id), row_number INTEGER, reason TEXT NOT NULL, raw TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS executions(id INTEGER PRIMARY KEY, batch_id INTEGER NOT NULL REFERENCES import_batches(id), account TEXT NOT NULL, symbol TEXT NOT NULL, action TEXT NOT NULL, quantity INTEGER NOT NULL, price INTEGER NOT NULL, commission INTEGER NOT NULL, fees INTEGER NOT NULL, executed_at INTEGER NOT NULL, source_row INTEGER NOT NULL, fingerprint TEXT NOT NULL UNIQUE);
CREATE INDEX IF NOT EXISTS executions_position_idx ON executions(account,symbol,executed_at,source_row);
CREATE TABLE IF NOT EXISTS round_trips(id INTEGER PRIMARY KEY, account TEXT NOT NULL,symbol TEXT NOT NULL,direction TEXT NOT NULL,entry_at INTEGER NOT NULL,exit_at INTEGER NOT NULL,entry_price INTEGER NOT NULL,exit_price INTEGER NOT NULL,max_quantity INTEGER NOT NULL,entered INTEGER NOT NULL,exited INTEGER NOT NULL,gross INTEGER NOT NULL,commissions INTEGER NOT NULL,fees INTEGER NOT NULL,net INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS round_trips_exit_idx ON round_trips(exit_at);
CREATE TABLE IF NOT EXISTS round_trip_executions(round_trip_id INTEGER NOT NULL REFERENCES round_trips(id) ON DELETE CASCADE,execution_id INTEGER NOT NULL REFERENCES executions(id),quantity INTEGER,commission INTEGER,fees INTEGER,PRIMARY KEY(round_trip_id,execution_id));
CREATE TABLE IF NOT EXISTS tags(id INTEGER PRIMARY KEY,name TEXT NOT NULL UNIQUE,color TEXT NOT NULL DEFAULT '#58a6ff',archived INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS round_trip_tags(round_trip_id INTEGER NOT NULL REFERENCES round_trips(id) ON DELETE CASCADE,tag_id INTEGER NOT NULL REFERENCES tags(id),PRIMARY KEY(round_trip_id,tag_id));
CREATE TABLE IF NOT EXISTS trade_notes(round_trip_id INTEGER PRIMARY KEY REFERENCES round_trips(id) ON DELETE CASCADE,note TEXT NOT NULL,updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS day_notes(day TEXT PRIMARY KEY,note TEXT NOT NULL,updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS market_bars(symbol TEXT NOT NULL,timeframe TEXT NOT NULL,time_us INTEGER NOT NULL,open INTEGER NOT NULL,high INTEGER NOT NULL,low INTEGER NOT NULL,close INTEGER NOT NULL,volume INTEGER NOT NULL,PRIMARY KEY(symbol,timeframe,time_us));
CREATE TABLE IF NOT EXISTS market_bar_coverage(symbol TEXT NOT NULL,timeframe TEXT NOT NULL,start_us INTEGER NOT NULL,end_us INTEGER NOT NULL,recorded_at INTEGER NOT NULL,PRIMARY KEY(symbol,timeframe,start_us,end_us));
CREATE INDEX IF NOT EXISTS market_bar_coverage_lookup_idx ON market_bar_coverage(symbol,timeframe,start_us,end_us);
CREATE TABLE IF NOT EXISTS excursion_results(round_trip_id INTEGER PRIMARY KEY REFERENCES round_trips(id),mfe INTEGER,mae INTEGER,mfe_at INTEGER,mae_at INTEGER,source TEXT,completeness TEXT,warnings TEXT,event_count INTEGER NOT NULL DEFAULT 0,calculated_at INTEGER);
`

func Open(path string, busy time.Duration) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", fmt.Sprintf("PRAGMA busy_timeout=%d", busy.Milliseconds())} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	// Forward-only additive migrations. Logical-leg values preserve the exact
	// cost allocation when a reversing execution closes one trade and opens the
	// next; raw executions remain untouched for audit purposes.
	for _, q := range []string{
		"ALTER TABLE excursion_results ADD COLUMN event_count INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE excursion_results ADD COLUMN mfe_at INTEGER",
		"ALTER TABLE excursion_results ADD COLUMN mae_at INTEGER",
		"ALTER TABLE round_trip_executions ADD COLUMN quantity INTEGER",
		"ALTER TABLE round_trip_executions ADD COLUMN commission INTEGER",
		"ALTER TABLE round_trip_executions ADD COLUMN fees INTEGER",
	} {
		if _, e = db.Exec(q); e != nil && !strings.Contains(strings.ToLower(e.Error()), "duplicate column") {
			db.Close()
			return nil, e
		}
	}
	s := &Store{db, path}
	var sessionMigration int
	if e = db.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=2)").Scan(&sessionMigration); e != nil {
		db.Close()
		return nil, e
	}
	var executionCount int
	if e = db.QueryRow("SELECT count(*) FROM executions").Scan(&executionCount); e != nil {
		db.Close()
		return nil, e
	}
	if sessionMigration == 0 && executionCount > 0 {
		if e = backupBeforeLogicalLegMigration(db, path); e != nil {
			db.Close()
			return nil, fmt.Errorf("backup before session reconstruction migration: %w", e)
		}
		tx, txErr := db.BeginTx(context.Background(), nil)
		if txErr != nil {
			db.Close()
			return nil, txErr
		}
		if _, txErr = tx.Exec(`CREATE TEMP TABLE old_trade_notes AS
			SELECT r.account,r.symbol,r.direction,r.entry_at,r.exit_at,n.note,n.updated_at
			FROM trade_notes n JOIN round_trips r ON r.id=n.round_trip_id`); txErr == nil {
			_, txErr = tx.Exec(`CREATE TEMP TABLE old_trade_tags AS
				SELECT r.account,r.symbol,r.direction,r.entry_at,r.exit_at,t.tag_id
				FROM round_trip_tags t JOIN round_trips r ON r.id=t.round_trip_id`)
		}
		if txErr == nil {
			txErr = s.rebuildTx(context.Background(), tx)
		}
		if txErr == nil {
			_, txErr = tx.Exec(`INSERT OR IGNORE INTO trade_notes(round_trip_id,note,updated_at)
				SELECT r.id,o.note,o.updated_at FROM old_trade_notes o JOIN round_trips r
				ON r.account=o.account AND r.symbol=o.symbol AND r.direction=o.direction
				AND r.entry_at=o.entry_at AND r.exit_at=o.exit_at`)
		}
		if txErr == nil {
			_, txErr = tx.Exec(`INSERT OR IGNORE INTO round_trip_tags(round_trip_id,tag_id)
				SELECT r.id,o.tag_id FROM old_trade_tags o JOIN round_trips r
				ON r.account=o.account AND r.symbol=o.symbol AND r.direction=o.direction
				AND r.entry_at=o.entry_at AND r.exit_at=o.exit_at`)
		}
		if txErr == nil {
			_, txErr = tx.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(2,?)", time.Now().Unix())
		}
		if txErr == nil {
			txErr = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if txErr != nil {
			db.Close()
			return nil, fmt.Errorf("migrate to session-relative reconstruction: %w", txErr)
		}
	}
	var missingLegs int
	if e = db.QueryRow("SELECT count(*) FROM round_trip_executions WHERE quantity IS NULL OR commission IS NULL OR fees IS NULL").Scan(&missingLegs); e != nil {
		db.Close()
		return nil, e
	}
	if missingLegs > 0 {
		if e = backupBeforeLogicalLegMigration(db, path); e != nil {
			db.Close()
			return nil, fmt.Errorf("backup before logical-leg migration: %w", e)
		}
		if e = s.repairLogicalLegs(context.Background()); e != nil {
			db.Close()
			return nil, fmt.Errorf("migrate logical execution legs: %w", e)
		}
	}
	return s, nil
}

func backupBeforeLogicalLegMigration(db *sql.DB, databasePath string) error {
	dir := filepath.Dir(databasePath)
	name := strings.TrimSuffix(filepath.Base(databasePath), filepath.Ext(databasePath))
	backup := filepath.Join(dir, name+"-pre-logical-leg-migration-"+time.Now().Format("20060102-150405")+".db")
	_, err := db.Exec("VACUUM INTO ?", backup)
	return err
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	e := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&value)
	return value, e
}
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at", key, value, time.Now().Unix())
	return e
}
func (s *Store) StoreBars(ctx context.Context, symbol, timeframe string, bars []Bar) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, b := range bars {
		_, e = tx.ExecContext(ctx, "INSERT INTO market_bars(symbol,timeframe,time_us,open,high,low,close,volume) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(symbol,timeframe,time_us) DO UPDATE SET open=excluded.open,high=excluded.high,low=excluded.low,close=excluded.close,volume=excluded.volume", symbol, timeframe, b.Time*1000, scale(b.Open), scale(b.High), scale(b.Low), scale(b.Close), int64(b.Volume))
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

// StoreBarsCoverage records the provider-confirmed interval together with its
// bars. Empty intervals are coverage too: that prevents repeated requests for
// a closed market or a symbol with no data.
func (s *Store) StoreBarsCoverage(ctx context.Context, symbol, timeframe string, start, end time.Time, bars []Bar) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, b := range bars {
		if _, e = tx.ExecContext(ctx, "INSERT INTO market_bars(symbol,timeframe,time_us,open,high,low,close,volume) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(symbol,timeframe,time_us) DO UPDATE SET open=excluded.open,high=excluded.high,low=excluded.low,close=excluded.close,volume=excluded.volume", symbol, timeframe, b.Time*1000, scale(b.Open), scale(b.High), scale(b.Low), scale(b.Close), int64(b.Volume)); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO market_bar_coverage(symbol,timeframe,start_us,end_us,recorded_at) VALUES(?,?,?,?,?)", symbol, timeframe, start.UnixMicro(), end.UnixMicro(), time.Now().Unix())
	if e != nil {
		return e
	}
	return tx.Commit()
}

// HasBarCoverage is deliberately separate from Bars: a non-empty cache is
// not evidence that every requested minute was downloaded.
func (s *Store) HasBarCoverage(ctx context.Context, symbol, timeframe string, start, end time.Time) (bool, error) {
	var found int
	e := s.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM market_bar_coverage WHERE symbol=? AND timeframe=? AND start_us<=? AND end_us>=?)", symbol, timeframe, start.UnixMicro(), end.UnixMicro()).Scan(&found)
	return found != 0, e
}
func (s *Store) Bars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]Bar, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT time_us,open,high,low,close,volume FROM market_bars WHERE symbol=? AND timeframe=? AND time_us>=? AND time_us<=? ORDER BY time_us", symbol, timeframe, start.UnixMicro(), end.UnixMicro())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Bar
	for rows.Next() {
		var b Bar
		var o, h, l, c, v int64
		if e = rows.Scan(&b.Time, &o, &h, &l, &c, &v); e != nil {
			return nil, e
		}
		b.Time /= 1000
		b.Open = float64(o) / float64(positions.Scale)
		b.High = float64(h) / float64(positions.Scale)
		b.Low = float64(l) / float64(positions.Scale)
		b.Close = float64(c) / float64(positions.Scale)
		b.Volume = float64(v)
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) SaveExcursion(ctx context.Context, id int64, x Excursion) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO excursion_results(round_trip_id,mfe,mae,mfe_at,mae_at,source,completeness,warnings,event_count,calculated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(round_trip_id) DO UPDATE SET mfe=excluded.mfe,mae=excluded.mae,mfe_at=excluded.mfe_at,mae_at=excluded.mae_at,source=excluded.source,completeness=excluded.completeness,warnings=excluded.warnings,event_count=excluded.event_count,calculated_at=excluded.calculated_at", id, x.MFE, x.MAE, x.MFEAt, x.MAEAt, x.Source, x.Completeness, x.Warnings, x.Events, x.CalculatedAt)
	return e
}
func (s *Store) Excursion(ctx context.Context, id int64) (Excursion, error) {
	var x Excursion
	e := s.DB.QueryRowContext(ctx, "SELECT mfe,mae,COALESCE(mfe_at,0),COALESCE(mae_at,0),source,completeness,warnings,event_count,calculated_at FROM excursion_results WHERE round_trip_id=?", id).Scan(&x.MFE, &x.MAE, &x.MFEAt, &x.MAEAt, &x.Source, &x.Completeness, &x.Warnings, &x.Events, &x.CalculatedAt)
	return x, e
}
func scale(f float64) int64 { return int64(f*float64(positions.Scale) + .5) }
func fingerprint(e positions.Execution) string {
	// Thinkorswim may report distinct partial fills with the same second,
	// symbol, side, quantity, and price. Occurrence distinguishes those fills
	// within a statement while staying stable across overlapping exports, whose
	// source-row offsets may differ.
	return fmt.Sprintf("%s|%s|%s|%d|%d|%d|%d", e.Account, e.Symbol, e.Action, e.Quantity, e.Price, e.At.UnixMicro(), e.Occurrence)
}
func (s *Store) Commit(ctx context.Context, sha, name string, execs []positions.Execution, rejected []string) (int, int, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return 0, 0, e
	}
	defer tx.Rollback()
	var exists int
	if e = tx.QueryRowContext(ctx, "SELECT id FROM import_batches WHERE sha256=?", sha).Scan(&exists); e == nil {
		return exists, 0, nil
	}
	if e != sql.ErrNoRows {
		return 0, 0, e
	}
	r, e := tx.ExecContext(ctx, "INSERT INTO import_batches(sha256,filename,imported_at,accepted_rows,rejected_rows) VALUES(?,?,?,?,?)", sha, filepath.Base(name), time.Now().Unix(), len(execs), len(rejected))
	if e != nil {
		return 0, 0, e
	}
	id, _ := r.LastInsertId()
	added := 0
	for _, x := range execs {
		res, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO executions(batch_id,account,symbol,action,quantity,price,commission,fees,executed_at,source_row,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, x.Account, x.Symbol, x.Action, x.Quantity, x.Price, x.Commission, x.Fees, x.At.UnixMicro(), x.Row, fingerprint(x))
		if e != nil {
			return 0, 0, e
		}
		n, _ := res.RowsAffected()
		added += int(n)
	}
	for _, v := range rejected {
		_, e = tx.ExecContext(ctx, "INSERT INTO raw_import_rows(batch_id,reason,raw) VALUES(?,?,?)", id, "Rejected during parsing", v)
		if e != nil {
			return 0, 0, e
		}
	}
	if e = s.rebuildTx(ctx, tx); e != nil {
		return 0, 0, e
	}
	return int(id), added, tx.Commit()
}

func (s *Store) StoreBrokerPnL(ctx context.Context, batchID int, statementDate string, day, ytd, feesYTD int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO broker_pnl_snapshots(statement_date,day,ytd,fees_ytd,batch_id)
		VALUES(?,?,?,?,?) ON CONFLICT(statement_date) DO UPDATE SET
		day=excluded.day,ytd=excluded.ytd,fees_ytd=excluded.fees_ytd,batch_id=excluded.batch_id`,
		statementDate, day, ytd, feesYTD, batchID)
	return err
}
func (s *Store) rebuildTx(ctx context.Context, tx *sql.Tx) error {
	xs := []positions.Execution{}
	rows, e := tx.QueryContext(ctx, "SELECT id,account,symbol,action,quantity,price,commission,fees,executed_at,source_row FROM executions ORDER BY account,symbol,executed_at,source_row")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var x positions.Execution
		var at int64
		if e = rows.Scan(&x.ID, &x.Account, &x.Symbol, &x.Action, &x.Quantity, &x.Price, &x.Commission, &x.Fees, &at, &x.Row); e != nil {
			return e
		}
		x.At = time.UnixMicro(at).UTC()
		xs = append(xs, x)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	// Excursions are derived from round-trip execution membership and must be
	// recalculated whenever that membership changes.
	if _, e = tx.ExecContext(ctx, "DELETE FROM excursion_results"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM round_trips"); e != nil {
		return e
	}
	for _, r := range positions.Build(xs) {
		rr, e := tx.ExecContext(ctx, "INSERT INTO round_trips(account,symbol,direction,entry_at,exit_at,entry_price,exit_price,max_quantity,entered,exited,gross,commissions,fees,net) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", r.Account, r.Symbol, r.Direction, r.Entry.UnixMicro(), r.Exit.UnixMicro(), r.EntryPrice, r.ExitPrice, r.MaxQuantity, r.Entered, r.Exited, r.Gross, r.Commissions, r.Fees, r.Net)
		if e != nil {
			return e
		}
		rid, _ := rr.LastInsertId()
		for _, x := range r.Executions {
			if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO round_trip_executions(round_trip_id,execution_id,quantity,commission,fees) VALUES(?,?,?,?,?)", rid, x.ID, x.Quantity, x.Commission, x.Fees); e != nil {
				return e
			}
		}
	}
	return nil
}

// repairLogicalLegs upgrades pre-leg-allocation databases without deleting
// round trips, notes, tags, or prior excursion records. IDs stay stable while
// the join rows are rebuilt from the canonical position engine.
func (s *Store) repairLogicalLegs(ctx context.Context) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	xs, e := executionsTx(ctx, tx)
	if e != nil {
		return e
	}
	used := map[int64]bool{}
	for _, r := range positions.Build(xs) {
		rows, e := tx.QueryContext(ctx, "SELECT id FROM round_trips WHERE account=? AND symbol=? AND direction=? AND entry_at=? AND exit_at=? ORDER BY id", r.Account, r.Symbol, r.Direction, r.Entry.UnixMicro(), r.Exit.UnixMicro())
		if e != nil {
			return e
		}
		var id int64
		for rows.Next() {
			var candidate int64
			if e = rows.Scan(&candidate); e != nil {
				rows.Close()
				return e
			}
			if !used[candidate] {
				id = candidate
				break
			}
		}
		rows.Close()
		if id == 0 {
			return fmt.Errorf("cannot match historical round trip %s %s at %s", r.Account, r.Symbol, r.Entry)
		}
		used[id] = true
		if _, e = tx.ExecContext(ctx, "UPDATE round_trips SET entry_price=?,exit_price=?,max_quantity=?,entered=?,exited=?,gross=?,commissions=?,fees=?,net=? WHERE id=?", r.EntryPrice, r.ExitPrice, r.MaxQuantity, r.Entered, r.Exited, r.Gross, r.Commissions, r.Fees, r.Net, id); e != nil {
			return e
		}
		// Any prior excursion may have charged a reversing broker execution to
		// both trades. Force it to be recalculated from the repaired legs.
		if _, e = tx.ExecContext(ctx, "DELETE FROM excursion_results WHERE round_trip_id=?", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM round_trip_executions WHERE round_trip_id=?", id); e != nil {
			return e
		}
		for _, x := range r.Executions {
			if _, e = tx.ExecContext(ctx, "INSERT INTO round_trip_executions(round_trip_id,execution_id,quantity,commission,fees) VALUES(?,?,?,?,?)", id, x.ID, x.Quantity, x.Commission, x.Fees); e != nil {
				return e
			}
		}
	}
	return tx.Commit()
}

func executionsTx(ctx context.Context, tx *sql.Tx) ([]positions.Execution, error) {
	rows, e := tx.QueryContext(ctx, "SELECT id,account,symbol,action,quantity,price,commission,fees,executed_at,source_row FROM executions ORDER BY account,symbol,executed_at,source_row")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var xs []positions.Execution
	for rows.Next() {
		var x positions.Execution
		var at int64
		if e = rows.Scan(&x.ID, &x.Account, &x.Symbol, &x.Action, &x.Quantity, &x.Price, &x.Commission, &x.Fees, &at, &x.Row); e != nil {
			return nil, e
		}
		x.At = time.UnixMicro(at).UTC()
		xs = append(xs, x)
	}
	return xs, rows.Err()
}

type Trade struct {
	ID             int64      `json:"id"`
	Account        string     `json:"-"`
	Symbol         string     `json:"symbol"`
	Direction      string     `json:"direction"`
	EntryAt        int64      `json:"entry_at"`
	ExitAt         int64      `json:"exit_at"`
	EntryPrice     int64      `json:"entry_price"`
	ExitPrice      int64      `json:"exit_price"`
	MaxQuantity    int64      `json:"max_quantity"`
	Entered        int64      `json:"entered"`
	Exited         int64      `json:"exited"`
	ExecutionCount int        `json:"execution_count"`
	Gross          int64      `json:"gross"`
	Commissions    int64      `json:"commissions"`
	Fees           int64      `json:"fees"`
	Net            int64      `json:"net"`
	Tags           []Tag      `json:"tags"`
	Note           string     `json:"note"`
	Excursion      *Excursion `json:"excursion,omitempty"`
}
type Tag struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Archived bool   `json:"archived"`
}
type ImportBatch struct {
	ID           int64               `json:"id"`
	SHA256       string              `json:"sha256"`
	Filename     string              `json:"filename"`
	ImportedAt   int64               `json:"imported_at"`
	AcceptedRows int64               `json:"accepted_rows"`
	RejectedRows int64               `json:"rejected_rows"`
	Rejected     []RejectedImportRow `json:"rejected"`
}
type RejectedImportRow struct {
	RowNumber int64  `json:"row_number"`
	Reason    string `json:"reason"`
	Raw       string `json:"raw"`
}

func (s *Store) ImportBatch(ctx context.Context, id int64) (ImportBatch, error) {
	var b ImportBatch
	e := s.DB.QueryRowContext(ctx, "SELECT id,sha256,filename,imported_at,accepted_rows,rejected_rows FROM import_batches WHERE id=?", id).Scan(&b.ID, &b.SHA256, &b.Filename, &b.ImportedAt, &b.AcceptedRows, &b.RejectedRows)
	if e != nil {
		return b, e
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT COALESCE(row_number,0),reason,raw FROM raw_import_rows WHERE batch_id=? ORDER BY id", id)
	if e != nil {
		return b, e
	}
	defer rows.Close()
	for rows.Next() {
		var r RejectedImportRow
		if e = rows.Scan(&r.RowNumber, &r.Reason, &r.Raw); e != nil {
			return b, e
		}
		b.Rejected = append(b.Rejected, r)
	}
	return b, rows.Err()
}

func (s *Store) Trades(ctx context.Context, start, end time.Time) ([]Trade, error) {
	q := "SELECT id,account,symbol,direction,entry_at,exit_at,entry_price,exit_price,max_quantity,entered,exited,gross,commissions,fees,net,(SELECT count(*) FROM round_trip_executions x WHERE x.round_trip_id=round_trips.id) FROM round_trips"
	a := []any{}
	where := []string{}
	if !start.IsZero() {
		where = append(where, "exit_at>=?")
		a = append(a, start.UnixMicro())
	}
	if !end.IsZero() {
		where = append(where, "exit_at<?")
		a = append(a, end.UnixMicro())
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY exit_at DESC"
	rows, e := s.DB.QueryContext(ctx, q, a...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Trade
	for rows.Next() {
		var t Trade
		if e = rows.Scan(&t.ID, &t.Account, &t.Symbol, &t.Direction, &t.EntryAt, &t.ExitAt, &t.EntryPrice, &t.ExitPrice, &t.MaxQuantity, &t.Entered, &t.Exited, &t.Gross, &t.Commissions, &t.Fees, &t.Net, &t.ExecutionCount); e != nil {
			return nil, e
		}
		t.Tags, _ = s.tagsFor(ctx, t.ID)
		_ = s.DB.QueryRowContext(ctx, "SELECT note FROM trade_notes WHERE round_trip_id=?", t.ID).Scan(&t.Note)
		if x, err := s.Excursion(ctx, t.ID); err == nil {
			t.Excursion = &x
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Trade(ctx context.Context, id int64) (Trade, []positions.Execution, error) {
	ts, e := s.Trades(ctx, time.Time{}, time.Time{})
	if e != nil {
		return Trade{}, nil, e
	}
	var t Trade
	found := false
	for _, v := range ts {
		if v.ID == id {
			t = v
			found = true
			break
		}
	}
	if !found {
		return t, nil, sql.ErrNoRows
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT e.id,e.account,e.symbol,e.action,COALESCE(r.quantity,e.quantity),e.price,COALESCE(r.commission,e.commission),COALESCE(r.fees,e.fees),e.executed_at,e.source_row FROM executions e JOIN round_trip_executions r ON r.execution_id=e.id WHERE r.round_trip_id=? ORDER BY e.executed_at,e.source_row", id)
	if e != nil {
		return t, nil, e
	}
	defer rows.Close()
	var xs []positions.Execution
	for rows.Next() {
		var x positions.Execution
		var at int64
		if e = rows.Scan(&x.ID, &x.Account, &x.Symbol, &x.Action, &x.Quantity, &x.Price, &x.Commission, &x.Fees, &at, &x.Row); e != nil {
			return t, nil, e
		}
		x.At = time.UnixMicro(at).UTC()
		xs = append(xs, x)
	}
	return t, xs, rows.Err()
}
func (s *Store) tagsFor(ctx context.Context, id int64) ([]Tag, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT t.id,t.name,t.color,t.archived FROM tags t JOIN round_trip_tags rt ON rt.tag_id=t.id WHERE rt.round_trip_id=?", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ts := make([]Tag, 0)
	for rows.Next() {
		var t Tag
		var a int
		rows.Scan(&t.ID, &t.Name, &t.Color, &a)
		t.Archived = a != 0
		ts = append(ts, t)
	}
	return ts, rows.Err()
}
func (s *Store) Tags(ctx context.Context) ([]Tag, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,name,color,archived FROM tags ORDER BY archived,name")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ts := make([]Tag, 0)
	for rows.Next() {
		var t Tag
		var a int
		rows.Scan(&t.ID, &t.Name, &t.Color, &a)
		t.Archived = a != 0
		ts = append(ts, t)
	}
	return ts, rows.Err()
}
func (s *Store) CreateTag(ctx context.Context, name, color string) (Tag, error) {
	r, e := s.DB.ExecContext(ctx, "INSERT INTO tags(name,color)VALUES(?,?)", name, color)
	if e != nil {
		return Tag{}, e
	}
	id, _ := r.LastInsertId()
	return Tag{ID: id, Name: name, Color: color}, nil
}
func (s *Store) UpdateTag(ctx context.Context, id int64, name, color string, archived bool) error {
	_, e := s.DB.ExecContext(ctx, "UPDATE tags SET name=?,color=?,archived=? WHERE id=?", name, color, boolInt(archived), id)
	return e
}
func (s *Store) AddTradeTag(ctx context.Context, tradeID, tagID int64) error {
	_, e := s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO round_trip_tags(round_trip_id,tag_id) VALUES(?,?)", tradeID, tagID)
	return e
}
func (s *Store) RemoveTradeTag(ctx context.Context, tradeID, tagID int64) error {
	_, e := s.DB.ExecContext(ctx, "DELETE FROM round_trip_tags WHERE round_trip_id=? AND tag_id=?", tradeID, tagID)
	return e
}
func (s *Store) BulkTradeTags(ctx context.Context, tradeIDs, tagIDs []int64, mode string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, tradeID := range tradeIDs {
		switch mode {
		case "set":
			if _, e = tx.ExecContext(ctx, "DELETE FROM round_trip_tags WHERE round_trip_id=?", tradeID); e != nil {
				return e
			}
		case "remove":
			for _, tagID := range tagIDs {
				if _, e = tx.ExecContext(ctx, "DELETE FROM round_trip_tags WHERE round_trip_id=? AND tag_id=?", tradeID, tagID); e != nil {
					return e
				}
			}
			continue
		case "add":
		default:
			return fmt.Errorf("unknown tag bulk mode %q", mode)
		}
		for _, tagID := range tagIDs {
			if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO round_trip_tags(round_trip_id,tag_id) VALUES(?,?)", tradeID, tagID); e != nil {
				return e
			}
		}
	}
	return tx.Commit()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (s *Store) SetTrade(ctx context.Context, id int64, note string, tags []int64) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO trade_notes(round_trip_id,note,updated_at) VALUES(?,?,?) ON CONFLICT(round_trip_id) DO UPDATE SET note=excluded.note,updated_at=excluded.updated_at", id, note, time.Now().Unix()); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM round_trip_tags WHERE round_trip_id=?", id); e != nil {
		return e
	}
	for _, tag := range tags {
		if _, e = tx.ExecContext(ctx, "INSERT INTO round_trip_tags(round_trip_id,tag_id)VALUES(?,?)", id, tag); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) SetTradeNote(ctx context.Context, id int64, note string) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO trade_notes(round_trip_id,note,updated_at) VALUES(?,?,?) ON CONFLICT(round_trip_id) DO UPDATE SET note=excluded.note,updated_at=excluded.updated_at", id, note, time.Now().Unix())
	return e
}
func (s *Store) DayNote(ctx context.Context, day string) (string, error) {
	var note string
	e := s.DB.QueryRowContext(ctx, "SELECT note FROM day_notes WHERE day=?", day).Scan(&note)
	if e == sql.ErrNoRows {
		return "", nil
	}
	return note, e
}
func (s *Store) SetDayNote(ctx context.Context, day, note string) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO day_notes(day,note,updated_at) VALUES(?,?,?) ON CONFLICT(day) DO UPDATE SET note=excluded.note,updated_at=excluded.updated_at", day, note, time.Now().Unix())
	return e
}
func (s *Store) Backup(ctx context.Context, dir string) (string, error) {
	if e := os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	p := filepath.Join(dir, "tale-of-the-tape-"+time.Now().Format("20060102-150405")+".db")
	_, e := s.DB.ExecContext(ctx, "VACUUM INTO ?", p)
	return p, e
}

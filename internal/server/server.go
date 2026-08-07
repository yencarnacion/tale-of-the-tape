package server

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"tale-of-the-tape/internal/analytics"
	"tale-of-the-tape/internal/config"
	"tale-of-the-tape/internal/excursion"
	"tale-of-the-tape/internal/importer"
	"tale-of-the-tape/internal/indicators"
	"tale-of-the-tape/internal/marketdata/massive"
	"tale-of-the-tape/internal/positions"
	"tale-of-the-tape/internal/storage"
)

//go:embed web/* web/vendor/lightweight-charts/*
var assets embed.FS

type Server struct {
	cfg      config.Config
	store    *storage.Store
	previews map[string]importer.Preview
	mu       sync.Mutex
}

func New(c config.Config, s *storage.Store) *Server {
	if s != nil {
		applyStoredSettings(&c, s)
	}
	return &Server{cfg: c, store: s, previews: map[string]importer.Preview{}}
}
func applyStoredSettings(c *config.Config, s *storage.Store) {
	ctx := context.Background()
	if value, err := s.Setting(ctx, "timezone"); err == nil {
		if _, valid := time.LoadLocation(value); valid == nil {
			c.App.Timezone = value
		}
	}
	if value, err := s.Setting(ctx, "scratch_tolerance"); err == nil {
		if parsed, valid := strconv.ParseFloat(value, 64); valid == nil && parsed >= 0 {
			c.Import.ScratchTolerance = parsed
		}
	}
	if value, err := s.Setting(ctx, "default_timeframe"); err == nil && (value == "1m" || value == "5m") {
		c.Chart.DefaultTimeframe = value
	}
	if value, err := s.Setting(ctx, "polygon_charts_url"); err == nil && validHTTPURL(value) {
		c.Chart.PolygonChartsURL = strings.TrimRight(value, "/")
	}
}
func validHTTPURL(value string) bool {
	u, err := url.ParseRequestURI(value)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/v1/health", s.health)
	m.HandleFunc("/api/v1/settings", s.settings)
	m.HandleFunc("/api/v1/imports/preview", s.preview)
	m.HandleFunc("/api/v1/imports/commit", s.commit)
	m.HandleFunc("/api/v1/imports", s.imports)
	m.HandleFunc("/api/v1/imports/", s.importDetail)
	m.HandleFunc("/api/v1/trades", s.trades)
	m.HandleFunc("/api/v1/trades/", s.trade)
	m.HandleFunc("/api/v1/tags", s.tags)
	m.HandleFunc("/api/v1/tags/", s.tag)
	m.HandleFunc("/api/v1/analytics/summary", s.summary)
	m.HandleFunc("/api/v1/analytics/equity", s.equity)
	m.HandleFunc("/api/v1/analytics/risk", s.riskAnalytics)
	m.HandleFunc("/api/v1/analytics/breakdowns", s.breakdowns)
	m.HandleFunc("/api/v1/calendar", s.calendar)
	m.HandleFunc("/api/v1/day-notes/", s.dayNote)
	m.HandleFunc("/api/v1/enrichment/range", s.enrichRange)
	m.HandleFunc("/api/v1/backup", s.backup)
	m.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusNoContent)
	})
	sub, _ := fs.Sub(assets, "web")
	m.Handle("/", http.FileServer(http.FS(sub)))
	return security(m)
}
func (s *Server) Serve(ctx context.Context) error {
	h := &http.Server{Addr: s.cfg.App.Addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- h.ListenAndServe() }()
	select {
	case err := <-errc:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return h.Shutdown(c)
	}
}
func security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				bad(w, "internal_error", "The request could not be completed.", 500)
			}
		}()
		// The embedded vanilla UI currently uses inline event attributes for
		// its local-only controls. Keep all script origins restricted to self,
		// while explicitly allowing those handlers.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self'; connect-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func bad(w http.ResponseWriter, c, msg string, status int) {
	w.WriteHeader(status)
	jsonOut(w, map[string]any{"error": map[string]string{"code": c, "message": msg}})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"status": "ok", "massive_configured": s.cfg.Massive.APIKey != ""})
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"app": s.cfg.App, "storage": map[string]string{"path": s.cfg.Storage.Path, "backups_path": s.cfg.Storage.BackupsPath}, "import": s.cfg.Import, "chart": s.cfg.Chart, "massive_configured": s.cfg.Massive.APIKey != ""})
	case http.MethodPatch:
		var in struct {
			Timezone         *string  `json:"timezone"`
			ScratchTolerance *float64 `json:"scratch_tolerance"`
			DefaultTimeframe *string  `json:"default_timeframe"`
			PolygonChartsURL *string  `json:"polygon_charts_url"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if e := decoder.Decode(&in); e != nil {
			bad(w, "invalid_json", "Invalid JSON.", http.StatusBadRequest)
			return
		}
		if in.Timezone != nil {
			if _, e := time.LoadLocation(*in.Timezone); e != nil {
				bad(w, "invalid_settings", "Timezone must be an IANA location.", http.StatusBadRequest)
				return
			}
		}
		if in.ScratchTolerance != nil && *in.ScratchTolerance < 0 {
			bad(w, "invalid_settings", "Scratch tolerance must not be negative.", http.StatusBadRequest)
			return
		}
		if in.DefaultTimeframe != nil && *in.DefaultTimeframe != "1m" && *in.DefaultTimeframe != "5m" {
			bad(w, "invalid_settings", "Default timeframe must be 1m or 5m.", http.StatusBadRequest)
			return
		}
		if in.PolygonChartsURL != nil && !validHTTPURL(strings.TrimSpace(*in.PolygonChartsURL)) {
			bad(w, "invalid_settings", "Polygon Charts URL must be an http or https URL.", http.StatusBadRequest)
			return
		}
		if in.Timezone != nil {
			if e := s.store.SetSetting(r.Context(), "timezone", *in.Timezone); e != nil {
				bad(w, "update_failed", e.Error(), http.StatusInternalServerError)
				return
			}
			s.cfg.App.Timezone = *in.Timezone
		}
		if in.ScratchTolerance != nil {
			if e := s.store.SetSetting(r.Context(), "scratch_tolerance", strconv.FormatFloat(*in.ScratchTolerance, 'f', -1, 64)); e != nil {
				bad(w, "update_failed", e.Error(), http.StatusInternalServerError)
				return
			}
			s.cfg.Import.ScratchTolerance = *in.ScratchTolerance
		}
		if in.DefaultTimeframe != nil {
			if e := s.store.SetSetting(r.Context(), "default_timeframe", *in.DefaultTimeframe); e != nil {
				bad(w, "update_failed", e.Error(), http.StatusInternalServerError)
				return
			}
			s.cfg.Chart.DefaultTimeframe = *in.DefaultTimeframe
		}
		if in.PolygonChartsURL != nil {
			value := strings.TrimRight(strings.TrimSpace(*in.PolygonChartsURL), "/")
			if e := s.store.SetSetting(r.Context(), "polygon_charts_url", value); e != nil {
				bad(w, "update_failed", e.Error(), http.StatusInternalServerError)
				return
			}
			s.cfg.Chart.PolygonChartsURL = value
		}
		jsonOut(w, map[string]bool{"ok": true})
	default:
		bad(w, "method_not_allowed", "Method not allowed.", http.StatusMethodNotAllowed)
	}
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Import.MaximumUploadMB<<20)
	if e := r.ParseMultipartForm(s.cfg.Import.MaximumUploadMB << 20); e != nil {
		bad(w, "invalid_upload", e.Error(), 400)
		return
	}
	f, h, e := r.FormFile("file")
	if e != nil {
		bad(w, "missing_file", "A CSV or ZIP file is required.", 400)
		return
	}
	defer f.Close()
	b, e := io.ReadAll(f)
	if e != nil {
		bad(w, "read_upload", e.Error(), 400)
		return
	}
	loc, _ := time.LoadLocation(s.cfg.Import.AssumedTimezone)
	p, e := importer.ParseBytes(h.Filename, b, loc)
	if e != nil {
		bad(w, "invalid_import", e.Error(), 400)
		return
	}
	s.mu.Lock()
	s.previews[p.SHA256] = p
	s.mu.Unlock()
	jsonOut(w, map[string]any{"token": p.SHA256, "files": p.Files, "account": p.Account, "accepted_rows": p.Accepted, "skipped_rows": p.Skipped, "rejected_rows": p.Rejected, "symbols": p.Symbols, "warnings": p.Warnings, "start": p.Start, "end": p.End})
}
func (s *Server) commit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	var in struct {
		Token    string `json:"token"`
		Filename string `json:"filename"`
	}
	if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
		bad(w, "invalid_json", "Invalid JSON.", 400)
		return
	}
	s.mu.Lock()
	p, ok := s.previews[in.Token]
	s.mu.Unlock()
	if !ok {
		bad(w, "expired_preview", "Preview not found; upload again.", 400)
		return
	}
	rej := make([]string, len(p.Rejected))
	for i, v := range p.Rejected {
		rej[i] = strings.Join(v.Raw, ",") + ": " + v.Reason
	}
	id, n, e := s.store.Commit(r.Context(), p.SHA256, in.Filename, p.Executions, rej)
	if e != nil {
		bad(w, "commit_failed", e.Error(), 500)
		return
	}
	for _, snapshot := range p.BrokerPnL {
		if e = s.store.StoreBrokerPnL(r.Context(), id, snapshot.StatementDate, snapshot.Day, snapshot.YTD, snapshot.FeesYTD); e != nil {
			bad(w, "commit_failed", e.Error(), 500)
			return
		}
	}
	jsonOut(w, map[string]any{"batch_id": id, "new_executions": n, "message": "Import committed and round trips rebuilt."})
}
func (s *Server) imports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		bad(w, "method_not_allowed", "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	rows, e := s.store.DB.QueryContext(r.Context(), "SELECT id,sha256,filename,imported_at,accepted_rows,rejected_rows FROM import_batches ORDER BY id DESC")
	if e != nil {
		bad(w, "database", e.Error(), 500)
		return
	}
	defer rows.Close()
	var x []map[string]any
	for rows.Next() {
		var id, at, a, b int64
		var sha, n string
		rows.Scan(&id, &sha, &n, &at, &a, &b)
		x = append(x, map[string]any{"id": id, "sha256": sha, "filename": n, "imported_at": at, "accepted_rows": a, "rejected_rows": b})
	}
	jsonOut(w, x)
}
func (s *Server) importDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		bad(w, "method_not_allowed", "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	id, e := strconv.ParseInt(path.Base(r.URL.Path), 10, 64)
	if e != nil || id < 1 {
		bad(w, "invalid_import", "Invalid import ID.", http.StatusBadRequest)
		return
	}
	b, e := s.store.ImportBatch(r.Context(), id)
	if e == sql.ErrNoRows {
		bad(w, "not_found", "Import not found.", http.StatusNotFound)
		return
	}
	if e != nil {
		bad(w, "database", e.Error(), http.StatusInternalServerError)
		return
	}
	jsonOut(w, b)
}
func (s *Server) dates(r *http.Request) (time.Time, time.Time, error) {
	var a, b time.Time
	var e error
	loc, e := time.LoadLocation(s.cfg.App.Timezone)
	if e != nil {
		return a, b, e
	}
	if v := r.URL.Query().Get("start"); v != "" {
		a, e = time.ParseInLocation("2006-01-02", v, loc)
		if e != nil {
			return a, b, e
		}
	}
	if v := r.URL.Query().Get("end"); v != "" {
		b, e = time.ParseInLocation("2006-01-02", v, loc)
		if e != nil {
			return a, b, e
		}
		b = b.AddDate(0, 0, 1)
	}
	if !a.IsZero() && !b.IsZero() && !b.After(a) {
		return a, b, fmt.Errorf("end date must not be earlier than start date")
	}
	return a, b, nil
}

// filteredTrades is the one population gate for list, dashboard, calendar,
// equity, breakdown, and range-enrichment endpoints. Date bounds are applied
// in the configured trading timezone before the in-memory dimensions below.
func (s *Server) filteredTrades(r *http.Request) ([]storage.Trade, error) {
	start, end, err := s.dates(r)
	if err != nil {
		return nil, err
	}
	trades, err := s.store.Trades(r.Context(), start, end)
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	loc, _ := time.LoadLocation(s.cfg.App.Timezone)
	tags := strings.FieldsFunc(strings.ToLower(q.Get("tag")), func(r rune) bool { return r == ',' })
	tagMode := strings.ToLower(q.Get("tag_mode"))
	if tagMode == "" {
		tagMode = "any"
	}
	var out []storage.Trade
	for _, trade := range trades {
		if value := q.Get("account"); value != "" && value != trade.Account {
			continue
		}
		if value := q.Get("symbol"); value != "" && !strings.EqualFold(value, trade.Symbol) {
			continue
		}
		if value := q.Get("direction"); value != "" && !strings.EqualFold(value, trade.Direction) {
			continue
		}
		net := float64(trade.Net) / float64(positions.Scale)
		switch q.Get("outcome") {
		case "win":
			if net <= s.cfg.Import.ScratchTolerance {
				continue
			}
		case "loss":
			if net >= -s.cfg.Import.ScratchTolerance {
				continue
			}
		case "scratch":
			if net > s.cfg.Import.ScratchTolerance || net < -s.cfg.Import.ScratchTolerance {
				continue
			}
		}
		hold := time.UnixMicro(trade.ExitAt).Sub(time.UnixMicro(trade.EntryAt))
		if bucket := q.Get("holding_time"); bucket != "" && bucket != holdingBucket(hold) {
			continue
		}
		entry := time.UnixMicro(trade.EntryAt).In(loc)
		if value := q.Get("weekday"); value != "" && !strings.EqualFold(value, entry.Weekday().String()) && value != strconv.Itoa(int(entry.Weekday())) {
			continue
		}
		if value := q.Get("time_bucket"); value != "" && value != entry.Truncate(30*time.Minute).Format("15:04") {
			continue
		}
		if len(tags) > 0 {
			matched := 0
			for _, wanted := range tags {
				for _, tag := range trade.Tags {
					if strings.EqualFold(wanted, tag.Name) {
						matched++
						break
					}
				}
			}
			if (tagMode == "all" && matched != len(tags)) || (tagMode != "all" && matched == 0) {
				continue
			}
		}
		if value := q.Get("excursion_completeness"); value != "" {
			x, e := s.store.Excursion(r.Context(), trade.ID)
			if e != nil || !strings.EqualFold(value, x.Completeness) {
				continue
			}
		}
		out = append(out, trade)
	}
	return out, nil
}
func holdingBucket(d time.Duration) string {
	switch {
	case d < 5*time.Minute:
		return "under_5m"
	case d < 30*time.Minute:
		return "5_30m"
	case d < time.Hour:
		return "30_60m"
	default:
		return "60m_plus"
	}
}
func (s *Server) trades(w http.ResponseWriter, r *http.Request) {
	ts, e := s.filteredTrades(r)
	if e != nil {
		bad(w, "invalid_filter", e.Error(), 400)
		return
	}
	jsonOut(w, ts)
}
func (s *Server) trade(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/trades/bulk-tags" {
		s.bulkTradeTags(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/chart") {
		s.chart(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/enrich") {
		s.enrich(w, r)
		return
	}
	if strings.Contains(r.URL.Path, "/tags/") {
		s.removeTradeTag(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/tags") {
		s.addTradeTag(w, r)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/trades/")
	id, e := strconv.ParseInt(path.Base(p), 10, 64)
	if e != nil {
		bad(w, "invalid_trade", "Invalid trade ID.", 400)
		return
	}
	if r.Method == "PATCH" {
		var in struct {
			Note   string  `json:"note"`
			TagIDs []int64 `json:"tag_ids"`
		}
		if e = json.NewDecoder(r.Body).Decode(&in); e != nil {
			bad(w, "invalid_json", "Invalid JSON.", 400)
			return
		}
		if in.TagIDs == nil {
			e = s.store.SetTradeNote(r.Context(), id, in.Note)
		} else {
			e = s.store.SetTrade(r.Context(), id, in.Note, in.TagIDs)
		}
		if e != nil {
			bad(w, "update_failed", e.Error(), 500)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
		return
	}
	t, xs, e := s.store.Trade(r.Context(), id)
	if e != nil {
		bad(w, "not_found", "Trade not found.", 404)
		return
	}
	x, _ := s.store.Excursion(r.Context(), id)
	loc, _ := time.LoadLocation(s.cfg.App.Timezone)
	tradingDay := time.UnixMicro(t.ExitAt).In(loc).Format("2006-01-02")
	jsonOut(w, map[string]any{"trade": t, "executions": xs, "excursion": x, "trading_day": tradingDay, "timezone": s.cfg.App.Timezone, "polygon_charts_url": s.cfg.Chart.PolygonChartsURL, "massive_status": map[bool]string{true: "configured", false: "Massive API key not configured"}[s.cfg.Massive.APIKey != ""]})
}
func (s *Server) bulkTradeTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		bad(w, "method_not_allowed", "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		TradeIDs []int64 `json:"trade_ids"`
		TagIDs   []int64 `json:"tag_ids"`
		Mode     string  `json:"mode"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&in); e != nil || len(in.TradeIDs) == 0 || (len(in.TagIDs) == 0 && in.Mode != "set") {
		bad(w, "invalid_bulk_tags", "Trade IDs and tag IDs are required (set may use an empty tag list).", http.StatusBadRequest)
		return
	}
	if in.Mode != "add" && in.Mode != "remove" && in.Mode != "set" {
		bad(w, "invalid_bulk_tags", "Mode must be add, remove, or set.", http.StatusBadRequest)
		return
	}
	if e := s.store.BulkTradeTags(r.Context(), in.TradeIDs, in.TagIDs, in.Mode); e != nil {
		bad(w, "bulk_tags", e.Error(), http.StatusInternalServerError)
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
func (s *Server) tradeID(r *http.Request) (int64, error) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/trades/")
	p = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(p, "/chart"), "/enrich"), "/tags")
	return strconv.ParseInt(path.Base(p), 10, 64)
}
func (s *Server) chart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	id, e := s.tradeID(r)
	if e != nil {
		bad(w, "invalid_trade", "Invalid trade ID.", 400)
		return
	}
	t, xs, e := s.store.Trade(r.Context(), id)
	if e != nil {
		bad(w, "not_found", "Trade not found.", 404)
		return
	}
	tf := r.URL.Query().Get("timeframe")
	if tf != "5m" {
		tf = "1m"
	}
	start, end := s.chartInterval(t, r.URL.Query().Get("view") == "full_session")
	bars, covered, e := s.aggregateBars(r.Context(), t.Symbol, tf, start, end)
	if e != nil {
		bad(w, "market_data", e.Error(), 502)
		return
	}
	if len(bars) == 0 && s.cfg.Massive.APIKey == "" {
		jsonOut(w, map[string]any{"status": "Massive API key not configured", "bars": []storage.Bar{}, "executions": xs})
		return
	}
	ib := make([]indicators.Bar, len(bars))
	for i, b := range bars {
		ib[i] = indicators.Bar{Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
	}
	status, source := "ok", "Massive aggregates"
	if !covered {
		status, source = "Cached bars may be incomplete; Massive API key not configured", "local cache"
	}
	x, _ := s.store.Excursion(r.Context(), id)
	jsonOut(w, map[string]any{"status": status, "source": source, "bars": bars, "indicators": indicators.Calculate(ib), "average_entry": averageEntryLine(xs, bars), "executions": xs, "excursion": x})
}

// averageEntryLine follows the same signed-position/weighted-basis rules as
// the canonical engine and emits no point once the position has been closed.
func averageEntryLine(execs []positions.Execution, bars []storage.Bar) []indicators.Point {
	if len(execs) == 0 || len(bars) == 0 {
		return nil
	}
	slices.SortStableFunc(execs, func(a, b positions.Execution) int {
		if a.At.Equal(b.At) {
			return a.Row - b.Row
		}
		if a.At.Before(b.At) {
			return -1
		}
		return 1
	})
	var line []indicators.Point
	var pos, basis int64
	next := 0
	for _, bar := range bars {
		at := time.UnixMilli(bar.Time)
		for next < len(execs) && !execs[next].At.After(at) {
			e := execs[next]
			quantity, side := e.Quantity, int64(1)
			if e.Action == "sell" {
				side = -1
			}
			for quantity > 0 {
				if pos == 0 || (pos > 0) == (side > 0) {
					old := absPosition(pos)
					basis = (basis*old + e.Price*quantity) / (old + quantity)
					pos += side * quantity
					quantity = 0
					continue
				}
				closed := minPosition(quantity, absPosition(pos))
				pos += side * closed
				quantity -= closed
				if pos == 0 {
					basis = 0
				}
			}
			next++
		}
		if pos != 0 {
			line = append(line, indicators.Point{Time: bar.Time, Value: float64(basis) / float64(positions.Scale)})
		}
	}
	return line
}
func absPosition(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func minPosition(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (s *Server) chartInterval(t storage.Trade, fullSession bool) (time.Time, time.Time) {
	entry, exit := time.UnixMicro(t.EntryAt), time.UnixMicro(t.ExitAt)
	if !fullSession {
		return entry.Add(-s.cfg.Massive.ChartPaddingBefore), exit.Add(s.cfg.Massive.ChartPaddingAfter)
	}
	loc, err := time.LoadLocation(s.cfg.App.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := entry.In(loc)
	openHour, closeHour := 9, 16
	openMinute, closeMinute := 30, 0
	if s.cfg.Chart.IncludeExtendedHours {
		openHour, openMinute, closeHour = 4, 0, 20
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), openHour, openMinute, 0, 0, loc)
	end := time.Date(local.Year(), local.Month(), local.Day(), closeHour, closeMinute, 0, 0, loc)
	return start, end
}

// aggregateBars returns bars and whether SQLite has provider-confirmed
// coverage for the whole interval. A partial cache is refreshed rather than
// silently treated as complete.
func (s *Server) aggregateBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]storage.Bar, bool, error) {
	covered, err := s.store.HasBarCoverage(ctx, symbol, timeframe, start, end)
	if err != nil {
		return nil, false, err
	}
	bars, err := s.store.Bars(ctx, symbol, timeframe, start, end)
	if err != nil || covered || s.cfg.Massive.APIKey == "" {
		return bars, covered, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.cfg.Massive.RequestTimeout)
	defer cancel()
	remote, err := massive.Bars(requestCtx, s.cfg.Massive.APIKey, symbol, timeframe, start, end)
	if err != nil {
		return nil, false, err
	}
	fetched := make([]storage.Bar, len(remote))
	for i, b := range remote {
		fetched[i] = storage.Bar{Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
	}
	if err = s.store.StoreBarsCoverage(ctx, symbol, timeframe, start, end, fetched); err != nil {
		return nil, false, err
	}
	bars, err = s.store.Bars(ctx, symbol, timeframe, start, end)
	return bars, err == nil, err
}
func (s *Server) enrich(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	id, e := s.tradeID(r)
	if e != nil {
		bad(w, "invalid_trade", "Invalid trade ID.", 400)
		return
	}
	t, xs, e := s.store.Trade(r.Context(), id)
	if e != nil {
		bad(w, "not_found", "Trade not found.", 404)
		return
	}
	start, end := time.UnixMicro(t.EntryAt), time.UnixMicro(t.ExitAt)
	if marks, source, ok := s.preferredMarks(r.Context(), t.Direction, t.Symbol, start, end); ok {
		x := excursion.Calculate(xs, marks)
		x.Completeness = "complete"
		x.Warning = excursionWarning(source)
		se := storedExcursion(x, source)
		if e = s.store.SaveExcursion(r.Context(), id, se); e != nil {
			bad(w, "save_excursion", e.Error(), 500)
			return
		}
		jsonOut(w, se)
		return
	}
	bars, covered, e := s.aggregateBars(r.Context(), t.Symbol, "1m", start, end)
	if e != nil {
		bad(w, "market_data", e.Error(), 502)
		return
	}
	if len(bars) == 0 {
		bad(w, "massive_not_configured", "Massive API key not configured and no cached bars are available.", 409)
		return
	}
	marks := aggregateMarks(t.Direction, bars)
	x := excursion.Calculate(xs, marks)
	x.Completeness = "approximate"
	x.Warning = "Aggregate bars are an approximate fallback; intrabar order is unknown. Thinkorswim timestamps may have second precision."
	if !covered {
		x.Warning += " Cached interval may be incomplete because Massive is not configured."
	}
	se := storedExcursion(x, x.Source)
	if e = s.store.SaveExcursion(r.Context(), id, se); e != nil {
		bad(w, "save_excursion", e.Error(), 500)
		return
	}
	jsonOut(w, se)
}

// enrichRange processes the same canonical excursion path for every selected
// completed trade. Individual failures are returned per trade so one provider
// gap never masks the rest of the batch.
func (s *Server) enrichRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	ts, e := s.filteredTrades(r)
	if e != nil {
		bad(w, "invalid_filter", e.Error(), 400)
		return
	}
	var done int
	failed := map[int64]string{}
	for _, t := range ts {
		_, xs, e := s.store.Trade(r.Context(), t.ID)
		if e != nil {
			failed[t.ID] = e.Error()
			continue
		}
		start, end := time.UnixMicro(t.EntryAt), time.UnixMicro(t.ExitAt)
		if marks, source, ok := s.preferredMarks(r.Context(), t.Direction, t.Symbol, start, end); ok {
			x := excursion.Calculate(xs, marks)
			x.Completeness = "complete"
			x.Warning = excursionWarning(source)
			if e = s.store.SaveExcursion(r.Context(), t.ID, storedExcursion(x, source)); e != nil {
				failed[t.ID] = e.Error()
				continue
			}
			done++
			continue
		}
		bars, covered, e := s.aggregateBars(r.Context(), t.Symbol, "1m", start, end)
		if e != nil {
			failed[t.ID] = e.Error()
			continue
		}
		if len(bars) == 0 {
			failed[t.ID] = "Massive API key not configured and no cached bars are available"
			continue
		}
		marks := aggregateMarks(t.Direction, bars)
		x := excursion.Calculate(xs, marks)
		x.Completeness = "approximate"
		x.Warning = "Aggregate bars are an approximate fallback; intrabar order is unknown. Thinkorswim timestamps may have second precision."
		if !covered {
			x.Warning += " Cached interval may be incomplete because Massive is not configured."
		}
		if e = s.store.SaveExcursion(r.Context(), t.ID, storedExcursion(x, x.Source)); e != nil {
			failed[t.ID] = e.Error()
			continue
		}
		done++
	}
	jsonOut(w, map[string]any{"requested": len(ts), "completed": done, "failed": failed})
}
func aggregateMarks(direction string, bars []storage.Bar) []excursion.Event {
	marks := make([]excursion.Event, 0, len(bars)*2)
	for _, b := range bars {
		at := time.UnixMilli(b.Time)
		low := int64(b.Low * float64(positions.Scale))
		high := int64(b.High * float64(positions.Scale))
		if direction == "long" {
			marks = append(marks, excursion.Event{At: at, Price: low, Source: "aggregates"}, excursion.Event{At: at, Price: high, Source: "aggregates"})
		} else {
			marks = append(marks, excursion.Event{At: at, Price: high, Source: "aggregates"}, excursion.Event{At: at, Price: low, Source: "aggregates"})
		}
	}
	return marks
}
func (s *Server) nbboMarks(ctx context.Context, direction, symbol string, start, end time.Time) ([]excursion.Event, bool) {
	if !s.cfg.Massive.PreferNBBO || s.cfg.Massive.APIKey == "" {
		return nil, false
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.cfg.Massive.RequestTimeout)
	quotes, e := massive.Quotes(requestCtx, s.cfg.Massive.APIKey, symbol, start, end)
	cancel()
	if e != nil || len(quotes) == 0 {
		return nil, false
	}
	marks := make([]excursion.Event, 0, len(quotes))
	for _, q := range quotes {
		p := q.Bid
		if direction == "short" {
			p = q.Ask
		}
		if p > 0 {
			marks = append(marks, excursion.Event{At: q.At, Price: int64(p * float64(positions.Scale)), Source: "nbbo"})
		}
	}
	return marks, len(marks) > 0
}

func (s *Server) preferredMarks(ctx context.Context, direction, symbol string, start, end time.Time) ([]excursion.Event, string, bool) {
	if marks, ok := s.nbboMarks(ctx, direction, symbol, start, end); ok {
		return marks, "nbbo", true
	}
	if !s.cfg.Massive.FallbackToTrades || s.cfg.Massive.APIKey == "" {
		return nil, "", false
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.cfg.Massive.RequestTimeout)
	prints, err := massive.Trades(requestCtx, s.cfg.Massive.APIKey, symbol, start, end)
	cancel()
	if err != nil || len(prints) == 0 {
		return nil, "", false
	}
	marks := make([]excursion.Event, 0, len(prints))
	for _, print := range prints {
		if print.Price > 0 {
			marks = append(marks, excursion.Event{At: print.At, Price: int64(print.Price * float64(positions.Scale)), Source: "trade_prints"})
		}
	}
	return marks, "trade_prints", len(marks) > 0
}

func excursionWarning(source string) string {
	if source == "trade_prints" {
		return "Historical trade-print marks; NBBO was unavailable. Thinkorswim timestamps may have second precision."
	}
	return "NBBO liquidation marks; Thinkorswim timestamps may have second precision."
}

func storedExcursion(x excursion.Result, source string) storage.Excursion {
	var mfeAt, maeAt int64
	if !x.MFEAt.IsZero() {
		mfeAt = x.MFEAt.UnixMicro()
	}
	if !x.MAEAt.IsZero() {
		maeAt = x.MAEAt.UnixMicro()
	}
	return storage.Excursion{
		MFE: x.MFE, MAE: x.MAE, MFEAt: mfeAt, MAEAt: maeAt,
		Source: source, Completeness: x.Completeness, Warnings: x.Warning, Events: x.Events, CalculatedAt: time.Now().Unix(),
	}
}

type EnrichmentReport struct {
	Requested, Completed int
	Failed               map[int64]string
}

// EnrichRange is the CLI-safe form of range enrichment.
func (s *Server) EnrichRange(ctx context.Context, start, end time.Time) (EnrichmentReport, error) {
	ts, e := s.store.Trades(ctx, start, end)
	if e != nil {
		return EnrichmentReport{}, e
	}
	r := EnrichmentReport{Requested: len(ts), Failed: map[int64]string{}}
	for _, t := range ts {
		_, xs, e := s.store.Trade(ctx, t.ID)
		if e != nil {
			r.Failed[t.ID] = e.Error()
			continue
		}
		a, b := time.UnixMicro(t.EntryAt), time.UnixMicro(t.ExitAt)
		if marks, source, ok := s.preferredMarks(ctx, t.Direction, t.Symbol, a, b); ok {
			x := excursion.Calculate(xs, marks)
			x.Completeness = "complete"
			x.Warning = excursionWarning(source)
			if e = s.store.SaveExcursion(ctx, t.ID, storedExcursion(x, source)); e != nil {
				r.Failed[t.ID] = e.Error()
				continue
			}
			r.Completed++
			continue
		}
		bars, covered, e := s.aggregateBars(ctx, t.Symbol, "1m", a, b)
		if e != nil {
			r.Failed[t.ID] = e.Error()
			continue
		}
		if len(bars) == 0 {
			r.Failed[t.ID] = "Massive API key not configured and no cached bars are available"
			continue
		}
		x := excursion.Calculate(xs, aggregateMarks(t.Direction, bars))
		x.Completeness = "approximate"
		x.Warning = "Aggregate bars are an approximate fallback; intrabar order is unknown. Thinkorswim timestamps may have second precision."
		if !covered {
			x.Warning += " Cached interval may be incomplete because Massive is not configured."
		}
		if e = s.store.SaveExcursion(ctx, t.ID, storedExcursion(x, x.Source)); e != nil {
			r.Failed[t.ID] = e.Error()
			continue
		}
		r.Completed++
	}
	return r, nil
}
func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct{ Name, Color string }
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil || strings.TrimSpace(in.Name) == "" {
			bad(w, "invalid_tag", "Tag name is required.", 400)
			return
		}
		if in.Color == "" {
			in.Color = "#58a6ff"
		}
		t, e := s.store.CreateTag(r.Context(), in.Name, in.Color)
		if e != nil {
			bad(w, "create_tag", e.Error(), 400)
			return
		}
		jsonOut(w, t)
		return
	}
	if r.Method != http.MethodGet {
		bad(w, "method_not_allowed", "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	x, e := s.store.Tags(r.Context())
	if e != nil {
		bad(w, "database", e.Error(), 500)
		return
	}
	jsonOut(w, x)
}
func (s *Server) tag(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(path.Base(r.URL.Path), 10, 64)
	if e != nil {
		bad(w, "invalid_tag", "Invalid tag ID.", 400)
		return
	}
	if r.Method == http.MethodDelete {
		if e = s.store.DeleteTag(r.Context(), id); e != nil {
			bad(w, "delete_tag", e.Error(), http.StatusInternalServerError)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
		return
	}
	if r.Method != http.MethodPatch {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	var in struct {
		Name     string `json:"name"`
		Color    string `json:"color"`
		Archived bool   `json:"archived"`
	}
	if e = json.NewDecoder(r.Body).Decode(&in); e != nil || strings.TrimSpace(in.Name) == "" {
		bad(w, "invalid_tag", "Tag name is required.", 400)
		return
	}
	if in.Color == "" {
		in.Color = "#58a6ff"
	}
	in.Name = strings.TrimSpace(in.Name)
	if e = s.store.UpdateTag(r.Context(), id, in.Name, in.Color, in.Archived); e != nil {
		bad(w, "update_tag", e.Error(), 500)
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
func (s *Server) addTradeTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	id, e := s.tradeID(r)
	if e != nil {
		bad(w, "invalid_trade", "Invalid trade ID.", 400)
		return
	}
	var in struct {
		TagID int64  `json:"tag_id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if e = json.NewDecoder(r.Body).Decode(&in); e != nil || (in.TagID < 1 && strings.TrimSpace(in.Name) == "") {
		bad(w, "invalid_tag", "A tag_id or name is required.", 400)
		return
	}
	if in.TagID > 0 {
		e = s.store.AddTradeTag(r.Context(), id, in.TagID)
	} else {
		if in.Color == "" {
			in.Color = "#58a6ff"
		}
		_, e = s.store.EnsureTradeTag(r.Context(), id, strings.TrimSpace(in.Name), in.Color)
	}
	if e != nil {
		bad(w, "assign_tag", e.Error(), 500)
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
func (s *Server) removeTradeTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/trades/"), "/")
	if len(parts) != 3 {
		bad(w, "invalid_tag", "Invalid trade/tag path.", 400)
		return
	}
	id, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil {
		bad(w, "invalid_trade", "Invalid trade ID.", 400)
		return
	}
	tagID, e := strconv.ParseInt(parts[2], 10, 64)
	if e != nil {
		bad(w, "invalid_tag", "Invalid tag ID.", 400)
		return
	}
	if e = s.store.RemoveTradeTag(r.Context(), id, tagID); e != nil {
		bad(w, "remove_tag", e.Error(), 500)
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	ts, e := s.filteredTrades(r)
	if e != nil {
		bad(w, "invalid_filter", e.Error(), 400)
		return
	}
	rr := make([]positions.RoundTrip, len(ts))
	for i := range ts {
		t := ts[len(ts)-1-i] // storage lists newest first; analytics paths are chronological.
		rr[i] = positions.RoundTrip{Entry: time.UnixMicro(t.EntryAt), Exit: time.UnixMicro(t.ExitAt), Entered: t.Entered, Exited: t.Exited, Net: t.Net, Gross: t.Gross, Commissions: t.Commissions, Fees: t.Fees}
	}
	summary := analytics.Calculate(rr, s.cfg.Import.ScratchTolerance, s.cfg.Analytics.KellyMinimumSample)
	s.enrichSummary(r.Context(), &summary, ts)
	end := r.URL.Query().Get("end")
	if end == "" {
		end = "9999-12-31"
	}
	var brokerYTD, brokerFees int64
	if err := s.store.DB.QueryRowContext(r.Context(), `SELECT ytd,fees_ytd,statement_date
		FROM broker_pnl_snapshots WHERE statement_date<=? ORDER BY statement_date DESC LIMIT 1`,
		end).Scan(&brokerYTD, &brokerFees, &summary.BrokerYTDDate); err == nil {
		ytd, fees := float64(brokerYTD)/float64(positions.Scale), float64(brokerFees)/float64(positions.Scale)
		summary.BrokerYTD, summary.BrokerFeesYTD = &ytd, &fees
	}
	jsonOut(w, summary)
}

func (s *Server) enrichSummary(ctx context.Context, summary *analytics.Summary, trades []storage.Trade) {
	loc, _ := time.LoadLocation(s.cfg.App.Timezone)
	var winningHolds, losingHolds, scratchHolds, mfes, maes, captures []float64
	days := map[string]int64{}
	dailyVolume := map[string]int64{}
	for _, trade := range trades {
		net := float64(trade.Net) / float64(positions.Scale)
		hold := float64(trade.ExitAt-trade.EntryAt) / float64(time.Minute.Microseconds())
		if net > s.cfg.Import.ScratchTolerance {
			winningHolds = append(winningHolds, hold)
		} else if net < -s.cfg.Import.ScratchTolerance {
			losingHolds = append(losingHolds, hold)
		} else {
			scratchHolds = append(scratchHolds, hold)
		}
		day := time.UnixMicro(trade.ExitAt).In(loc).Format("2006-01-02")
		days[day] += trade.Net
		dailyVolume[day] += trade.Entered
		x, err := s.store.Excursion(ctx, trade.ID)
		if err != nil || x.Completeness == "" {
			continue
		}
		mfe, mae := float64(x.MFE)/float64(positions.Scale), float64(x.MAE)/float64(positions.Scale)
		mfes, maes = append(mfes, mfe), append(maes, mae)
		if x.MFE > 0 {
			captures = append(captures, float64(trade.Net)/float64(x.MFE))
		}
	}
	summary.AverageWinningHoldMinutes = averagePtr(winningHolds)
	summary.AverageLosingHoldMinutes = averagePtr(losingHolds)
	summary.AverageScratchHoldMinutes = averagePtr(scratchHolds)
	if len(days) > 0 {
		summary.AverageDaily = summary.Net / float64(len(days))
		var volume int64
		for _, value := range dailyVolume {
			volume += value
		}
		summary.AverageDailyVolume = float64(volume) / float64(len(days))
	}
	summary.AverageMFE, summary.MedianMFE = averagePtr(mfes), medianPtr(mfes)
	summary.AverageMAE, summary.MedianMAE = averagePtr(maes), medianPtr(maes)
	summary.AverageMFECaptureRatio = averagePtr(captures)
	var orderedDays []string
	for day := range days {
		orderedDays = append(orderedDays, day)
	}
	slices.Sort(orderedDays)
	greenRun, redRun := 0, 0
	for _, day := range orderedDays {
		net := float64(days[day]) / float64(positions.Scale)
		switch {
		case net > s.cfg.Import.ScratchTolerance:
			greenRun++
			redRun = 0
		case net < -s.cfg.Import.ScratchTolerance:
			redRun++
			greenRun = 0
		default:
			greenRun, redRun = 0, 0
		}
		summary.MaxGreenDayStreak = max(summary.MaxGreenDayStreak, greenRun)
		summary.MaxRedDayStreak = max(summary.MaxRedDayStreak, redRun)
	}
	summary.CurrentGreenDayStreak, summary.CurrentRedDayStreak = greenRun, redRun
}

func averagePtr(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	var total float64
	for _, value := range values {
		total += value
	}
	average := total / float64(len(values))
	return &average
}
func medianPtr(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	copyValues := slices.Clone(values)
	slices.Sort(copyValues)
	mid := len(copyValues) / 2
	value := copyValues[mid]
	if len(copyValues)%2 == 0 {
		value = (copyValues[mid-1] + copyValues[mid]) / 2
	}
	return &value
}
func (s *Server) equity(w http.ResponseWriter, r *http.Request) {
	ts, e := s.filteredTrades(r)
	if e != nil {
		bad(w, "invalid_filter", e.Error(), 400)
		return
	}
	var x []map[string]any
	var eq, highWater int64
	if len(ts) > 0 {
		// Anchor closed-trade equity at the first entry so the first realized
		// result is visible as a move from zero instead of an isolated endpoint.
		x = append(x, map[string]any{"time": ts[len(ts)-1].EntryAt / 1_000_000, "value": float64(0)})
	}
	for i := len(ts) - 1; i >= 0; i-- {
		eq += ts[i].Net
		if eq > highWater {
			highWater = eq
		}
		value := float64(eq) / float64(positions.Scale)
		if r.URL.Query().Get("series") == "drawdown" {
			value = -float64(highWater-eq) / float64(positions.Scale)
		}
		x = append(x, map[string]any{"time": ts[i].ExitAt / 1_000_000, "value": value})
	}
	jsonOut(w, x)
}

type riskPoint struct {
	Time       int64   `json:"time"`
	Expectancy float64 `json:"expectancy"`
	Volatility float64 `json:"volatility"`
}

type riskAnalytics struct {
	AverageDrawdown       float64     `json:"average_drawdown"`
	BiggestDrawdown       float64     `json:"biggest_drawdown"`
	AverageDrawdownDays   float64     `json:"average_drawdown_days"`
	AverageDrawdownTrades float64     `json:"average_drawdown_trades"`
	CurrentDrawdown       float64     `json:"current_drawdown"`
	CurrentDrawdownDays   float64     `json:"current_drawdown_days"`
	CurrentDrawdownTrades int         `json:"current_drawdown_trades"`
	Episodes              int         `json:"episodes"`
	Window                int         `json:"window"`
	Rolling               []riskPoint `json:"rolling"`
}

func (s *Server) riskAnalytics(w http.ResponseWriter, r *http.Request) {
	ts, err := s.filteredTrades(r)
	if err != nil {
		bad(w, "invalid_filter", err.Error(), http.StatusBadRequest)
		return
	}
	const window = 20
	out := riskAnalytics{Window: window, Rolling: []riskPoint{}}
	var equity, highWater int64
	var episodeStart time.Time
	var episodeDepth int64
	var episodeTrades int
	var depthTotal int64
	var daysTotal float64
	var tradesTotal int
	values := make([]float64, 0, len(ts))
	closeEpisode := func(at time.Time) {
		if episodeTrades == 0 {
			return
		}
		out.Episodes++
		depthTotal += episodeDepth
		daysTotal += at.Sub(episodeStart).Hours() / 24
		tradesTotal += episodeTrades
		episodeStart, episodeDepth, episodeTrades = time.Time{}, 0, 0
	}
	for i := len(ts) - 1; i >= 0; i-- {
		trade := ts[i]
		net := float64(trade.Net) / float64(positions.Scale)
		values = append(values, net)
		equity += trade.Net
		at := time.UnixMicro(trade.ExitAt)
		if equity >= highWater {
			closeEpisode(at)
			highWater = equity
		} else {
			if episodeTrades == 0 {
				episodeStart = at
			}
			episodeTrades++
			depth := highWater - equity
			if depth > episodeDepth {
				episodeDepth = depth
			}
			if float64(depth)/float64(positions.Scale) > out.BiggestDrawdown {
				out.BiggestDrawdown = float64(depth) / float64(positions.Scale)
			}
		}
		if len(values) >= window {
			sample := values[len(values)-window:]
			var total float64
			for _, value := range sample {
				total += value
			}
			mean := total / window
			var squares float64
			for _, value := range sample {
				delta := value - mean
				squares += delta * delta
			}
			out.Rolling = append(out.Rolling, riskPoint{Time: trade.ExitAt / 1_000_000, Expectancy: mean, Volatility: math.Sqrt(squares / float64(window-1))})
		}
	}
	if episodeTrades > 0 {
		last := time.UnixMicro(ts[0].ExitAt)
		out.CurrentDrawdown = float64(highWater-equity) / float64(positions.Scale)
		out.CurrentDrawdownDays = last.Sub(episodeStart).Hours() / 24
		out.CurrentDrawdownTrades = episodeTrades
		closeEpisode(last)
	}
	if out.Episodes > 0 {
		out.AverageDrawdown = float64(depthTotal) / float64(positions.Scale) / float64(out.Episodes)
		out.AverageDrawdownDays = daysTotal / float64(out.Episodes)
		out.AverageDrawdownTrades = float64(tradesTotal) / float64(out.Episodes)
	}
	jsonOut(w, out)
}

type breakdown struct {
	Name    string            `json:"name"`
	Summary analytics.Summary `json:"summary"`
}

// breakdowns keeps pattern recognition on the same date-filtered population
// as the dashboard summary. Tags deliberately count a multi-tagged trade in
// each selected tag's own cohort rather than attempting to allocate its P&L.
func (s *Server) breakdowns(w http.ResponseWriter, r *http.Request) {
	ts, e := s.filteredTrades(r)
	if e != nil {
		bad(w, "invalid_filter", e.Error(), 400)
		return
	}
	groups := map[string]map[string][]storage.Trade{
		"tag": {}, "symbol": {}, "direction": {}, "weekday": {}, "entry_time": {}, "holding_time": {},
	}
	loc, _ := time.LoadLocation(s.cfg.App.Timezone)
	for _, t := range ts {
		add := func(kind, name string) { groups[kind][name] = append(groups[kind][name], t) }
		add("symbol", t.Symbol)
		add("direction", t.Direction)
		entry := time.UnixMicro(t.EntryAt).In(loc)
		add("weekday", entry.Weekday().String())
		add("entry_time", entry.Truncate(30*time.Minute).Format("15:04"))
		d := time.UnixMicro(t.ExitAt).Sub(time.UnixMicro(t.EntryAt))
		switch {
		case d < 5*time.Minute:
			add("holding_time", "under 5m")
		case d < 30*time.Minute:
			add("holding_time", "5–30m")
		case d < time.Hour:
			add("holding_time", "30–60m")
		default:
			add("holding_time", "60m+")
		}
		if len(t.Tags) == 0 {
			add("tag", "Untagged")
		}
		for _, tag := range t.Tags {
			add("tag", tag.Name)
		}
	}
	out := map[string][]breakdown{}
	for kind, buckets := range groups {
		for name, cohort := range buckets {
			rs := make([]positions.RoundTrip, len(cohort))
			for i, trade := range cohort {
				rs[i] = positions.RoundTrip{Entry: time.UnixMicro(trade.EntryAt), Exit: time.UnixMicro(trade.ExitAt), Entered: trade.Entered, Exited: trade.Exited, Net: trade.Net, Gross: trade.Gross, Commissions: trade.Commissions, Fees: trade.Fees}
			}
			summary := analytics.Calculate(rs, s.cfg.Import.ScratchTolerance, s.cfg.Analytics.KellyMinimumSample)
			s.enrichSummary(r.Context(), &summary, cohort)
			out[kind] = append(out[kind], breakdown{Name: name, Summary: summary})
		}
		// Stable API output makes comparisons and tests deterministic.
		slices.SortFunc(out[kind], func(a, b breakdown) int { return strings.Compare(a.Name, b.Name) })
	}
	jsonOut(w, out)
}
func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	ts, e := s.filteredTrades(r)
	if e != nil {
		bad(w, "invalid_filter", e.Error(), 400)
		return
	}
	out := map[string]map[string]any{}
	loc, _ := time.LoadLocation(s.cfg.App.Timezone)
	for _, t := range ts {
		d := time.UnixMicro(t.ExitAt).In(loc).Format("2006-01-02")
		if out[d] == nil {
			out[d] = map[string]any{"date": d, "net": int64(0), "trades": 0}
		}
		out[d]["net"] = out[d]["net"].(int64) + t.Net
		out[d]["trades"] = out[d]["trades"].(int) + 1
	}
	jsonOut(w, out)
}
func (s *Server) dayNote(w http.ResponseWriter, r *http.Request) {
	day := path.Base(r.URL.Path)
	if _, e := time.Parse("2006-01-02", day); e != nil {
		bad(w, "invalid_date", "Date uses YYYY-MM-DD.", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		note, e := s.store.DayNote(r.Context(), day)
		if e != nil {
			bad(w, "database", e.Error(), http.StatusInternalServerError)
			return
		}
		jsonOut(w, map[string]string{"date": day, "note": note})
	case http.MethodPatch:
		var in struct {
			Note string `json:"note"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if e := decoder.Decode(&in); e != nil {
			bad(w, "invalid_json", "Invalid JSON.", http.StatusBadRequest)
			return
		}
		if e := s.store.SetDayNote(r.Context(), day, in.Note); e != nil {
			bad(w, "update_failed", e.Error(), http.StatusInternalServerError)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	default:
		bad(w, "method_not_allowed", "Method not allowed.", http.StatusMethodNotAllowed)
	}
}
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		bad(w, "method_not_allowed", "Method not allowed.", 405)
		return
	}
	p, e := s.store.Backup(r.Context(), s.cfg.Storage.BackupsPath)
	if e != nil {
		bad(w, "backup_failed", e.Error(), 500)
		return
	}
	jsonOut(w, map[string]string{"path": p})
}

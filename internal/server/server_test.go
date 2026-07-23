package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"tale-of-the-tape/internal/config"
	"tale-of-the-tape/internal/positions"
	"tale-of-the-tape/internal/storage"
	"testing"
	"time"
)

func TestChartAndEnrichWithoutMassiveKey(t *testing.T) {
	dir := t.TempDir()
	st, e := storage.Open(dir+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	at := time.Now().UTC()
	_, _, e = st.Commit(context.Background(), "x", "x.csv", []positions.Execution{{Account: "a", Symbol: "ABC", Action: "buy", Quantity: 1, Price: 10 * positions.Scale, At: at, Row: 1}, {Account: "a", Symbol: "ABC", Action: "sell", Quantity: 1, Price: 11 * positions.Scale, At: at.Add(time.Minute), Row: 2}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	cfg := config.Defaults()
	s := New(cfg, st)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/trades/1/chart", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("chart status %d: %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/trades/1/enrich", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("enrich status %d", w.Code)
	}
}

func TestChartUsesCachedBars(t *testing.T) {
	dir := t.TempDir()
	st, e := storage.Open(dir+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	at := time.Now().UTC().Truncate(time.Minute)
	_, _, e = st.Commit(context.Background(), "y", "y.csv", []positions.Execution{{Account: "a", Symbol: "ABC", Action: "buy", Quantity: 1, Price: 10 * positions.Scale, At: at, Row: 1}, {Account: "a", Symbol: "ABC", Action: "sell", Quantity: 1, Price: 11 * positions.Scale, At: at.Add(time.Minute), Row: 2}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = st.StoreBars(context.Background(), "ABC", "1m", []storage.Bar{{Time: at.UnixMilli(), Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100}}); e != nil {
		t.Fatal(e)
	}
	if e = st.SaveExcursion(context.Background(), 1, storage.Excursion{MFE: positions.Scale, MAE: -positions.Scale, MFEAt: at.UnixMicro(), MAEAt: at.Add(time.Minute).UnixMicro(), Source: "nbbo", Completeness: "complete"}); e != nil {
		t.Fatal(e)
	}
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/trades/1/chart", nil))
	if w.Code != 200 {
		t.Fatalf("chart status=%d %s", w.Code, w.Body.String())
	}
	var got struct {
		Bars       []storage.Bar         `json:"bars"`
		Executions []positions.Execution `json:"executions"`
		Excursion  storage.Excursion     `json:"excursion"`
	}
	if e = json.NewDecoder(w.Body).Decode(&got); e != nil {
		t.Fatal(e)
	}
	if len(got.Bars) != 1 || len(got.Executions) != 2 || got.Excursion.MFEAt != at.UnixMicro() || got.Excursion.MAEAt != at.Add(time.Minute).UnixMicro() {
		t.Fatalf("bars=%d executions=%d excursion=%#v", len(got.Bars), len(got.Executions), got.Excursion)
	}
}

func TestRangeEnrichmentUsesCachedBars(t *testing.T) {
	dir := t.TempDir()
	st, e := storage.Open(dir+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	at := time.Now().UTC().Truncate(time.Minute)
	_, _, e = st.Commit(context.Background(), "z", "z.csv", []positions.Execution{{Account: "a", Symbol: "ABC", Action: "buy", Quantity: 1, Price: 10 * positions.Scale, At: at, Row: 1}, {Account: "a", Symbol: "ABC", Action: "sell", Quantity: 1, Price: 11 * positions.Scale, At: at.Add(time.Minute), Row: 2}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = st.StoreBars(context.Background(), "ABC", "1m", []storage.Bar{{Time: at.UnixMilli(), Open: 10, High: 12, Low: 9, Close: 11, Volume: 100}}); e != nil {
		t.Fatal(e)
	}
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/enrichment/range", nil))
	if w.Code != 200 {
		t.Fatalf("range status=%d %s", w.Code, w.Body.String())
	}
	if _, e := st.Excursion(context.Background(), 1); e != nil {
		t.Fatalf("excursion not saved: %v", e)
	}
}

func TestBreakdownsShareTheDateFilteredPopulation(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(dir+"/t.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	rows := []positions.Execution{
		{Account: "a", Symbol: "ABC", Action: "buy", Quantity: 1, Price: 10 * positions.Scale, At: at, Row: 1},
		{Account: "a", Symbol: "ABC", Action: "sell", Quantity: 1, Price: 11 * positions.Scale, At: at.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "XYZ", Action: "sell", Quantity: 1, Price: 10 * positions.Scale, At: at.AddDate(0, 0, 2), Row: 3},
		{Account: "a", Symbol: "XYZ", Action: "buy", Quantity: 1, Price: 9 * positions.Scale, At: at.AddDate(0, 0, 2).Add(time.Minute), Row: 4},
	}
	if _, _, err = st.Commit(context.Background(), "breakdowns", "b.csv", rows, nil); err != nil {
		t.Fatal(err)
	}
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/breakdowns?start=2026-01-02&end=2026-01-02", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var got map[string][]struct {
		Name    string `json:"name"`
		Summary struct {
			Total int `json:"total_trades"`
		} `json:"summary"`
	}
	if err = json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got["symbol"]) != 1 || got["symbol"][0].Name != "ABC" || got["symbol"][0].Summary.Total != 1 {
		t.Fatalf("unexpected symbol breakdown: %#v", got["symbol"])
	}
}

func TestDatesRejectEndBeforeStart(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?start=2026-01-03&end=2026-01-02", nil)
	if _, _, err := New(config.Defaults(), nil).dates(r); err == nil {
		t.Fatal("expected invalid date range")
	}
}

func TestDatesUseConfiguredTradingTimezone(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?start=2026-01-02&end=2026-01-02", nil)
	start, end, err := New(config.Defaults(), nil).dates(r)
	if err != nil {
		t.Fatal(err)
	}
	if start.Format(time.RFC3339) != "2026-01-02T00:00:00-05:00" || end.Format(time.RFC3339) != "2026-01-03T00:00:00-05:00" {
		t.Fatalf("range=%s-%s", start, end)
	}
}

func TestBrowserSecurityPolicySupportsEmbeddedUIHandlersAndFavicon(t *testing.T) {
	st, err := storage.Open(t.TempDir()+"/t.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := New(config.Defaults(), st).Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if policy := w.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("inline UI handlers blocked by policy: %q", policy)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("favicon status=%d", w.Code)
	}
}

func TestImportDetailReturnsStoredRejectedRows(t *testing.T) {
	st, err := storage.Open(t.TempDir()+"/t.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	if _, _, err = st.Commit(context.Background(), "import-detail", "statement.csv", []positions.Execution{{Account: "a", Symbol: "ABC", Action: "buy", Quantity: 1, Price: positions.Scale, At: at, Row: 1}, {Account: "a", Symbol: "ABC", Action: "sell", Quantity: 1, Price: 2 * positions.Scale, At: at.Add(time.Minute), Row: 2}}, []string{"unsupported option"}); err != nil {
		t.Fatal(err)
	}
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/imports/1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var got storage.ImportBatch
	if err = json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Filename != "statement.csv" || len(got.Rejected) != 1 || got.Rejected[0].Reason != "Rejected during parsing" {
		t.Fatalf("import detail=%#v", got)
	}
}

func TestChartFullSessionUsesConfiguredNewYorkHours(t *testing.T) {
	cfg := config.Defaults()
	s := New(cfg, nil)
	entry := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	start, end := s.chartInterval(storage.Trade{EntryAt: entry.UnixMicro(), ExitAt: entry.Add(time.Minute).UnixMicro()}, true)
	if start.Format(time.RFC3339) != "2026-01-02T04:00:00-05:00" || end.Format(time.RFC3339) != "2026-01-02T20:00:00-05:00" {
		t.Fatalf("full session=%s-%s", start, end)
	}
}

func TestDayNoteRoundTrip(t *testing.T) {
	st, err := storage.Open(t.TempDir()+"/t.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/v1/day-notes/2026-01-02", strings.NewReader(`{"note":"Wait for confirmation."}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("patch=%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/day-notes/2026-01-02", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Wait for confirmation.") {
		t.Fatalf("get=%d %s", w.Code, w.Body.String())
	}
}

func TestBulkTradeTags(t *testing.T) {
	st, err := storage.Open(t.TempDir()+"/t.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	rows := []positions.Execution{
		{Account: "a", Symbol: "A", Action: "buy", Quantity: 1, Price: positions.Scale, At: at, Row: 1}, {Account: "a", Symbol: "A", Action: "sell", Quantity: 1, Price: 2 * positions.Scale, At: at.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "B", Action: "buy", Quantity: 1, Price: positions.Scale, At: at.Add(2 * time.Minute), Row: 3}, {Account: "a", Symbol: "B", Action: "sell", Quantity: 1, Price: 2 * positions.Scale, At: at.Add(3 * time.Minute), Row: 4},
	}
	if _, _, err = st.Commit(context.Background(), "bulk", "b.csv", rows, nil); err != nil {
		t.Fatal(err)
	}
	tag, err := st.CreateTag(context.Background(), "breakout", "#58a6ff")
	if err != nil {
		t.Fatal(err)
	}
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/trades/bulk-tags", strings.NewReader(`{"trade_ids":[1,2],"tag_ids":[1],"mode":"add"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("bulk=%d %s", w.Code, w.Body.String())
	}
	got, err := st.Trades(context.Background(), time.Time{}, time.Time{})
	if err != nil || len(got) != 2 || got[0].Tags[0].ID != tag.ID || got[1].Tags[0].ID != tag.ID {
		t.Fatalf("tags=%#v err=%v", got, err)
	}
}

func TestSettingsPatchPersistsNonSecretValues(t *testing.T) {
	st, err := storage.Open(t.TempDir()+"/t.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(config.Defaults(), st)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"timezone":"America/Chicago","scratch_tolerance":0.02,"default_timeframe":"5m"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("patch=%d %s", w.Code, w.Body.String())
	}
	reloaded := New(config.Defaults(), st)
	if reloaded.cfg.App.Timezone != "America/Chicago" || reloaded.cfg.Import.ScratchTolerance != 0.02 || reloaded.cfg.Chart.DefaultTimeframe != "5m" {
		t.Fatalf("stored settings=%#v", reloaded.cfg)
	}
}

func TestAverageEntryLineTracksScaleInAndEndsFlat(t *testing.T) {
	z := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	execs := []positions.Execution{
		{Action: "buy", Quantity: 100, Price: 10 * positions.Scale, At: z, Row: 1},
		{Action: "buy", Quantity: 100, Price: 12 * positions.Scale, At: z.Add(time.Minute), Row: 2},
		{Action: "sell", Quantity: 200, Price: 13 * positions.Scale, At: z.Add(3 * time.Minute), Row: 3},
	}
	bars := []storage.Bar{{Time: z.UnixMilli()}, {Time: z.Add(time.Minute).UnixMilli()}, {Time: z.Add(2 * time.Minute).UnixMilli()}, {Time: z.Add(3 * time.Minute).UnixMilli()}}
	line := averageEntryLine(execs, bars)
	if len(line) != 3 || line[0].Value != 10 || line[1].Value != 11 || line[2].Value != 11 {
		t.Fatalf("basis line=%#v", line)
	}
}

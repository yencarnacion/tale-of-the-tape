package storage

import (
	"context"
	"os"
	"tale-of-the-tape/internal/positions"
	"testing"
	"time"
)

func TestTradesHonorsEndWithoutStart(t *testing.T) {
	s, e := Open(t.TempDir()+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	xs := []positions.Execution{{Account: "a", Symbol: "A", Action: "buy", Quantity: 1, Price: 1, At: a, Row: 1}, {Account: "a", Symbol: "A", Action: "sell", Quantity: 1, Price: 2, At: a.Add(time.Minute), Row: 2}, {Account: "a", Symbol: "B", Action: "buy", Quantity: 1, Price: 1, At: a.AddDate(0, 0, 2), Row: 3}, {Account: "a", Symbol: "B", Action: "sell", Quantity: 1, Price: 2, At: a.AddDate(0, 0, 2).Add(time.Minute), Row: 4}}
	if _, _, e = s.Commit(context.Background(), "x", "x", xs, nil); e != nil {
		t.Fatal(e)
	}
	got, e := s.Trades(context.Background(), time.Time{}, a.AddDate(0, 0, 1))
	if e != nil || len(got) != 1 {
		t.Fatalf("got=%d err=%v", len(got), e)
	}
}

func TestDailyLossTradesFlagsCrossSymbolOverlap(t *testing.T) {
	s, err := Open(t.TempDir()+"/daily-loss.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	xs := []positions.Execution{
		{Account: "a", Symbol: "A", Action: "buy", Quantity: 1, Price: positions.Scale, At: at, Row: 1},
		{Account: "a", Symbol: "B", Action: "buy", Quantity: 1, Price: positions.Scale, At: at.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "B", Action: "sell", Quantity: 1, Price: 2 * positions.Scale, At: at.Add(2 * time.Minute), Row: 3},
		{Account: "a", Symbol: "A", Action: "sell", Quantity: 1, Price: 2 * positions.Scale, At: at.Add(3 * time.Minute), Row: 4},
	}
	if _, _, err = s.Commit(context.Background(), "overlap", "overlap.csv", xs, nil); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 2; id++ {
		if err = s.SaveExcursion(context.Background(), id, Excursion{MAE: -1, MAEAt: at.Add(time.Minute).UnixMicro(), Source: "nbbo", Completeness: "complete", Events: 1}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DailyLossTrades(context.Background(), time.UTC)
	if err != nil || len(got) != 2 || !got[0].Overlaps || !got[1].Overlaps {
		t.Fatalf("trades=%#v err=%v", got, err)
	}
}

func TestCommitRetainsDistinctSameSecondFills(t *testing.T) {
	s, e := Open(t.TempDir()+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	at := time.Date(2026, 7, 31, 13, 50, 21, 0, time.UTC)
	rows := []positions.Execution{
		{Account: "a", Symbol: "IREN", Action: "sell", Quantity: 200, Price: 39 * positions.Scale, At: at.Add(-time.Minute), Row: 1},
		{Account: "a", Symbol: "IREN", Action: "buy", Quantity: 100, Price: 37_360_000, At: at, Row: 2, Occurrence: 1},
		{Account: "a", Symbol: "IREN", Action: "buy", Quantity: 100, Price: 37_360_000, At: at, Row: 3, Occurrence: 2},
	}
	_, added, e := s.Commit(context.Background(), "same-second-fills", "statement.csv", rows, nil)
	if e != nil || added != len(rows) {
		t.Fatalf("added=%d err=%v; distinct statement rows must not be collapsed", added, e)
	}
	var count int
	if e = s.DB.QueryRow("SELECT count(*) FROM executions").Scan(&count); e != nil || count != len(rows) {
		t.Fatalf("executions=%d err=%v", count, e)
	}
	_, added, e = s.Commit(context.Background(), "same-second-fills-overlap", "overlap.csv", rows, nil)
	if e != nil || added != 0 {
		t.Fatalf("overlapping statement added=%d err=%v", added, e)
	}
}

func TestBarCoverageRequiresTheWholeRequestedInterval(t *testing.T) {
	s, e := Open(t.TempDir()+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	start := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	if e = s.StoreBarsCoverage(context.Background(), "ABC", "1m", start, end, nil); e != nil {
		t.Fatal(e)
	}
	for _, want := range []struct {
		start, end time.Time
		covered    bool
	}{
		{start, end, true},
		{start.Add(5 * time.Minute), end.Add(-5 * time.Minute), true},
		{start.Add(-time.Minute), end, false},
		{start, end.Add(time.Minute), false},
	} {
		got, err := s.HasBarCoverage(context.Background(), "ABC", "1m", want.start, want.end)
		if err != nil || got != want.covered {
			t.Fatalf("coverage %s-%s: got=%v err=%v want=%v", want.start, want.end, got, err, want.covered)
		}
	}
}

func TestTradeReturnsLogicalReversalLegWithAllocatedCosts(t *testing.T) {
	s, e := Open(t.TempDir()+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	rows := []positions.Execution{
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 60, Price: 10 * positions.Scale, At: at, Row: 1},
		{Account: "a", Symbol: "X", Action: "sell", Quantity: 100, Price: 9 * positions.Scale, Commission: 10 * positions.Scale, Fees: 3 * positions.Scale, At: at.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 40, Price: 8 * positions.Scale, At: at.Add(2 * time.Minute), Row: 3},
	}
	if _, _, e = s.Commit(context.Background(), "reversal", "r.csv", rows, nil); e != nil {
		t.Fatal(e)
	}
	_, first, e := s.Trade(context.Background(), 1)
	if e != nil {
		t.Fatal(e)
	}
	_, second, e := s.Trade(context.Background(), 2)
	if e != nil {
		t.Fatal(e)
	}
	if first[1].Quantity != 60 || first[1].Commission != 6*positions.Scale || first[1].Fees != 1_800_000 {
		t.Fatalf("first reversal leg=%#v", first[1])
	}
	if second[0].Quantity != 40 || second[0].Commission != 4*positions.Scale || second[0].Fees != 1_200_000 {
		t.Fatalf("second reversal leg=%#v", second[0])
	}
}

func TestRepairLogicalLegsPreservesRoundTripIdentity(t *testing.T) {
	s, e := Open(t.TempDir()+"/t.db", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	rows := []positions.Execution{
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 60, Price: 10 * positions.Scale, At: at, Row: 1},
		{Account: "a", Symbol: "X", Action: "sell", Quantity: 100, Price: 9 * positions.Scale, Commission: 10 * positions.Scale, At: at.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 40, Price: 8 * positions.Scale, At: at.Add(2 * time.Minute), Row: 3},
	}
	if _, _, e = s.Commit(context.Background(), "repair", "r.csv", rows, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE round_trip_executions SET quantity=NULL,commission=NULL,fees=NULL"); e != nil {
		t.Fatal(e)
	}
	if e = s.repairLogicalLegs(context.Background()); e != nil {
		t.Fatal(e)
	}
	trade, xs, e := s.Trade(context.Background(), 1)
	if e != nil || trade.ID != 1 || xs[1].Quantity != 60 || xs[1].Commission != 6*positions.Scale {
		t.Fatalf("repair did not preserve first trade: trade=%#v xs=%#v err=%v", trade, xs, e)
	}
}

func TestReconcileImportedDatabaseOptIn(t *testing.T) {
	path := os.Getenv("TOTT_RECONCILE_DB")
	if path == "" {
		t.Skip("set TOTT_RECONCILE_DB to a disposable SQLite copy")
	}
	s, err := Open(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = s.rebuildTx(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	var net int64
	if err = s.DB.QueryRow("SELECT count(*),COALESCE(sum(net),0) FROM round_trips").Scan(&count, &net); err != nil {
		t.Fatal(err)
	}
	t.Logf("round_trips=%d net=%.2f", count, float64(net)/float64(positions.Scale))
}

func TestEmptyTagCollectionsAreJSONArrays(t *testing.T) {
	s, err := Open(t.TempDir()+"/tags.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tags, err := s.Tags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tags == nil {
		t.Fatal("empty tag collection must be an array, not null")
	}
}

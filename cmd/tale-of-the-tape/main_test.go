package main

import (
	"context"
	"testing"
	"time"

	"tale-of-the-tape/internal/storage"
)

func TestDemoLoadsTaggedTradesAndCompleteLocalChartCoverage(t *testing.T) {
	store, err := storage.Open(t.TempDir()+"/demo.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = loadDemo(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	trades, err := store.Trades(context.Background(), time.Time{}, time.Time{})
	if err != nil || len(trades) != 3 {
		t.Fatalf("trades=%d err=%v", len(trades), err)
	}
	if len(trades[0].Tags)+len(trades[1].Tags)+len(trades[2].Tags) != 2 {
		t.Fatalf("tags=%#v", trades)
	}
	base := time.Date(2026, 7, 22, 13, 30, 0, 0, time.UTC)
	for _, symbol := range []string{"AAPL", "MSFT", "NVDA"} {
		covered, err := store.HasBarCoverage(context.Background(), symbol, "1m", base.Add(-30*time.Minute), base.Add(100*time.Minute))
		if err != nil || !covered {
			t.Fatalf("symbol=%s covered=%v err=%v", symbol, covered, err)
		}
	}
}

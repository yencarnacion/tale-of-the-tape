package massive

import (
	"context"
	"os"
	"runtime/debug"
	"testing"
	"time"
)

func TestLiveBarsOptIn(t *testing.T) {
	if os.Getenv("MASSIVE_LIVE_TEST") != "1" {
		t.Skip("set MASSIVE_LIVE_TEST=1 to run")
	}
	key := os.Getenv("MASSIVE_API_KEY")
	if key == "" {
		t.Fatal("missing key")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Massive client panic: %v\n%s", r, debug.Stack())
		}
	}()
	bars, e := Bars(context.Background(), key, "IBIT", "1m", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	if len(bars) == 0 {
		t.Fatal("no bars")
	}
	quotes, e := Quotes(context.Background(), key, "IBIT", time.Date(2026, 1, 2, 13, 51, 0, 0, time.UTC), time.Date(2026, 1, 2, 13, 52, 0, 0, time.UTC))
	if e != nil {
		t.Logf("quotes unavailable: %v", e)
	} else if len(quotes) > 0 && quotes[0].Bid > quotes[0].Ask {
		t.Fatal("crossed quote accepted")
	}
	prints, e := Trades(context.Background(), key, "IBIT", time.Date(2026, 1, 2, 13, 51, 0, 0, time.UTC), time.Date(2026, 1, 2, 13, 52, 0, 0, time.UTC))
	if e != nil {
		t.Logf("trade prints unavailable: %v", e)
	} else if len(prints) > 0 && prints[0].Price <= 0 {
		t.Fatal("nonpositive trade print accepted")
	}
}

package indicators

import (
	"testing"
	"time"
)

func TestVWAPResetsAtSessionOpen(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	at := func(h, m int) int64 { return time.Date(2026, 1, 2, h, m, 0, 0, loc).UnixMilli() }
	s := Calculate([]Bar{{Time: at(9, 29), High: 10, Low: 10, Close: 10, Volume: 1}, {Time: at(9, 30), High: 20, Low: 20, Close: 20, Volume: 1}, {Time: at(9, 31), High: 22, Low: 22, Close: 22, Volume: 1}})
	if s.VWAP[1].Value != 20 || s.VWAP[2].Value != 21 {
		t.Fatalf("vwap %#v", s.VWAP)
	}
}

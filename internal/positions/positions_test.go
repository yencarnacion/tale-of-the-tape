package positions

import (
	"testing"
	"time"
)

func TestBuildScaleOutAndReversal(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	xs := []Execution{{Account: "a", Symbol: "X", Action: "buy", Quantity: 100, Price: 10 * Scale, At: at, Row: 1}, {Account: "a", Symbol: "X", Action: "sell", Quantity: 40, Price: 11 * Scale, At: at.Add(time.Minute), Row: 2}, {Account: "a", Symbol: "X", Action: "sell", Quantity: 100, Price: 9 * Scale, At: at.Add(2 * time.Minute), Row: 3}, {Account: "a", Symbol: "X", Action: "buy", Quantity: 60, Price: 8 * Scale, At: at.Add(3 * time.Minute), Row: 4}}
	got := Build(xs)
	if len(got) != 2 {
		t.Fatalf("round trips=%d", len(got))
	}
	if got[0].Direction != "long" || got[0].Gross != -20*Scale {
		t.Fatalf("first %#v", got[0])
	}
	if got[1].Direction != "short" || got[1].Gross != 40*Scale {
		t.Fatalf("second %#v", got[1])
	}
}

func TestReversalAllocatesCostsToLogicalLegsExactly(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	xs := []Execution{
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 60, Price: 10 * Scale, At: at, Row: 1},
		{Account: "a", Symbol: "X", Action: "sell", Quantity: 100, Price: 9 * Scale, Commission: 10 * Scale, Fees: 3 * Scale, At: at.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 40, Price: 8 * Scale, At: at.Add(2 * time.Minute), Row: 3},
	}
	got := Build(xs)
	if len(got) != 2 {
		t.Fatalf("round trips=%d", len(got))
	}
	if got[0].Commissions != 6*Scale || got[0].Fees != 1_800_000 || got[1].Commissions != 4*Scale || got[1].Fees != 1_200_000 {
		t.Fatalf("costs not allocated exactly: first=%#v second=%#v", got[0], got[1])
	}
	if leg := got[0].Executions[1]; leg.Quantity != 60 || leg.Commission != 6*Scale || leg.Fees != 1_800_000 {
		t.Fatalf("closing leg=%#v", leg)
	}
	if leg := got[1].Executions[0]; leg.Quantity != 40 || leg.Commission != 4*Scale || leg.Fees != 1_200_000 {
		t.Fatalf("opening leg=%#v", leg)
	}
}

func TestBuildSessionsDoesNotCarryResidualInventoryIntoNextDay(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	dayOne := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	dayTwo := dayOne.AddDate(0, 0, 1)
	execs := []Execution{
		// An intentionally incomplete day leaves residual inventory.
		{Account: "a", Symbol: "IREN", Action: "sell", Quantity: 1500, Price: 40 * Scale, At: dayOne, Row: 1},
		// The next day's day trade is independently balanced.
		{Account: "a", Symbol: "IREN", Action: "buy", Quantity: 806, Price: 41 * Scale, At: dayTwo, Row: 2},
		{Account: "a", Symbol: "IREN", Action: "sell", Quantity: 806, Price: 42 * Scale, At: dayTwo.Add(time.Minute), Row: 3},
	}
	got := BuildSessions(execs, loc)
	if len(got) != 1 || got[0].Entry.Day() != dayTwo.In(loc).Day() || got[0].Net != 806*Scale {
		t.Fatalf("sessions=%#v", got)
	}
}

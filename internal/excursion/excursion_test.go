package excursion

import (
	"tale-of-the-tape/internal/positions"
	"testing"
	"time"
)

func TestScaleOutLongPath(t *testing.T) {
	z := time.Unix(0, 0)
	x := []positions.Execution{{Action: "buy", Quantity: 100, Price: 10 * positions.Scale, At: z}, {Action: "sell", Quantity: 50, Price: 12 * positions.Scale, At: z.Add(2 * time.Minute)}, {Action: "sell", Quantity: 50, Price: 11 * positions.Scale, At: z.Add(3 * time.Minute)}}
	m := []Event{{At: z.Add(time.Minute), Price: 13 * positions.Scale, Source: "aggregates"}, {At: z.Add(2*time.Minute + time.Second), Price: 11 * positions.Scale, Source: "aggregates"}}
	r := Calculate(x, m)
	if r.MFE != 300*positions.Scale || r.MAE != 150*positions.Scale || r.Final != 150*positions.Scale {
		t.Fatalf("%#v", r)
	}
}

func TestFinalExecutionParticipatesInExcursionExtrema(t *testing.T) {
	z := time.Unix(0, 0)
	execs := []positions.Execution{
		{Action: "buy", Quantity: 100, Price: 10 * positions.Scale, At: z},
		{Action: "sell", Quantity: 100, Price: 15 * positions.Scale, At: z.Add(2 * time.Minute)},
	}
	result := Calculate(execs, []Event{{At: z.Add(time.Minute), Price: 11 * positions.Scale, Source: "nbbo"}})
	if result.Final != 500*positions.Scale || result.MFE != result.Final || result.MAE != 100*positions.Scale || !result.MFEAt.Equal(execs[1].At) || !result.MAEAt.Equal(z.Add(time.Minute)) {
		t.Fatalf("final fill was not included: %#v", result)
	}
}

func TestShortPathIncludesFeesAndSameTimestampExecutionsFirst(t *testing.T) {
	z := time.Unix(0, 0)
	execs := []positions.Execution{
		{Action: "sell", Quantity: 100, Price: 10 * positions.Scale, Commission: positions.Scale, At: z, Row: 1},
		{Action: "buy", Quantity: 100, Price: 11 * positions.Scale, Fees: 2 * positions.Scale, At: z.Add(2 * time.Minute), Row: 2},
	}
	marks := []Event{
		{At: z.Add(time.Minute), Price: 9 * positions.Scale, Source: "nbbo"},
		// At the exit timestamp the execution is processed before this mark.
		{At: z.Add(2 * time.Minute), Price: 12 * positions.Scale, Source: "nbbo"},
	}
	result := Calculate(execs, marks)
	if result.MFE != 99*positions.Scale || result.MAE != -103*positions.Scale || result.Final != -103*positions.Scale {
		t.Fatalf("short path=%#v", result)
	}
}

func TestReversalLegReconcilesToItsOwnCanonicalNet(t *testing.T) {
	z := time.Unix(0, 0)
	trades := positions.Build([]positions.Execution{
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 60, Price: 10 * positions.Scale, At: z, Row: 1},
		{Account: "a", Symbol: "X", Action: "sell", Quantity: 100, Price: 9 * positions.Scale, Commission: 10 * positions.Scale, Fees: 3 * positions.Scale, At: z.Add(time.Minute), Row: 2},
		{Account: "a", Symbol: "X", Action: "buy", Quantity: 40, Price: 8 * positions.Scale, At: z.Add(2 * time.Minute), Row: 3},
	})
	for i, trade := range trades {
		result := Calculate(trade.Executions, []Event{{At: trade.Entry.Add(30 * time.Second), Price: trade.EntryPrice, Source: "nbbo"}})
		if result.Final != trade.Net {
			t.Fatalf("trade %d final=%d canonical=%d", i, result.Final, trade.Net)
		}
	}
}

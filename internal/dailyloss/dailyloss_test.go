package dailyloss

import "testing"

const dollar = int64(1_000_000)

func TestCalculateStopsOnIntratradeLossAndSkipsLaterTrades(t *testing.T) {
	r := Calculate([]Trade{
		{Date: "2026-01-02", At: 1, Net: 500 * dollar, MAE: -100 * dollar, HasMAE: true},
		{Date: "2026-01-02", At: 2, Net: 2_000 * dollar, MAE: -3_600 * dollar, HasMAE: true},
		{Date: "2026-01-02", At: 3, Net: 1_000 * dollar, MAE: -50 * dollar, HasMAE: true},
	}, 3_000*dollar)
	if r.Actual != 3_500*dollar || r.WithStop != -3_000*dollar || !r.Days[0].Stopped || r.Days[0].Skipped != 1 {
		t.Fatalf("report=%#v", r)
	}
}

func TestCalculateExcludesUnpricedDayFromHypotheticalTotals(t *testing.T) {
	r := Calculate([]Trade{{Date: "2026-01-02", At: 1, Net: -4_000 * dollar}}, 3_000*dollar)
	if r.CompleteDays != 0 || r.Actual != 0 || r.Days[0].CompleteMarketData {
		t.Fatalf("report=%#v", r)
	}
}

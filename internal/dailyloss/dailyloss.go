// Package dailyloss models a daily stop from intratrade mark-to-market loss.
package dailyloss

import "sort"

// Trade is a completed same-day position. MAE is net of costs and is measured
// from that trade's entry; a nil MAE means that intratrade market data is not
// available, so the stop cannot be evaluated for that day.
type Trade struct {
	Date     string
	At       int64
	Net, MAE int64
	HasMAE   bool
}

type Day struct {
	Date                        string
	Actual, WithStop            int64
	Trades, Skipped             int
	Stopped, CompleteMarketData bool
}

type Report struct {
	Limit            int64
	Days             []Day
	Actual, WithStop int64
	CompleteDays     int
}

// Calculate applies a stop when the realized P&L before the current trade,
// plus that trade's MAE, reaches -limit. At that point the model liquidates at
// the limit and excludes every later same-day trade. Incomplete days remain in
// the per-day output but are excluded from hypothetical totals.
func Calculate(trades []Trade, limit int64) Report {
	if limit <= 0 {
		return Report{Limit: limit}
	}
	sort.SliceStable(trades, func(i, j int) bool {
		if trades[i].Date == trades[j].Date {
			return trades[i].At < trades[j].At
		}
		return trades[i].Date < trades[j].Date
	})
	r := Report{Limit: limit}
	for i := 0; i < len(trades); {
		j := i
		d := Day{Date: trades[i].Date, CompleteMarketData: true}
		for j < len(trades) && trades[j].Date == d.Date {
			d.Trades++
			d.Actual += trades[j].Net
			if !trades[j].HasMAE {
				d.CompleteMarketData = false
			}
			j++
		}
		if d.CompleteMarketData {
			for k := i; k < j; k++ {
				t := trades[k]
				if d.Stopped {
					d.Skipped++
					continue
				}
				if d.WithStop+t.MAE <= -limit {
					d.WithStop = -limit
					d.Stopped = true
					continue
				}
				d.WithStop += t.Net
			}
			r.Actual += d.Actual
			r.WithStop += d.WithStop
			r.CompleteDays++
		}
		r.Days = append(r.Days, d)
		i = j
	}
	return r
}

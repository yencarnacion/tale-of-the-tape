// Package dailyloss models a daily stop from intratrade mark-to-market loss.
package dailyloss

import "sort"

// Trade is a completed same-day position. MAE is net of costs and is measured
// from that trade's entry; a nil MAE means that intratrade market data is not
// available, so the stop cannot be evaluated for that day.
type Trade struct {
	Date          string
	EntryAt       int64
	ExitAt, MAEAt int64
	Net, MAE      int64
	HasMAE        bool
	Overlaps      bool
}

type Day struct {
	Date                        string
	Actual, WithStop            int64
	Trades, Skipped             int
	Stopped, CompleteMarketData bool
	OverlappingTrades           bool
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
			return trades[i].EntryAt < trades[j].EntryAt
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
			if trades[j].Overlaps {
				d.CompleteMarketData = false
				d.OverlappingTrades = true
			}
			j++
		}
		if d.CompleteMarketData {
			type event struct {
				at     int64
				trade  int
				isExit bool
			}
			events := make([]event, 0, (j-i)*2)
			for k := i; k < j; k++ {
				events = append(events, event{at: trades[k].MAEAt, trade: k}, event{at: trades[k].ExitAt, trade: k, isExit: true})
			}
			sort.SliceStable(events, func(a, b int) bool {
				if events[a].at == events[b].at {
					return events[a].isExit && !events[b].isExit
				}
				return events[a].at < events[b].at
			})
			realized := int64(0)
			exited := make([]bool, len(trades))
			for _, e := range events {
				if e.isExit {
					realized += trades[e.trade].Net
					exited[e.trade] = true
					continue
				}
				base := realized
				if exited[e.trade] {
					base -= trades[e.trade].Net
				}
				if base+trades[e.trade].MAE <= -limit {
					d.WithStop = -limit
					d.Stopped = true
					for k := i; k < j; k++ {
						if trades[k].EntryAt > e.at {
							d.Skipped++
						}
					}
					break
				}
			}
			if !d.Stopped {
				d.WithStop = d.Actual
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

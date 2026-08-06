// Canonical signed-position and weighted-average P&L engine.
package positions

import (
	"fmt"
	"sort"
	"time"
)

const Scale int64 = 1_000_000 // prices and money are integer millionths of USD.
type Execution struct {
	ID         int64     `json:"id"`
	Account    string    `json:"-"`
	Symbol     string    `json:"symbol"`
	Action     string    `json:"action"`
	Quantity   int64     `json:"quantity"`
	Price      int64     `json:"price"`
	Commission int64     `json:"commission"`
	Fees       int64     `json:"fees"`
	At         time.Time `json:"at"`
	Row        int       `json:"source_row"`
	Occurrence int       `json:"-"`
}
type RoundTrip struct {
	Account, Symbol, Direction    string
	Entry, Exit                   time.Time
	EntryPrice, ExitPrice         int64
	MaxQuantity, Entered, Exited  int64
	Gross, Commissions, Fees, Net int64
	Executions                    []Execution
}

// Build deterministically splits a reversing fill into a closing and opening leg.
func Build(in []Execution) []RoundTrip {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	return BuildSessions(in, loc)
}

// BuildContinuous reconstructs positions across the full execution history.
// Unlike BuildSessions, it does not reset inventory at a new trading day. It
// is useful for reports that must exclude positions held overnight.
func BuildContinuous(in []Execution) []RoundTrip {
	by := map[string][]Execution{}
	for _, e := range in {
		key := e.Account + "\x00" + e.Symbol
		by[key] = append(by[key], e)
	}
	var out []RoundTrip
	for _, xs := range by {
		sort.SliceStable(xs, func(i, j int) bool {
			if xs[i].At.Equal(xs[j].At) {
				return xs[i].Row < xs[j].Row
			}
			return xs[i].At.Before(xs[j].At)
		})
		out = append(out, buildOne(xs)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Exit.Before(out[j].Exit) })
	return out
}

// BuildSessions reconstructs intraday flat-to-flat trades relative to each
// trading day's opening inventory. This prevents overnight/core-position
// residue from swallowing otherwise complete day trades on later dates.
func BuildSessions(in []Execution, loc *time.Location) []RoundTrip {
	by := map[string][]Execution{}
	for _, e := range in {
		session := e.At.In(loc).Format("2006-01-02")
		key := e.Account + "\x00" + e.Symbol + "\x00" + session
		by[key] = append(by[key], e)
	}
	var out []RoundTrip
	for _, xs := range by {
		sort.SliceStable(xs, func(i, j int) bool {
			if xs[i].At.Equal(xs[j].At) {
				return xs[i].Row < xs[j].Row
			}
			return xs[i].At.Before(xs[j].At)
		})
		out = append(out, buildOne(xs)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Exit.Before(out[j].Exit) })
	return out
}
func signed(e Execution) int64 {
	if e.Action == "buy" {
		return e.Quantity
	}
	return -e.Quantity
}
func buildOne(xs []Execution) []RoundTrip {
	var r []RoundTrip
	var cur *RoundTrip
	var pos, basis int64
	start := func(e Execution) {
		d := "long"
		if signed(e) < 0 {
			d = "short"
		}
		cur = &RoundTrip{Account: e.Account, Symbol: e.Symbol, Direction: d, Entry: e.At}
		pos = 0
		basis = 0
	}
	appendLeg := func(e Execution, qty, commission, fees int64) {
		ee := e
		ee.Quantity = qty
		ee.Commission = commission
		ee.Fees = fees
		cur.Executions = append(cur.Executions, ee)
		cur.Commissions += commission
		cur.Fees += fees
	}
	for _, e := range xs {
		remaining := abs(signed(e))
		commissionRemaining, feesRemaining := e.Commission, e.Fees
		sign := int64(1)
		if signed(e) < 0 {
			sign = -1
		}
		for remaining > 0 {
			if cur == nil {
				start(e)
			}
			if pos == 0 || sameSign(pos, sign) {
				q := remaining
				old := abs(pos)
				basis = (basis*old + e.Price*q) / (old + q)
				pos += sign * q
				cur.Entered += q
				if abs(pos) > cur.MaxQuantity {
					cur.MaxQuantity = abs(pos)
				}
				appendLeg(e, q, commissionRemaining, feesRemaining)
				commissionRemaining, feesRemaining = 0, 0
				remaining = 0
				continue
			}
			q := min(remaining, abs(pos)) // close at price
			commission := commissionRemaining * q / remaining
			fees := feesRemaining * q / remaining
			commissionRemaining -= commission
			feesRemaining -= fees
			if pos > 0 {
				cur.Gross += (e.Price - basis) * q
			} else {
				cur.Gross += (basis - e.Price) * q
			}
			cur.Exited += q
			pos += sign * q
			appendLeg(e, q, commission, fees)
			remaining -= q
			if pos == 0 {
				cur.Exit = e.At
				cur.EntryPrice = weightedEntries(cur.Executions, cur.Direction)
				cur.ExitPrice = weightedExits(cur.Executions, cur.Direction)
				cur.Net = cur.Gross - cur.Commissions - cur.Fees
				r = append(r, *cur)
				cur = nil
				basis = 0
			}
		}
	}
	return r
}
func weightedEntries(xs []Execution, direction string) int64 {
	var n, d int64
	for _, e := range xs {
		entry := (direction == "long" && e.Action == "buy") || (direction == "short" && e.Action == "sell")
		if entry {
			n += e.Price * e.Quantity
			d += e.Quantity
		}
	}
	if d == 0 {
		return 0
	}
	return n / d
}
func weightedExits(xs []Execution, direction string) int64 {
	var n, d int64
	for _, e := range xs {
		exit := (direction == "long" && e.Action == "sell") || (direction == "short" && e.Action == "buy")
		if exit {
			n += e.Price * e.Quantity
			d += e.Quantity
		}
	}
	if d == 0 {
		return 0
	}
	return n / d
}
func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func sameSign(a, b int64) bool { return (a < 0) == (b < 0) }
func Money(v int64) string     { return fmt.Sprintf("%.2f", float64(v)/float64(Scale)) }

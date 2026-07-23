// Package excursion calculates MFE/MAE from the complete marked trade-equity path.
package excursion

import (
	"sort"
	"tale-of-the-tape/internal/positions"
	"time"
)

type Event struct {
	At     time.Time
	Price  int64
	Source string
}
type Result struct {
	MFE, MAE, Final               int64
	MFEAt, MAEAt                  time.Time
	Events                        int
	Source, Completeness, Warning string
}

// Calculate uses executions-before-marks when timestamps match. This is the documented policy for second-precision broker timestamps.
func Calculate(execs []positions.Execution, marks []Event) Result {
	if len(execs) == 0 {
		return Result{Completeness: "unavailable", Warning: "trade has no executions"}
	}
	sort.SliceStable(execs, func(i, j int) bool {
		if execs[i].At.Equal(execs[j].At) {
			return execs[i].Row < execs[j].Row
		}
		return execs[i].At.Before(execs[j].At)
	})
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].At.Before(marks[j].At) })
	var pos, basis, realized, costs int64
	lo, hi := int64(0), int64(0)
	seen := false
	ei := 0
	r := Result{}
	apply := func(e positions.Execution) {
		q := e.Quantity
		side := int64(1)
		if e.Action == "sell" {
			side = -1
		}
		costs += e.Commission + e.Fees
		for q > 0 {
			if pos == 0 || (pos > 0) == (side > 0) {
				old := abs(pos)
				basis = (basis*old + e.Price*q) / (old + q)
				pos += side * q
				q = 0
				continue
			}
			take := min(q, abs(pos))
			if pos > 0 {
				realized += (e.Price - basis) * take
			} else {
				realized += (basis - e.Price) * take
			}
			pos += side * take
			q -= take
			if pos == 0 {
				basis = 0
			}
		}
	}
	for _, m := range marks {
		for ei < len(execs) && !execs[ei].At.After(m.At) {
			apply(execs[ei])
			ei++
		}
		if m.Price <= 0 {
			continue
		}
		v := realized - costs
		if pos > 0 {
			v += (m.Price - basis) * pos
		} else if pos < 0 {
			v += (basis - m.Price) * (-pos)
		}
		if !seen || v > hi {
			hi = v
			r.MFEAt = m.At
		}
		if !seen || v < lo {
			lo = v
			r.MAEAt = m.At
		}
		seen = true
	}
	for ei < len(execs) {
		apply(execs[ei])
		ei++
	}
	r.MFE, r.MAE, r.Final, r.Events, r.Completeness = hi, lo, realized-costs, len(marks), "complete"
	// A final exit is itself part of the realized trade-equity path. There may
	// be no provider event after it, so include the reconciled canonical value
	// explicitly rather than letting a winning/losing final fill disappear from
	// the excursion extrema.
	if seen {
		if r.Final > r.MFE {
			r.MFE = r.Final
			r.MFEAt = execs[len(execs)-1].At
		}
		if r.Final < r.MAE {
			r.MAE = r.Final
			r.MAEAt = execs[len(execs)-1].At
		}
	}
	if !seen {
		r.Completeness = "unavailable"
		r.Warning = "no usable market marks"
	}
	if len(marks) > 0 {
		r.Source = marks[0].Source
	}
	return r
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

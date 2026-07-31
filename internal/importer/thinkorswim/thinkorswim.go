// Package thinkorswim parses the cash-statement transaction section exported by Thinkorswim.
package thinkorswim

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tale-of-the-tape/internal/positions"
)

type Rejected struct {
	Row    int      `json:"row"`
	Reason string   `json:"reason"`
	Raw    []string `json:"raw"`
}
type Result struct {
	Account    string                `json:"account"`
	Executions []positions.Execution `json:"-"`
	Accepted   int                   `json:"accepted"`
	Skipped    int                   `json:"skipped"`
	Rejected   []Rejected            `json:"rejected"`
	Symbols    []string              `json:"symbols"`
	Warnings   []string              `json:"warnings"`
	Start, End time.Time
}

var accountRE = regexp.MustCompile(`(?i)Account Statement for\s+([^\s(]+)`)
var tradeRE = regexp.MustCompile(`(?i)^\s*(BOT|SOLD)\s+([+-]?[\d,]+)\s+(.+?)\s+@\s*\$?([\d.]+)\s*$`)
var optionRE = regexp.MustCompile(`(?i)\b(PUT|CALL|VERTICAL|CONDOR|STRADDLE|FUTURE)\b`)

func Parse(data []byte, loc *time.Location) Result {
	out := Result{}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if m := accountRE.FindStringSubmatch(text); len(m) > 1 {
		out.Account = m[1]
	}
	r := csv.NewReader(bytes.NewReader([]byte(text)))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true // Thinkorswim uses Excel-style ="reference" cells.
	header := map[string]int{}
	row := 0
	seen := map[string]bool{}
	occurrences := map[string]int{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		row++
		if err != nil {
			out.Rejected = append(out.Rejected, Rejected{row, "Malformed CSV row: " + err.Error(), nil})
			continue
		}
		if len(rec) == 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(rec[0]), "DATE") {
			header = map[string]int{}
			for i, v := range rec {
				header[strings.ToUpper(strings.TrimSpace(v))] = i
			}
			continue
		}
		if _, ok := header["TYPE"]; !ok {
			continue
		}
		get := func(k string) string {
			if i, ok := header[k]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if get("TYPE") != "TRD" {
			out.Skipped++
			continue
		}
		desc := strings.Trim(get("DESCRIPTION"), "\"")
		m := tradeRE.FindStringSubmatch(desc)
		if len(m) == 0 {
			out.Rejected = append(out.Rejected, Rejected{row, "Unsupported or malformed trade description", rec})
			continue
		}
		symbol := strings.TrimSpace(m[3])
		if optionRE.MatchString(symbol) || strings.Contains(symbol, " ") {
			out.Rejected = append(out.Rejected, Rejected{row, "Unsupported instrument (only U.S. equities/ETFs are supported)", rec})
			continue
		}
		q, err := strconv.ParseInt(strings.ReplaceAll(m[2], ",", ""), 10, 64)
		if err != nil || q == 0 {
			out.Rejected = append(out.Rejected, Rejected{row, "Invalid quantity", rec})
			continue
		}
		price, err := scaled(m[4])
		if err != nil {
			out.Rejected = append(out.Rejected, Rejected{row, "Invalid execution price", rec})
			continue
		}
		stamp, err := parseTime(get("DATE"), get("TIME"), loc)
		if err != nil {
			out.Rejected = append(out.Rejected, Rejected{row, "Invalid DATE/TIME: " + err.Error(), rec})
			continue
		}
		action := "buy"
		if strings.EqualFold(m[1], "SOLD") {
			action = "sell"
		}
		comm, _ := scaled(get("COMMISSIONS & FEES"))
		fees, _ := scaled(get("MISC FEES")) // TOS displays charges as negative amounts; canonical storage keeps costs positive.
		if comm < 0 {
			comm = -comm
		}
		if fees < 0 {
			fees = -fees
		}
		e := positions.Execution{Account: out.Account, Symbol: strings.ToUpper(symbol), Action: action, Quantity: abs(q), Price: price, Commission: comm, Fees: fees, At: stamp.UTC(), Row: row}
		identity := fmt.Sprintf("%s|%s|%d|%d|%d", e.Symbol, e.Action, e.Quantity, e.Price, e.At.UnixMicro())
		occurrences[identity]++
		e.Occurrence = occurrences[identity]
		out.Executions = append(out.Executions, e)
		out.Accepted++
		if !seen[e.Symbol] {
			seen[e.Symbol] = true
			out.Symbols = append(out.Symbols, e.Symbol)
		}
		if out.Start.IsZero() || e.At.Before(out.Start) {
			out.Start = e.At
		}
		if out.End.IsZero() || e.At.After(out.End) {
			out.End = e.At
		}
	}
	if out.Accepted == 0 {
		out.Warnings = append(out.Warnings, "No supported Thinkorswim TRD equity rows were found.")
	}
	return out
}
func parseTime(date, clock string, loc *time.Location) (time.Time, error) {
	date = strings.TrimSpace(date)
	clock = strings.TrimSpace(clock)
	for _, f := range []string{"1/2/06 15:04:05", "01/02/2006 15:04:05"} {
		if t, e := time.ParseInLocation(f, date+" "+clock, loc); e == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expected M/D/YY HH:MM:SS")
}
func scaled(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "--" {
		return 0, nil
	}
	neg := strings.Contains(v, "(")
	v = strings.NewReplacer("$", "", ",", "", "(", "", ")", "").Replace(v)
	if strings.HasPrefix(v, "-") {
		neg = true
		v = strings.TrimPrefix(v, "-")
	}
	whole, frac, ok := strings.Cut(v, ".")
	if !ok {
		frac = ""
	}
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	frac += strings.Repeat("0", 6-len(frac))
	w, e := strconv.ParseInt(whole, 10, 64)
	if e != nil {
		return 0, e
	}
	f, e := strconv.ParseInt(frac, 10, 64)
	if e != nil {
		return 0, e
	}
	n := w*positions.Scale + f
	if neg {
		return -n, nil
	}
	return n, nil
}
func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

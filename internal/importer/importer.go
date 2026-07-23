package importer

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tale-of-the-tape/internal/importer/thinkorswim"
	"tale-of-the-tape/internal/positions"
)

type Preview struct {
	SHA256     string                 `json:"sha256"`
	Files      int                    `json:"files"`
	Account    string                 `json:"account"`
	Accepted   int                    `json:"accepted_rows"`
	Skipped    int                    `json:"skipped_rows"`
	Executions []positions.Execution  `json:"-"`
	Rejected   []thinkorswim.Rejected `json:"rejected_rows"`
	Symbols    []string               `json:"symbols"`
	Warnings   []string               `json:"warnings"`
	Start, End time.Time              `json:"-"`
	BrokerPnL  []BrokerPnL            `json:"-"`
}

type BrokerPnL struct {
	StatementDate string `json:"statement_date"`
	Day           int64  `json:"day"`
	YTD           int64  `json:"ytd"`
	FeesYTD       int64  `json:"fees_ytd"`
}

func ParseFile(path string, loc *time.Location) (Preview, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Preview{}, err
	}
	return ParseBytes(filepath.Base(path), b, loc)
}
func ParseBytes(name string, b []byte, loc *time.Location) (Preview, error) {
	p := Preview{}
	p.SHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
	files := map[string][]byte{}
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			return p, fmt.Errorf("read zip: %w", e)
		}
		for _, f := range z.File {
			if f.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
				continue
			}
			r, e := f.Open()
			if e != nil {
				return p, e
			}
			d, e := io.ReadAll(io.LimitReader(r, 30<<20))
			r.Close()
			if e != nil {
				return p, e
			}
			files[f.Name] = d
		}
	} else {
		files[name] = b
	}
	if len(files) == 0 {
		return p, fmt.Errorf("no CSV files found")
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	syms := map[string]bool{}
	for _, k := range keys {
		r := thinkorswim.Parse(files[k], loc)
		if broker, ok := parseBrokerPnL(k, files[k]); ok {
			p.BrokerPnL = append(p.BrokerPnL, broker)
		}
		p.Files++
		p.Accepted += r.Accepted
		p.Skipped += r.Skipped
		p.Executions = append(p.Executions, r.Executions...)
		p.Rejected = append(p.Rejected, r.Rejected...)
		p.Warnings = append(p.Warnings, r.Warnings...)
		if p.Account == "" {
			p.Account = r.Account
		}
		for _, s := range r.Symbols {
			syms[s] = true
		}
		if p.Start.IsZero() || (!r.Start.IsZero() && r.Start.Before(p.Start)) {
			p.Start = r.Start
		}
		if r.End.After(p.End) {
			p.End = r.End
		}
	}
	for s := range syms {
		p.Symbols = append(p.Symbols, s)
	}
	sort.Strings(p.Symbols)
	return p, nil
}

func parseBrokerPnL(name string, data []byte) (BrokerPnL, bool) {
	var out BrokerPnL
	for _, token := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if len(token) >= 10 {
			if _, err := time.Parse("2006-01-02", token[:10]); err == nil {
				out.StatementDate = token[:10]
			}
		}
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord, r.LazyQuotes = -1, true
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		if len(row) > 5 && strings.EqualFold(strings.TrimSpace(row[1]), "OVERALL TOTALS") {
			out.Day, _ = brokerMoney(row[4])
			out.YTD, _ = brokerMoney(row[5])
		}
		if len(row) > 1 && strings.EqualFold(strings.TrimSpace(row[0]), "Total Commissions & Fees YTD") {
			out.FeesYTD, _ = brokerMoney(row[1])
		}
	}
	return out, out.StatementDate != "" && out.YTD != 0
}

func brokerMoney(value string) (int64, error) {
	value = strings.TrimSpace(value)
	negative := strings.Contains(value, "(")
	value = strings.NewReplacer("$", "", ",", "", "(", "", ")", "").Replace(value)
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	if negative {
		f = -f
	}
	return int64(math.Round(f * float64(positions.Scale))), nil
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tale-of-the-tape/internal/config"
	"tale-of-the-tape/internal/dailyloss"
	"tale-of-the-tape/internal/importer"
	"tale-of-the-tape/internal/positions"
	"tale-of-the-tape/internal/server"
	"tale-of-the-tape/internal/storage"
)

func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	_ = config.LoadDotEnv(".env")
	mode := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	cfgPath := fs.String("config", "config.yaml", "config path")
	addr := fs.String("addr", "", "loopback HTTP address")
	dbPath := fs.String("db", "", "SQLite database path")
	file := fs.String("file", "", "Thinkorswim CSV or ZIP")
	date := fs.String("date", "", "date YYYY-MM-DD")
	start := fs.String("start", "", "start YYYY-MM-DD")
	end := fs.String("end", "", "end YYYY-MM-DD")
	dailyLoss := fs.Float64("max-daily-loss", 3000, "daily loss limit in dollars")
	minLoss := fs.Float64("min-loss", 100, "lowest limit tested in dollars")
	maxLoss := fs.Float64("max-loss", 0, "highest limit tested in dollars; 0 selects the data maximum")
	stepLoss := fs.Float64("loss-step", 100, "limit increment tested in dollars")
	if e := fs.Parse(args); e != nil {
		return e
	}
	cfg, e := config.Load(*cfgPath)
	if e != nil {
		return e
	}
	if *addr != "" {
		cfg.App.Addr = *addr
	}
	if *dbPath != "" {
		cfg.Storage.Path = *dbPath
	}
	if mode == "demo" {
		cfg.Storage.Path = "data/tale-of-the-tape-demo.db"
	}
	if e = cfg.Validate(); e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, e := storage.Open(cfg.Storage.Path, cfg.Storage.BusyTimeout)
	if e != nil {
		return fmt.Errorf("open SQLite: %w", e)
	}
	defer st.Close()
	switch mode {
	case "serve", "demo":
		if mode == "demo" {
			if e = loadDemo(ctx, st); e != nil {
				return e
			}
		}
		fmt.Printf("%s: http://%s\n", cfg.App.Name, cfg.App.Addr)
		return server.New(cfg, st).Serve(ctx)
	case "import":
		if *file == "" {
			return fmt.Errorf("import -file is required")
		}
		loc, _ := time.LoadLocation(cfg.Import.AssumedTimezone)
		p, e := importer.ParseFile(*file, loc)
		if e != nil {
			return e
		}
		rej := make([]string, len(p.Rejected))
		for i, v := range p.Rejected {
			rej[i] = v.Reason
		}
		id, n, e := st.Commit(ctx, p.SHA256, *file, p.Executions, rej)
		if e != nil {
			return e
		}
		fmt.Printf("import batch %d: %d accepted rows, %d new executions, %d rejected rows\n", id, p.Accepted, n, len(p.Rejected))
		return nil
	case "backup":
		p, e := st.Backup(ctx, cfg.Storage.BackupsPath)
		if e != nil {
			return e
		}
		fmt.Println(p)
		return nil
	case "enrich":
		if *date == "" && (*start == "" || *end == "") {
			return fmt.Errorf("enrich requires -date or -start and -end")
		}
		var from, to time.Time
		if *date != "" {
			from, e = time.Parse("2006-01-02", *date)
			if e != nil {
				return fmt.Errorf("date: %w", e)
			}
			to = from.AddDate(0, 0, 1)
		} else {
			from, e = time.Parse("2006-01-02", *start)
			if e != nil {
				return fmt.Errorf("start: %w", e)
			}
			to, e = time.Parse("2006-01-02", *end)
			if e != nil {
				return fmt.Errorf("end: %w", e)
			}
			to = to.AddDate(0, 0, 1)
		}
		report, e := server.New(cfg, st).EnrichRange(ctx, from, to)
		if e != nil {
			return e
		}
		fmt.Printf("enrichment: %d/%d completed", report.Completed, report.Requested)
		if len(report.Failed) > 0 {
			fmt.Printf(", %d failed\n", len(report.Failed))
			for id, msg := range report.Failed {
				fmt.Printf("trade %d: %s\n", id, msg)
			}
			return fmt.Errorf("enrichment incomplete")
		}
		fmt.Println()
		return nil
	case "verify":
		return verify(st)
	case "daily-loss-report":
		loc, _ := time.LoadLocation(cfg.Import.AssumedTimezone)
		return dailyLossReport(ctx, st, loc, *dailyLoss, *minLoss, *maxLoss, *stepLoss, *start, *end)
	default:
		return fmt.Errorf("unknown command %q; use serve, demo, import, enrich, backup, verify, or daily-loss-report", mode)
	}
}
func verify(st *storage.Store) error {
	var result string
	if e := st.DB.QueryRow("PRAGMA integrity_check").Scan(&result); e != nil || result != "ok" {
		return fmt.Errorf("database integrity check failed")
	}
	fmt.Println("database integrity check: ok")
	return nil
}

func dailyLossReport(ctx context.Context, st *storage.Store, loc *time.Location, requested, minimum, maximum, step float64, start, end string) error {
	if requested <= 0 || minimum <= 0 || step <= 0 || maximum < 0 {
		return fmt.Errorf("daily-loss-report limits must be positive (and max-loss cannot be negative)")
	}
	trades, err := st.DailyLossTrades(ctx, loc)
	if err != nil {
		return err
	}
	filtered := make([]dailyloss.Trade, 0, len(trades))
	for _, t := range trades {
		if (start != "" && t.Date < start) || (end != "" && t.Date > end) {
			continue
		}
		filtered = append(filtered, dailyloss.Trade{Date: t.Date, EntryAt: t.EntryAt, ExitAt: t.ExitAt, Net: t.Net, MAE: t.MAE, MAEAt: t.MAEAt, HasMAE: t.HasMAE, Overlaps: t.Overlaps})
	}
	limit := int64(requested * float64(positions.Scale))
	report := dailyloss.Calculate(filtered, limit)
	if len(report.Days) == 0 {
		return fmt.Errorf("no same-day flat-to-flat trades in the selected date range")
	}
	overlapDays, missingDays := 0, 0
	for _, d := range report.Days {
		if d.OverlappingTrades {
			overlapDays++
		} else if !d.CompleteMarketData {
			missingDays++
		}
	}
	fmt.Printf("daily-loss report: $%.2f limit; %d same-day trades; %d/%d days eligible (%d overlap, %d missing market data)\n", requested, len(filtered), report.CompleteDays, len(report.Days), overlapDays, missingDays)
	fmt.Println("date        actual       with-stop    stopped  skipped  market-data")
	for _, d := range report.Days {
		status := "complete"
		if d.OverlappingTrades {
			status = "overlap-needs-portfolio-path"
		} else if !d.CompleteMarketData {
			status = "missing"
		}
		fmt.Printf("%-10s %12.2f %12s %8t %8d  %s\n", d.Date, float64(d.Actual)/float64(positions.Scale), moneyOrNA(d.WithStop, d.CompleteMarketData), d.Stopped, d.Skipped, status)
	}
	fmt.Printf("eligible totals: actual $%.2f; with $%.2f daily loss $%.2f; change $%.2f\n", float64(report.Actual)/float64(positions.Scale), requested, float64(report.WithStop)/float64(positions.Scale), float64(report.WithStop-report.Actual)/float64(positions.Scale))
	if report.CompleteDays == 0 {
		return fmt.Errorf("no complete market-data days; run enrich before interpreting a daily-loss simulation")
	}
	maxObserved := maximum
	if maxObserved == 0 {
		for _, t := range filtered {
			if t.HasMAE && float64(-t.MAE)/float64(positions.Scale) > maxObserved {
				maxObserved = float64(-t.MAE) / float64(positions.Scale)
			}
		}
	}
	bestLimit, best := int64(0), report
	for candidate := minimum; candidate <= maxObserved+0.000001; candidate += step {
		r := dailyloss.Calculate(filtered, int64(candidate*float64(positions.Scale)))
		if r.CompleteDays == report.CompleteDays && (bestLimit == 0 || r.WithStop > best.WithStop) {
			bestLimit, best = int64(candidate*float64(positions.Scale)), r
		}
	}
	if bestLimit > 0 {
		fmt.Printf("current recommendation: $%.2f max daily loss; hypothetical P&L $%.2f (tested $%.2f through $%.2f in $%.2f steps; recalculates as enriched days are added)\n", float64(bestLimit)/float64(positions.Scale), float64(best.WithStop)/float64(positions.Scale), minimum, maxObserved, step)
		if bestLimit == int64(minimum*float64(positions.Scale)) || bestLimit == int64(maxObserved*float64(positions.Scale)) {
			fmt.Println("warning: the best result is on a search boundary; treat it as an in-sample bound, not a stable optimum")
		}
	}
	return nil
}

func moneyOrNA(value int64, available bool) string {
	if !available {
		return "          N/A"
	}
	return fmt.Sprintf("%12.2f", float64(value)/float64(positions.Scale))
}
func loadDemo(ctx context.Context, st *storage.Store) error {
	var n int
	if err := st.DB.QueryRowContext(ctx, "SELECT count(*) FROM executions").Scan(&n); err != nil || n > 0 {
		return err
	}
	base := time.Date(2026, 7, 22, 13, 30, 0, 0, time.UTC)
	q := int64(100)
	p := int64(10_000_000)
	xs := []positions.Execution{
		{Account: "demo", Symbol: "AAPL", Action: "buy", Quantity: q, Price: p, At: base, Row: 1},
		{Account: "demo", Symbol: "AAPL", Action: "sell", Quantity: 40, Price: 10_300_000, At: base.Add(5 * time.Minute), Row: 2},
		{Account: "demo", Symbol: "AAPL", Action: "sell", Quantity: 60, Price: 10_150_000, At: base.Add(10 * time.Minute), Row: 3},
		{Account: "demo", Symbol: "MSFT", Action: "sell", Quantity: q, Price: 20_000_000, At: base.Add(30 * time.Minute), Row: 4},
		{Account: "demo", Symbol: "MSFT", Action: "buy", Quantity: q, Price: 20_250_000, At: base.Add(40 * time.Minute), Row: 5},
		{Account: "demo", Symbol: "NVDA", Action: "buy", Quantity: q, Price: 30_000_000, At: base.Add(60 * time.Minute), Row: 6},
		{Account: "demo", Symbol: "NVDA", Action: "sell", Quantity: q, Price: 30_000_000, At: base.Add(70 * time.Minute), Row: 7},
	}
	if _, _, err := st.Commit(ctx, "demo-v1", "synthetic-demo", xs, nil); err != nil {
		return err
	}
	for symbol, firstPrice := range map[string]float64{"AAPL": 10, "MSFT": 20, "NVDA": 30} {
		bars := []storage.Bar{}
		for i := 0; i < 130; i++ {
			p := firstPrice + float64(i%12)*.03
			at := base.Add(-30*time.Minute + time.Duration(i)*time.Minute).UnixMilli()
			bars = append(bars, storage.Bar{Time: at, Open: p, High: p + .08, Low: p - .08, Close: p + .02, Volume: float64(10000 + i*100)})
		}
		if err := st.StoreBarsCoverage(ctx, symbol, "1m", base.Add(-30*time.Minute), base.Add(100*time.Minute), bars); err != nil {
			return err
		}
	}
	opening, err := st.CreateTag(ctx, "opening drive", "#58a6ff")
	if err != nil {
		return err
	}
	reversal, err := st.CreateTag(ctx, "reversal", "#f85149")
	if err != nil {
		return err
	}
	trades, err := st.Trades(ctx, time.Time{}, time.Time{})
	if err != nil {
		return err
	}
	for _, trade := range trades {
		if trade.Symbol == "AAPL" {
			err = st.AddTradeTag(ctx, trade.ID, opening.ID)
		} else if trade.Symbol == "MSFT" {
			err = st.AddTradeTag(ctx, trade.ID, reversal.ID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

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
	default:
		return fmt.Errorf("unknown command %q; use serve, demo, import, enrich, backup, or verify", mode)
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

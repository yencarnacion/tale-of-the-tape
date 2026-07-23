# Tale of the Tape

A private, local Go/SQLite day-trading review journal for U.S. equities. It
imports Thinkorswim transaction CSVs (or ZIP archives), reconstructs canonical
flat-to-flat trades, and serves an embedded browser UI on loopback only.

## Start

```bash
cp .env.example .env          # Massive is optional
./go.sh                       # http://127.0.0.1:3000
./go.sh import -file import/2026.zip
./go.sh enrich -date 2026-01-02
./go.sh enrich -start 2026-01-01 -end 2026-01-31
./go.sh backup
./go.sh verify
```

Use Thinkorswim's Account Statement export. The importer finds the cash
transaction section headed `DATE,TIME,TYPE,...` and accepts `TRD` stock/ETF
descriptions such as `BOT +500 IBIT @50.10` and `SOLD -500 IBIT @50.40`.
Options and non-trade rows are retained as rejected/skipped import diagnostics;
they are never interpreted as equities. ZIP archives of many statements are
accepted and deduplicated by file SHA-256 and stable execution fingerprints.

## Data and privacy

All imports, notes, tags, and analytics remain in the configured local SQLite
database. If configured, Massive receives only a requested symbol and history
interval; the API key remains in `.env` and is never included in browser JSON.
Use `./go.sh backup` for a consistent `VACUUM INTO` SQLite backup. To restore,
stop the app and replace the database file with the backup.

## Charts and MFE/MAE

Click a trade to request local cached one- or five-minute candlesticks. A cache
miss sends only that symbol and its configured historical interval to Massive;
the returned bars and provider-confirmed interval coverage are persisted
locally, so a partial cache is not mistaken for a complete chart. `Calculate MFE / MAE` walks the actual
execution sequence, including scale-ins, partial exits, reversals, commissions,
and fees, and marks the remaining open position at each market event.

When the subscription supplies historical quotes, the preferred path uses
Massive NBBO: bid marks for a long liquidation and ask marks for a short
liquidation. Those results are stored as `source: nbbo`. If quotes are absent
or unusable, it next uses historical trade prints (`source: trade_prints`) and
records that NBBO was unavailable. Aggregate bars are the final fallback and
record `source: aggregates`, `completeness: approximate`, and an explicit
intrabar-order warning. Same-timestamp policy is executions before market marks;
Thinkorswim's second-precision timestamps are called out in stored warnings.

P&L uses weighted-average basis and integer millionths of dollars. Net P&L is
gross realized P&L minus commissions and fees. A win/loss/scratch uses the
configured scratch tolerance. Profit factor is gross wins divided by absolute
gross losses; expectancy uses win probability and mean win/loss; Kelly excludes
scratches and is displayed for analysis only.

## Development

```bash
GOCACHE=/tmp/tale-of-the-tape-cache gofmt -w .
GOCACHE=/tmp/tale-of-the-tape-cache go vet ./...
GOCACHE=/tmp/tale-of-the-tape-cache go test ./...
GOCACHE=/tmp/tale-of-the-tape-cache go test -race ./...
```

See [docs/UPSTREAM.md](docs/UPSTREAM.md), [NOTICE](NOTICE), and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for attribution.

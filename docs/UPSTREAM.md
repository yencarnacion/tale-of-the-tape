# Upstream inspection

Inspected 2026-07-23.

- TradeTally: `507c5ca62b6d5ddfcbfe41e251f57f78d4740ac4` — reviewed its Apache
  license, README, Thinkorswim parser, analytics controller, MAE estimator,
  and analytics UI components. Tale of the Tape independently implements its
  Go parser and canonical P&L engine; it deliberately does not use the MAE
  heuristic.
- tape-reading-tool: `c62f85ccc7cf913a7b1d509ee91b38e4d36613fa` — reviewed
  `go.sh`, configuration, SQLite startup, server structure, Massive setup,
  and context shutdown approach.

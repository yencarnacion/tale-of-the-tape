package importer

import (
	"testing"

	"tale-of-the-tape/internal/positions"
)

func TestParseBrokerPnL(t *testing.T) {
	data := []byte("Profits and Losses\nSymbol,Description,P/L Open,P/L %,P/L Day,P/L YTD,P/L Diff,Margin Req\n,OVERALL TOTALS,$0.00,0.00%,\"$607.80\",\"($126,201.47)\",$81.20,$0.00\n\nAccount Summary\nTotal Commissions & Fees YTD,\"$1,122.99\"\n")
	got, ok := parseBrokerPnL("2026/202607/2026-07-23-AccountStatement.csv", data)
	if !ok {
		t.Fatal("expected broker snapshot")
	}
	if got.StatementDate != "2026-07-23" || got.Day != 60780*positions.Scale/100 || got.YTD != -12620147*positions.Scale/100 || got.FeesYTD != 112299*positions.Scale/100 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
}

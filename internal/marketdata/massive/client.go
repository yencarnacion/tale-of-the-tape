// Package massive owns the server-side official Massive client configuration.
package massive

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/massive-com/client-go/v3/rest"
	"github.com/massive-com/client-go/v3/rest/gen"
	"tale-of-the-tape/internal/indicators"
)

type Quote struct {
	At       time.Time
	Bid, Ask float64
}
type Trade struct {
	At    time.Time
	Price float64
}

// New returns the official Massive REST client. Callers must keep the key
// server-side and send only symbol/time-range requests.
func New(apiKey string) *rest.Client {
	return rest.NewWithOptions(apiKey, rest.WithTrace(false), rest.WithPagination(true))
}

// Bars fetches official Massive aggregate bars; callers send only symbol and date range.
func Bars(ctx context.Context, key, symbol, timeframe string, start, end time.Time) ([]indicators.Bar, error) {
	if key == "" {
		return nil, fmt.Errorf("Massive API key not configured")
	}
	multiplier := 1
	if timeframe == "5m" {
		multiplier = 5
	}
	limit := 50000
	// The generated Massive client currently requires Sort to be non-nil even
	// though the API documents it as optional.
	response, err := New(key).GetStocksAggregates(ctx, symbol, multiplier, gen.Minute, start.Format("2006-01-02"), end.Format("2006-01-02"), &gen.GetStocksAggregatesParams{Limit: &limit, Sort: "asc"})
	if err != nil {
		return nil, err
	}
	if err = rest.CheckResponse(response); err != nil {
		return nil, err
	}
	parsed, err := gen.ParseGetStocksAggregatesResponse(response)
	if err != nil {
		return nil, err
	}
	if parsed.JSON200 == nil || parsed.JSON200.Results == nil {
		return []indicators.Bar{}, nil
	}
	out := make([]indicators.Bar, 0, len(*parsed.JSON200.Results))
	for _, bar := range *parsed.JSON200.Results {
		out = append(out, indicators.Bar{Time: int64(bar.Timestamp), Open: bar.O, High: bar.H, Low: bar.L, Close: bar.C, Volume: bar.V})
	}
	return out, nil
}

// Quotes returns usable NBBO quote events. The official client's iterator
// follows its pagination links; invalid/crossed quotes are discarded.
func Quotes(ctx context.Context, key, symbol string, start, end time.Time) ([]Quote, error) {
	if key == "" {
		return nil, fmt.Errorf("Massive API key not configured")
	}
	from, to := strconv.FormatInt(start.UnixNano(), 10), strconv.FormatInt(end.UnixNano(), 10)
	limit := 50000
	order := gen.GetStocksQuotesParamsOrderAsc
	sortBy := gen.GetStocksQuotesParamsSortTimestamp
	client := New(key)
	response, err := client.GetStocksQuotesWithResponse(ctx, symbol, &gen.GetStocksQuotesParams{TimestampGte: &from, TimestampLte: &to, Order: &order, Sort: &sortBy, Limit: &limit})
	if err != nil {
		return nil, err
	}
	if err = rest.CheckResponse(response); err != nil {
		return nil, err
	}
	it := rest.NewIteratorFromResponse(client, response)
	out := []Quote{}
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m := it.Item()
		bid, bok := number(m["bid_price"])
		ask, aok := number(m["ask_price"])
		stamp, tok := integer(m["sip_timestamp"])
		if !bok || !aok || !tok || bid <= 0 || ask <= 0 || bid > ask {
			continue
		}
		out = append(out, Quote{At: time.Unix(0, stamp), Bid: bid, Ask: ask})
	}
	if err = it.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Trades returns usable historical trade prints for the documented excursion
// fallback. They are streamed through the same official-client iterator and
// are intentionally never persisted by default.
func Trades(ctx context.Context, key, symbol string, start, end time.Time) ([]Trade, error) {
	if key == "" {
		return nil, fmt.Errorf("Massive API key not configured")
	}
	from, to := strconv.FormatInt(start.UnixNano(), 10), strconv.FormatInt(end.UnixNano(), 10)
	limit := 50000
	order := gen.GetStocksTradesParamsOrderAsc
	sortBy := gen.GetStocksTradesParamsSortTimestamp
	client := New(key)
	response, err := client.GetStocksTradesWithResponse(ctx, symbol, &gen.GetStocksTradesParams{TimestampGte: &from, TimestampLte: &to, Order: &order, Sort: &sortBy, Limit: &limit})
	if err != nil {
		return nil, err
	}
	if err = rest.CheckResponse(response); err != nil {
		return nil, err
	}
	it := rest.NewIteratorFromResponse(client, response)
	out := []Trade{}
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m := it.Item()
		price, pok := number(m["price"])
		stamp, tok := integer(m["sip_timestamp"])
		if !pok || !tok || price <= 0 {
			continue
		}
		out = append(out, Trade{At: time.Unix(0, stamp), Price: price})
	}
	if err = it.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case json.Number:
		f, e := x.Float64()
		return f, e == nil
	case string:
		f, e := strconv.ParseFloat(x, 64)
		return f, e == nil
	default:
		return 0, false
	}
}
func integer(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case json.Number:
		n, e := x.Int64()
		return n, e == nil
	case string:
		n, e := strconv.ParseInt(x, 10, 64)
		return n, e == nil
	default:
		return 0, false
	}
}

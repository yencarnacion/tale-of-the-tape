package analytics

import (
	"math"
	"tale-of-the-tape/internal/positions"
	"testing"
)

func TestScratchAndKelly(t *testing.T) {
	x := []positions.RoundTrip{{Net: 100 * positions.Scale}, {Net: -50 * positions.Scale}, {Net: 0}}
	s := Calculate(x, .01, 2)
	if s.Wins != 1 || s.Losses != 1 || s.Scratches != 1 || s.ProfitFactor != 2 {
		t.Fatalf("%#v", s)
	}
	if s.RawKelly == nil {
		t.Fatal("expected Kelly")
	}
}

func TestInsufficientKellySampleRetainsPreliminaryValue(t *testing.T) {
	s := Calculate([]positions.RoundTrip{{Net: 100 * positions.Scale}, {Net: -50 * positions.Scale}}, .01, 30)
	if s.RawKelly != nil || s.HalfKelly != nil {
		t.Fatal("qualified Kelly must remain unavailable below the configured sample")
	}
	if s.PreliminaryRawKelly == nil || s.PreliminaryHalfKelly == nil {
		t.Fatal("expected explicitly preliminary Kelly values")
	}
	if s.KellySample != 2 || s.KellyMinimumSample != 30 {
		t.Fatalf("sample=%d minimum=%d", s.KellySample, s.KellyMinimumSample)
	}
}

func TestExtendedStatistics(t *testing.T) {
	s := Calculate([]positions.RoundTrip{
		{Net: 100 * positions.Scale, Entered: 100},
		{Net: -50 * positions.Scale, Entered: 50},
		{Net: 25 * positions.Scale, Entered: 25},
	}, .01, 30)
	if s.TotalVolume != 175 || s.AveragePerShare == nil || *s.AveragePerShare != 75.0/175.0 {
		t.Fatalf("volume=%d perShare=%v", s.TotalVolume, s.AveragePerShare)
	}
	if s.TradePnLStandardDeviation == nil || math.Abs(*s.TradePnLStandardDeviation-75) > 1e-9 {
		t.Fatalf("deviation=%v", s.TradePnLStandardDeviation)
	}
	if s.SystemQualityNumber != nil {
		t.Fatal("SQN must be unavailable below 30 trades")
	}
	if s.ProbabilityRandomChance == nil || *s.ProbabilityRandomChance != 1 {
		t.Fatalf("chance=%v", s.ProbabilityRandomChance)
	}
	if s.KRatio == nil {
		t.Fatal("expected K-ratio")
	}
}

package analytics

import (
	"math"
	"slices"
	"tale-of-the-tape/internal/positions"
)

type Summary struct {
	BrokerYTD                     *float64 `json:"broker_ytd,omitempty"`
	BrokerYTDDate                 string   `json:"broker_ytd_date,omitempty"`
	BrokerFeesYTD                 *float64 `json:"broker_fees_ytd,omitempty"`
	Total                         int      `json:"total_trades"`
	Wins                          int      `json:"wins"`
	Losses                        int      `json:"losses"`
	Scratches                     int      `json:"scratches"`
	Net                           float64  `json:"net_pnl"`
	Gross                         float64  `json:"gross_pnl"`
	Commissions                   float64  `json:"commissions"`
	Fees                          float64  `json:"fees"`
	Average                       float64  `json:"average_trade"`
	AverageDaily                  float64  `json:"average_daily_pnl"`
	AverageDailyVolume            float64  `json:"average_daily_volume"`
	AveragePerShare               *float64 `json:"average_per_share"`
	TotalVolume                   int64    `json:"total_volume"`
	TradePnLStandardDeviation     *float64 `json:"trade_pnl_standard_deviation"`
	SystemQualityNumber           *float64 `json:"system_quality_number"`
	ProbabilityPositiveExpectancy *float64 `json:"probability_positive_expectancy"`
	ExpectancyDailyLower90        *float64 `json:"expectancy_daily_lower_90"`
	ExpectancyDailyUpper90        *float64 `json:"expectancy_daily_upper_90"`
	ExpectancyTradingDays         int      `json:"expectancy_trading_days"`
	KRatio                        *float64 `json:"k_ratio"`
	Median                        *float64 `json:"median_trade"`
	AverageWinner                 float64  `json:"average_winner"`
	MedianWinner                  *float64 `json:"median_winner"`
	AverageLoser                  float64  `json:"average_loser"`
	MedianLoser                   *float64 `json:"median_loser"`
	LargestWinner                 float64  `json:"largest_winner"`
	LargestLoser                  float64  `json:"largest_loser"`
	ProfitFactor                  float64  `json:"profit_factor"`
	Expectancy                    float64  `json:"expectancy"`
	MaxDrawdown                   float64  `json:"max_drawdown"`
	WinRate                       *float64 `json:"win_rate"`
	WinRateExScratch              *float64 `json:"win_rate_excluding_scratches"`
	RawKelly                      *float64 `json:"raw_kelly"`
	HalfKelly                     *float64 `json:"half_kelly"`
	PreliminaryRawKelly           *float64 `json:"preliminary_raw_kelly"`
	PreliminaryHalfKelly          *float64 `json:"preliminary_half_kelly"`
	KellySample                   int      `json:"kelly_sample"`
	KellyMinimumSample            int      `json:"kelly_minimum_sample"`
	MaxWinStreak                  int      `json:"max_win_streak"`
	MaxLossStreak                 int      `json:"max_loss_streak"`
	CurrentWinStreak              int      `json:"current_win_streak"`
	CurrentLossStreak             int      `json:"current_loss_streak"`
	CurrentGreenDayStreak         int      `json:"current_green_day_streak"`
	CurrentRedDayStreak           int      `json:"current_red_day_streak"`
	MaxGreenDayStreak             int      `json:"max_green_day_streak"`
	MaxRedDayStreak               int      `json:"max_red_day_streak"`
	AverageWinningHoldMinutes     *float64 `json:"average_winning_hold_minutes"`
	AverageLosingHoldMinutes      *float64 `json:"average_losing_hold_minutes"`
	AverageScratchHoldMinutes     *float64 `json:"average_scratch_hold_minutes"`
	AverageMFE                    *float64 `json:"average_mfe"`
	MedianMFE                     *float64 `json:"median_mfe"`
	AverageMAE                    *float64 `json:"average_mae"`
	MedianMAE                     *float64 `json:"median_mae"`
	AverageMFECaptureRatio        *float64 `json:"average_mfe_capture_ratio"`
}

func Calculate(trades []positions.RoundTrip, tolerance float64, minimumKelly int) Summary {
	s := Summary{Total: len(trades)}
	var values, winning, losing []float64
	var equity, highWater float64
	winRun, lossRun := 0, 0
	for _, trade := range trades {
		net := float64(trade.Net) / float64(positions.Scale)
		s.Net += net
		values = append(values, net)
		s.Gross += float64(trade.Gross) / float64(positions.Scale)
		s.Commissions += float64(trade.Commissions) / float64(positions.Scale)
		s.Fees += float64(trade.Fees) / float64(positions.Scale)
		s.TotalVolume += trade.Entered
		equity += net
		if equity > highWater {
			highWater = equity
		}
		if highWater-equity > s.MaxDrawdown {
			s.MaxDrawdown = highWater - equity
		}
		switch {
		case net > tolerance:
			s.Wins++
			winning = append(winning, net)
			winRun++
			lossRun = 0
			if winRun > s.MaxWinStreak {
				s.MaxWinStreak = winRun
			}
		case net < -tolerance:
			s.Losses++
			losing = append(losing, net)
			lossRun++
			winRun = 0
			if lossRun > s.MaxLossStreak {
				s.MaxLossStreak = lossRun
			}
		default:
			s.Scratches++
			winRun, lossRun = 0, 0
		}
	}
	if s.Total > 0 {
		v := float64(s.Wins) / float64(s.Total)
		s.WinRate = &v
		s.Average = s.Net / float64(s.Total)
		s.Median = floatPtr(median(values))
		if s.TotalVolume > 0 {
			perShare := s.Net / float64(s.TotalVolume)
			s.AveragePerShare = &perShare
		}
		if len(values) > 1 {
			deviation := sampleStandardDeviation(values, s.Average)
			s.TradePnLStandardDeviation = &deviation
			if len(values) >= 30 && deviation > 0 {
				sqn := math.Sqrt(float64(len(values))) * s.Average / deviation
				s.SystemQualityNumber = &sqn
			}
			s.KRatio = kRatio(values)
		}
	}
	decisive := s.Wins + s.Losses
	s.KellySample, s.KellyMinimumSample = decisive, minimumKelly
	if decisive > 0 {
		v := float64(s.Wins) / float64(decisive)
		s.WinRateExScratch = &v
	}
	var totalWinners, totalLosers float64
	for _, value := range winning {
		totalWinners += value
		s.AverageWinner += value
		if value > s.LargestWinner {
			s.LargestWinner = value
		}
	}
	for _, value := range losing {
		totalLosers += value
		s.AverageLoser += value
		if value < s.LargestLoser {
			s.LargestLoser = value
		}
	}
	if len(winning) > 0 {
		s.AverageWinner /= float64(len(winning))
		s.MedianWinner = floatPtr(median(winning))
	}
	if len(losing) > 0 {
		s.AverageLoser /= float64(len(losing))
		s.MedianLoser = floatPtr(median(losing))
	}
	if totalLosers < 0 {
		s.ProfitFactor = totalWinners / (-totalLosers)
	}
	if decisive > 0 {
		s.Expectancy = float64(s.Wins)/float64(decisive)*s.AverageWinner - float64(s.Losses)/float64(decisive)*(-s.AverageLoser)
	}
	if s.Wins > 0 && s.Losses > 0 && s.AverageLoser != 0 {
		p := float64(s.Wins) / float64(decisive)
		b := s.AverageWinner / (-s.AverageLoser)
		k := p - (1-p)/b
		half := k / 2
		s.PreliminaryRawKelly, s.PreliminaryHalfKelly = &k, &half
		if decisive >= minimumKelly {
			s.RawKelly, s.HalfKelly = &k, &half
		}
	}
	s.CurrentWinStreak, s.CurrentLossStreak = winRun, lossRun
	return s
}

func floatPtr(v float64) *float64 { return &v }

func sampleStandardDeviation(values []float64, mean float64) float64 {
	var sum float64
	for _, value := range values {
		delta := value - mean
		sum += delta * delta
	}
	return math.Sqrt(sum / float64(len(values)-1))
}

// kRatio is the cumulative-P&L slope divided by its standard error and the
// number of observations (Kestner's equity-curve K-ratio).
func kRatio(values []float64) *float64 {
	n := len(values)
	if n < 3 {
		return nil
	}
	var sumX, sumY, sumXX, sumXY, equity float64
	equityValues := make([]float64, n)
	for i, value := range values {
		x := float64(i + 1)
		equity += value
		equityValues[i] = equity
		sumX, sumY = sumX+x, sumY+equity
		sumXX, sumXY = sumXX+x*x, sumXY+x*equity
	}
	denominator := float64(n)*sumXX - sumX*sumX
	if denominator == 0 {
		return nil
	}
	slope := (float64(n)*sumXY - sumX*sumY) / denominator
	intercept := (sumY - slope*sumX) / float64(n)
	var residual float64
	for i, actual := range equityValues {
		delta := actual - (intercept + slope*float64(i+1))
		residual += delta * delta
	}
	sxx := sumXX - sumX*sumX/float64(n)
	if residual == 0 || sxx == 0 {
		return nil
	}
	standardError := math.Sqrt((residual / float64(n-2)) / sxx)
	ratio := slope / (standardError * float64(n))
	return &ratio
}

// BayesianDailyExpectancy estimates the posterior probability that true daily
// expectancy is positive. Each trading day is one observation so trades from
// the same session are not incorrectly treated as independent. A single zero
// P&L pseudo-day supplies a transparent, conservative prior centered on no edge.
// Under the normal model with unknown variance and reference prior, the
// posterior for the mean is Student-t.
func BayesianDailyExpectancy(values []float64) (probability, lower90, upper90 *float64) {
	if len(values) == 0 {
		return nil, nil, nil
	}
	data := make([]float64, 0, len(values)+1)
	data = append(data, 0) // one prior pseudo-day centered on zero expectancy
	data = append(data, values...)
	var mean float64
	for _, value := range data {
		mean += value
	}
	mean /= float64(len(data))
	var squares float64
	for _, value := range data {
		delta := value - mean
		squares += delta * delta
	}
	df := float64(len(data) - 1)
	if squares == 0 {
		p := .5
		return &p, &mean, &mean
	}
	standardError := math.Sqrt((squares / df) / float64(len(data)))
	p := studentTCDF(mean/standardError, df)
	critical := studentTQuantile(.95, df)
	lower, upper := mean-critical*standardError, mean+critical*standardError
	return &p, &lower, &upper
}

func studentTQuantile(p, df float64) float64 {
	low, high := -64.0, 64.0
	for range 100 {
		mid := (low + high) / 2
		if studentTCDF(mid, df) < p {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

func studentTCDF(t, df float64) float64 {
	if t == 0 {
		return .5
	}
	x := df / (df + t*t)
	tail := .5 * regularizedBeta(x, df/2, .5)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

func regularizedBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	logBeta, _ := math.Lgamma(a)
	logB, _ := math.Lgamma(b)
	logAB, _ := math.Lgamma(a + b)
	front := math.Exp(logAB - logBeta - logB + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(x, a, b) / a
	}
	return 1 - front*betaContinuedFraction(1-x, b, a)/b
}

func betaContinuedFraction(x, a, b float64) float64 {
	const tiny = 1e-300
	c := 1.0
	d := 1 - (a+b)*x/(a+1)
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= 200; m++ {
		m2 := float64(2 * m)
		fm := float64(m)
		aa := fm * (b - fm) * x / ((a + m2 - 1) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + fm) * (a + b + fm) * x / ((a + m2) * (a + m2 + 1))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < 3e-14 {
			break
		}
	}
	return h
}

func median(values []float64) float64 {
	copyValues := slices.Clone(values)
	slices.Sort(copyValues)
	mid := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[mid]
	}
	return (copyValues[mid-1] + copyValues[mid]) / 2
}

package indicators

import "time"

type Bar struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}
type Series struct {
	VWAP   []Point `json:"vwap"`
	SMA9   []Point `json:"sma9"`
	SMA20  []Point `json:"sma20"`
	EMA9   []Point `json:"ema9"`
	EMA20  []Point `json:"ema20"`
	Upper  []Point `json:"upper"`
	Middle []Point `json:"middle"`
	Lower  []Point `json:"lower"`
}
type Point struct {
	Time  int64   `json:"time"`
	Value float64 `json:"value"`
}

func Calculate(b []Bar) Series {
	var s Series
	var cv, vol float64
	var e9, e20 float64
	loc, _ := time.LoadLocation("America/New_York")
	session := ""
	for i, x := range b {
		et := time.UnixMilli(x.Time).In(loc)
		key := ""
		if et.Hour() > 9 || (et.Hour() == 9 && et.Minute() >= 30) {
			key = et.Format("2006-01-02")
		}
		if key != "" && key != session {
			cv, vol, session = 0, 0, key
		}
		typ := (x.High + x.Low + x.Close) / 3
		cv += typ * x.Volume
		vol += x.Volume
		if vol > 0 {
			s.VWAP = append(s.VWAP, Point{x.Time, cv / vol})
		}
		s.SMA9 = append(s.SMA9, Point{x.Time, avg(b, max(0, i-8), i)})
		s.SMA20 = append(s.SMA20, Point{x.Time, avg(b, max(0, i-19), i)})
		if i == 0 {
			e9 = x.Close
			e20 = x.Close
		} else {
			e9 = x.Close*0.2 + e9*0.8
			e20 = x.Close*(2.0/21.0) + e20*(19.0/21.0)
		}
		s.EMA9 = append(s.EMA9, Point{x.Time, e9})
		s.EMA20 = append(s.EMA20, Point{x.Time, e20})
		start := max(0, i-19)
		m := avg(b, start, i)
		var variance float64
		for j := start; j <= i; j++ {
			d := b[j].Close - m
			variance += d * d
		}
		std := sqrt(variance / float64(i-start+1))
		s.Middle = append(s.Middle, Point{x.Time, m})
		s.Upper = append(s.Upper, Point{x.Time, m + 2*std})
		s.Lower = append(s.Lower, Point{x.Time, m - 2*std})
	}
	return s
}
func avg(b []Bar, a, z int) float64 {
	var n float64
	for i := a; i <= z; i++ {
		n += b[i].Close
	}
	return n / float64(z-a+1)
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func sqrt(x float64) float64 {
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 12; i++ {
		z = (z + x/z) / 2
	}
	return z
}

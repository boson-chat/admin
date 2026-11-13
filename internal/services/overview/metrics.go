package overview

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
)

// Stat is one live number with an optional 1-hour trend.
type Stat struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Group string   `json:"group"`
	Unit  string   `json:"unit,omitempty"`
	Query string   `json:"query"`
	Value *float64 `json:"value"` // null = no data
	// Breakdown splits the value by a label (e.g. login outcome).
	Breakdown []Part    `json:"breakdown,omitempty"`
	Trend     []TrendPt `json:"trend,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type Part struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// TrendPt is [unix seconds, value].
type TrendPt [2]float64

// MetricsView is the metrics block of the overview.
type MetricsView struct {
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
	Stats      []Stat `json:"stats"`
}

type statDef struct {
	Key, Label, Group, Unit string
	Query                   string
	// By is the label to break the value down by (Query must be "sum by (By) (...)").
	By string
	// TrendQuery is summed over everything; empty = no trend.
	TrendQuery string
}

// The queries assume the metrics backend scrapes the services' Prometheus
// endpoints (or ingests OTLP with Prometheus naming). Names from the
// quicvoice and auth READMEs.
var statDefs = []statDef{
	{Key: "voice_connections", Label: "QUIC connections", Group: "voice", Query: `sum(quicvoice_connections)`, TrendQuery: `sum(quicvoice_connections)`},
	{Key: "voice_rooms", Label: "Live rooms", Group: "voice", Query: `sum(quicvoice_rooms)`, TrendQuery: `sum(quicvoice_rooms)`},
	{Key: "voice_participants", Label: "Participants", Group: "voice", Query: `sum(quicvoice_participants)`, TrendQuery: `sum(quicvoice_participants)`},
	{Key: "voice_frames", Label: "Audio frames forwarded", Group: "voice", Unit: "/s",
		Query: `sum(rate(quicvoice_audio_frames_forwarded_total[5m]))`, TrendQuery: `sum(rate(quicvoice_audio_frames_forwarded_total[5m]))`},
	{Key: "auth_logins", Label: "Logins, last hour", Group: "auth", By: "outcome",
		Query:      `sum by (outcome) (increase(auth_login_attempts_total[1h]))`,
		TrendQuery: `sum(increase(auth_login_attempts_total[5m]))`},
	{Key: "auth_refresh_reuse", Label: "Refresh-token reuse, 24h", Group: "auth",
		Query: `sum(increase(auth_token_refresh_reuse_total[24h]))`},
	{Key: "auth_rate_limited", Label: "Rate-limited requests, 1h", Group: "auth",
		Query: `sum(increase(auth_rate_limit_hits_total[1h]))`},
}

func (s *OverviewService) metrics(ctx context.Context) MetricsView {
	if s.Prom == nil {
		return MetricsView{Configured: false, Stats: []Stat{}}
	}
	end := s.Now()
	start := end.Add(-time.Hour)
	stats := make([]Stat, len(statDefs))
	var wg sync.WaitGroup
	for i, d := range statDefs {
		stats[i] = Stat{Key: d.Key, Label: d.Label, Group: d.Group, Unit: d.Unit, Query: d.Query}
		st := &stats[i]
		wg.Go(func() {
			samples, err := s.Prom.Query(ctx, d.Query)
			if err != nil {
				st.Error = err.Error()
				return
			}
			if len(samples) == 0 {
				return
			}
			var total float64
			for _, smp := range samples {
				v := finite(smp.Value)
				total += v
				if d.By != "" {
					label := smp.Labels[d.By]
					if label == "" {
						label = "unknown"
					}
					st.Breakdown = append(st.Breakdown, Part{Label: label, Value: round(v)})
				}
			}
			sort.Slice(st.Breakdown, func(a, b int) bool { return st.Breakdown[a].Value > st.Breakdown[b].Value })
			total = round(total)
			st.Value = &total
		})
		if d.TrendQuery != "" {
			wg.Go(func() {
				pts, err := s.Prom.QueryRange(ctx, d.TrendQuery, start, end, time.Minute)
				if err != nil {
					return // the instant value carries the error
				}
				st.Trend = make([]TrendPt, 0, len(pts))
				for _, p := range pts {
					st.Trend = append(st.Trend, TrendPt{float64(p.T.Unix()), round(finite(p.V))})
				}
			})
		}
	}
	wg.Wait()
	view := MetricsView{Configured: true, Stats: stats}
	// If every query failed the same way, say it once.
	if len(stats) > 0 && stats[0].Error != "" {
		same := true
		for _, st := range stats {
			if st.Error != stats[0].Error {
				same = false
				break
			}
		}
		if same {
			view.Error = stats[0].Error
		}
	}
	return view
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

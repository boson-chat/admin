// Package promql queries a Prometheus-compatible HTTP API (Prometheus,
// VictoriaMetrics).
package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Sample is one series' value at one time.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Point is one value of a range series.
type Point struct {
	T time.Time
	V float64
}

type PromClientImpl interface {
	// Query runs an instant query and returns the vector (or scalar as one sample).
	Query(ctx context.Context, q string) ([]Sample, error)
	// QueryRange runs a range query and returns the first series (sum your query).
	QueryRange(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]Point, error)
}

type PromClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewPromClient(baseURL string, timeout time.Duration) PromClientImpl {
	return &PromClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: timeout}}
}

type apiResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

func (c *PromClient) get(ctx context.Context, path string, params url.Values) (*apiResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metrics backend unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("metrics backend: HTTP %d, not a Prometheus API response", resp.StatusCode)
	}
	if ar.Status != "success" {
		return nil, fmt.Errorf("metrics query failed: %s: %s", ar.ErrorType, ar.Error)
	}
	return &ar, nil
}

func (c *PromClient) Query(ctx context.Context, q string) ([]Sample, error) {
	ar, err := c.get(ctx, "/api/v1/query", url.Values{"query": {q}})
	if err != nil {
		return nil, err
	}
	switch ar.Data.ResultType {
	case "vector":
		var vec []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(ar.Data.Result, &vec); err != nil {
			return nil, err
		}
		out := make([]Sample, 0, len(vec))
		for _, v := range vec {
			f, err := parseValue(v.Value[1])
			if err != nil {
				return nil, err
			}
			out = append(out, Sample{Labels: v.Metric, Value: f})
		}
		return out, nil
	case "scalar":
		var sc [2]any
		if err := json.Unmarshal(ar.Data.Result, &sc); err != nil {
			return nil, err
		}
		f, err := parseValue(sc[1])
		if err != nil {
			return nil, err
		}
		return []Sample{{Value: f}}, nil
	default:
		return nil, fmt.Errorf("unexpected result type %q", ar.Data.ResultType)
	}
}

func (c *PromClient) QueryRange(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]Point, error) {
	ar, err := c.get(ctx, "/api/v1/query_range", url.Values{
		"query": {q},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	})
	if err != nil {
		return nil, err
	}
	if ar.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("unexpected result type %q", ar.Data.ResultType)
	}
	var mat []struct {
		Values [][2]any `json:"values"`
	}
	if err := json.Unmarshal(ar.Data.Result, &mat); err != nil {
		return nil, err
	}
	if len(mat) == 0 {
		return []Point{}, nil
	}
	out := make([]Point, 0, len(mat[0].Values))
	for _, v := range mat[0].Values {
		ts, ok := v[0].(float64)
		if !ok {
			return nil, fmt.Errorf("bad timestamp")
		}
		f, err := parseValue(v[1])
		if err != nil {
			return nil, err
		}
		sec, frac := math.Modf(ts)
		out = append(out, Point{T: time.Unix(int64(sec), int64(frac*1e9)), V: f})
	}
	return out, nil
}

func parseValue(v any) (float64, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("bad sample value %v", v)
	}
	return strconv.ParseFloat(s, 64)
}

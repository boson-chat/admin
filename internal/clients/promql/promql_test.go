package promql

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueryAndRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		switch {
		case r.URL.Path == "/api/v1/query" && q == "vec":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"outcome":"success"},"value":[1700000000.5,"12"]},
				{"metric":{"outcome":"invalid_credentials"},"value":[1700000000.5,"3.5"]}]}}`))
		case r.URL.Path == "/api/v1/query" && q == "sc":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[1700000000,"7"]}}`))
		case r.URL.Path == "/api/v1/query" && q == "bad(":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
		case r.URL.Path == "/api/v1/query_range":
			if r.URL.Query().Get("step") != "60" {
				t.Errorf("step = %q", r.URL.Query().Get("step"))
			}
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
				{"metric":{"device":"eth0"},"values":[[1700000000,"1"],[1700000060,"NaN"],[1700000120,"3"]]},
				{"metric":{"device":"eth1"},"values":[[1700000000,"4"]]}]}}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte("not found"))
		}
	}))
	defer srv.Close()
	c := NewPromClient(srv.URL, time.Second)
	ctx := context.Background()

	got, err := c.Query(ctx, "vec")
	if err != nil || len(got) != 2 || got[1].Value != 3.5 || got[0].Labels["outcome"] != "success" {
		t.Fatalf("vector: %v %+v", err, got)
	}
	if got, err = c.Query(ctx, "sc"); err != nil || len(got) != 1 || got[0].Value != 7 {
		t.Fatalf("scalar: %v %+v", err, got)
	}
	if _, err = c.Query(ctx, "bad("); err == nil || err.Error() != "metrics query failed: bad_data: parse error" {
		t.Fatalf("error: %v", err)
	}
	if _, err = c.Query(ctx, "other"); err == nil {
		t.Fatal("non-API response accepted")
	}
	pts, err := c.QueryRange(ctx, "sum(x)", time.Unix(1700000000, 0), time.Unix(1700000120, 0), time.Minute)
	if err != nil || len(pts) != 3 || pts[2].V != 3 || pts[1].T.Unix() != 1700000060 {
		t.Fatalf("range: %v %+v", err, pts)
	}
	series, err := c.(*PromClient).QueryRangeSeries(ctx, "x", time.Unix(1700000000, 0), time.Unix(1700000120, 0), time.Minute)
	if err != nil || len(series) != 2 || series[1].Labels["device"] != "eth1" || series[1].Points[0].V != 4 || len(series[0].Points) != 3 {
		t.Fatalf("series: %v %+v", err, series)
	}
}

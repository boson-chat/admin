package authapi

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestStatsAgainstAuth runs against a real auth's internal listener (with
// GET /internal/v1/stats) when AUTH_TEST_INTERNAL_URL and
// AUTH_TEST_INTERNAL_TOKEN are set.
func TestStatsAgainstAuth(t *testing.T) {
	base, tok := os.Getenv("AUTH_TEST_INTERNAL_URL"), os.Getenv("AUTH_TEST_INTERNAL_TOKEN")
	if base == "" || tok == "" {
		t.Skip("AUTH_TEST_INTERNAL_URL / AUTH_TEST_INTERNAL_TOKEN not set")
	}
	c := NewStatsClient(base, tok, 10*time.Second)
	st, err := c.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Accounts < 0 || st.Active7d > st.Active30d || st.Active30d > st.Accounts {
		t.Fatalf("stats don't add up: %+v", st)
	}
	if _, err := NewStatsClient(base, tok+"-wrong", 10*time.Second).Stats(context.Background()); err == nil {
		t.Fatal("a wrong token was accepted")
	}
}

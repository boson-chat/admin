package federation

import "testing"

func TestOlderThan(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"1.25.0", true}, {"1.24.3", true}, {"0.99.0", true},
		{"1.26.0", false}, {"1.26.1", false}, {"2.0.0", false},
		{"1.25.0-rc.1", true}, {"dev", false}, {"", false}, {"1.26", false},
	} {
		if got := olderThan(tc.v, RequiresBlockDisconnect); got != tc.want {
			t.Errorf("%q: %v", tc.v, got)
		}
	}
}

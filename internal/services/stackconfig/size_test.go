package stackconfig

import "testing"

// Sizes as the files service reads and writes them (decimal units, binary
// with KiB/MiB/...).
func TestParseFormatSize(t *testing.T) {
	for in, want := range map[string]int64{
		"5GB": 5e9, "5 gb": 5e9, "500MB": 500e6, "25MiB": 25 << 20, "1.5GB": 1500e6,
		"100": 100, "1KB": 1000, "2tb": 2e12, "7 k": 7000, "0": 0,
	} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "lots", "-1GB", "5 PB", "1e9"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) accepted", bad)
		}
	}
	for n, want := range map[int64]string{5e9: "5GB", 1500e6: "1500MB", 25 << 20: "25MiB", 100: "100", 1025: "1025", 0: "0"} {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
	k, _ := KeyByName(KeyFilesAccountMaxFile)
	if _, err := k.Clean("10"); err == nil {
		t.Error("10 bytes is below the minimum file size")
	}
	if v, err := k.Clean("100 mb"); err != nil || v != "100MB" {
		t.Errorf("clean: %v %v", v, err)
	}
	if k, _ := KeyByName(KeyFilesMinFree); true {
		if v, err := k.Clean("0"); err != nil || v != "0" {
			t.Errorf("min_free 0: %v %v", v, err)
		}
	}
}

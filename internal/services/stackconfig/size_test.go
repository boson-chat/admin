package stackconfig

import "testing"

func TestParseFormatSize(t *testing.T) {
	for in, want := range map[string]int64{
		"5GB": 5 << 30, "5 gb": 5 << 30, "500MB": 500 << 20, "25MiB": 25 << 20, "1.5GB": 1536 << 20,
		"100": 100, "1KB": 1024, "2tb": 2 << 40, "7 k": 7 << 10,
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
	for n, want := range map[int64]string{5 << 30: "5GB", 1536 << 20: "1536MB", 100: "100B", 1025: "1025B", 0: "0B"} {
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
}

package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/promql"
)

func TestCPUBucketEdges(t *testing.T) {
	for n, want := range map[int]string{-1: "null", 0: "null", 1: "1", 2: "2", 3: "3-4", 4: "3-4", 5: "5-8", 8: "5-8",
		9: "9-16", 16: "9-16", 17: "17-32", 32: "17-32", 33: "33+", 512: "33+"} {
		if got := str(CPUBucket(n)); got != want {
			t.Errorf("CPUBucket(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestRAMBucketEdges(t *testing.T) {
	const mib = 1 << 20
	for bytes, want := range map[int64]string{
		0: "null", -1: "null", 512 * mib: "<1", gib: "<1", gib + 1: "1-2", 2 * gib: "1-2",
		3900 * mib: "2-4", // a "4 GB" VPS reports a little under 4 GiB
		4 * gib:    "2-4", 4*gib + 1: "4-8", 8 * gib: "4-8", 16 * gib: "8-16", 32 * gib: "16-32",
		64 * gib: "32-64", 64*gib + 1: "64+", 1024 * gib: "64+",
	} {
		if got := str(RAMBucket(bytes)); got != want {
			t.Errorf("RAMBucket(%d) = %s, want %s", bytes, got, want)
		}
	}
}

func TestDiskBucketEdges(t *testing.T) {
	g := float64(gib)
	for bytes, want := range map[float64]string{
		0: "null", -5: "null", 10 * g: "<25", 25 * g: "<25", 25*g + 1: "25-50", 50 * g: "25-50", 50*g + 1: "50-100",
		100 * g: "50-100", 250 * g: "100-250", 500 * g: "250-500", 931 * g: "500-1000", 1000 * g: "500-1000",
		1000*g + 1: "1000+", 8000 * g: "1000+",
	} {
		if got := str(DiskBucket(bytes)); got != want {
			t.Errorf("DiskBucket(%v GiB) = %s, want %s", bytes/g, got, want)
		}
	}
}

func TestDockerArch(t *testing.T) {
	for in, want := range map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64",
		"armv7l": "other", "riscv64": "other", "s390x": "other", "": "null"} {
		if got := str(dockerArch(in)); got != want {
			t.Errorf("dockerArch(%q) = %s, want %s", in, got, want)
		}
	}
}

// fakeDockerInfo is a socket proxy answering GET /info (or refusing it, as
// an older stack's proxy does).
func fakeDockerInfo(t *testing.T, body string, status int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/info" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeVM is VictoriaMetrics answering node_filesystem_size_bytes with the
// given mount point → size, and nothing for other queries.
func fakeVM(t *testing.T, mounts map[string]float64) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		type res struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		out := []res{}
		if strings.HasPrefix(q, "node_filesystem_size_bytes{") {
			if !strings.Contains(q, `job="node"`) || !strings.Contains(q, "fstype!~") {
				t.Errorf("query %s", q)
			}
			for m, v := range mounts {
				out = append(out, res{Metric: map[string]string{"mountpoint": m, "device": "/dev/secret-disk", "nodename": "secret-node.example.org"},
					Value: [2]any{float64(time.Now().Unix()), jsonNum(v)}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": out}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func jsonNum(v float64) string { b, _ := json.Marshal(v); return string(b) }

func TestHostSizeFromDockerAndVictoriaMetrics(t *testing.T) {
	g := float64(gib)
	cases := []struct {
		name   string
		info   string
		status int
		mounts map[string]float64 // nil: no metrics profile
		want   string             // cpu,ram,disk,arch or "null"
	}{
		{"docker root on /", `{"NCPU":2,"MemTotal":4022210560,"Architecture":"x86_64","DockerRootDir":"/var/lib/docker","Name":"secret-host"}`, 200,
			map[string]float64{"/": 38 * g, "/boot": 0.9 * g, "/home": 400 * g}, "2,2-4,25-50,amd64"},
		{"docker root on its own disk", `{"NCPU":16,"MemTotal":68719476736,"Architecture":"aarch64","DockerRootDir":"/var/lib/docker"}`, 200,
			map[string]float64{"/": 20 * g, "/var/lib/docker": 1800 * g, "/var/lib/dockerish": 5 * g}, "9-16,32-64,1000+,arm64"},
		{"docker root moved under /srv", `{"NCPU":64,"MemTotal":274877906944,"Architecture":"x86_64","DockerRootDir":"/srv/docker/data"}`, 200,
			map[string]float64{"/": 30 * g, "/srv": 300 * g, "/srv/docker/data/overlay2/x": 1 * g}, "33+,64+,250-500,amd64"},
		{"no metrics profile", `{"NCPU":1,"MemTotal":1000000000,"Architecture":"riscv64","DockerRootDir":"/var/lib/docker"}`, 200,
			nil, "1,<1,null,other"},
		{"an older proxy refuses /info: disk of /", `Forbidden`, 403,
			map[string]float64{"/": 160 * g}, "null,null,100-250,null"},
		{"nothing known", `Forbidden`, 403, nil, "null"},
		{"metrics without filesystems", `Forbidden`, 403, map[string]float64{}, "null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			docker := fakeDockerInfo(t, c.info, c.status)
			dc := dockerapi.NewDockerClient(docker.URL, time.Second, nil).(*dockerapi.Client)
			s := Sources{DockerInfo: dc.Info}
			if c.mounts != nil {
				s.Prom = promql.NewPromClient(fakeVM(t, c.mounts).URL, time.Second)
			}
			b, r := buildJSON(t, &Collector{S: s}, Input{InstallID: testID, Reason: ReasonDaily, Now: now})
			got := "null"
			if z := r.HostSize; z != nil {
				got = strings.Join([]string{str(z.CPUCores), str(z.RAMGB), str(z.DiskGB), str(z.Arch)}, ",")
			}
			if got != c.want {
				t.Errorf("host_size = %s, want %s", got, c.want)
			}
			for _, sec := range []string{"secret-host", "secret-disk", "secret-node", "/var/lib/docker", "/srv"} {
				if strings.Contains(string(b), sec) {
					t.Errorf("the report contains %q", sec)
				}
			}
			if err := validate(t, compileSchema(t), b); err != nil {
				t.Errorf("doesn't validate: %v\n%s", err, b)
			}
		})
	}
}

func TestMountFor(t *testing.T) {
	smp := []promql.Sample{
		{Labels: map[string]string{"mountpoint": "/"}, Value: 10},
		{Labels: map[string]string{"mountpoint": "/var"}, Value: 20},
		{Labels: map[string]string{"mountpoint": "/var/lib/docker"}, Value: 30},
		{Labels: map[string]string{}, Value: 99},
	}
	for path, want := range map[string]float64{"/var/lib/docker": 30, "/var/lib/docker/": 30, "/var/lib/dockerx": 20, "/opt": 10, "/": 10} {
		if got, ok := mountFor(smp, path); !ok || got != want {
			t.Errorf("mountFor(%s) = %v %v, want %v", path, got, ok, want)
		}
	}
	if _, ok := mountFor(smp[1:3], "/opt"); ok {
		t.Error("no / and nothing containing /opt")
	}
}

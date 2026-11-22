package appstore

import (
	"strings"
	"testing"
	"time"
)

func TestSourceUpdateTimeout(t *testing.T) {
	if got := updateTimeout(Installed{BuildMode: ModeSource}); got != buildTimeout {
		t.Fatalf("source update timeout = %v, want %v", got, buildTimeout)
	}
	if got := updateTimeout(Installed{BuildMode: ModeFast}); got != 30*time.Minute {
		t.Fatalf("fast update timeout = %v", got)
	}
}

func TestLimitBuildJobs(t *testing.T) {
	for _, c := range []struct {
		cpus      int
		available uint64
		want      int
	}{
		{cpus: 8, available: 8 << 30, want: 8},
		{cpus: 8, available: 3 << 30, want: 3},
		{cpus: 2, available: 8 << 30, want: 2},
		{cpus: 8, available: 512 << 20, want: 1},
		{cpus: 0, available: 0, want: 1},
	} {
		if got := limitBuildJobs(c.cpus, c.available); got != c.want {
			t.Errorf("limitBuildJobs(%d, %d) = %d, want %d", c.cpus, c.available, got, c.want)
		}
	}
}

func TestSourceDockerfileUsesBoundedJobs(t *testing.T) {
	df, err := SourceDockerfile("mariadb", "mariadb:11.4")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ARG BUILD_JOBS", `cmake --build b -j"${BUILD_JOBS:-$(nproc)}"`} {
		if !strings.Contains(df, want) {
			t.Fatalf("Dockerfile lacks %q:\n%s", want, df)
		}
	}
}

package server

import (
	"testing"
	"time"
)

func TestPHPUpdateBuildTimeout(t *testing.T) {
	if phpUpdateBuildTimeout != 6*time.Hour {
		t.Fatalf("PHP source update timeout = %v, want 6h", phpUpdateBuildTimeout)
	}
}

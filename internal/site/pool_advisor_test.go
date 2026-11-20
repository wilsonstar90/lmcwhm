package site

import (
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func TestAutoPoolTarget(t *testing.T) {
	low := Site{Type: "php", Advanced: webenv.Advanced{PHPPool: webenv.PHPPoolLow}}
	if target, streak := autoPoolTarget(low, 200, webenv.FPMStatus{}); target != webenv.PHPPoolLow || streak != 1 {
		t.Fatalf("quiet low pool changed: %s %d", target, streak)
	}
	if target, streak := autoPoolTarget(low, 10000, webenv.FPMStatus{}); target != webenv.PHPPoolBusy || streak != 6 {
		t.Fatalf("sustained traffic threshold: %s %d", target, streak)
	}
	if target, streak := autoPoolTarget(low, 100, webenv.FPMStatus{MaxChildrenReached: 1}); target != webenv.PHPPoolBusy || streak != 3 {
		t.Fatalf("saturated low pool did not request busy: %s %d", target, streak)
	}
	busy := Site{Type: "php", Advanced: webenv.Advanced{PHPPool: webenv.PHPPoolBusy}}
	if target, streak := autoPoolTarget(busy, 100, webenv.FPMStatus{ActiveProcesses: 2}); target != webenv.PHPPoolLow || streak != 144 {
		t.Fatalf("quiet busy pool did not request low: %s %d", target, streak)
	}
	if target, _ := autoPoolTarget(busy, 3000, webenv.FPMStatus{ActiveProcesses: 1}); target != webenv.PHPPoolBusy {
		t.Fatalf("busy pool downgraded during traffic: %s", target)
	}
	if target, _ := autoPoolTarget(busy, 100, webenv.FPMStatus{ActiveProcesses: 2, ListenQueue: 1}); target != webenv.PHPPoolBusy {
		t.Fatalf("busy pool downgraded with a queue: %s", target)
	}
}

package monitor

import "testing"

func TestIsPartition(t *testing.T) {
	for name, want := range map[string]bool{
		"sda": false, "sda1": true, "vda": false, "vda2": true, "nvme0n1": false, "nvme0n1p1": true, "dm-0": false, "xvda1": true,
	} {
		if got := isPartition(name); got != want {
			t.Errorf("isPartition(%s) = %v", name, got)
		}
	}
}

func TestAverage(t *testing.T) {
	a := average([]Point{{CPU: 10, NetRx: 100}, {CPU: 30, NetRx: 300}})
	if a.CPU != 20 || a.NetRx != 200 {
		t.Fatalf("%+v", a)
	}
}

package sysinfo

import (
	"context"
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
)

func TestCPUShares(t *testing.T) {
	a := cpu.TimesStat{User: 100, System: 50, Idle: 800, Iowait: 10, Guest: 5}
	b := cpu.TimesStat{User: 130, System: 60, Idle: 850, Iowait: 20, Guest: 15}
	s := cpuShares(a, b)
	// 100 units passed (guest is inside user and not counted again).
	if s["user"] != 30 || s["system"] != 10 || s["idle"] != 50 || s["iowait"] != 10 || s["guest"] != 10 {
		t.Fatalf("%v", s)
	}
}

func TestDiskNames(t *testing.T) {
	for name, want := range map[string]bool{"sda": true, "sda1": false, "vdb": true, "vdb2": false, "nvme0n1": true, "nvme0n1p3": false,
		"loop4": false, "dm-0": false, "md0": false, "mmcblk0": true, "mmcblk0p1": false, "zram0": false} {
		if wholeDisk(name) != want {
			t.Errorf("%s: %v", name, !want)
		}
	}
	for name, want := range map[string]bool{"eth0": false, "ens3": false, "lo": true, "veth12ab": true, "docker0": true, "br-1a2b": true} {
		if skipNIC(name) != want {
			t.Errorf("nic %s", name)
		}
	}
}

func TestCollectDetail(t *testing.T) {
	d := CollectDetail(context.Background())
	if d.CPU.Threads == 0 || d.Mem.Total == 0 || len(d.Disks) == 0 {
		t.Fatalf("%+v", d)
	}
}

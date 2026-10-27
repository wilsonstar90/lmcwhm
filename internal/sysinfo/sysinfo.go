// Package sysinfo samples host metrics for the dashboard.
package sysinfo

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type Overview struct {
	Hostname   string     `json:"hostname"`
	OS         string     `json:"os"`
	Platform   string     `json:"platform"`
	Kernel     string     `json:"kernel"`
	Arch       string     `json:"arch"`
	Uptime     uint64     `json:"uptime"`
	CPUModel   string     `json:"cpu_model"`
	CPUCores   int        `json:"cpu_cores"`
	CPUPercent float64    `json:"cpu_percent"`
	PerCore    []float64  `json:"per_core"`
	Load       [3]float64 `json:"load"`
	MemTotal   uint64     `json:"mem_total"`
	MemUsed    uint64     `json:"mem_used"`
	SwapTotal  uint64     `json:"swap_total"`
	SwapUsed   uint64     `json:"swap_used"`
	Disks      []Disk     `json:"disks"`
	NetSent    uint64     `json:"net_sent"`
	NetRecv    uint64     `json:"net_recv"`
	SampledAt  int64      `json:"sampled_at"`
}

type Disk struct {
	Mount string `json:"mount"`
	FS    string `json:"fs"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

// Collect gathers a snapshot; CPU usage is measured over a short window.
func Collect(ctx context.Context) Overview {
	o := Overview{Arch: runtime.GOARCH, SampledAt: time.Now().Unix()}
	if h, err := host.InfoWithContext(ctx); err == nil {
		o.Hostname, o.OS, o.Platform, o.Kernel, o.Uptime = h.Hostname, h.OS, h.Platform+" "+h.PlatformVersion, h.KernelVersion, h.Uptime
	}
	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		o.CPUModel = infos[0].ModelName
	}
	o.CPUCores, _ = cpu.CountsWithContext(ctx, true)
	if pc, err := cpu.PercentWithContext(ctx, 300*time.Millisecond, true); err == nil {
		o.PerCore = pc
		var sum float64
		for _, v := range pc {
			sum += v
		}
		if len(pc) > 0 {
			o.CPUPercent = sum / float64(len(pc))
		}
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		o.Load = [3]float64{l.Load1, l.Load5, l.Load15}
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		o.MemTotal, o.MemUsed = v.Total, v.Used
	}
	if s, err := mem.SwapMemoryWithContext(ctx); err == nil {
		o.SwapTotal, o.SwapUsed = s.Total, s.Used
	}
	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if seen[p.Device] || skipDisk(p.Fstype, p.Mountpoint) {
				continue
			}
			seen[p.Device] = true
			if u, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil && u.Total > 0 {
				o.Disks = append(o.Disks, Disk{Mount: p.Mountpoint, FS: p.Fstype, Total: u.Total, Used: u.Used})
			}
		}
	}
	if io, err := net.IOCountersWithContext(ctx, false); err == nil && len(io) > 0 {
		o.NetSent, o.NetRecv = io[0].BytesSent, io[0].BytesRecv
	}
	return o
}

var pseudoFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "devfs": true, "overlay": true, "squashfs": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "autofs": true, "nsfs": true,
}

// skipDisk hides pseudo filesystems, snap/docker layers and macOS system volumes.
func skipDisk(fstype, mount string) bool {
	if pseudoFS[fstype] {
		return true
	}
	for _, p := range []string{"/snap/", "/var/lib/docker/", "/run/", "/boot/efi", "/System/Volumes/"} {
		if strings.HasPrefix(mount, p) {
			return true
		}
	}
	return false
}

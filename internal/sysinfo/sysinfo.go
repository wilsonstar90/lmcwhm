// Package sysinfo samples host metrics for the dashboard.
package sysinfo

import (
	"context"
	"regexp"
	"runtime"
	"sort"
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
	NICs       []NIC      `json:"nics"`
	DiskIO     []DiskIO   `json:"disk_io"`
	SampledAt  int64      `json:"sampled_at"`
}

// NIC is one network interface's byte counters.
type NIC struct {
	Name string `json:"name"`
	Sent uint64 `json:"sent"`
	Recv uint64 `json:"recv"`
}

// DiskIO is one whole disk's counters; the dashboard turns two samples into
// rates, operations per second and latency (busy time / operations).
type DiskIO struct {
	Name       string `json:"name"`
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	ReadCount  uint64 `json:"read_count"`
	WriteCount uint64 `json:"write_count"`
	ReadTime   uint64 `json:"read_time"`  // ms
	WriteTime  uint64 `json:"write_time"` // ms
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
	if io, err := net.IOCountersWithContext(ctx, true); err == nil {
		for _, n := range io {
			if !skipNIC(n.Name) {
				o.NICs = append(o.NICs, NIC{Name: n.Name, Sent: n.BytesSent, Recv: n.BytesRecv})
			}
		}
		sort.Slice(o.NICs, func(i, j int) bool { return o.NICs[i].Name < o.NICs[j].Name })
	}
	if io, err := disk.IOCountersWithContext(ctx); err == nil {
		for name, d := range io {
			if wholeDisk(name) {
				o.DiskIO = append(o.DiskIO, DiskIO{Name: name, ReadBytes: d.ReadBytes, WriteBytes: d.WriteBytes,
					ReadCount: d.ReadCount, WriteCount: d.WriteCount, ReadTime: d.ReadTime, WriteTime: d.WriteTime})
			}
		}
		sort.Slice(o.DiskIO, func(i, j int) bool { return o.DiskIO[i].Name < o.DiskIO[j].Name })
	}
	return o
}

// skipNIC hides loopback and the per-container virtual interfaces.
func skipNIC(name string) bool {
	for _, p := range []string{"lo", "veth", "docker", "br-", "virbr", "tun", "tap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

var partitionRe = regexp.MustCompile(`^((sd|vd|xvd|hd)[a-z]+\d+|(nvme\d+n\d+|mmcblk\d+)p\d+)$`)

// wholeDisk keeps disks and drops partitions, loop, RAM and optical devices.
func wholeDisk(name string) bool {
	// Device-mapper and md devices repeat the IO of the disks under them.
	for _, p := range []string{"loop", "ram", "sr", "fd", "zram", "dm-", "md"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return !partitionRe.MatchString(name)
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

package sysinfo

import (
	"context"
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

// Detail is what the dashboard's load, CPU, memory and disk cards show when
// opened (like aaPanel's status cards).
type Detail struct {
	Load struct {
		Avg          [3]float64         `json:"avg"`
		ProcsRunning int                `json:"procs_running"`
		ProcsTotal   int                `json:"procs_total"`
		Times        map[string]float64 `json:"times"` // % of CPU time: user, system, idle, iowait, ...
	} `json:"load"`
	CPU struct {
		Model   string    `json:"model"`
		Sockets int       `json:"sockets"`
		Cores   int       `json:"cores"`
		Threads int       `json:"threads"`
		PerCore []float64 `json:"per_core"`
		Percent float64   `json:"percent"`
	} `json:"cpu"`
	Mem struct {
		Total     uint64 `json:"total"`
		Used      uint64 `json:"used"`
		Free      uint64 `json:"free"`
		Shared    uint64 `json:"shared"`
		Available uint64 `json:"available"`
		Buffers   uint64 `json:"buffers"`
		Cached    uint64 `json:"cached"`
	} `json:"mem"`
	Disks []DiskDetail `json:"disks"`
}

type DiskDetail struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device"`
	FS          string  `json:"fs"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	Percent     float64 `json:"percent"`
	InodesTotal uint64  `json:"inodes_total"`
	InodesUsed  uint64  `json:"inodes_used"`
	InodesFree  uint64  `json:"inodes_free"`
	InodesPct   float64 `json:"inodes_percent"`
}

// CollectDetail samples CPU times and per-core use over a short window.
func CollectDetail(ctx context.Context) Detail {
	var d Detail
	if l, err := load.AvgWithContext(ctx); err == nil {
		d.Load.Avg = [3]float64{l.Load1, l.Load5, l.Load15}
	}
	if m, err := load.MiscWithContext(ctx); err == nil {
		d.Load.ProcsRunning, d.Load.ProcsTotal = m.ProcsRunning, m.ProcsTotal
	}
	if d.Load.ProcsTotal == 0 { // not reported outside Linux
		if pids, err := process.PidsWithContext(ctx); err == nil {
			d.Load.ProcsTotal = len(pids)
		}
	}
	t0, err0 := cpu.TimesWithContext(ctx, false)
	pc, _ := cpu.PercentWithContext(ctx, 300*time.Millisecond, true)
	t1, err1 := cpu.TimesWithContext(ctx, false)
	if err0 == nil && err1 == nil && len(t0) > 0 && len(t1) > 0 {
		d.Load.Times = cpuShares(t0[0], t1[0])
	}
	d.CPU.PerCore = pc
	for _, v := range pc {
		d.CPU.Percent += v / float64(len(pc))
	}
	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		d.CPU.Model = infos[0].ModelName
		sockets := map[string]bool{}
		for _, i := range infos {
			sockets[i.PhysicalID] = true
		}
		d.CPU.Sockets = len(sockets)
	}
	d.CPU.Cores, _ = cpu.CountsWithContext(ctx, false)
	d.CPU.Threads, _ = cpu.CountsWithContext(ctx, true)
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		d.Mem.Total, d.Mem.Used, d.Mem.Free, d.Mem.Shared = v.Total, v.Used, v.Free, v.Shared
		d.Mem.Available, d.Mem.Buffers, d.Mem.Cached = v.Available, v.Buffers, v.Cached
	}
	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if seen[p.Device] || skipDisk(p.Fstype, p.Mountpoint) {
				continue
			}
			seen[p.Device] = true
			if u, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil && u.Total > 0 {
				d.Disks = append(d.Disks, DiskDetail{Mount: p.Mountpoint, Device: p.Device, FS: p.Fstype, Total: u.Total, Used: u.Used,
					Free: u.Free, Percent: u.UsedPercent, InodesTotal: u.InodesTotal, InodesUsed: u.InodesUsed, InodesFree: u.InodesFree,
					InodesPct: u.InodesUsedPercent})
			}
		}
	}
	return d
}

// cpuShares turns two cumulative samples into each state's share of the time.
func cpuShares(a, b cpu.TimesStat) map[string]float64 {
	diff := map[string]float64{
		"user": b.User - a.User, "nice": b.Nice - a.Nice, "system": b.System - a.System, "idle": b.Idle - a.Idle,
		"iowait": b.Iowait - a.Iowait, "irq": b.Irq - a.Irq, "softirq": b.Softirq - a.Softirq, "steal": b.Steal - a.Steal,
		"guest": b.Guest - a.Guest, "guest_nice": b.GuestNice - a.GuestNice,
	}
	// guest time is already counted in user / nice.
	var total float64
	for k, v := range diff {
		if k != "guest" && k != "guest_nice" {
			total += v
		}
	}
	out := map[string]float64{}
	for k, v := range diff {
		if total > 0 {
			out[k] = float64(int(v/total*1000+0.5)) / 10
		} else {
			out[k] = 0
		}
	}
	return out
}

var ErrUnsupported = errors.New("system.unsupported")

// ReleaseMemory writes dirty pages back and drops the page cache, dentries
// and inodes (aaPanel's 立即釋放); it returns how much memory became available.
func ReleaseMemory(ctx context.Context) (uint64, error) {
	if runtime.GOOS != "linux" {
		return 0, ErrUnsupported
	}
	before, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return 0, err
	}
	syscall.Sync()
	if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0o200); err != nil {
		return 0, err
	}
	after, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil || after.Free < before.Free {
		return 0, err
	}
	return after.Free - before.Free, nil
}

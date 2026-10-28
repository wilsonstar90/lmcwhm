// Package monitor samples host metrics every few seconds, keeps the last
// hour in memory and one-minute averages in SQLite for history charts.
package monitor

import (
	"context"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

const (
	Interval  = 5 * time.Second
	liveSize  = int(time.Hour / Interval)
	Retention = 30 * 24 * time.Hour
)

// Point is one sample (or an average over a bucket).
type Point struct {
	T     int64   `json:"t"`
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	Swap  float64 `json:"swap"`
	Load1 float64 `json:"load1"`
	Disk  float64 `json:"disk"`
	DiskR float64 `json:"disk_r"`
	DiskW float64 `json:"disk_w"`
	NetRx float64 `json:"net_rx"`
	NetTx float64 `json:"net_tx"`
}

type Monitor struct {
	st *store.Store

	mu   sync.RWMutex
	live []Point // ring, oldest first once full
	// previous counters for rates
	prevT                        time.Time
	prevR, prevW, prevRx, prevTx uint64
	minute                       []Point
}

func New(st *store.Store) *Monitor { return &Monitor{st: st} }

func (m *Monitor) sample(ctx context.Context) (Point, bool) {
	now := time.Now()
	p := Point{T: now.Unix()}
	if c, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(c) > 0 {
		p.CPU = c[0]
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		p.Mem = v.UsedPercent
	}
	if s, err := mem.SwapMemoryWithContext(ctx); err == nil && s.Total > 0 {
		p.Swap = s.UsedPercent
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		p.Load1 = l.Load1
	}
	if u, err := disk.UsageWithContext(ctx, "/"); err == nil {
		p.Disk = u.UsedPercent
	}
	var r, w, rx, tx uint64
	if io, err := disk.IOCountersWithContext(ctx); err == nil {
		for name, c := range io {
			if isPartition(name) || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
				continue // whole disks only: partitions would double count
			}
			r += c.ReadBytes
			w += c.WriteBytes
		}
	}
	if n, err := net.IOCountersWithContext(ctx, true); err == nil {
		for _, c := range n {
			if c.Name == "lo" || strings.HasPrefix(c.Name, "veth") || strings.HasPrefix(c.Name, "docker") || strings.HasPrefix(c.Name, "br-") {
				continue // loopback and container interfaces count traffic twice
			}
			rx += c.BytesRecv
			tx += c.BytesSent
		}
	}
	first := m.prevT.IsZero()
	if dt := now.Sub(m.prevT).Seconds(); !first && dt > 0 {
		rate := func(cur, prev uint64) float64 {
			if cur < prev {
				return 0 // counter reset
			}
			return float64(cur-prev) / dt
		}
		p.DiskR, p.DiskW, p.NetRx, p.NetTx = rate(r, m.prevR), rate(w, m.prevW), rate(rx, m.prevRx), rate(tx, m.prevTx)
	}
	m.prevT, m.prevR, m.prevW, m.prevRx, m.prevTx = now, r, w, rx, tx
	return p, !first
}

// isPartition skips sda1, nvme0n1p1, vda2 … but keeps sda, nvme0n1, vda, dm-0.
func isPartition(name string) bool {
	if len(name) == 0 {
		return false
	}
	last := name[len(name)-1]
	if last < '0' || last > '9' {
		return false
	}
	for _, prefix := range []string{"sd", "vd", "xvd", "hd"} {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	for i := len(name) - 1; i > 0; i-- {
		if name[i] == 'p' && name[i-1] >= '0' && name[i-1] <= '9' && len(name) >= 4 && name[:4] == "nvme" {
			return true
		}
	}
	return false
}

func average(ps []Point) Point {
	var a Point
	for _, p := range ps {
		a.CPU += p.CPU
		a.Mem += p.Mem
		a.Swap += p.Swap
		a.Load1 += p.Load1
		a.Disk += p.Disk
		a.DiskR += p.DiskR
		a.DiskW += p.DiskW
		a.NetRx += p.NetRx
		a.NetTx += p.NetTx
	}
	n := float64(len(ps))
	a.CPU, a.Mem, a.Swap, a.Load1, a.Disk = a.CPU/n, a.Mem/n, a.Swap/n, a.Load1/n, a.Disk/n
	a.DiskR, a.DiskW, a.NetRx, a.NetTx = a.DiskR/n, a.DiskW/n, a.NetRx/n, a.NetTx/n
	return a
}

// Run samples until ctx ends.
func (m *Monitor) Run(ctx context.Context, logf func(string, ...any)) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		if p, ok := m.sample(ctx); ok {
			m.mu.Lock()
			m.live = append(m.live, p)
			if len(m.live) > liveSize {
				m.live = m.live[len(m.live)-liveSize:]
			}
			m.minute = append(m.minute, p)
			flush := len(m.minute) > 0 && p.T/60 != m.minute[0].T/60
			var batch []Point
			if flush {
				batch, m.minute = m.minute[:len(m.minute)-1], m.minute[len(m.minute)-1:]
			}
			m.mu.Unlock()
			if len(batch) > 0 {
				a := average(batch)
				a.T = batch[0].T / 60 * 60
				if _, err := m.st.DB.ExecContext(ctx, `INSERT OR REPLACE INTO metrics(ts, cpu, mem, swap, load1, disk, disk_r, disk_w, net_rx, net_tx)
					VALUES(?,?,?,?,?,?,?,?,?,?)`, a.T, a.CPU, a.Mem, a.Swap, a.Load1, a.Disk, a.DiskR, a.DiskW, a.NetRx, a.NetTx); err != nil {
					logf("monitor: store", "err", err)
				}
			}
			if time.Since(lastPrune) > time.Hour {
				lastPrune = time.Now()
				m.st.DB.ExecContext(ctx, `DELETE FROM metrics WHERE ts < ?`, time.Now().Add(-Retention).Unix())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Series returns points for the last d: raw 5-second samples up to an hour,
// otherwise minute rows averaged into about 300 buckets.
func (m *Monitor) Series(ctx context.Context, d time.Duration) ([]Point, error) {
	since := time.Now().Add(-d).Unix()
	if d <= time.Hour {
		m.mu.RLock()
		defer m.mu.RUnlock()
		out := []Point{}
		for _, p := range m.live {
			if p.T >= since {
				out = append(out, p)
			}
		}
		return out, nil
	}
	bucket := int64(d.Seconds()) / 300
	if bucket < 60 {
		bucket = 60
	}
	bucket = bucket / 60 * 60
	rows, err := m.st.DB.QueryContext(ctx, `SELECT ts/?1*?1 AS b, avg(cpu), avg(mem), avg(swap), avg(load1), avg(disk), avg(disk_r), avg(disk_w), avg(net_rx), avg(net_tx)
		FROM metrics WHERE ts >= ?2 GROUP BY b ORDER BY b`, bucket, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.T, &p.CPU, &p.Mem, &p.Swap, &p.Load1, &p.Disk, &p.DiskR, &p.DiskW, &p.NetRx, &p.NetTx); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Proc is one row of the process list.
type Proc struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"` // % of one core over the sampling window
	RSS     uint64  `json:"rss"`
	Cmdline string  `json:"cmdline"`
}

// TopProcesses measures CPU over a short window and returns the busiest
// processes (and the largest by memory) — at most n of each, merged.
func TopProcesses(ctx context.Context, n int) ([]Proc, error) {
	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	before := map[int32]float64{}
	for _, p := range ps {
		if t, err := p.TimesWithContext(ctx); err == nil {
			before[p.Pid] = t.User + t.System
		}
	}
	const window = 500 * time.Millisecond
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(window):
	}
	out := []Proc{}
	for _, p := range ps {
		t0, ok := before[p.Pid]
		if !ok {
			continue
		}
		t, err := p.TimesWithContext(ctx)
		if err != nil {
			continue // exited
		}
		pr := Proc{PID: p.Pid, CPU: (t.User + t.System - t0) / window.Seconds() * 100}
		pr.Name, _ = p.NameWithContext(ctx)
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil {
			pr.RSS = mi.RSS
		}
		if uids, err := p.UidsWithContext(ctx); err == nil && len(uids) > 0 {
			pr.User = strconv.Itoa(int(uids[0]))
			if u, err := user.LookupId(pr.User); err == nil {
				pr.User = u.Username
			}
		}
		pr.Cmdline, _ = p.CmdlineWithContext(ctx)
		if len(pr.Cmdline) > 300 {
			pr.Cmdline = pr.Cmdline[:300]
		}
		out = append(out, pr)
	}
	byCPU := append([]Proc(nil), out...)
	sort.Slice(byCPU, func(i, j int) bool { return byCPU[i].CPU > byCPU[j].CPU })
	byMem := append([]Proc(nil), out...)
	sort.Slice(byMem, func(i, j int) bool { return byMem[i].RSS > byMem[j].RSS })
	seen := map[int32]bool{}
	top := []Proc{}
	for _, list := range [][]Proc{byCPU, byMem} {
		for i := 0; i < len(list) && i < n; i++ {
			if !seen[list[i].PID] {
				seen[list[i].PID] = true
				top = append(top, list[i])
			}
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].CPU > top[j].CPU })
	return top, nil
}

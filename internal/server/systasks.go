package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/cron"
)

// Built-in scheduled tasks: the panel's own maintenance runs as cron jobs
// the user can see, run, reschedule or switch off (aaPanel's default tasks).

var systemCommands = map[string]bool{"ssl_renew": true, "time_sync": true, "release_memory": true, "security_scan": true}

// malwareTask is the command of a "malware_scan" job.
type malwareTask struct {
	Paths  []string `json:"paths"`
	ClamAV bool     `json:"clamav"`
}

func (s *Server) ValidateTask(ctx context.Context, typ, cmd string) error {
	switch typ {
	case "system":
		if systemCommands[cmd] {
			return nil
		}
	case "malware_scan":
		var m malwareTask
		if s.Malware == nil || json.Unmarshal([]byte(cmd), &m) != nil {
			return cron.ErrCommand
		}
		for _, p := range m.Paths {
			if _, err := s.Malware.CleanPath(p); err != nil {
				return err
			}
		}
		return nil
	}
	return cron.ErrCommand
}

func (s *Server) RunTask(ctx context.Context, typ, cmd string, out io.Writer) error {
	if typ == "malware_scan" {
		return s.malwareTask(ctx, cmd, out)
	}
	switch cmd {
	case "ssl_renew":
		if s.Sites == nil {
			return errors.New("websites are not available")
		}
		return s.Sites.RenewDue(ctx, out)
	case "time_sync":
		return s.timeSyncTask(ctx, out)
	case "release_memory":
		return releaseMemory(out)
	case "security_scan":
		return s.securityScanTask(ctx, out)
	}
	return cron.ErrCommand
}

func (s *Server) timeSyncTask(ctx context.Context, out io.Writer) error {
	if s.Toolbox == nil {
		return errors.New("toolbox not available")
	}
	if err := s.Toolbox.SyncTime(ctx); err != nil {
		return err
	}
	b, _ := exec.CommandContext(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "-p", "Timezone").Output()
	fmt.Fprintf(out, "NTP client told to correct the clock\n%s", b)
	return nil
}

func memAvailable() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "MemAvailable:"); ok {
			n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return n * 1024
		}
	}
	return 0
}

// releaseMemory writes dirty pages out and drops the page cache, dentries
// and inodes (aaPanel 釋放內存). The kernel refills the cache as files are read.
func releaseMemory(out io.Writer) error {
	before := memAvailable()
	exec.Command("sync").Run()
	if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0o200); err != nil {
		return err
	}
	after := memAvailable()
	fmt.Fprintf(out, "available memory: %d MB -> %d MB (freed %d MB)\n", before>>20, after>>20, max(after-before, 0)>>20)
	return nil
}

func (s *Server) securityScanTask(ctx context.Context, out io.Writer) error {
	if s.SecScan == nil {
		return errors.New("security scan not available")
	}
	rep, err := s.SecScan.Scan(ctx, s.secscanInput(ctx))
	if err != nil {
		return err
	}
	var crit []string
	counts := map[string]int{}
	for _, f := range rep.Findings {
		counts[f.Level]++
		if f.Level == "critical" {
			crit = append(crit, f.Code)
		}
	}
	fmt.Fprintf(out, "score %d: %d critical, %d warning, %d info\n", rep.Score, counts["critical"], counts["warning"], counts["info"])
	if len(crit) > 0 && s.Alerts != nil {
		s.Alerts.Emit(ctx, alert.KindSecurityScan, "weekly", "critical",
			fmt.Sprintf("Security scan: score %d, %d critical", rep.Score, len(crit)), strings.Join(crit, ", "), false)
	}
	return nil
}

func (s *Server) malwareTask(ctx context.Context, cmd string, out io.Writer) error {
	if s.Malware == nil {
		return errors.New("malware scan not available")
	}
	var m malwareTask
	json.Unmarshal([]byte(cmd), &m)
	if len(m.Paths) == 0 {
		m.Paths = []string{s.Malware.WebRoot}
	}
	var paths []string
	for _, p := range m.Paths {
		c, err := s.Malware.CleanPath(p)
		if err != nil {
			return err
		}
		paths = append(paths, c)
	}
	sc, err := s.Malware.Run(ctx, paths, m.ClamAV, func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) })
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%d files scanned, %d suspicious\n", sc.Files, len(sc.Findings))
	for _, f := range sc.Findings {
		fmt.Fprintf(out, "  %s\n", f.Path)
	}
	if len(sc.Findings) > 0 && s.Alerts != nil {
		s.Alerts.Emit(ctx, alert.KindMalware, strconv.FormatInt(sc.ID, 10), "critical",
			fmt.Sprintf("Webshell scan: %d suspicious files", len(sc.Findings)), sc.Findings[0].Path, false)
	}
	return nil
}

// ---- default jobs ----

type defaultJob struct {
	key, typ, cmd, schedule string
	timeout                 int
	names                   map[string]string
}

var defaultJobs = []defaultJob{
	{"ssl_renew", "system", "ssl_renew", "10 3 * * *", 3600, map[string]string{
		"en": "Renew SSL certificates (Let's Encrypt / LiteSSL)", "zh-HK": "續簽 SSL 證書（Let's Encrypt / LiteSSL）", "zh-TW": "續簽 SSL 憑證（Let's Encrypt / LiteSSL）",
		"zh-CN": "续签 SSL 证书（Let's Encrypt / LiteSSL）", "ja": "SSL 証明書の更新（Let's Encrypt / LiteSSL）"}},
	{"time_sync", "system", "time_sync", "40 4 * * *", 300, map[string]string{
		"en": "Sync server time", "zh-HK": "伺服器時間同步", "zh-TW": "伺服器時間同步", "zh-CN": "服务器时间同步", "ja": "サーバー時刻の同期"}},
	{"release_memory", "system", "release_memory", "20 5 * * *", 300, map[string]string{
		"en": "Release memory daily", "zh-HK": "每日釋放內存", "zh-TW": "每日釋放記憶體", "zh-CN": "每天释放内存", "ja": "毎日メモリ解放"}},
	{"security_scan", "system", "security_scan", "30 3 * * 1", 3600, map[string]string{
		"en": "Security scan", "zh-HK": "安全掃描", "zh-TW": "安全掃描", "zh-CN": "安全扫描", "ja": "セキュリティスキャン"}},
	{"malware_scan", "malware_scan", `{"paths":[],"clamav":false}`, "30 2 * * 0", 7200, map[string]string{
		"en": "Webshell scan of websites", "zh-HK": "網站木馬掃描", "zh-TW": "網站木馬掃描", "zh-CN": "网站木马扫描", "ja": "サイトの Webshell スキャン"}},
}

const defaultJobsKey = "cron_defaults"

// EnsureSystemJobs creates each built-in task once; one the user deleted
// stays deleted (the created keys are remembered).
func (s *Server) EnsureSystemJobs(ctx context.Context) {
	if s.Cron == nil {
		return
	}
	var done map[string]bool
	var raw string
	if s.Store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, defaultJobsKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &done)
	}
	if done == nil {
		done = map[string]bool{}
	}
	lang := s.Settings.Get().Language
	changed := false
	for _, d := range defaultJobs {
		if done[d.key] {
			continue
		}
		name := d.names[lang]
		if name == "" {
			name = d.names["en"]
		}
		if _, err := s.Cron.Create(ctx, cron.Input{Name: name, Type: d.typ, Schedule: d.schedule, Command: d.cmd, Enabled: true, Timeout: d.timeout}); err != nil {
			s.Log.Error("default task", "task", d.key, "err", err)
			continue
		}
		done[d.key], changed = true, true
	}
	if changed {
		b, _ := json.Marshal(done)
		s.Store.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, defaultJobsKey, string(b))
	}
}

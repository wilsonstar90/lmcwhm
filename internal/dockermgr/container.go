package dockermgr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// The settings window of one container (aaPanel 容器設定): details, rename,
// restart policy, commit, edit / upgrade by recreating, networks, logs, stats.

var (
	ErrComposeOwned = errors.New("docker.compose_owned")
	ErrNoLogFile    = errors.New("docker.no_log_file")
)

// ---- inspect ----

type portBinding struct{ HostIp, HostPort string }

type inspectJSON struct {
	ID      string `json:"Id"`
	Name    string
	Created string
	Image   string
	LogPath string
	State   struct {
		Status                string
		Running               bool
		StartedAt, FinishedAt string
	}
	Config struct {
		Image, WorkingDir, User, Hostname string
		Cmd, Entrypoint, Env              []string
		Labels                            map[string]string
	}
	HostConfig struct {
		RestartPolicy struct {
			Name              string
			MaximumRetryCount int
		}
		PortBindings map[string][]portBinding
		NetworkMode  string
		NanoCpus     int64
		Memory       int64
	}
	NetworkSettings struct {
		Ports    map[string][]portBinding
		Networks map[string]struct{ IPAddress, GlobalIPv6Address, Gateway, MacAddress string }
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
}

func inspect(ctx context.Context, id string) (*inspectJSON, error) {
	if !idRe.MatchString(id) {
		return nil, ErrBadName
	}
	out, err := dockerx.Run(ctx, "inspect", "--type", "container", id)
	if err != nil {
		return nil, err
	}
	var v []inspectJSON
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v) != 1 {
		return nil, fmt.Errorf("docker inspect %s: %v", id, err)
	}
	return &v[0], nil
}

type imageJSON struct {
	ID          string `json:"Id"`
	RepoDigests []string
	Config      struct{ Cmd, Entrypoint, Env []string }
}

func inspectImage(ctx context.Context, ref string) (*imageJSON, error) {
	out, err := dockerx.Run(ctx, "image", "inspect", ref)
	if err != nil {
		return nil, err
	}
	var v []imageJSON
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v) != 1 {
		return nil, fmt.Errorf("docker image inspect %s: %v", ref, err)
	}
	return &v[0], nil
}

func unix(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 1971 {
		return 0
	}
	return t.Unix()
}

type PortMap struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort string `json:"container_port"` // "80/tcp"
}

type NetInfo struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	IPv6    string `json:"ipv6"`
	Gateway string `json:"gateway"`
	MAC     string `json:"mac"`
}

type Mount struct {
	Type        string `json:"type"` // bind | volume | tmpfs
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type ContainerInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Image      string    `json:"image"`
	ImageID    string    `json:"image_id"`
	Digests    []string  `json:"digests"`
	State      string    `json:"state"`
	Created    int64     `json:"created"`
	StartedAt  int64     `json:"started_at"`
	FinishedAt int64     `json:"finished_at"`
	Ports      []PortMap `json:"ports"`
	HostPorts  []int     `json:"host_ports"`
	Networks   []NetInfo `json:"networks"`
	Cmd        []string  `json:"cmd"`
	Entrypoint []string  `json:"entrypoint"`
	Env        []string  `json:"env"`
	Mounts     []Mount   `json:"mounts"`
	Restart    string    `json:"restart"`
	MaxRetry   int       `json:"max_retry"`
	WorkingDir string    `json:"working_dir"`
	User       string    `json:"user"`
	Hostname   string    `json:"hostname"`
	Managed    bool      `json:"managed"`
	Project    string    `json:"project"`
	HasLogFile bool      `json:"has_log_file"`
}

// Info is the parsed `docker inspect` of one container.
func (m *Manager) Info(ctx context.Context, id string) (*ContainerInfo, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	in := &ContainerInfo{
		ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Image: c.Config.Image, ImageID: c.Image, State: c.State.Status,
		Created: unix(c.Created), StartedAt: unix(c.State.StartedAt), FinishedAt: unix(c.State.FinishedAt),
		Cmd: nonNil(c.Config.Cmd), Entrypoint: nonNil(c.Config.Entrypoint), Env: nonNil(c.Config.Env),
		Restart: c.HostConfig.RestartPolicy.Name, MaxRetry: c.HostConfig.RestartPolicy.MaximumRetryCount,
		WorkingDir: c.Config.WorkingDir, User: c.Config.User, Hostname: c.Config.Hostname,
		Managed: c.Config.Labels["lmcwhm.managed"] == "true", Project: c.Config.Labels["com.docker.compose.project"],
		HasLogFile: c.LogPath != "", Ports: []PortMap{}, HostPorts: []int{}, Networks: []NetInfo{}, Mounts: []Mount{}, Digests: []string{},
	}
	if in.Restart == "" {
		in.Restart = "no"
	}
	if img, err := inspectImage(ctx, c.Image); err == nil {
		in.Digests = nonNil(img.RepoDigests)
	}
	// Running containers report live bindings; stopped ones only the configured ones.
	ports := c.NetworkSettings.Ports
	if len(ports) == 0 {
		ports = c.HostConfig.PortBindings
	}
	seen := map[int]bool{}
	for cp, binds := range ports {
		if len(binds) == 0 {
			in.Ports = append(in.Ports, PortMap{ContainerPort: cp})
		}
		for _, b := range binds {
			hp, _ := strconv.Atoi(b.HostPort)
			in.Ports = append(in.Ports, PortMap{HostIP: b.HostIp, HostPort: hp, ContainerPort: cp})
			if hp > 0 && !seen[hp] && (b.HostIp == "" || b.HostIp == "0.0.0.0" || b.HostIp == "127.0.0.1" || b.HostIp == "::") {
				seen[hp] = true
				in.HostPorts = append(in.HostPorts, hp)
			}
		}
	}
	sort.Slice(in.Ports, func(i, j int) bool {
		if in.Ports[i].ContainerPort != in.Ports[j].ContainerPort {
			return in.Ports[i].ContainerPort < in.Ports[j].ContainerPort
		}
		return in.Ports[i].HostIP < in.Ports[j].HostIP
	})
	sort.Ints(in.HostPorts)
	for name, n := range c.NetworkSettings.Networks {
		in.Networks = append(in.Networks, NetInfo{Name: name, IP: n.IPAddress, IPv6: n.GlobalIPv6Address, Gateway: n.Gateway, MAC: n.MacAddress})
	}
	sort.Slice(in.Networks, func(i, j int) bool { return in.Networks[i].Name < in.Networks[j].Name })
	for _, mt := range c.Mounts {
		in.Mounts = append(in.Mounts, Mount{Type: mt.Type, Name: mt.Name, Source: mt.Source, Destination: mt.Destination, RW: mt.RW})
	}
	return in, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- edit: the container as create settings ----

// Spec rebuilds the create settings of a container, leaving out what the
// image already sets (its env and command), so the edit form stays short.
func (m *Manager) Spec(ctx context.Context, id string) (CreateInput, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return CreateInput{}, err
	}
	return spec(ctx, c), nil
}

func spec(ctx context.Context, c *inspectJSON) CreateInput {
	in := CreateInput{
		Name: strings.TrimPrefix(c.Name, "/"), Image: c.Config.Image, Restart: c.HostConfig.RestartPolicy.Name,
		Ports: []string{}, Env: []string{}, Volumes: []string{},
	}
	if in.Restart == "" {
		in.Restart = "no"
	}
	img, _ := inspectImage(ctx, c.Image)
	imgEnv := map[string]bool{}
	if img != nil {
		for _, e := range img.Config.Env {
			imgEnv[e] = true
		}
	}
	for _, e := range c.Config.Env {
		if !imgEnv[e] {
			in.Env = append(in.Env, e)
		}
	}
	for cp, binds := range c.HostConfig.PortBindings {
		cport := strings.TrimSuffix(cp, "/tcp")
		for _, b := range binds {
			p := b.HostPort + ":" + cport
			if b.HostIp != "" && b.HostIp != "0.0.0.0" {
				ip := b.HostIp
				if strings.Contains(ip, ":") {
					ip = "[" + ip + "]"
				}
				p = ip + ":" + p
			}
			in.Ports = append(in.Ports, p)
		}
	}
	sort.Strings(in.Ports)
	for _, mt := range c.Mounts {
		src := mt.Source
		switch mt.Type {
		case "volume":
			src = mt.Name // anonymous volumes keep their data under their generated name
		case "bind":
		default:
			continue
		}
		v := src + ":" + mt.Destination
		if !mt.RW {
			v += ":ro"
		}
		in.Volumes = append(in.Volumes, v)
	}
	if nm := c.HostConfig.NetworkMode; nm != "" && nm != "default" && nm != "bridge" && idRe.MatchString(nm) {
		in.Network = nm
	}
	if c.HostConfig.NanoCpus > 0 {
		in.CPUs = strconv.FormatFloat(float64(c.HostConfig.NanoCpus)/1e9, 'f', -1, 64)
	}
	if mem := c.HostConfig.Memory; mem > 0 {
		in.Memory = strconv.FormatInt(mem, 10)
		if mem%(1<<20) == 0 {
			in.Memory = strconv.FormatInt(mem>>20, 10) + "m"
		}
	}
	if img == nil || strings.Join(c.Config.Cmd, "\x00") != strings.Join(img.Config.Cmd, "\x00") {
		in.Command = shellJoin(c.Config.Cmd)
		// "sh -c X" round-trips to X, as create runs commands that way.
		if len(c.Config.Cmd) == 3 && c.Config.Cmd[0] == "sh" && c.Config.Cmd[1] == "-c" {
			in.Command = c.Config.Cmd[2]
		}
	}
	return in
}

func shellJoin(a []string) string {
	out := make([]string, len(a))
	for i, s := range a {
		if s == "" || strings.ContainsAny(s, " \t\n'\"\\$`;&|<>(){}*?!#~") {
			s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
		out[i] = s
	}
	return strings.Join(out, " ")
}

// editable refuses the panel's own containers and Compose services (which
// their project recreates).
func editable(c *inspectJSON) error {
	if c.Config.Labels["lmcwhm.managed"] == "true" {
		return ErrManaged
	}
	if c.Config.Labels["com.docker.compose.project"] != "" {
		return ErrComposeOwned
	}
	return nil
}

// Recreate replaces a container with new settings (task). Volumes and bind
// mounts are kept; the old container is restored if the new one fails.
func (m *Manager) Recreate(ctx context.Context, id string, in CreateInput, pull bool) (string, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return "", err
	}
	if err := editable(c); err != nil {
		return "", err
	}
	if in.Name == "" {
		in.Name = strings.TrimPrefix(c.Name, "/")
	}
	if _, err := in.args(); err != nil {
		return "", err
	}
	return m.tasks.Start("docker.recreate:"+in.Name, func(ctx context.Context, log tasks.Logf) error {
		if pull || !dockerx.ImageExists(ctx, in.Image) {
			log("pulling %s", in.Image)
			if _, err := dockerx.Run(ctx, "pull", "-q", in.Image); err != nil {
				return err
			}
		}
		return recreate(ctx, log, c, in)
	}), nil
}

// Upgrade pulls image (default: the current one) and recreates the container
// with it when the image changed, or always with force (task).
func (m *Manager) Upgrade(ctx context.Context, id, image string, forcePull, force bool) (string, error) {
	c, err := inspect(ctx, id)
	if err != nil {
		return "", err
	}
	if err := editable(c); err != nil {
		return "", err
	}
	in := spec(ctx, c)
	if image = strings.TrimSpace(image); image != "" {
		in.Image = image
	}
	if _, err := in.args(); err != nil {
		return "", err
	}
	return m.tasks.Start("docker.upgrade:"+in.Name, func(ctx context.Context, log tasks.Logf) error {
		if forcePull || !dockerx.ImageExists(ctx, in.Image) {
			log("pulling %s", in.Image)
			out, err := dockerx.Run(ctx, "pull", in.Image)
			for _, l := range strings.Split(out, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					log("%s", l)
				}
			}
			if err != nil {
				return err
			}
		}
		img, err := inspectImage(ctx, in.Image)
		if err != nil {
			return err
		}
		if img.ID == c.Image && !force {
			log("%s is already on the latest %s; nothing to do", in.Name, in.Image)
			return nil
		}
		return recreate(ctx, log, c, in)
	}), nil
}

func recreate(ctx context.Context, log tasks.Logf, c *inspectJSON, in CreateInput) error {
	args, err := in.args()
	if err != nil {
		return err
	}
	old := strings.TrimPrefix(c.Name, "/")
	backup := old + "_lmcwhm_old"
	_ = dockerx.Remove(ctx, backup)
	log("stopping %s", old)
	if _, err := dockerx.Run(ctx, "stop", c.ID); err != nil {
		return err
	}
	if _, err := dockerx.Run(ctx, "rename", c.ID, backup); err != nil {
		return err
	}
	restore := func(cause error) error {
		log("restoring the previous container: %v", cause)
		_ = dockerx.Remove(ctx, in.Name)
		dockerx.Run(ctx, "rename", c.ID, old)
		if c.State.Running {
			dockerx.Run(ctx, "start", c.ID)
		}
		return cause
	}
	log("docker %s", strings.Join(redactEnv(args), " "))
	newID, err := dockerx.Run(ctx, args...)
	if err != nil {
		return restore(err)
	}
	// Reattach the extra networks the old container was on.
	for name := range c.NetworkSettings.Networks {
		if name == in.Network || (in.Network == "" && name == "bridge") || !idRe.MatchString(name) {
			continue
		}
		if _, err := dockerx.Run(ctx, "network", "connect", name, newID); err != nil {
			log("network %s: %v", name, err)
		}
	}
	if _, err := dockerx.Run(ctx, "rm", c.ID); err != nil {
		log("removing the previous container: %v", err)
	}
	log("container %s recreated (%s)", in.Name, newID[:min(12, len(newID))])
	return nil
}

// ---- small changes ----

func (m *Manager) guard(ctx context.Context, id string) error {
	if !idRe.MatchString(id) {
		return ErrBadName
	}
	if m.managed(ctx, id) {
		return ErrManaged
	}
	return nil
}

func (m *Manager) Rename(ctx context.Context, id, name string) error {
	if err := m.guard(ctx, id); err != nil {
		return err
	}
	if !idRe.MatchString(name) {
		return ErrBadName
	}
	_, err := dockerx.Run(ctx, "rename", id, name)
	return err
}

// SetRestart changes the restart policy in place (docker update).
func (m *Manager) SetRestart(ctx context.Context, id, policy string, retries int) error {
	if err := m.guard(ctx, id); err != nil {
		return err
	}
	switch policy {
	case "no", "always", "unless-stopped":
	case "on-failure":
		if retries > 0 {
			policy += ":" + strconv.Itoa(min(retries, 1000))
		}
	default:
		return ErrBadSpec
	}
	_, err := dockerx.Run(ctx, "update", "--restart", policy, id)
	return err
}

// Commit saves the container's filesystem as image repo:tag (task).
func (m *Manager) Commit(ctx context.Context, id, repo, tag, message string) (string, error) {
	if !idRe.MatchString(id) {
		return "", ErrBadName
	}
	if tag == "" {
		tag = "latest"
	}
	ref := repo + ":" + tag
	if !imageRe.MatchString(ref) || strings.ContainsAny(tag, ":/@") || len(message) > 500 {
		return "", ErrBadSpec
	}
	return m.tasks.Start("docker.commit:"+ref, func(ctx context.Context, log tasks.Logf) error {
		args := []string{"commit"}
		if message != "" {
			args = append(args, "-m", message)
		}
		log("docker commit %s %s", id, ref)
		out, err := dockerx.Run(ctx, append(args, id, ref)...)
		if err == nil {
			log("image %s (%s)", ref, out)
		}
		return err
	}), nil
}

// Network connects or disconnects the container.
func (m *Manager) Network(ctx context.Context, id, network, action string) error {
	if err := m.guard(ctx, id); err != nil {
		return err
	}
	if !idRe.MatchString(network) || (action != "connect" && action != "disconnect") {
		return ErrBadSpec
	}
	_, err := dockerx.Run(ctx, "network", action, network, id)
	return err
}

// ---- logs ----

func logArgs(id string, since, until int64, tail int) []string {
	a := []string{"logs", "--timestamps"}
	if since > 0 {
		a = append(a, "--since", strconv.FormatInt(since, 10))
	}
	if until > 0 {
		a = append(a, "--until", strconv.FormatInt(until, 10))
	}
	if tail > 0 {
		a = append(a, "--tail", strconv.Itoa(tail))
	}
	return append(a, id)
}

// LogsRange returns the last tail lines between since and until (unix, 0 = open).
func (m *Manager) LogsRange(ctx context.Context, id string, since, until int64, tail int) (string, error) {
	if !idRe.MatchString(id) {
		return "", ErrBadName
	}
	out, err := exec.CommandContext(ctx, "docker", logArgs(id, since, until, tail)...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// StreamLogs writes the whole range to w (download).
func (m *Manager) StreamLogs(ctx context.Context, w io.Writer, id string, since, until int64) error {
	if !idRe.MatchString(id) {
		return ErrBadName
	}
	cmd := exec.CommandContext(ctx, "docker", logArgs(id, since, until, 0)...)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

// ClearLogs truncates the container's json log file.
func (m *Manager) ClearLogs(ctx context.Context, id string) error {
	c, err := inspect(ctx, id)
	if err != nil {
		return err
	}
	if c.LogPath == "" || !filepath.IsAbs(c.LogPath) {
		return ErrNoLogFile
	}
	return os.Truncate(c.LogPath, 0)
}

// ---- stats ----

type Usage struct {
	CPU      float64 `json:"cpu"` // % of one core
	MemUsed  int64   `json:"mem_used"`
	MemLimit int64   `json:"mem_limit"`
	NetRx    int64   `json:"net_rx"`
	NetTx    int64   `json:"net_tx"`
	BlkRead  int64   `json:"blk_read"`
	BlkWrite int64   `json:"blk_write"`
	PIDs     int     `json:"pids"`
	At       int64   `json:"at"`
}

var sizeRe = regexp.MustCompile(`^([0-9.]+)\s*([a-zA-Z]*)$`)

// parseSize reads docker's human sizes: "1.5MiB", "12.3kB", "0B".
func parseSize(s string) int64 {
	mt := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if mt == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(mt[1], 64)
	mult := map[string]float64{
		"": 1, "b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	}[strings.ToLower(mt[2])]
	return int64(math.Round(f * mult))
}

func pair(s string) (int64, int64) {
	a, b, _ := strings.Cut(s, "/")
	return parseSize(a), parseSize(b)
}

func parseUsage(s statsLine) Usage {
	u := Usage{At: time.Now().Unix()}
	u.CPU, _ = strconv.ParseFloat(strings.TrimSuffix(s.CPUPerc, "%"), 64)
	u.MemUsed, u.MemLimit = pair(s.MemUsage)
	u.NetRx, u.NetTx = pair(s.NetIO)
	u.BlkRead, u.BlkWrite = pair(s.BlockIO)
	u.PIDs, _ = strconv.Atoi(s.PIDs)
	return u
}

// Stats samples one container's usage.
func (m *Manager) Stats(ctx context.Context, id string) (Usage, error) {
	if !idRe.MatchString(id) {
		return Usage{}, ErrBadName
	}
	st, err := jsonLines[statsLine](ctx, "stats", "--no-stream", "--no-trunc", id)
	if err != nil {
		return Usage{}, err
	}
	if len(st) == 0 {
		return Usage{At: time.Now().Unix()}, nil
	}
	return parseUsage(st[0]), nil
}

// ---- overview ----

type Overview struct {
	Containers struct {
		Total   int    `json:"total"`
		Running int    `json:"running"`
		Size    string `json:"size"`
	} `json:"containers"`
	Compose struct {
		Total   int `json:"total"`
		Running int `json:"running"`
	} `json:"compose"`
	Images struct {
		Total int    `json:"total"`
		Size  string `json:"size"`
	} `json:"images"`
	Volumes struct {
		Total int    `json:"total"`
		Size  string `json:"size"`
	} `json:"volumes"`
	Networks struct {
		Total int `json:"total"`
	} `json:"networks"`
	Registries struct {
		Total int `json:"total"`
	} `json:"registries"`
}

// Overview counts every kind of resource for the tiles on top.
func (m *Manager) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	df, err := jsonLines[struct{ Type, TotalCount, Active, Size string }](ctx, "system", "df")
	if err != nil {
		return o, err
	}
	for _, d := range df {
		n, _ := strconv.Atoi(d.TotalCount)
		switch d.Type {
		case "Images":
			o.Images.Total, o.Images.Size = n, d.Size
		case "Containers":
			o.Containers.Total, o.Containers.Size = n, d.Size
			o.Containers.Running, _ = strconv.Atoi(d.Active)
		case "Local Volumes":
			o.Volumes.Total, o.Volumes.Size = n, d.Size
		}
	}
	// "Active" counts containers that exist, not running ones on some versions.
	if ids, err := dockerx.Run(ctx, "ps", "-q"); err == nil {
		o.Containers.Running = len(strings.Fields(ids))
	}
	if nets, err := dockerx.Run(ctx, "network", "ls", "-q"); err == nil {
		o.Networks.Total = len(strings.Fields(nets))
	}
	if entries, err := os.ReadDir(filepath.Join(m.Home, "compose")); err == nil {
		running := map[string]bool{}
		if ls, err := jsonLines[struct{ Name, Status string }](ctx, "compose", "ls"); err == nil {
			for _, p := range ls {
				running[p.Name] = strings.Contains(p.Status, "running")
			}
		}
		for _, e := range entries {
			if e.IsDir() && projectRe.MatchString(e.Name()) {
				o.Compose.Total++
				if running[e.Name()] {
					o.Compose.Running++
				}
			}
		}
	}
	o.Registries.Total = len(m.Registries())
	return o, nil
}

// ---- registries ----

type Registry struct {
	Server   string `json:"server"`
	Username string `json:"username"`
}

func dockerConfig() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return filepath.Join(d, "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker", "config.json")
}

// Registries lists the registries docker is logged in to.
func (m *Manager) Registries() []Registry {
	out := []Registry{}
	b, err := os.ReadFile(dockerConfig())
	if err != nil {
		return out
	}
	var cfg struct {
		Auths map[string]struct{ Auth string } `json:"auths"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return out
	}
	for server, a := range cfg.Auths {
		r := Registry{Server: server}
		if raw, err := base64.StdEncoding.DecodeString(a.Auth); err == nil {
			r.Username, _, _ = strings.Cut(string(raw), ":")
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

var (
	registryRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.\-]*(:\d{1,5})?(/[a-zA-Z0-9._\-/]*)?$`)
	userRe     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@+\-]{0,127}$`)
)

// Login stores credentials for a registry ("" = Docker Hub).
func (m *Manager) Login(ctx context.Context, server, user, password string) error {
	server = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(server), "https://"), "http://")
	if (server != "" && !registryRe.MatchString(server)) || !userRe.MatchString(user) || password == "" {
		return ErrBadSpec
	}
	args := []string{"login", "--username", user, "--password-stdin"}
	if server != "" {
		args = append(args, server)
	}
	_, err := dockerx.RunInput(ctx, []byte(password), args...)
	return err
}

func (m *Manager) Logout(ctx context.Context, server string) error {
	// Docker Hub is stored as https://index.docker.io/v1/.
	hub := strings.HasPrefix(server, "https://") && !strings.ContainsAny(server, " \t\n")
	if server != "" && !registryRe.MatchString(server) && !hub {
		return ErrBadSpec
	}
	args := []string{"logout"}
	if server != "" {
		args = append(args, server)
	}
	_, err := dockerx.Run(ctx, args...)
	return err
}

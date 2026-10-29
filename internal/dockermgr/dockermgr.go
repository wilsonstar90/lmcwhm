// Package dockermgr is the Docker management centre: containers, images,
// volumes, networks and Compose projects, driven through the docker CLI.
package dockermgr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

var (
	ErrManaged     = errors.New("docker.managed")
	ErrBadName     = errors.New("docker.invalid_name")
	ErrBadSpec     = errors.New("docker.invalid_spec")
	ErrBadAction   = errors.New("docker.invalid_action")
	ErrNoProject   = errors.New("docker.project_not_found")
	ErrProjectUsed = errors.New("docker.project_exists")
	ErrBadCompose  = errors.New("docker.invalid_compose")
)

var (
	idRe      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{0,127}$`)
	imageRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-/:@]{0,254}$`)
	projectRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_\-]{0,62}$`)
)

type Manager struct {
	tasks *tasks.Manager
	Home  string // /opt/lmcwhm
}

func New(tm *tasks.Manager, home string) *Manager { return &Manager{tasks: tm, Home: home} }

// jsonLines runs a docker command with --format '{{json .}}' and decodes each line.
func jsonLines[T any](ctx context.Context, args ...string) ([]T, error) {
	out, err := dockerx.Run(ctx, append(args, "--format", "{{json .}}")...)
	if err != nil {
		return nil, err
	}
	items := []T{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, sc.Err()
}

func labels(s string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// ---- containers ----

type Container struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	State     string `json:"state"`
	Status    string `json:"status"`
	Ports     string `json:"ports"`
	Created   string `json:"created"`
	Managed   bool   `json:"managed"` // created by lmcwhm: not removable here
	Project   string `json:"project"` // compose project
	CPU       string `json:"cpu"`
	Mem       string `json:"mem"`
	MemPerc   string `json:"mem_perc"`
	NetIO     string `json:"net_io"`
	BlockIO   string `json:"block_io"`
	HostPorts []int  `json:"host_ports"`
	CreatedAt int64  `json:"created_at"`
	Usage     *Usage `json:"usage"` // with stats, running containers only
}

type psLine struct {
	ID, Names, Image, State, Status, Ports, Labels, CreatedAt string
}

type statsLine struct {
	ID, CPUPerc, MemUsage, MemPerc, NetIO, BlockIO, PIDs string
}

var hostPortRe = regexp.MustCompile(`(?:0\.0\.0\.0|127\.0\.0\.1|\[::\]|::):(\d+)->`)

// Containers lists all containers; with stats, running ones include usage.
func (m *Manager) Containers(ctx context.Context, stats bool) ([]Container, error) {
	ps, err := jsonLines[psLine](ctx, "ps", "-a", "--no-trunc")
	if err != nil {
		return nil, err
	}
	usage := map[string]statsLine{}
	if stats {
		if st, err := jsonLines[statsLine](ctx, "stats", "--no-stream", "--no-trunc"); err == nil {
			for _, s := range st {
				usage[s.ID] = s
			}
		}
	}
	out := make([]Container, 0, len(ps))
	for _, p := range ps {
		lb := labels(p.Labels)
		c := Container{
			ID: p.ID, Name: p.Names, Image: p.Image, State: p.State, Status: p.Status, Ports: p.Ports, Created: p.CreatedAt,
			Managed: lb["lmcwhm.managed"] == "true", Project: lb["com.docker.compose.project"], HostPorts: []int{},
		}
		seen := map[int]bool{}
		for _, mt := range hostPortRe.FindAllStringSubmatch(p.Ports, -1) {
			var n int
			fmt.Sscan(mt[1], &n)
			if !seen[n] {
				seen[n] = true
				c.HostPorts = append(c.HostPorts, n)
			}
		}
		if u, ok := usage[p.ID]; ok {
			c.CPU, c.Mem, c.MemPerc, c.NetIO, c.BlockIO = u.CPUPerc, u.MemUsage, u.MemPerc, u.NetIO, u.BlockIO
			us := parseUsage(u)
			c.Usage = &us
		}
		if t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", p.CreatedAt); err == nil {
			c.CreatedAt = t.Unix()
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) managed(ctx context.Context, id string) bool {
	return dockerx.Label(ctx, id, "lmcwhm.managed") == "true"
}

// Action runs start|stop|restart|pause|unpause|remove on a container. The
// panel's own containers (lmcwhm.managed) can be restarted but not removed.
func (m *Manager) Action(ctx context.Context, id, action string) error {
	if !idRe.MatchString(id) {
		return ErrBadName
	}
	switch action {
	case "start", "stop", "restart", "pause", "unpause":
		_, err := dockerx.Run(ctx, action, id)
		return err
	case "remove":
		if m.managed(ctx, id) {
			return ErrManaged
		}
		_, err := dockerx.Run(ctx, "rm", "-f", id)
		return err
	}
	return ErrBadAction
}

func (m *Manager) Logs(ctx context.Context, id string, tail int) (string, error) {
	if !idRe.MatchString(id) {
		return "", ErrBadName
	}
	return dockerx.Logs(ctx, id, tail), nil
}

// Inspect returns `docker inspect` as indented JSON.
func (m *Manager) Inspect(ctx context.Context, id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", ErrBadName
	}
	out, err := dockerx.Run(ctx, "inspect", id)
	if err != nil {
		return "", err
	}
	var v any
	if json.Unmarshal([]byte(out), &v) == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			return string(b), nil
		}
	}
	return out, nil
}

type CreateInput struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Ports   []string `json:"ports"`   // "8080:80", "127.0.0.1:8080:80", "53:53/udp"
	Env     []string `json:"env"`     // KEY=value
	Volumes []string `json:"volumes"` // /host:/container[:ro] or volume:/container
	Restart string   `json:"restart"` // no | always | unless-stopped | on-failure
	Network string   `json:"network"`
	Command string   `json:"command"`
	CPUs    string   `json:"cpus"`
	Memory  string   `json:"memory"` // e.g. 512m
}

var (
	portRe   = regexp.MustCompile(`^(?:(?:\d{1,3}\.){3}\d{1,3}:|\[[0-9a-fA-F:]+\]:)?\d{1,5}(?:-\d{1,5})?:\d{1,5}(?:-\d{1,5})?(?:/(?:tcp|udp))?$`)
	envRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	volumeRe = regexp.MustCompile(`^[^:\s]+:/[^:\s]*(?::(?:ro|rw))?$`)
	cpusRe   = regexp.MustCompile(`^\d+(\.\d+)?$`)
	memRe    = regexp.MustCompile(`^\d+[bkmg]?$`)
)

func (in CreateInput) args() ([]string, error) {
	if (in.Name != "" && !idRe.MatchString(in.Name)) || !imageRe.MatchString(in.Image) {
		return nil, ErrBadSpec
	}
	a := []string{"run", "-d"}
	if in.Name != "" {
		a = append(a, "--name", in.Name)
	}
	switch in.Restart {
	case "", "no":
	case "always", "unless-stopped", "on-failure":
		a = append(a, "--restart", in.Restart)
	default:
		return nil, ErrBadSpec
	}
	for _, p := range in.Ports {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !portRe.MatchString(p) {
			return nil, ErrBadSpec
		}
		a = append(a, "-p", p)
	}
	for _, e := range in.Env {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if !envRe.MatchString(e) {
			return nil, ErrBadSpec
		}
		a = append(a, "-e", e)
	}
	for _, v := range in.Volumes {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if !volumeRe.MatchString(v) {
			return nil, ErrBadSpec
		}
		a = append(a, "-v", v)
	}
	if in.Network != "" {
		if !idRe.MatchString(in.Network) {
			return nil, ErrBadSpec
		}
		a = append(a, "--network", in.Network)
	}
	if in.CPUs != "" {
		if !cpusRe.MatchString(in.CPUs) {
			return nil, ErrBadSpec
		}
		a = append(a, "--cpus", in.CPUs)
	}
	if in.Memory != "" {
		if !memRe.MatchString(strings.ToLower(in.Memory)) {
			return nil, ErrBadSpec
		}
		a = append(a, "--memory", strings.ToLower(in.Memory))
	}
	a = append(a, in.Image)
	if c := strings.TrimSpace(in.Command); c != "" {
		a = append(a, "sh", "-c", c)
	}
	return a, nil
}

// Create pulls the image when missing and runs a new container (task).
func (m *Manager) Create(ctx context.Context, in CreateInput) (string, error) {
	args, err := in.args()
	if err != nil {
		return "", err
	}
	return m.tasks.Start("docker.create:"+in.Image, func(ctx context.Context, log tasks.Logf) error {
		if !dockerx.ImageExists(ctx, in.Image) {
			log("pulling %s", in.Image)
			if _, err := dockerx.Run(ctx, "pull", "-q", in.Image); err != nil {
				return err
			}
		}
		log("docker %s", strings.Join(redactEnv(args), " "))
		id, err := dockerx.Run(ctx, args...)
		if err != nil {
			return err
		}
		log("container %s started", id[:min(12, len(id))])
		return nil
	}), nil
}

// redactEnv hides environment values (often secrets) in task logs.
func redactEnv(args []string) []string {
	out := append([]string(nil), args...)
	for i := 1; i < len(out); i++ {
		if out[i-1] == "-e" {
			if k, _, ok := strings.Cut(out[i], "="); ok {
				out[i] = k + "=***"
			}
		}
	}
	return out
}

// ---- images ----

type Image struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Size       string `json:"size"`
	Created    string `json:"created"`
}

func (m *Manager) Images(ctx context.Context) ([]Image, error) {
	raw, err := jsonLines[struct{ ID, Repository, Tag, Size, CreatedSince string }](ctx, "images")
	if err != nil {
		return nil, err
	}
	out := make([]Image, 0, len(raw))
	for _, r := range raw {
		out = append(out, Image{ID: r.ID, Repository: r.Repository, Tag: r.Tag, Size: r.Size, Created: r.CreatedSince})
	}
	return out, nil
}

func (m *Manager) Pull(ctx context.Context, image string) (string, error) {
	if !imageRe.MatchString(image) {
		return "", ErrBadSpec
	}
	return m.tasks.Start("docker.pull:"+image, func(ctx context.Context, log tasks.Logf) error {
		log("pulling %s", image)
		out, err := dockerx.Run(ctx, "pull", image)
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				log("%s", l)
			}
		}
		return err
	}), nil
}

func (m *Manager) RemoveImage(ctx context.Context, id string) error {
	if !imageRe.MatchString(id) {
		return ErrBadName
	}
	_, err := dockerx.Run(ctx, "rmi", id)
	return err
}

// Prune removes dangling images, stopped containers' unused networks, and build cache.
func (m *Manager) Prune(ctx context.Context, what string) (string, error) {
	switch what {
	case "images":
		return dockerx.Run(ctx, "image", "prune", "-f")
	case "volumes":
		return dockerx.Run(ctx, "volume", "prune", "-f")
	case "networks":
		return dockerx.Run(ctx, "network", "prune", "-f")
	case "builder":
		return dockerx.Run(ctx, "builder", "prune", "-f")
	}
	return "", ErrBadAction
}

// ---- volumes / networks ----

type Volume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
}

func (m *Manager) Volumes(ctx context.Context) ([]Volume, error) {
	raw, err := jsonLines[struct{ Name, Driver, Mountpoint string }](ctx, "volume", "ls")
	if err != nil {
		return nil, err
	}
	out := make([]Volume, 0, len(raw))
	for _, r := range raw {
		out = append(out, Volume{Name: r.Name, Driver: r.Driver, Mountpoint: r.Mountpoint})
	}
	return out, nil
}

func (m *Manager) CreateVolume(ctx context.Context, name string) error {
	if !idRe.MatchString(name) {
		return ErrBadName
	}
	_, err := dockerx.Run(ctx, "volume", "create", name)
	return err
}

func (m *Manager) RemoveVolume(ctx context.Context, name string) error {
	if !idRe.MatchString(name) {
		return ErrBadName
	}
	_, err := dockerx.Run(ctx, "volume", "rm", name)
	return err
}

type Network struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

func (m *Manager) Networks(ctx context.Context) ([]Network, error) {
	raw, err := jsonLines[struct{ ID, Name, Driver, Scope string }](ctx, "network", "ls")
	if err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(raw))
	for _, r := range raw {
		out = append(out, Network{ID: r.ID, Name: r.Name, Driver: r.Driver, Scope: r.Scope})
	}
	return out, nil
}

var subnetRe = regexp.MustCompile(`^[0-9a-fA-F.:]+/\d{1,3}$`)

func (m *Manager) CreateNetwork(ctx context.Context, name, subnet string) error {
	if !idRe.MatchString(name) || (subnet != "" && !subnetRe.MatchString(subnet)) {
		return ErrBadName
	}
	args := []string{"network", "create", "--driver", "bridge"}
	if subnet != "" {
		args = append(args, "--subnet", subnet)
	}
	_, err := dockerx.Run(ctx, append(args, name)...)
	return err
}

func (m *Manager) RemoveNetwork(ctx context.Context, name string) error {
	if !idRe.MatchString(name) {
		return ErrBadName
	}
	if name == "bridge" || name == "host" || name == "none" {
		return ErrManaged
	}
	_, err := dockerx.Run(ctx, "network", "rm", name)
	return err
}

// ---- compose projects ----

// Projects live in <home>/compose/<name>/docker-compose.yml (+ optional .env).
func (m *Manager) composeDir(name string) string { return filepath.Join(m.Home, "compose", name) }

type Project struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Running  int    `json:"running"`
	Services int    `json:"services"`
	Status   string `json:"status"`
}

func (m *Manager) compose(ctx context.Context, name string, args ...string) (string, error) {
	dir := m.composeDir(name)
	return dockerx.Run(ctx, append([]string{"compose", "--project-directory", dir, "-f", filepath.Join(dir, "docker-compose.yml"), "-p", name}, args...)...)
}

func (m *Manager) Projects(ctx context.Context) ([]Project, error) {
	entries, err := os.ReadDir(filepath.Join(m.Home, "compose"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// Live status of every compose project docker knows about.
	status := map[string]string{}
	if ls, err := jsonLines[struct{ Name, Status string }](ctx, "compose", "ls", "-a"); err == nil {
		for _, p := range ls {
			status[p.Name] = p.Status
		}
	}
	out := []Project{}
	for _, e := range entries {
		if !e.IsDir() || !projectRe.MatchString(e.Name()) {
			continue
		}
		p := Project{Name: e.Name(), Dir: m.composeDir(e.Name()), Status: status[e.Name()]}
		if svcs, err := m.compose(ctx, e.Name(), "config", "--services"); err == nil {
			p.Services = len(strings.Fields(svcs))
		}
		if ids, err := m.compose(ctx, e.Name(), "ps", "-q", "--status", "running"); err == nil {
			p.Running = len(strings.Fields(ids))
		}
		out = append(out, p)
	}
	return out, nil
}

type ProjectFiles struct {
	Compose string `json:"compose"`
	Env     string `json:"env"`
}

func (m *Manager) ProjectFiles(name string) (ProjectFiles, error) {
	if !projectRe.MatchString(name) {
		return ProjectFiles{}, ErrBadName
	}
	dir := m.composeDir(name)
	yml, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		return ProjectFiles{}, ErrNoProject
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	return ProjectFiles{Compose: string(yml), Env: string(env)}, nil
}

// SaveProject writes the files after `docker compose config` accepts them;
// create refuses an existing project.
func (m *Manager) SaveProject(ctx context.Context, name string, f ProjectFiles, create bool) error {
	if !projectRe.MatchString(name) {
		return ErrBadName
	}
	dir := m.composeDir(name)
	_, statErr := os.Stat(filepath.Join(dir, "docker-compose.yml"))
	if create && statErr == nil {
		return ErrProjectUsed
	}
	if !create && statErr != nil {
		return ErrNoProject
	}
	tmp, err := os.MkdirTemp(filepath.Join(m.Home), ".compose-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "docker-compose.yml"), []byte(f.Compose), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte(f.Env), 0o600); err != nil {
		return err
	}
	if out, err := dockerx.Run(ctx, "compose", "--project-directory", tmp, "-f", filepath.Join(tmp, "docker-compose.yml"), "config", "-q"); err != nil {
		return fmt.Errorf("%w: %v %s", ErrBadCompose, err, out)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(f.Compose), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(f.Env), 0o600)
}

// ProjectAction runs up|down|restart|pull for a project (task).
func (m *Manager) ProjectAction(ctx context.Context, name, action string) (string, error) {
	if _, err := m.ProjectFiles(name); err != nil {
		return "", err
	}
	var args []string
	switch action {
	case "up":
		args = []string{"up", "-d", "--remove-orphans"}
	case "down":
		args = []string{"down"}
	case "restart":
		args = []string{"restart"}
	case "pull":
		args = []string{"pull"}
	default:
		return "", ErrBadAction
	}
	return m.tasks.Start("compose."+action+":"+name, func(ctx context.Context, log tasks.Logf) error {
		log("docker compose -p %s %s", name, strings.Join(args, " "))
		out, err := m.compose(ctx, name, args...)
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				log("%s", l)
			}
		}
		return err
	}), nil
}

// DeleteProject stops the project (removing its containers) and deletes its files (task).
func (m *Manager) DeleteProject(ctx context.Context, name string, volumes bool) (string, error) {
	if _, err := m.ProjectFiles(name); err != nil {
		return "", err
	}
	return m.tasks.Start("compose.delete:"+name, func(ctx context.Context, log tasks.Logf) error {
		args := []string{"down", "--remove-orphans"}
		if volumes {
			args = append(args, "-v")
		}
		if out, err := m.compose(ctx, name, args...); err != nil {
			log("compose down: %v %s", err, out)
		}
		log("removing %s", m.composeDir(name))
		return os.RemoveAll(m.composeDir(name))
	}), nil
}

func (m *Manager) ProjectLogs(ctx context.Context, name string, tail int) (string, error) {
	if _, err := m.ProjectFiles(name); err != nil {
		return "", err
	}
	out, err := m.compose(ctx, name, "logs", "--no-color", "--tail", fmt.Sprint(tail))
	if err != nil {
		return err.Error(), nil
	}
	return out, nil
}

package appstore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/mem"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

var (
	ErrUnknown      = errors.New("app.unknown")
	ErrInstalled    = errors.New("app.already_installed")
	ErrNotFound     = errors.New("app.not_found")
	ErrInUse        = errors.New("app.in_use")
	ErrArch         = errors.New("app.unsupported_arch")
	ErrLowMemory    = errors.New("app.low_memory")
	ErrNoConfig     = errors.New("app.no_config")
	ErrNotService   = errors.New("app.not_service")
	ErrNotInstalled = errors.New("app.not_installed")
)

// Installed is an installed app version as shown in the UI.
type Installed struct {
	ID        int64  `json:"id"`
	App       string `json:"app"`
	Name      string `json:"name"`
	Kind      Kind   `json:"kind"`
	Version   string `json:"version"`
	Image     string `json:"image"`
	Port      int    `json:"port"`
	Container string `json:"container"`
	State     string `json:"state"` // service container state; "" for runtimes
	HasConfig bool   `json:"has_config"`
	Databases bool   `json:"databases"`
	CreatedAt int64  `json:"created_at"`
	secret    string
}

// AdminUser is the engine's superuser that the panel uses.
func (i Installed) AdminUser() string {
	switch i.App {
	case "mysql", "mariadb":
		return "root"
	case "postgres":
		return "postgres"
	case "mongodb":
		return "root"
	case "sqlserver":
		return "sa"
	}
	return ""
}

func (i Installed) Password() string { return i.secret }

type Service struct {
	st    *store.Store
	box   *secrets.Box
	tasks *tasks.Manager
	Home  string // /opt/lmcwhm
	// InUse reports whether something (e.g. a website) still depends on an app version.
	InUse func(ctx context.Context, app, version string) bool
}

func New(st *store.Store, box *secrets.Box, tm *tasks.Manager, home string) *Service {
	return &Service{st: st, box: box, tasks: tm, Home: home}
}

func (sv *Service) dir(app, version string) string {
	return filepath.Join(sv.Home, "apps", app, version)
}

// ---- persistence ----

func (sv *Service) list(ctx context.Context, where string, args ...any) ([]Installed, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, app, version, port, secret, created_at FROM apps `+where+` ORDER BY app, version DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Installed{}
	for rows.Next() {
		var i Installed
		var sealed string
		if err := rows.Scan(&i.ID, &i.App, &i.Version, &i.Port, &sealed, &i.CreatedAt); err != nil {
			return nil, err
		}
		a, v, ok := Lookup(i.App, i.Version)
		if !ok {
			continue // dropped from the catalog
		}
		i.Name, i.Kind, i.Image, i.Databases = a.Name, a.Kind, v.Image, a.Databases
		i.HasConfig = configFile(i.App) != ""
		if a.Kind != KindRuntime {
			i.Container = ContainerName(i.App, i.Version)
		}
		if i.secret, err = sv.box.Open(sealed); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// List returns installed apps with live container states.
func (sv *Service) List(ctx context.Context) ([]Installed, error) {
	all, err := sv.list(ctx, "")
	if err != nil {
		return nil, err
	}
	for k := range all {
		if all[k].Container != "" {
			all[k].State = dockerx.State(ctx, all[k].Container)
		}
	}
	return all, nil
}

func (sv *Service) Get(ctx context.Context, id int64) (Installed, error) {
	all, err := sv.list(ctx, "WHERE id = ?", id)
	if err != nil {
		return Installed{}, err
	}
	if len(all) == 0 {
		return Installed{}, ErrNotFound
	}
	i := all[0]
	if i.Container != "" {
		i.State = dockerx.State(ctx, i.Container)
	}
	return i, nil
}

func (sv *Service) find(ctx context.Context, app, version string) (Installed, error) {
	all, err := sv.list(ctx, "WHERE app = ? AND version = ?", app, version)
	if err != nil {
		return Installed{}, err
	}
	if len(all) == 0 {
		return Installed{}, ErrNotInstalled
	}
	return all[0], nil
}

// RuntimeImage returns the image of an installed runtime version.
func (sv *Service) RuntimeImage(ctx context.Context, app, version string) (string, error) {
	i, err := sv.find(ctx, app, version)
	if err != nil {
		return "", err
	}
	if i.Kind != KindRuntime {
		return "", ErrUnknown
	}
	return i.Image, nil
}

// UsedPorts lists the loopback ports held by installed services.
func (sv *Service) UsedPorts(ctx context.Context) map[int]bool {
	used := map[int]bool{}
	all, _ := sv.list(ctx, "")
	for _, i := range all {
		if i.Port > 0 {
			used[i.Port] = true
		}
	}
	return used
}

// ---- install ----

func (sv *Service) pickPort(ctx context.Context, preferred int, avoid map[int]bool) int {
	used := sv.UsedPorts(ctx)
	for p := preferred; p < preferred+100; p++ {
		if !used[p] && !avoid[p] && webenv.PortFree(p) {
			return p
		}
	}
	return 0
}

// newPassword satisfies SQL Server's complexity rule and needs no quoting anywhere.
func newPassword() string {
	return "Lw7" + settings.RandomString(21, settings.Alnum)
}

// Install pulls and starts an app version (task). avoid lists ports reserved
// elsewhere (website projects).
func (sv *Service) Install(ctx context.Context, key, version string, avoid map[int]bool) (string, error) {
	a, v, ok := Lookup(key, version)
	if !ok {
		return "", ErrUnknown
	}
	if _, err := sv.find(ctx, key, version); err == nil {
		return "", ErrInstalled
	}
	if a.AMD64Only && runtime.GOARCH != "amd64" {
		return "", ErrArch
	}
	if a.MinMemMB > 0 {
		if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil && vm.Total < uint64(a.MinMemMB)<<20 {
			return "", fmt.Errorf("%w: %d", ErrLowMemory, a.MinMemMB)
		}
	}
	return sv.tasks.Start("app.install:"+key+"-"+version, func(ctx context.Context, log tasks.Logf) error {
		if !dockerx.Available(ctx) {
			return dockerx.ErrNoDocker
		}
		if !dockerx.ImageExists(ctx, v.Image) {
			log("pulling %s", v.Image)
			if _, err := dockerx.Run(ctx, "pull", "-q", v.Image); err != nil {
				return err
			}
		}
		inst := Installed{App: key, Version: version, Kind: a.Kind, Image: v.Image}
		if a.Kind != KindRuntime {
			inst.Port = sv.pickPort(ctx, a.Port, avoid)
			if inst.Port == 0 {
				return fmt.Errorf("no free port near %d", a.Port)
			}
			inst.secret = newPassword()
			inst.Container = ContainerName(key, version)
			dir := sv.dir(key, version)
			// Data kept by an earlier uninstall belongs to the old admin
			// password, so it cannot be reused; set it aside instead of deleting it.
			if _, err := os.Stat(dir); err == nil {
				old := fmt.Sprintf("%s.old-%d", dir, time.Now().Unix())
				if err := os.Rename(dir, old); err != nil {
					return err
				}
				log("previous data moved to %s", old)
			}
			if err := sv.prepare(ctx, inst, dir); err != nil {
				return err
			}
			log("starting %s on 127.0.0.1:%d", inst.Container, inst.Port)
			if err := sv.start(ctx, inst, log); err != nil {
				dockerx.Remove(ctx, inst.Container)
				os.RemoveAll(dir)
				return err
			}
		}
		_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO apps(app, version, port, secret, created_at) VALUES(?,?,?,?,?)`,
			key, version, inst.Port, sv.box.Seal(inst.secret), time.Now().Unix())
		if err != nil {
			return err
		}
		if pmaEngine(key) {
			if err := sv.RefreshPMA(ctx); err != nil {
				log("phpMyAdmin: %v", err)
			}
		}
		log("%s %s installed", a.Name, version)
		return nil
	}), nil
}

// start creates/starts the service container and waits until it answers.
func (sv *Service) start(ctx context.Context, i Installed, log tasks.Logf) error {
	if err := webenv.EnsureContainer(ctx, log, i.Container, sv.args(i), nil, true); err != nil {
		return err
	}
	log("waiting for %s to accept connections", i.Container)
	return sv.waitReady(ctx, i)
}

// Boot recreates service containers whose settings changed after an upgrade.
func (sv *Service) Boot(ctx context.Context) error {
	if !dockerx.Available(ctx) {
		return nil
	}
	all, err := sv.list(ctx, "")
	if err != nil {
		return err
	}
	if err := sv.RefreshPMA(ctx); err != nil {
		return err
	}
	return sv.tasks.RunSync(ctx, func(ctx context.Context) error {
		for _, i := range all {
			if i.Container == "" || dockerx.State(ctx, i.Container) == "" {
				continue // removed by hand: the user can reinstall
			}
			if err := webenv.EnsureContainer(ctx, func(string, ...any) {}, i.Container, sv.args(i), nil, false); err != nil {
				return err
			}
		}
		return nil
	})
}

// Uninstall removes an app version; data is kept unless removeData.
func (sv *Service) Uninstall(ctx context.Context, id int64, removeData bool) (string, error) {
	i, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if sv.InUse != nil && sv.InUse(ctx, i.App, i.Version) {
		return "", ErrInUse
	}
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM databases WHERE app_id = ?`, id).Scan(&n)
	if n > 0 {
		return "", ErrInUse
	}
	return sv.tasks.Start("app.uninstall:"+i.App+"-"+i.Version, func(ctx context.Context, log tasks.Logf) error {
		if i.Container != "" {
			log("removing container %s", i.Container)
			if err := dockerx.Remove(ctx, i.Container); err != nil {
				return err
			}
		}
		if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id); err != nil {
			return err
		}
		if pmaEngine(i.App) {
			if err := sv.RefreshPMA(ctx); err != nil {
				log("phpMyAdmin: %v", err)
			}
		}
		if removeData {
			log("removing %s", sv.dir(i.App, i.Version))
			os.RemoveAll(sv.dir(i.App, i.Version))
			if _, err := dockerx.Run(ctx, "rmi", i.Image); err == nil {
				log("image %s removed", i.Image)
			}
		}
		log("%s %s uninstalled", i.Name, i.Version)
		return nil
	}), nil
}

// ---- service control ----

func (sv *Service) service(ctx context.Context, id int64) (Installed, error) {
	i, err := sv.Get(ctx, id)
	if err != nil {
		return i, err
	}
	if i.Container == "" {
		return i, ErrNotService
	}
	return i, nil
}

// Control starts, stops or restarts a service container.
func (sv *Service) Control(ctx context.Context, id int64, action string) error {
	i, err := sv.service(ctx, id)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		return webenv.EnsureContainer(ctx, func(string, ...any) {}, i.Container, sv.args(i), nil, true)
	case "stop", "restart":
		_, err := dockerx.Run(ctx, action, i.Container)
		return err
	}
	return ErrUnknown
}

func (sv *Service) Logs(ctx context.Context, id int64, n int) (string, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return "", err
	}
	return dockerx.Logs(ctx, i.Container, n), nil
}

// ---- configuration file ----

// configFile is the user-editable config inside the app directory ("" = none).
func configFile(app string) string {
	switch app {
	case "mysql", "mariadb":
		return "conf/lmcwhm.cnf"
	case "redis":
		return "conf/redis.conf"
	}
	return ""
}

func (sv *Service) Config(ctx context.Context, id int64) (string, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return "", err
	}
	if configFile(i.App) == "" {
		return "", ErrNoConfig
	}
	b, err := os.ReadFile(filepath.Join(sv.dir(i.App, i.Version), configFile(i.App)))
	return string(b), err
}

// SaveConfig writes the config and restarts the service; when it does not
// come back, the previous file is restored (task).
func (sv *Service) SaveConfig(ctx context.Context, id int64, body string) (string, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return "", err
	}
	if configFile(i.App) == "" {
		return "", ErrNoConfig
	}
	path := filepath.Join(sv.dir(i.App, i.Version), configFile(i.App))
	return sv.tasks.Start("app.config:"+i.App+"-"+i.Version, func(ctx context.Context, log tasks.Logf) error {
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
		log("restarting %s", i.Container)
		dockerx.Run(ctx, "restart", i.Container)
		if err := sv.waitReady(ctx, i); err == nil {
			log("configuration applied")
			return nil
		} else {
			log("service did not come back: %v", err)
		}
		log("restoring the previous configuration")
		os.WriteFile(path, old, 0o644)
		dockerx.Run(ctx, "restart", i.Container)
		if err := sv.waitReady(ctx, i); err != nil {
			return err
		}
		return errors.New("app.config_rejected")
	}), nil
}

// ---- container specifications ----

func (sv *Service) prepare(ctx context.Context, i Installed, dir string) error {
	for _, d := range []string{"data", "conf"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	switch i.App {
	case "mysql", "mariadb":
		cnf := "[mysqld]\ncharacter-set-server = utf8mb4\ncollation-server = utf8mb4_unicode_ci\nskip-name-resolve\nmax_connections = 500\n# innodb_buffer_pool_size = 256M\n"
		return os.WriteFile(filepath.Join(dir, "conf", "lmcwhm.cnf"), []byte(cnf), 0o644)
	case "redis":
		conf := fmt.Sprintf("# Generated by lmcwhm; the port is published on 127.0.0.1:%d.\nbind 0.0.0.0\nport 6379\nrequirepass %s\nappendonly yes\ndir /data\nmaxmemory-policy allkeys-lru\n", i.Port, i.secret)
		return os.WriteFile(filepath.Join(dir, "conf", "redis.conf"), []byte(conf), 0o644)
	case "sqlserver":
		return os.Chown(filepath.Join(dir, "data"), 10001, 0) // the image runs as mssql (10001)
	case "phpmyadmin":
		return sv.preparePMA(ctx, i, dir)
	}
	return nil
}

// args is the `docker run` tail for a service. Databases sit on the default
// bridge and publish their standard port on loopback only.
func (sv *Service) args(i Installed) []string {
	dir := sv.dir(i.App, i.Version)
	data := filepath.Join(dir, "data")
	conf := filepath.Join(dir, "conf")
	pub := func(inner int) []string { return []string{"-p", fmt.Sprintf("127.0.0.1:%d:%d", i.Port, inner)} }
	var a []string
	switch i.App {
	case "mysql":
		a = append(pub(3306), "-e", "MYSQL_ROOT_PASSWORD="+i.secret, "-v", data+":/var/lib/mysql", "-v", conf+":/etc/mysql/conf.d:ro", i.Image)
	case "mariadb":
		a = append(pub(3306), "-e", "MARIADB_ROOT_PASSWORD="+i.secret, "-v", data+":/var/lib/mysql", "-v", conf+":/etc/mysql/conf.d:ro", i.Image)
	case "postgres":
		a = append(pub(5432), "-e", "POSTGRES_PASSWORD="+i.secret, "-v", data+":/var/lib/postgresql/data", i.Image)
	case "mongodb":
		a = append(pub(27017), "-e", "MONGO_INITDB_ROOT_USERNAME=root", "-e", "MONGO_INITDB_ROOT_PASSWORD="+i.secret, "-v", data+":/data/db", i.Image)
	case "sqlserver":
		a = append(pub(1433), "-e", "ACCEPT_EULA=Y", "-e", "MSSQL_SA_PASSWORD="+i.secret, "-v", data+":/var/opt/mssql", i.Image)
	case "redis":
		a = append(pub(6379), "-v", data+":/data", "-v", conf+":/usr/local/etc/redis:ro", i.Image, "redis-server", "/usr/local/etc/redis/redis.conf")
	case "memcached":
		a = append(pub(11211), i.Image, "memcached", "-m", "64")
	case "adminer":
		// Host network so it reaches every database on 127.0.0.1; the panel proxies /adminer/ to it.
		a = []string{"--network", "host", i.Image, "php", "-S", fmt.Sprintf("127.0.0.1:%d", i.Port), "-t", "/var/www/html"}
	case "phpmyadmin":
		a = sv.pmaArgs(i)
	}
	return a
}

// ---- readiness ----

// waitReady polls the engine until it answers three times in a row (database
// images restart once after initialising, which a single answer would miss).
func (sv *Service) waitReady(ctx context.Context, i Installed) error {
	start := time.Now()
	deadline := start.Add(3 * time.Minute)
	ok := 0
	var last error
	for time.Now().Before(deadline) {
		if last = sv.ping(ctx, i); last == nil {
			if ok++; ok >= 3 {
				return nil
			}
		} else {
			ok = 0
			if dockerx.State(ctx, i.Container) != "running" && time.Since(start) > 20*time.Second {
				return fmt.Errorf("%s is not running: %s", i.Container, dockerx.Logs(ctx, i.Container, 20))
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("%s did not become ready: %v", i.Container, last)
}

func (sv *Service) ping(ctx context.Context, i Installed) error {
	switch i.App {
	case "mysql", "mariadb", "postgres", "sqlserver":
		_, err := sv.Query(ctx, i, "SELECT 1;")
		return err
	case "mongodb":
		_, err := sv.Mongo(ctx, i, "db.adminCommand({ping: 1}).ok")
		return err
	case "redis":
		out, err := dockerx.Run(ctx, "exec", "-e", "REDISCLI_AUTH="+i.secret, i.Container, "redis-cli", "ping")
		if err == nil && out != "PONG" {
			err = errors.New(out)
		}
		return err
	case "adminer", "phpmyadmin":
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", i.Port), time.Second)
		if err == nil {
			c.Close()
		}
		return err
	}
	if s := dockerx.State(ctx, i.Container); s != "running" {
		return fmt.Errorf("state %q", s)
	}
	return nil
}

// ---- engine access ----

// Query runs SQL statements as the engine's superuser inside its container
// (over TCP, so an initialising server that only listens on its socket does
// not count as ready).
func (sv *Service) Query(ctx context.Context, i Installed, sqlText string) (string, error) {
	switch i.App {
	case "mysql":
		return dockerx.RunInput(ctx, []byte(sqlText), "exec", "-i", "-e", "MYSQL_PWD="+i.secret, i.Container,
			"mysql", "-uroot", "-h127.0.0.1", "-P3306", "--batch", "--skip-column-names")
	case "mariadb":
		return dockerx.RunInput(ctx, []byte(sqlText), "exec", "-i", "-e", "MYSQL_PWD="+i.secret, i.Container,
			"mariadb", "-uroot", "-h127.0.0.1", "-P3306", "--batch", "--skip-column-names")
	case "postgres":
		return dockerx.RunInput(ctx, []byte(sqlText), "exec", "-i", "-e", "PGPASSWORD="+i.secret, i.Container,
			"psql", "-h", "127.0.0.1", "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-qtA")
	case "sqlserver":
		var err error
		for _, bin := range []string{"/opt/mssql-tools18/bin/sqlcmd", "/opt/mssql-tools/bin/sqlcmd"} {
			var out string
			out, err = dockerx.Run(ctx, "exec", "-e", "SQLCMDPASSWORD="+i.secret, i.Container,
				bin, "-S", "127.0.0.1", "-U", "sa", "-C", "-b", "-h", "-1", "-Q", sqlText)
			if err == nil || !strings.Contains(err.Error(), "no such file") {
				return out, err
			}
		}
		return "", err
	}
	return "", ErrUnknown
}

// Mongo evaluates JavaScript with mongosh as the root user.
func (sv *Service) Mongo(ctx context.Context, i Installed, js string) (string, error) {
	return dockerx.Run(ctx, "exec", i.Container, "mongosh", "--quiet", "--host", "127.0.0.1",
		"-u", "root", "-p", i.secret, "--authenticationDatabase", "admin", "--eval", js)
}

// AdminerPort returns the loopback port of an installed Adminer (0 = none).
func (sv *Service) AdminerPort(ctx context.Context) int {
	all, err := sv.list(ctx, "WHERE app = 'adminer'")
	if err != nil || len(all) == 0 {
		return 0
	}
	return all[0].Port
}

package dockermgr

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestCreateArgs(t *testing.T) {
	a, err := CreateInput{
		Name: "web", Image: "nginx:1.27", Ports: []string{"8081:80", "127.0.0.1:8443:443/tcp"},
		Env: []string{"A=1", "SECRET=x y"}, Volumes: []string{"/srv/www:/usr/share/nginx/html:ro", "data:/data"},
		Restart: "unless-stopped", Memory: "512M", CPUs: "1.5", Command: "nginx -g 'daemon off;'",
	}.args()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(a, " ")
	for _, want := range []string{"--name web", "-p 8081:80", "-p 127.0.0.1:8443:443/tcp", "-e SECRET=x y", "--restart unless-stopped", "--memory 512m", "nginx:1.27 sh -c"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if r := strings.Join(redactEnv(a), " "); strings.Contains(r, "x y") {
		t.Errorf("env not redacted: %s", r)
	}
	for _, bad := range []CreateInput{
		{Image: "--privileged"},
		{Image: "nginx", Name: "-x"},
		{Image: "nginx", Ports: []string{"80:80 --privileged"}},
		{Image: "nginx", Env: []string{"--net=host"}},
		{Image: "nginx", Volumes: []string{"/:/host --privileged"}},
		{Image: "nginx", Restart: "sometimes"},
		{Image: "nginx", Network: "--host"},
	} {
		if _, err := bad.args(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestLabelsAndPorts(t *testing.T) {
	lb := labels("lmcwhm.managed=true,com.docker.compose.project=blog")
	if lb["lmcwhm.managed"] != "true" || lb["com.docker.compose.project"] != "blog" {
		t.Errorf("labels = %v", lb)
	}
	m := hostPortRe.FindAllStringSubmatch("0.0.0.0:8080->80/tcp, [::]:8080->80/tcp, 127.0.0.1:9000->9000/tcp", -1)
	if len(m) != 3 || m[0][1] != "8080" || m[2][1] != "9000" {
		t.Errorf("ports = %v", m)
	}
}

func TestParseUsage(t *testing.T) {
	u := parseUsage(statsLine{CPUPerc: "12.5%", MemUsage: "512MiB / 1.5GiB", NetIO: "1.2kB / 3MB", BlockIO: "0B / 4.1MB", PIDs: "7"})
	if u.CPU != 12.5 || u.MemUsed != 512<<20 || u.MemLimit != 3<<29 || u.NetRx != 1200 || u.NetTx != 3e6 || u.BlkWrite != 4.1e6 || u.PIDs != 7 {
		t.Errorf("usage = %+v", u)
	}
	if parseSize("--") != 0 {
		t.Error("bad size parsed")
	}
}

func TestSpec(t *testing.T) {
	c := &inspectJSON{Name: "/web"}
	c.Config.Image = "nginx:1.27"
	c.Config.Env = []string{"TZ=Asia/Hong_Kong"}
	c.Config.Cmd = []string{"sh", "-c", "nginx -g 'daemon off;'"}
	c.HostConfig.RestartPolicy.Name = "always"
	c.HostConfig.PortBindings = map[string][]portBinding{"80/tcp": {{HostIp: "127.0.0.1", HostPort: "8081"}}, "53/udp": {{HostPort: "53"}}}
	c.HostConfig.Memory = 512 << 20
	c.HostConfig.NanoCpus = 1.5e9
	c.HostConfig.NetworkMode = "bridge"
	c.Mounts = []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}{{Type: "bind", Source: "/srv/www", Destination: "/usr/share/nginx/html"}, {Type: "volume", Name: "abc123", Source: "/var/lib/docker/volumes/abc123/_data", Destination: "/data", RW: true}}
	in := spec(context.Background(), c)
	want := CreateInput{
		Name: "web", Image: "nginx:1.27", Restart: "always", Ports: []string{"127.0.0.1:8081:80", "53:53/udp"}, Env: []string{"TZ=Asia/Hong_Kong"},
		Volumes: []string{"/srv/www:/usr/share/nginx/html:ro", "abc123:/data"}, CPUs: "1.5", Memory: "512m", Command: "nginx -g 'daemon off;'",
	}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("spec =\n%+v\nwant\n%+v", in, want)
	}
	if _, err := in.args(); err != nil {
		t.Errorf("spec does not round-trip: %v", err)
	}
	if got := shellJoin([]string{"redis-server", "--save", "", "a b"}); got != `redis-server --save '' 'a b'` {
		t.Errorf("shellJoin = %s", got)
	}
}

package dockermgr

import (
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

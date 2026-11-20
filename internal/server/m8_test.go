package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/dockermgr"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
)

func TestAlertSourcesSkipUninstalledApps(t *testing.T) {
	e := newEnv(t, nil)
	dir := t.TempDir()
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.Apps = appstore.New(e.srv.Store, box, nil, dir)
	if _, err := e.srv.Store.DB.Exec(`INSERT INTO apps(app, version, secret, created_at) VALUES('mariadb', '11.4', ?, 1)`, box.Seal("")); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' '{"Names":"lmcwhm-mongodb70","State":"running","Labels":"lmcwhm.managed=true"}' '{"Names":"lmcwhm-mariadb114","State":"running","Labels":"lmcwhm.managed=true"}' '{"Names":"lmcwhm-nginx","State":"running","Labels":"lmcwhm.managed=true"}'
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	e.srv.Docker = dockermgr.New(nil, dir)
	states := e.srv.AlertSources().Services(context.Background())
	if _, ok := states["lmcwhm-mongodb70"]; ok {
		t.Fatal("uninstalled MongoDB appeared in service alerts")
	}
	if states["lmcwhm-mariadb114"] != "running" || states["lmcwhm-nginx"] != "running" {
		t.Fatalf("installed or core services missing: %v", states)
	}
}

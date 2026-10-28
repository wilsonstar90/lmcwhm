package wordpress

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadCoreKeepsLongPaths(t *testing.T) {
	long := "wp-includes/php-ai-client/third-party/Http/Discovery/Exception/StrategyUnavailableExceptionWithAVeryLongName.php"
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, body := range map[string]string{"wordpress/index.php": "<?php", "wordpress/" + long: "x", "../evil.php": "no"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zip" {
			w.Write(zbuf.Bytes())
			return
		}
		w.Write([]byte(`{"offers":[{"download":"` + srv.URL + `/zip","current":"9.9"}]}`))
	}))
	defer srv.Close()
	coreAPI = srv.URL + "/api"
	newClient = srv.Client
	root := t.TempDir()
	if err := downloadCore(context.Background(), root, t.Logf); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, long)); string(b) != "x" {
		t.Fatal("long path lost")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.php")); err == nil {
		t.Fatal("entry outside wordpress/ extracted")
	}
}

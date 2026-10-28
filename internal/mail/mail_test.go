package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestConfigParsing(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	sv := New(st, dir)
	ctx := context.Background()
	cfg := sv.cfgDir()
	os.MkdirAll(filepath.Join(cfg, "opendkim", "keys", "a.com"), 0o755)
	os.WriteFile(filepath.Join(cfg, "opendkim", "keys", "a.com", "mail.txt"), []byte(
		"mail._domainkey\tIN\tTXT\t( \"v=DKIM1; h=sha256; k=rsa; \"\n\t  \"p=MIIBIjAN\"\n\t  \"BgkqhkiG9w0B\" )  ; ----- DKIM key mail for a.com\n"), 0o644)
	os.WriteFile(filepath.Join(cfg, "postfix-accounts.cf"), []byte("bob@a.com|{SHA512-CRYPT}$6$x\nann@a.com|{SHA512-CRYPT}$6$y\n"), 0o644)
	os.WriteFile(filepath.Join(cfg, "postfix-virtual.cf"), []byte("info@a.com bob@a.com,ann@a.com\n@a.com bob@a.com\n"), 0o644)
	if k := sv.DKIM("a.com"); k != "v=DKIM1; h=sha256; k=rsa; p=MIIBIjANBgkqhkiG9w0B" {
		t.Fatalf("dkim %q", k)
	}
	accs, _ := sv.Accounts(ctx)
	if len(accs) != 2 || accs[0].Address != "ann@a.com" {
		t.Fatalf("accounts %+v", accs)
	}
	al := sv.Aliases()
	if len(al) != 2 || len(al[0].To) != 2 || al[1].From != "@a.com" {
		t.Fatalf("aliases %+v", al)
	}
	sv.saveSettings(ctx, Settings{Enabled: true, Hostname: "mail.a.com"})
	recs := sv.Records(ctx, "a.com", "1.2.3.4", "")
	var got []string
	for _, r := range recs {
		got = append(got, r.Type+" "+r.Name)
	}
	if strings.Join(got, ",") != "A mail.a.com,MX a.com,TXT a.com,TXT mail._domainkey.a.com,TXT _dmarc.a.com" {
		t.Fatalf("records %v", got)
	}
	if _, err := sv.checkAddr(ctx, "bob@a.com"); err != ErrNoDomain {
		t.Fatalf("unknown domain: %v", err)
	}
	sv.addDomainRow(ctx, "a.com")
	if a, err := sv.checkAddr(ctx, " Bob@A.com "); err != nil || a != "bob@a.com" {
		t.Fatalf("addr %q %v", a, err)
	}
	if _, err := sv.checkAddr(ctx, "bad addr@a.com"); err != ErrAddress {
		t.Fatal("bad address accepted")
	}
	doms, _ := sv.Domains(ctx)
	if len(doms) != 1 || doms[0].Accounts != 2 || doms[0].DKIM == "" {
		t.Fatalf("domains %+v", doms)
	}
	if parentDomain("mail.a.com") != "a.com" || parentDomain("a.com") != "a.com" {
		t.Fatal("parentDomain")
	}
}

package geoip

import (
	"os"
	"testing"
)

func TestPrivate(t *testing.T) {
	d := New(t.TempDir())
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.9", "::1", "fe80::1", "100.64.1.1", "::ffff:172.16.0.1"} {
		if !d.Lookup(ip).Private {
			t.Errorf("%s not private", ip)
		}
	}
	if d.Lookup("8.8.8.8").Private || d.Lookup("bad").Country != "" {
		t.Error("public / invalid")
	}
}

// LMCWHM_GEOIP_DIR points at downloaded databases (country.mmdb, asn.mmdb).
func TestLookupReal(t *testing.T) {
	dir := os.Getenv("LMCWHM_GEOIP_DIR")
	if dir == "" {
		t.Skip("LMCWHM_GEOIP_DIR not set")
	}
	d := New(dir)
	i := d.Lookup("8.8.8.8")
	if i.Country != "US" || i.ASN != 15169 {
		t.Fatalf("8.8.8.8 = %+v", i)
	}
	if st := d.Status(); st.Edition == "" || !st.HasASN || st.Built == 0 {
		t.Fatalf("status = %+v", st)
	}
}

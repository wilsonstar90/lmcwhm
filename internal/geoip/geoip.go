// Package geoip looks up the location and network of IP addresses in local
// DB-IP Lite databases (CC BY 4.0, https://db-ip.com), downloaded monthly
// without an account: country (or city) plus ASN.
package geoip

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

var ErrEdition = errors.New("geoip.invalid_edition")

// Info is what the UI shows for an address. Names are keyed by language
// (en, ja, zh-CN, …); the browser names the country itself from Country.
type Info struct {
	IP      string            `json:"ip"`
	Private bool              `json:"private,omitempty"` // loopback / LAN / link-local
	Country string            `json:"country,omitempty"` // ISO code, e.g. "HK"
	Region  map[string]string `json:"region,omitempty"`
	City    map[string]string `json:"city,omitempty"`
	ASN     uint              `json:"asn,omitempty"`
	Org     string            `json:"org,omitempty"`
}

type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
}

type asnRecord struct {
	Number uint   `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

// Status describes the installed databases.
type Status struct {
	Edition   string `json:"edition"` // country | city | "" (none)
	Built     int64  `json:"built"`   // database build time
	Size      int64  `json:"size"`
	HasASN    bool   `json:"has_asn"`
	Updating  bool   `json:"updating"`
	LastError string `json:"last_error,omitempty"`
}

type DB struct {
	Dir string // /opt/lmcwhm/geoip
	// BaseURL is DB-IP's download location (overridden in tests).
	BaseURL string

	mu       sync.RWMutex
	loc, asn *maxminddb.Reader
	edition  string
	updating bool
	lastErr  string
	cache    sync.Map // ip → Info
}

func New(dir string) *DB {
	d := &DB{Dir: dir, BaseURL: "https://download.db-ip.com/free"}
	d.open()
	return d
}

func (d *DB) path(name string) string { return filepath.Join(d.Dir, name+".mmdb") }

// open (re)loads whatever databases are on disk; city wins over country.
func (d *DB) open() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range []*maxminddb.Reader{d.loc, d.asn} {
		if r != nil {
			r.Close()
		}
	}
	d.loc, d.asn, d.edition = nil, nil, ""
	for _, ed := range []string{"city", "country"} {
		if r, err := maxminddb.Open(d.path(ed)); err == nil {
			d.loc, d.edition = r, ed
			break
		}
	}
	if r, err := maxminddb.Open(d.path("asn")); err == nil {
		d.asn = r
	}
	d.cache.Clear()
}

func (d *DB) Status() Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	st := Status{Edition: d.edition, HasASN: d.asn != nil, Updating: d.updating, LastError: d.lastErr}
	if d.loc != nil {
		st.Built = int64(d.loc.Metadata.BuildEpoch)
		if fi, err := os.Stat(d.path(d.edition)); err == nil {
			st.Size = fi.Size()
		}
	}
	return st
}

func private(a netip.Addr) bool {
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(a) // CGNAT
}

// Lookup returns what is known about ip ("" fields when the databases are missing).
func (d *DB) Lookup(ip string) Info {
	if v, ok := d.cache.Load(ip); ok {
		return v.(Info)
	}
	info := Info{IP: ip}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return info
	}
	a = a.Unmap()
	if private(a) {
		info.Private = true
		return info
	}
	d.mu.RLock()
	if d.loc != nil {
		var r record
		if d.loc.Lookup(a).Decode(&r) == nil {
			info.Country = r.Country.ISOCode
			if len(r.City.Names) > 0 {
				info.City = r.City.Names
			}
			if len(r.Subdivisions) > 0 && len(r.Subdivisions[0].Names) > 0 {
				info.Region = r.Subdivisions[0].Names
			}
		}
	}
	if d.asn != nil {
		var r asnRecord
		if d.asn.Lookup(a).Decode(&r) == nil {
			info.ASN, info.Org = r.Number, r.Org
		}
	}
	d.mu.RUnlock()
	d.cache.Store(ip, info)
	return info
}

// Update downloads the edition (country | city) and the ASN database for the
// current month, falling back to last month early in the month.
func (d *DB) Update(ctx context.Context, edition string) error {
	if edition != "country" && edition != "city" {
		return ErrEdition
	}
	d.mu.Lock()
	if d.updating {
		d.mu.Unlock()
		return nil
	}
	d.updating = true
	d.mu.Unlock()
	err := d.update(ctx, edition)
	d.mu.Lock()
	d.updating = false
	d.lastErr = ""
	if err != nil {
		d.lastErr = err.Error()
	}
	d.mu.Unlock()
	if err == nil {
		if edition == "city" {
			os.Remove(d.path("country"))
		} else {
			os.Remove(d.path("city"))
		}
		d.open()
	}
	return err
}

func (d *DB) update(ctx context.Context, edition string) error {
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{edition, "asn"} {
		var err error
		for _, month := range []time.Time{time.Now().UTC(), time.Now().UTC().AddDate(0, -1, 0)} {
			url := fmt.Sprintf("%s/dbip-%s-lite-%s.mmdb.gz", d.BaseURL, name, month.Format("2006-01"))
			if err = d.fetch(ctx, url, d.path(name)); err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// fetch downloads and gunzips url, then validates it before replacing dst.
func (d *DB) fetch(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, res.Status)
	}
	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(gz, 1<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		var r *maxminddb.Reader
		if r, err = maxminddb.Open(tmp); err == nil {
			r.Close()
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// RefreshLoop installs the country edition on first start and refreshes the
// installed edition once its build is over 35 days old.
func (d *DB) RefreshLoop(ctx context.Context, log func(string, ...any)) {
	for {
		st := d.Status()
		ed := st.Edition
		if ed == "" {
			ed = "country"
		}
		if st.Edition == "" || !st.HasASN || time.Since(time.Unix(st.Built, 0)) > 35*24*time.Hour {
			if err := d.Update(ctx, ed); err != nil {
				log("geoip update failed", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

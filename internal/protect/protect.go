// Package protect keeps website protection (網站防護: fake spiders, data
// centres, scrapers, CC, CDN-only) and dedicated rules (專屬規則), and
// turns them into what the edge proxy enforces (webenv.Protection).
package protect

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/geoip"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

var (
	ErrBadRule     = errors.New("protect.invalid_rule")
	ErrBadSettings = errors.New("protect.invalid_settings")
	ErrNotFound    = errors.New("protect.not_found")
	ErrBuiltin     = errors.New("protect.builtin")
)

// Cond is one condition of a rule (see webenv.Fields / webenv.Ops).
type Cond struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Negate bool     `json:"negate"`
	Values []string `json:"values"`
}

type Rule struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	CMS         string `json:"cms"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Builtin     bool   `json:"builtin"`
	All         bool   `json:"match_all"` // all conditions (else any)
	Conds       []Cond `json:"conditions"`
	Modified    bool   `json:"modified,omitempty"` // a built-in rule whose conditions differ from its default
	Renamed     bool   `json:"renamed,omitempty"`  // a built-in rule with its own name / description
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type ASN struct {
	Number uint   `json:"number"`
	Name   string `json:"name"`
}

// Settings are the panel-wide parts.
type Settings struct {
	RulesOn  bool     `json:"rules_on"` // dedicated rules switch
	Crawlers []string `json:"crawlers"` // User-Agents refused by 爬蟲防護
	IDC      []ASN    `json:"idc"`      // networks refused by 禁 IDC
}

const settingsKey = "protect"

type Service struct {
	st  *store.Store
	geo *geoip.DB
	fw  *firewall.Service

	mu     sync.Mutex
	seeded bool
	idcKey string // what the cached ranges were built from
	idc    []string
}

func New(st *store.Store, geo *geoip.DB, fw *firewall.Service) *Service {
	return &Service{st: st, geo: geo, fw: fw}
}

// ---- settings ----

func (sv *Service) Settings(ctx context.Context) Settings {
	s := Settings{Crawlers: defaultCrawlers, IDC: defaultIDC}
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (sv *Service) SaveSettings(ctx context.Context, s Settings) (Settings, error) {
	s.Crawlers = clean(s.Crawlers)
	if len(s.Crawlers) > 500 || len(s.IDC) > 500 {
		return s, ErrBadSettings
	}
	for _, v := range s.Crawlers {
		if !validValue(v) {
			return s, ErrBadSettings
		}
	}
	for _, a := range s.IDC {
		if a.Number == 0 || a.Number > 4294967295 || len(a.Name) > 100 {
			return s, ErrBadSettings
		}
	}
	b, _ := json.Marshal(s)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, string(b))
	return s, err
}

func clean(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// validValue: one line of a pattern file (no control characters; "#" would start a comment).
func validValue(v string) bool {
	return v != "" && len(v) <= 512 && !strings.HasPrefix(v, "#") && strings.IndexFunc(v, unicode.IsControl) < 0
}

// ---- sites ----

// Default is what a site without saved settings gets: protection on, every
// check off (dedicated rules then apply when their switch is on).
var Default = webenv.SiteProtect{On: true}

func (sv *Service) Sites(ctx context.Context) (map[string]webenv.SiteProtect, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT domain, settings FROM site_protect`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]webenv.SiteProtect{}
	for rows.Next() {
		var d, raw string
		if err := rows.Scan(&d, &raw); err != nil {
			return nil, err
		}
		p := Default
		json.Unmarshal([]byte(raw), &p)
		out[d] = p
	}
	return out, rows.Err()
}

// SaveSites stores the settings of each domain.
func (sv *Service) SaveSites(ctx context.Context, by map[string]webenv.SiteProtect) error {
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for d, p := range by {
		if p.CC < 0 || p.CC > 100000 || d == "" {
			return ErrBadSettings
		}
		b, _ := json.Marshal(p)
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_protect(domain, settings) VALUES(?, ?) ON CONFLICT(domain) DO UPDATE SET settings = excluded.settings`, d, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Forget drops a deleted site's settings.
func (sv *Service) Forget(ctx context.Context, domain string) {
	sv.st.DB.ExecContext(ctx, `DELETE FROM site_protect WHERE domain = ?`, domain)
}

// ---- rules ----

func builtinByCode(code string) (Rule, bool) {
	for _, b := range builtins {
		if b.Code == code {
			return b, true
		}
	}
	return Rule{}, false
}

func condsJSON(c []Cond) string {
	b, _ := json.Marshal(c)
	return string(b)
}

// seed adds built-in rules the database does not have yet.
func (sv *Service) seed(ctx context.Context) error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.seeded {
		return nil
	}
	now := time.Now().Unix()
	for _, b := range builtins {
		if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO waf_rules(code, cms, name, description, enabled, builtin, match_all, conditions, created_at, updated_at)
			VALUES(?,?,?,?,?,1,?,?,?,?) ON CONFLICT(code) DO NOTHING`, b.Code, b.CMS, b.Name, b.Description, b.Enabled, b.All, condsJSON(b.Conds), now, now); err != nil {
			return err
		}
	}
	sv.seeded = true
	return nil
}

func (sv *Service) Rules(ctx context.Context) ([]Rule, error) {
	if err := sv.seed(ctx); err != nil {
		return nil, err
	}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, code, cms, name, description, enabled, builtin, match_all, conditions, created_at, updated_at FROM waf_rules ORDER BY builtin DESC, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		var conds string
		if err := rows.Scan(&r.ID, &r.Code, &r.CMS, &r.Name, &r.Description, &r.Enabled, &r.Builtin, &r.All, &conds, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(conds), &r.Conds)
		if b, ok := builtinByCode(r.Code); ok && r.Builtin {
			r.Modified = conds != condsJSON(b.Conds) || r.All != b.All
			r.Renamed = r.Name != b.Name || r.Description != b.Description
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *Rule) validate() error {
	r.Name, r.Description, r.CMS = strings.TrimSpace(r.Name), strings.TrimSpace(r.Description), strings.TrimSpace(strings.ToLower(r.CMS))
	if r.Name == "" || len(r.Name) > 120 || len(r.Description) > 400 || len(r.CMS) > 40 || len(r.Conds) == 0 || len(r.Conds) > 10 {
		return ErrBadRule
	}
	for i := range r.Conds {
		c := &r.Conds[i]
		if webenv.Fields[c.Field] == "" || webenv.Ops[c.Op] == "" {
			return ErrBadRule
		}
		c.Values = clean(c.Values)
		if len(c.Values) == 0 || len(c.Values) > 100 {
			return ErrBadRule
		}
		for _, v := range c.Values {
			if !validValue(v) {
				return ErrBadRule
			}
			if c.Op == "regex" {
				if _, err := regexp.Compile(v); err != nil {
					return ErrBadRule
				}
			}
		}
	}
	return nil
}

// SaveRule creates (ID 0) or changes a rule; a built-in one keeps its code.
func (sv *Service) SaveRule(ctx context.Context, r Rule) (Rule, error) {
	if err := sv.seed(ctx); err != nil {
		return r, err
	}
	if err := r.validate(); err != nil {
		return r, err
	}
	now := time.Now().Unix()
	if r.ID == 0 {
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO waf_rules(code, cms, name, description, enabled, builtin, match_all, conditions, created_at, updated_at)
			VALUES(?,?,?,?,?,0,?,?,?,?)`, fmt.Sprintf("new-%d", now), r.CMS, r.Name, r.Description, r.Enabled, r.All, condsJSON(r.Conds), now, now)
		if err != nil {
			return r, err
		}
		r.ID, _ = res.LastInsertId()
		r.Code, r.Builtin = fmt.Sprintf("c%d", r.ID), false
		_, err = sv.st.DB.ExecContext(ctx, `UPDATE waf_rules SET code = ? WHERE id = ?`, r.Code, r.ID)
		return r, err
	}
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE waf_rules SET cms=?, name=?, description=?, enabled=?, match_all=?, conditions=?, updated_at=? WHERE id=?`,
		r.CMS, r.Name, r.Description, r.Enabled, r.All, condsJSON(r.Conds), now, r.ID)
	if err != nil {
		return r, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return r, ErrNotFound
	}
	return r, nil
}

// SetEnabled switches one rule on or off.
func (sv *Service) SetEnabled(ctx context.Context, id int64, on bool) error {
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE waf_rules SET enabled = ?, updated_at = ? WHERE id = ?`, on, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (sv *Service) DeleteRule(ctx context.Context, id int64) error {
	var builtin bool
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT builtin FROM waf_rules WHERE id = ?`, id).Scan(&builtin); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if builtin {
		return ErrBuiltin // built-in rules are switched off, not deleted
	}
	_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM waf_rules WHERE id = ?`, id)
	return err
}

// ResetRule restores a built-in rule's conditions and switch.
func (sv *Service) ResetRule(ctx context.Context, id int64) error {
	var code string
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT code FROM waf_rules WHERE id = ? AND builtin = 1`, id).Scan(&code); err != nil {
		return ErrNotFound
	}
	b, ok := builtinByCode(code)
	if !ok {
		return ErrNotFound
	}
	_, err := sv.st.DB.ExecContext(ctx, `UPDATE waf_rules SET cms=?, name=?, description=?, enabled=?, match_all=?, conditions=?, updated_at=? WHERE id=?`,
		b.CMS, b.Name, b.Description, b.Enabled, b.All, condsJSON(b.Conds), time.Now().Unix(), id)
	return err
}

// ---- the edge ----

// idcRanges are the data-centre networks, rebuilt when the AS list or the
// ASN database changes (walking the database takes a moment).
func (sv *Service) idcRanges(asns []ASN) []string {
	if sv.geo == nil {
		return nil
	}
	set := map[uint]bool{}
	var nums []string
	for _, a := range asns {
		set[a.Number] = true
		nums = append(nums, fmt.Sprint(a.Number))
	}
	sort.Strings(nums)
	key := fmt.Sprint(sv.geo.ASNBuilt(), nums)
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if key != sv.idcKey {
		var out []string
		for _, p := range sv.geo.ASNRanges(set) {
			out = append(out, p.String())
		}
		sv.idc, sv.idcKey = out, key
	}
	return sv.idc
}

// IDCCount is how many ranges 禁 IDC currently refuses (0 without the ASN database).
func (sv *Service) IDCCount(ctx context.Context) int {
	return len(sv.idcRanges(sv.Settings(ctx).IDC))
}

// spiders are the User-Agent tokens of the trusted crawler lists in use:
// only a crawler whose ranges are known can be called fake.
func (sv *Service) spiders(ctx context.Context) []string {
	if sv.fw == nil {
		return []string{}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, id := range sv.fw.FeedConfig(ctx).Enabled {
		for _, ua := range spiderUA[id] {
			if !seen[ua] {
				seen[ua] = true
				out = append(out, ua)
			}
		}
	}
	sort.Strings(out)
	return out
}

// SpiderTokens is what 假蜘蛛 checks (empty: no trusted crawler list is on).
func (sv *Service) SpiderTokens(ctx context.Context) []string { return sv.spiders(ctx) }

// Edge is everything the edge enforces.
func (sv *Service) Edge(ctx context.Context) webenv.Protection {
	s := sv.Settings(ctx)
	sites, _ := sv.Sites(ctx)
	p := webenv.Protection{Sites: sites, Default: Default, SpiderUA: sv.spiders(ctx), CrawlerUA: s.Crawlers}
	if anyIDC(p.Sites) {
		p.IDC = sv.idcRanges(s.IDC)
	}
	if s.RulesOn {
		rules, _ := sv.Rules(ctx)
		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			er := webenv.EdgeRule{Code: r.Code, All: r.All}
			for _, c := range r.Conds {
				er.Conds = append(er.Conds, webenv.EdgeCond{Field: c.Field, Op: c.Op, Negate: c.Negate, Values: c.Values})
			}
			p.Rules = append(p.Rules, er)
		}
	}
	return p
}

func anyIDC(m map[string]webenv.SiteProtect) bool {
	for _, x := range m {
		if x.On && x.IDC {
			return true
		}
	}
	return false
}

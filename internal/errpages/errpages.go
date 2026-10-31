// Package errpages keeps the custom 4xx / 5xx pages and which page each
// status uses, panel-wide and per website; nginx serves them (with SSI).
package errpages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

var (
	ErrBadPage    = errors.New("errpages.invalid_page")
	ErrBadMapping = errors.New("errpages.invalid_mapping")
	ErrNotFound   = errors.New("errpages.not_found")
	ErrInUse      = errors.New("errpages.in_use")
	ErrBuiltin    = errors.New("errpages.builtin")
)

type Page struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"` // built-in pages
	Name      string `json:"name"`
	HTML      string `json:"html"`
	UpdatedAt int64  `json:"updated_at"`
	Modified  bool   `json:"modified,omitempty"`
}

// Mapping: status → page id, panel-wide and per site ("0" = nginx's own
// page, overriding the panel-wide one); Intercept also replaces the error
// pages of a website's backend (PHP, project, proxy).
type Mapping struct {
	Global    map[string]int64            `json:"global"`
	Sites     map[string]map[string]int64 `json:"sites"`
	Intercept map[string]bool             `json:"intercept"`
}

const mappingKey = "error_pages"

type Service struct {
	st     *store.Store
	mu     sync.Mutex
	seeded bool
}

func New(st *store.Store) *Service { return &Service{st: st} }

func builtinOf(code string) (Page, bool) {
	for _, b := range builtins {
		if b.Code == code {
			return b, true
		}
	}
	return Page{}, false
}

// seed adds the built-in pages and, the first time, maps 403 / 404 / 429 /
// 5xx to them.
func (sv *Service) seed(ctx context.Context) error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.seeded {
		return nil
	}
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_pages WHERE code != ''`).Scan(&n)
	if n == 0 {
		ids := map[string]int64{}
		for _, b := range builtins {
			res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO error_pages(code, name, html, updated_at) VALUES(?,?,?,?)`, b.Code, b.Name, b.HTML, time.Now().Unix())
			if err != nil {
				return err
			}
			ids[b.Code], _ = res.LastInsertId()
		}
		var raw string
		if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, mappingKey).Scan(&raw) != nil {
			m := Mapping{Global: map[string]int64{"403": ids["blocked"], "404": ids["notfound"], "429": ids["limit"],
				"500": ids["generic"], "502": ids["maintenance"], "503": ids["maintenance"], "504": ids["maintenance"]}}
			if err := sv.saveMapping(ctx, m); err != nil {
				return err
			}
		}
	}
	sv.seeded = true
	return nil
}

func (sv *Service) Pages(ctx context.Context) ([]Page, error) {
	if err := sv.seed(ctx); err != nil {
		return nil, err
	}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, code, name, html, updated_at FROM error_pages ORDER BY code = '', id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.HTML, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if b, ok := builtinOf(p.Code); ok {
			p.Modified = p.HTML != b.HTML
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SavePage creates (ID 0) or changes a page; a built-in page keeps its code.
func (sv *Service) SavePage(ctx context.Context, p Page) (Page, error) {
	if err := sv.seed(ctx); err != nil {
		return p, err
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 100 || len(p.HTML) > 256<<10 || strings.TrimSpace(p.HTML) == "" {
		return p, ErrBadPage
	}
	p.UpdatedAt = time.Now().Unix()
	if p.ID == 0 {
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO error_pages(code, name, html, updated_at) VALUES('', ?, ?, ?)`, p.Name, p.HTML, p.UpdatedAt)
		if err != nil {
			return p, err
		}
		p.ID, _ = res.LastInsertId()
		p.Code = ""
		return p, nil
	}
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE error_pages SET name = ?, html = ?, updated_at = ? WHERE id = ?`, p.Name, p.HTML, p.UpdatedAt, p.ID)
	if err != nil {
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, ErrNotFound
	}
	return p, nil
}

// ResetPage restores a built-in page.
func (sv *Service) ResetPage(ctx context.Context, id int64) error {
	var code string
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT code FROM error_pages WHERE id = ?`, id).Scan(&code); err != nil {
		return ErrNotFound
	}
	b, ok := builtinOf(code)
	if !ok {
		return ErrNotFound
	}
	_, err := sv.st.DB.ExecContext(ctx, `UPDATE error_pages SET name = ?, html = ?, updated_at = ? WHERE id = ?`, b.Name, b.HTML, time.Now().Unix(), id)
	return err
}

// DeletePage removes a custom page that no status uses.
func (sv *Service) DeletePage(ctx context.Context, id int64) error {
	var code string
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT code FROM error_pages WHERE id = ?`, id).Scan(&code); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if code != "" {
		return ErrBuiltin
	}
	m := sv.Mapping(ctx)
	used := false
	for _, v := range m.Global {
		used = used || v == id
	}
	for _, s := range m.Sites {
		for _, v := range s {
			used = used || v == id
		}
	}
	if used {
		return ErrInUse
	}
	_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM error_pages WHERE id = ?`, id)
	return err
}

func (sv *Service) Mapping(ctx context.Context) Mapping {
	sv.seed(ctx)
	m := Mapping{}
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, mappingKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &m)
	}
	if m.Global == nil {
		m.Global = map[string]int64{}
	}
	if m.Sites == nil {
		m.Sites = map[string]map[string]int64{}
	}
	if m.Intercept == nil {
		m.Intercept = map[string]bool{}
	}
	return m
}

func validCode(c string) bool {
	n, err := strconv.Atoi(c)
	return err == nil && n >= 400 && n <= 599 && strconv.Itoa(n) == c
}

// SaveMapping checks the codes and pages and stores the mapping.
func (sv *Service) SaveMapping(ctx context.Context, m Mapping) (Mapping, error) {
	pages, err := sv.Pages(ctx)
	if err != nil {
		return m, err
	}
	known := map[int64]bool{0: true}
	for _, p := range pages {
		known[p.ID] = true
	}
	check := func(x map[string]int64) error {
		for c, id := range x {
			if !validCode(c) || !known[id] {
				return ErrBadMapping
			}
		}
		return nil
	}
	if err := check(m.Global); err != nil {
		return m, err
	}
	for d, x := range m.Sites {
		if d == "" || len(x) == 0 {
			delete(m.Sites, d)
			continue
		}
		if err := check(x); err != nil {
			return m, err
		}
	}
	for c, id := range m.Global {
		if id == 0 {
			delete(m.Global, c) // nginx's own page
		}
	}
	return m, sv.saveMapping(ctx, m)
}

func (sv *Service) saveMapping(ctx context.Context, m Mapping) error {
	b, _ := json.Marshal(m)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, mappingKey, string(b))
	return err
}

// Edge is what nginx serves: every page by file name and the mapping.
func (sv *Service) Edge(ctx context.Context) webenv.ErrorPages {
	out := webenv.ErrorPages{Pages: map[string][]byte{}, Global: map[int]string{}, Sites: map[string]map[int]string{}, Intercept: map[string]bool{}}
	pages, err := sv.Pages(ctx)
	if err != nil {
		return out
	}
	name := map[int64]string{}
	for _, p := range pages {
		n := "p" + strconv.FormatInt(p.ID, 10)
		name[p.ID] = n
		out.Pages[n] = []byte(p.HTML)
	}
	m := sv.Mapping(ctx)
	conv := func(x map[string]int64) map[int]string {
		r := map[int]string{}
		for c, id := range x {
			code, _ := strconv.Atoi(c)
			if id == 0 {
				r[code] = "" // nginx's own page
			} else if n, ok := name[id]; ok {
				r[code] = n
			}
		}
		return r
	}
	out.Global = conv(m.Global)
	for d, x := range m.Sites {
		out.Sites[d] = conv(x)
	}
	for d, on := range m.Intercept {
		if on {
			out.Intercept[d] = true
		}
	}
	return out
}

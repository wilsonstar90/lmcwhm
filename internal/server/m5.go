package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/cfdns"
	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/linode"
	"github.com/wilsonstar90/lmcwhm/internal/netinfo"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func (s *Server) m5Routes(r chi.Router) {
	if s.Firewall != nil {
		r.Get("/firewall", s.handleFirewall)
		r.Put("/firewall", s.handlePutFirewall)
		r.Post("/firewall/confirm", s.handleFirewallConfirm)
		r.Post("/firewall/rules", s.handleSaveFWRule)
		r.Put("/firewall/rules/{id}", s.handleSaveFWRule)
		r.Post("/firewall/rules/{id}/delete", s.handleDeleteFWRule)
		r.Get("/firewall/ips", s.handleFWIPs)
		r.Post("/firewall/ips", s.handleAddFWIP)
		r.Post("/firewall/ips/{id}/delete", s.handleDeleteFWIP)
		r.Post("/firewall/sync", s.handleFWSync)
		r.Post("/firewall/linode/verify", s.handleLinodeVerify)
		r.Post("/firewall/linode/attach", s.handleLinodeAttach)
		r.Get("/firewall/cloudflare", s.handleCFZones)
		r.Post("/firewall/cloudflare/level", s.handleCFLevel)
		r.Get("/firewall/feeds", s.handleFWFeeds)
		r.Put("/firewall/feeds", s.handlePutFWFeeds)
		r.Post("/firewall/feeds/refresh", s.handleFWFeedsRefresh)
	}
	if s.WAF != nil {
		r.Get("/waf", s.handleWAF)
		r.Put("/waf", s.handlePutWAF)
		r.Get("/waf/events", s.handleWAFEvents)
	}
	if s.CrowdSec != nil {
		r.Get("/crowdsec", s.handleCrowdSec)
		r.Post("/crowdsec/install", s.handleCSInstall)
		r.Post("/crowdsec/uninstall", s.handleCSUninstall)
		r.Get("/crowdsec/alerts", s.handleCSAlerts)
		r.Get("/crowdsec/collections", s.handleCSCollections)
		r.Post("/crowdsec/decisions", s.handleCSAdd)
		r.Post("/crowdsec/decisions/delete", s.handleCSDelete)
	}
}

func writeFWErr(w http.ResponseWriter, err error) {
	if errors.Is(err, firewall.ErrNftRejects) {
		writeJSON(w, http.StatusBadRequest, apiError{Code: firewall.ErrNftRejects.Error(), Params: map[string]any{"detail": strings.TrimPrefix(err.Error(), firewall.ErrNftRejects.Error()+": ")}})
		return
	}
	writeCodedErr(w, err, firewall.ErrNotFound)
}

// ---- firewall ----

// closeIfArmed ends the connection after this response, so the browser's
// confirmation has to open a new one through the new ruleset.
func closeIfArmed(w http.ResponseWriter, armed bool) {
	if armed {
		w.Header().Set("Connection", "close")
	}
}

type fwView struct {
	firewall.Config
	LinodeToken    string              `json:"linode_token,omitempty"` // never returned
	HasLinodeToken bool                `json:"has_linode_token"`
	Available      bool                `json:"available"`
	Pending        bool                `json:"pending"`
	ConfirmSecs    int                 `json:"confirm_secs"`
	Protected      []int               `json:"protected_ports"`
	Live           string              `json:"live"`
	LastSync       firewall.SyncReport `json:"last_sync"`
	HasCFToken     bool                `json:"has_cf_token"`
	Rules          []firewall.Rule     `json:"rules"`
}

func (s *Server) fwView(r *http.Request) (fwView, error) {
	c, err := s.Firewall.Config(r.Context())
	if err != nil {
		return fwView{}, err
	}
	rules, err := s.Firewall.Rules(r.Context())
	if err != nil {
		return fwView{}, err
	}
	v := fwView{Config: c, HasLinodeToken: c.LinodeToken != "", Available: s.Firewall.Available(), Pending: s.Firewall.Pending(),
		ConfirmSecs: int(firewall.ConfirmWindow.Seconds()), Protected: s.Firewall.ProtectedPorts(), Live: s.Firewall.Live(r.Context()),
		LastSync: firewall.LastSync(), Rules: rules}
	v.Config.LinodeToken = ""
	if wc, err := s.Web.Get(r.Context()); err == nil {
		v.HasCFToken = wc.CFToken != ""
	}
	return v, nil
}

func (s *Server) handleFirewall(w http.ResponseWriter, r *http.Request) {
	v, err := s.fwView(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePutFirewall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		firewall.Config
		ClearLinodeToken bool `json:"clear_linode_token"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.Firewall.Config(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	apply := func(c *firewall.Config) {
		token, unconfirmed := c.LinodeToken, c.Unconfirmed
		*c = in.Config
		c.Unconfirmed = unconfirmed
		if in.Config.LinodeToken == "" {
			c.LinodeToken = token
		}
		if in.ClearLinodeToken {
			c.LinodeToken, c.LinodeFirewall, c.LinodeSync, c.LinodePorts = "", 0, false, false
		}
	}
	armed := false
	if cur.Enabled == in.Enabled && cur.Policy == in.Policy && cur.Ping == in.Ping && cur.Protect == in.Protect {
		// Cloud settings only: the server's ruleset stays as it is.
		err = s.Firewall.SaveCloud(r.Context(), apply)
	} else {
		_, armed, err = s.Firewall.Update(r.Context(), apply)
	}
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.config", "enabled="+strconv.FormatBool(in.Enabled)+" policy="+in.Policy)
	go s.syncFirewall()
	closeIfArmed(w, armed)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "confirm": armed})
}

func (s *Server) handleFirewallConfirm(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.Confirm(r.Context(), connStart(r)); err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.confirm", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSaveFWRule(w http.ResponseWriter, r *http.Request) {
	var in firewall.Rule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = idParam(r)
	rule, armed, err := s.Firewall.SaveRule(r.Context(), in)
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.rule", rule.Action+" "+rule.Proto+" "+rule.Ports)
	closeIfArmed(w, armed)
	writeJSON(w, http.StatusOK, map[string]any{"rule": rule, "confirm": armed})
}

func (s *Server) handleDeleteFWRule(w http.ResponseWriter, r *http.Request) {
	armed, err := s.Firewall.DeleteRule(r.Context(), idParam(r))
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.rule_delete", strconv.FormatInt(idParam(r), 10))
	closeIfArmed(w, armed)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "confirm": armed})
}

// handleFWIPs lists the block / allow entries; public blocklist feeds (tens of
// thousands of entries) are left out and shown as counts by /firewall/feeds.
func (s *Server) handleFWIPs(w http.ResponseWriter, r *http.Request) {
	ips, err := s.Firewall.IPs(r.Context(), r.URL.Query().Get("list"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := ips[:0]
	for _, x := range ips {
		if !strings.HasPrefix(x.Source, "feed:") {
			out = append(out, x)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleFWFeeds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Firewall.Feeds(r.Context()))
}

func (s *Server) handlePutFWFeeds(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled []string        `json:"enabled"`
		Custom  []firewall.Feed `json:"custom"`
		Hours   int             `json:"hours"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Firewall.SaveFeeds(r.Context(), in.Enabled, in.Custom, in.Hours); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "firewall.feeds", strings.Join(in.Enabled, ","))
	go s.Firewall.RefreshFeeds(context.Background())
	writeJSON(w, http.StatusOK, s.Firewall.Feeds(r.Context()))
}

func (s *Server) handleFWFeedsRefresh(w http.ResponseWriter, r *http.Request) {
	go s.Firewall.RefreshFeeds(context.Background())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAddFWIP blocks or allows an address. Blocking the address the panel
// user is connected from is refused, so a typo cannot lock them out.
func (s *Server) handleAddFWIP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IP      string `json:"ip"`
		List    string `json:"list"`
		Minutes int    `json:"minutes"` // 0 = permanent
		Remark  string `json:"remark"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.List == "block" {
		p, _, err := firewall.NormIP(in.IP)
		if a, aerr := netip.ParseAddr(clientIP(r)); err == nil && aerr == nil && p.Contains(a.Unmap()) {
			writeErr(w, http.StatusBadRequest, firewall.ErrSelf)
			return
		}
	}
	x, err := s.Firewall.AddIP(r.Context(), in.IP, in.List, time.Duration(in.Minutes)*time.Minute, "manual", in.Remark)
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall."+in.List, x.IP)
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) handleDeleteFWIP(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.DeleteIP(r.Context(), idParam(r)); err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.ip_delete", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) syncFirewall() firewall.SyncReport {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	token := ""
	if wc, err := s.Web.Get(ctx); err == nil {
		token = wc.CFToken
	}
	return s.Firewall.SyncRemote(ctx, token)
}

// SyncFirewall is the debounced hook run after the block list changes.
func (s *Server) SyncFirewall() { s.syncFirewall() }

func (s *Server) handleFWSync(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.syncFirewall())
}

func (s *Server) handleLinodeVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	decode(r, &in)
	tok := strings.TrimSpace(in.Token)
	if tok == "" {
		c, _ := s.Firewall.Config(r.Context())
		tok = c.LinodeToken
	}
	cl := linode.New(tok)
	fws, err := cl.Firewalls(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	inst, ierr := cl.InstanceByIP(r.Context(), s.serverAddrs(r.Context())...)
	out := map[string]any{"ok": true, "firewalls": fws}
	if ierr == nil {
		out["linode"] = inst
	} else {
		out["linode_error"] = ierr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLinodeAttach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Firewall int `json:"firewall"`
		Linode   int `json:"linode"`
	}
	decode(r, &in)
	c, _ := s.Firewall.Config(r.Context())
	if err := linode.New(c.LinodeToken).Attach(r.Context(), in.Firewall, in.Linode); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.auditCat(r, "security", "linode.attach", strconv.Itoa(in.Firewall))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cf(r *http.Request) *cfdns.Client {
	wc, _ := s.Web.Get(r.Context())
	return cfdns.New(wc.CFToken)
}

func (s *Server) handleCFZones(w http.ResponseWriter, r *http.Request) {
	cf := s.cf(r)
	zones, err := cf.Zones(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "items": []any{}})
		return
	}
	type zone struct {
		cfdns.Zone
		Level  string `json:"level"`
		Blocks int    `json:"blocks"`
	}
	out := make([]zone, len(zones))
	for i, z := range zones {
		out[i].Zone = z
		out[i].Level, _ = cf.SecurityLevel(r.Context(), z.ID)
		if rules, err := cf.AccessRules(r.Context(), z.ID); err == nil {
			out[i].Blocks = len(rules)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleCFLevel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Zone  string `json:"zone"`
		Level string `json:"level"`
	}
	decode(r, &in)
	switch in.Level {
	case "off", "essentially_off", "low", "medium", "high", "under_attack":
	default:
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	if err := s.cf(r).SetSecurityLevel(r.Context(), in.Zone, in.Level); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.auditCat(r, "security", "cloudflare.security_level", in.Zone+" "+in.Level)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- WAF ----

func (s *Server) handleWAF(w http.ResponseWriter, r *http.Request) {
	type siteWAF struct {
		ID      int64    `json:"id"`
		Domain  string   `json:"domain"`
		Off     bool     `json:"waf_off"`
		Exclude []string `json:"waf_exclude"`
	}
	sites := []siteWAF{}
	if list, err := s.Sites.List(r.Context()); err == nil {
		for _, st := range list {
			if !st.Passthrough {
				sites = append(sites, siteWAF{st.ID, st.Domain, st.WAFOff, st.WAFExclude})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.WAF.Settings(), "image": webenv.WAFImage, "sites": sites})
}

func (s *Server) handlePutWAF(w http.ResponseWriter, r *http.Request) {
	var in webenv.WAFSettings
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.WAF.Save(r.Context(), in); err != nil {
		writeCodedErr(w, err)
		return
	}
	s.auditCat(r, "security", "waf.config", "enabled="+strconv.FormatBool(in.Enabled)+" engine="+in.Engine)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": s.Sites.Reapply("waf.apply")})
}

func (s *Server) handleWAFEvents(w http.ResponseWriter, r *http.Request) {
	ev, err := s.WAF.Events(intParam(r, "lines", 300, 5000), r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ev})
}

// ---- CrowdSec ----

func (s *Server) handleCrowdSec(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.CrowdSec.Status(r.Context()))
}

func (s *Server) handleCSInstall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Community bool `json:"community"`
	}
	decode(r, &in)
	s.auditCat(r, "security", "crowdsec.install", "community="+strconv.FormatBool(in.Community))
	writeJSON(w, http.StatusOK, map[string]string{"task_id": s.CrowdSec.Install(r.Context(), in.Community)})
}

func (s *Server) handleCSUninstall(w http.ResponseWriter, r *http.Request) {
	s.auditCat(r, "security", "crowdsec.uninstall", "")
	writeJSON(w, http.StatusOK, map[string]string{"task_id": s.CrowdSec.Uninstall(r.Context())})
}

func (s *Server) handleCSAlerts(w http.ResponseWriter, r *http.Request) {
	out, err := s.CrowdSec.Alerts(r.Context(), intParam(r, "limit", 100, 1000))
	if err != nil {
		writeCodedErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"items":`))
	w.Write(out)
	w.Write([]byte(`}`))
}

func (s *Server) handleCSCollections(w http.ResponseWriter, r *http.Request) {
	out, err := s.CrowdSec.Collections(r.Context())
	if err != nil {
		writeCodedErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func (s *Server) handleCSAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IP     string `json:"ip"`
		Hours  int    `json:"hours"`
		Reason string `json:"reason"`
	}
	decode(r, &in)
	if in.Hours <= 0 {
		in.Hours = 4
	}
	if err := s.CrowdSec.AddDecision(r.Context(), in.IP, time.Duration(in.Hours)*time.Hour, in.Reason); err != nil {
		writeCodedErr(w, err)
		return
	}
	s.auditCat(r, "security", "crowdsec.ban", in.IP)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCSDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IP string `json:"ip"`
	}
	decode(r, &in)
	if err := s.CrowdSec.DeleteDecision(r.Context(), in.IP); err != nil {
		writeCodedErr(w, err)
		return
	}
	s.auditCat(r, "security", "crowdsec.unban", in.IP)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// serverAddrs are the addresses this server may be known by: the ones set in
// the web settings, the detected public IPs and every global interface address.
func (s *Server) serverAddrs(ctx context.Context) []string {
	var out []string
	if wc, err := s.Web.Get(ctx); err == nil {
		out = append(out, wc.ServerIPv4, wc.ServerIPv6)
	}
	out = append(out, netinfo.PublicIPv4(ctx), netinfo.PublicIPv6(ctx))
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

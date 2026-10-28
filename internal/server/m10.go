package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/cfdns"
	"github.com/wilsonstar90/lmcwhm/internal/mail"
	"github.com/wilsonstar90/lmcwhm/internal/netinfo"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// DNS Zone Editor (Cloudflare) and the mail server.

func (s *Server) m10Routes(r chi.Router) {
	if s.Web != nil {
		r.Get("/dns/zones", s.handleDNSZones)
		r.Get("/dns/zones/{zone}/records", s.handleDNSRecords)
		r.Post("/dns/zones/{zone}/records", s.handleDNSRecordSave)
		r.Put("/dns/zones/{zone}/records/{rid}", s.handleDNSRecordSave)
		r.Post("/dns/zones/{zone}/records/{rid}/delete", s.handleDNSRecordDelete)
		r.Get("/dns/templates", s.handleDNSTemplates)
		r.Post("/dns/zones/{zone}/template", s.handleDNSTemplate)
	}
	if s.Backup != nil && s.Shares != nil {
		r.Post("/backups/{id}/share", s.handleBackupShare)
	}
	if s.Mail != nil {
		r.Get("/mail", s.handleMail)
		r.Put("/mail", s.handleMailSave)
		r.Post("/mail/domains", s.handleMailDomainAdd)
		r.Post("/mail/domains/{domain}/delete", s.handleMailDomainDelete)
		r.Get("/mail/domains/{domain}/dns", s.handleMailDNS)
		r.Post("/mail/domains/{domain}/dns", s.handleMailDNSApply)
		r.Get("/mail/domains/{domain}/check", s.handleMailCheck)
		r.Get("/mail/accounts", s.handleMailAccounts)
		r.Post("/mail/accounts", s.handleMailAccountAdd)
		r.Post("/mail/accounts/update", s.handleMailAccountUpdate)
		r.Post("/mail/accounts/delete", s.handleMailAccountDelete)
		r.Post("/mail/aliases", s.handleMailAliasAdd)
		r.Post("/mail/aliases/delete", s.handleMailAliasDelete)
		r.Get("/mail/logs", s.handleMailLogs)
	}
}

// cfErr reports a Cloudflare API error with its message (CF messages have
// spaces, so writeErr would hide them).
func cfErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cfdns.ErrNoToken):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, cfdns.ErrRecord):
		writeCodeErr(w, err, cfdns.ErrRecord)
	default:
		writeJSON(w, http.StatusBadGateway, apiError{Code: "cf.api_error", Params: map[string]any{"message": err.Error()}})
	}
}

func (s *Server) handleDNSZones(w http.ResponseWriter, r *http.Request) {
	zs, err := s.cf(r).Zones(r.Context())
	if err != nil {
		cfErr(w, err)
		return
	}
	if zs == nil {
		zs = []cfdns.Zone{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": zs, "types": cfdns.RecordTypes})
}

func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	recs, err := s.cf(r).Records(r.Context(), chi.URLParam(r, "zone"))
	if err != nil {
		cfErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": recs})
}

func (s *Server) handleDNSRecordSave(w http.ResponseWriter, r *http.Request) {
	var in cfdns.FullRecord
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	zone, id := chi.URLParam(r, "zone"), chi.URLParam(r, "rid")
	var out cfdns.FullRecord
	var err error
	if id == "" {
		out, err = s.cf(r).CreateRecord(r.Context(), zone, in)
	} else {
		out, err = s.cf(r).UpdateRecord(r.Context(), zone, id, in)
	}
	if err != nil {
		cfErr(w, err)
		return
	}
	s.auditCat(r, "dns", "dns.record_save", out.Type+" "+out.Name+" "+out.Content)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDNSRecordDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.cf(r).DeleteRecord(r.Context(), chi.URLParam(r, "zone"), chi.URLParam(r, "rid")); err != nil {
		cfErr(w, err)
		return
	}
	s.auditCat(r, "dns", "dns.record_delete", chi.URLParam(r, "rid"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDNSTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": cfdns.Templates})
}

// serverIPs are the public addresses DNS records should point at.
func (s *Server) serverIPs(ctx context.Context) (string, string) {
	var v4, v6 string
	if wc, err := s.Web.Get(ctx); err == nil {
		v4, v6 = wc.ServerIPv4, wc.ServerIPv6
	}
	if v4 == "" {
		v4 = netinfo.PublicIPv4(ctx)
	}
	if v6 == "" {
		v6 = netinfo.PublicIPv6(ctx)
	}
	return v4, v6
}

func (s *Server) handleDNSTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template string            `json:"template"`
		Domain   string            `json:"domain"`
		Params   map[string]string `json:"params"`
		Preview  bool              `json:"preview"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var tpl *cfdns.Template
	for i := range cfdns.Templates {
		if cfdns.Templates[i].Key == in.Template {
			tpl = &cfdns.Templates[i]
		}
	}
	if tpl == nil {
		writeErr(w, http.StatusBadRequest, cfdns.ErrRecord)
		return
	}
	vars := map[string]string{"domain": strings.ToLower(strings.TrimSpace(in.Domain))}
	vars["ip4"], vars["ip6"] = s.serverIPs(r.Context())
	for _, p := range tpl.Params {
		vars[p] = strings.ToLower(strings.TrimSpace(in.Params[p]))
	}
	recs := cfdns.Expand(*tpl, vars)
	if in.Preview {
		writeJSON(w, http.StatusOK, map[string]any{"records": recs})
		return
	}
	n, err := s.cf(r).ApplyTemplate(r.Context(), chi.URLParam(r, "zone"), recs)
	if err != nil {
		cfErr(w, err)
		return
	}
	s.auditCat(r, "dns", "dns.template", tpl.Key+" "+vars["domain"])
	writeJSON(w, http.StatusOK, map[string]any{"added": n})
}

// handleBackupShare makes a 24-hour download link for a local backup, so a
// new server can fetch a snapshot with `lmcwhm snapshot restore <link>`.
func (s *Server) handleBackupShare(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Backup.Get(r.Context(), idParam(r))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	p, ok := s.Backup.LocalPath(rec)
	if !ok {
		writeErr(w, http.StatusBadRequest, backup.ErrBadDest)
		return
	}
	sh, err := s.Shares.Create(r.Context(), p, "", 24*time.Hour, 3, sessionFrom(r).User.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "backup", "backup.share", rec.Name)
	writeJSON(w, http.StatusOK, sh)
}

// ---- mail ----

func (s *Server) handleMail(w http.ResponseWriter, r *http.Request) {
	st := s.Mail.Status(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "aliases": s.Mail.Aliases()})
}

func (s *Server) handleMailSave(w http.ResponseWriter, r *http.Request) {
	var in mail.Settings
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "mail", "mail.settings", strconv.FormatBool(in.Enabled)+" "+in.Hostname)
	id := s.Tasks.StartLong("mail", 15*time.Minute, func(ctx context.Context, log tasks.Logf) error {
		return s.Mail.Enable(ctx, in, log)
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

func (s *Server) mailResult(w http.ResponseWriter, r *http.Request, action, detail string, err error) {
	if err != nil {
		if errors.Is(err, mail.ErrAddress) || errors.Is(err, mail.ErrPortBusy) {
			writeCodeErr(w, err, mail.ErrAddress, mail.ErrPortBusy)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "mail", action, detail)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMailDomainAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domain string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mailResult(w, r, "mail.domain_add", in.Domain, s.Mail.AddDomain(r.Context(), in.Domain))
}

func (s *Server) handleMailDomainDelete(w http.ResponseWriter, r *http.Request) {
	d := chi.URLParam(r, "domain")
	s.mailResult(w, r, "mail.domain_delete", d, s.Mail.DeleteDomain(r.Context(), d))
}

func (s *Server) handleMailDNS(w http.ResponseWriter, r *http.Request) {
	v4, v6 := s.serverIPs(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"records": s.Mail.Records(r.Context(), chi.URLParam(r, "domain"), v4, v6)})
}

// handleMailDNSApply writes the domain's mail records through Cloudflare.
func (s *Server) handleMailDNSApply(w http.ResponseWriter, r *http.Request) {
	d := chi.URLParam(r, "domain")
	cf := s.cf(r)
	zone, err := cf.ZoneFor(r.Context(), d)
	if err != nil {
		cfErr(w, err)
		return
	}
	v4, v6 := s.serverIPs(r.Context())
	var recs []cfdns.FullRecord
	for _, x := range s.Mail.Records(r.Context(), d, v4, v6) {
		fr := cfdns.FullRecord{Type: x.Type, Name: x.Name, Content: x.Content, TTL: 1, Comment: "lmcwhm mail"}
		if x.Type == "MX" {
			p := x.Priority
			fr.Priority = &p
		}
		recs = append(recs, fr)
	}
	n, err := cf.ApplyTemplate(r.Context(), zone.ID, recs)
	if err != nil {
		cfErr(w, err)
		return
	}
	s.auditCat(r, "mail", "mail.dns", d)
	writeJSON(w, http.StatusOK, map[string]any{"added": n, "zone": zone.Name})
}

func (s *Server) handleMailCheck(w http.ResponseWriter, r *http.Request) {
	v4, _ := s.serverIPs(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"items": s.Mail.Deliverability(r.Context(), chi.URLParam(r, "domain"), v4)})
}

func (s *Server) handleMailAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Mail.Accounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleMailAccountAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ Address, Password, Quota string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.Mail.AddAccount(r.Context(), in.Address, in.Password)
	if err == nil && in.Quota != "" && in.Quota != "0" {
		err = s.Mail.SetQuota(r.Context(), in.Address, in.Quota)
	}
	s.mailResult(w, r, "mail.account_add", in.Address, err)
}

func (s *Server) handleMailAccountUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct{ Address, Password, Quota string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var err error
	if in.Password != "" {
		err = s.Mail.SetPassword(r.Context(), in.Address, in.Password)
	}
	if err == nil && in.Quota != "" {
		err = s.Mail.SetQuota(r.Context(), in.Address, in.Quota)
	}
	s.mailResult(w, r, "mail.account_update", in.Address, err)
}

func (s *Server) handleMailAccountDelete(w http.ResponseWriter, r *http.Request) {
	var in struct{ Address string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mailResult(w, r, "mail.account_delete", in.Address, s.Mail.DeleteAccount(r.Context(), in.Address))
}

func (s *Server) handleMailAliasAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ From, To string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mailResult(w, r, "mail.alias_add", in.From+" → "+in.To, s.Mail.AddAlias(r.Context(), in.From, in.To))
}

func (s *Server) handleMailAliasDelete(w http.ResponseWriter, r *http.Request) {
	var in struct{ From, To string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mailResult(w, r, "mail.alias_delete", in.From+" → "+in.To, s.Mail.DeleteAlias(r.Context(), in.From, in.To))
}

func (s *Server) handleMailLogs(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(s.Mail.Dir + "/logs/mail.log")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	if len(b) > 256<<10 {
		b = b[len(b)-256<<10:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": string(b)})
}

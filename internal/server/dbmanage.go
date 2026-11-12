package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
)

// Database settings windows (status, tuning, port, logs, binary logs, Redis
// keys and persistence) and one-time phpMyAdmin sign-on links.

func (s *Server) dbManageRoutes(r chi.Router) {
	r.Get("/databases/{id}/phpmyadmin", s.handleDBPMALink)
	r.Get("/apps/{id}/phpmyadmin", s.handleRootPMALink)
	r.Get("/apps/{id}/service", s.handleAppService)
	r.Post("/apps/{id}/port", s.handleAppPort)
	r.Post("/apps/{id}/oom", s.handleAppOOM)

	r.Get("/apps/{id}/mysql/status", s.handleMySQLStatus)
	r.Put("/apps/{id}/mysql/tuning", s.handleMySQLTuning)
	r.Get("/apps/{id}/mysql/slowlog", s.handleSlowLog)
	r.Post("/apps/{id}/mysql/slowlog", s.handleSetSlowLog)
	r.Post("/apps/{id}/mysql/slowlog/clear", s.handleClearSlowLog)
	r.Get("/apps/{id}/mysql/binlogs", s.handleBinlogs)
	r.Post("/apps/{id}/mysql/binlogs", s.handleSetBinlog)
	r.Post("/apps/{id}/mysql/binlogs/purge", s.handlePurgeBinlogs)

	r.Get("/apps/{id}/redis/settings", s.handleRedisSettings)
	r.Put("/apps/{id}/redis/settings", s.handlePutRedisSettings)
	r.Post("/apps/{id}/redis/password", s.handleRedisPassword)
	r.Get("/apps/{id}/redis/info", s.handleRedisInfo)
	r.Post("/apps/{id}/redis/bgsave", s.handleRedisBGSave)
	r.Get("/apps/{id}/redis/dbs", s.handleRedisDBs)
	r.Get("/apps/{id}/redis/keys", s.handleRedisKeys)
	r.Get("/apps/{id}/redis/key", s.handleRedisKey)
	r.Post("/apps/{id}/redis/key", s.handleRedisSetKey)
	r.Post("/apps/{id}/redis/expire", s.handleRedisExpire)
	r.Post("/apps/{id}/redis/delete", s.handleRedisDelete)
	r.Post("/apps/{id}/redis/flush", s.handleRedisFlush)
}

// writeAppErr maps engine-management errors; Redis and SQL error text is
// passed through as a detail.
func writeAppErr(w http.ResponseWriter, err error) {
	var re appstore.RedisError
	switch {
	case errors.Is(err, appstore.ErrNotFound), errors.Is(err, appstore.ErrNotInstalled):
		writeErr(w, http.StatusNotFound, appstore.ErrNotFound)
	case errors.Is(err, appstore.ErrBadSetting):
		writeCodeErr(w, err, appstore.ErrBadSetting)
	case errors.As(err, &re):
		writeJSON(w, http.StatusBadRequest, apiError{Code: "app.engine_error", Params: map[string]any{"detail": re.Error()}})
	case errors.Is(err, appstore.ErrBadPort), errors.Is(err, appstore.ErrPortInUse), errors.Is(err, appstore.ErrWrongApp),
		errors.Is(err, appstore.ErrNotService), errors.Is(err, appstore.ErrNoConfig):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeJSON(w, http.StatusBadRequest, apiError{Code: "app.engine_error", Params: map[string]any{"detail": err.Error()}})
	}
}

func (s *Server) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeAppErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) respondTask(w http.ResponseWriter, r *http.Request, action, detail string, id string, err error) {
	if err != nil {
		writeAppErr(w, err)
		return
	}
	s.auditCat(r, "database", action, detail)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

// ---- phpMyAdmin sign-on ----

// pmaLinks holds one-time sign-on links: they live a minute and work once.
// It also remembers, per panel session, the credentials of the last link
// used, so phpMyAdmin can sign in again by itself when its own session
// expires (it sends the browser back to the sign-on script without a link).
type pmaLinks struct {
	mu   sync.Mutex
	m    map[string]pmaLink
	last map[string]pmaLink // by a hash of the panel session
}

// pmaRemember is how long a panel session may sign phpMyAdmin in again.
const pmaRemember = 12 * time.Hour

type pmaLink struct {
	creds   appstore.PMACredentials
	expires time.Time
}

func (l *pmaLinks) issue(c appstore.PMACredentials) string {
	b := make([]byte, 24)
	rand.Read(b)
	t := hex.EncodeToString(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]pmaLink{}
	}
	for k, v := range l.m {
		if time.Now().After(v.expires) {
			delete(l.m, k)
		}
	}
	l.m[t] = pmaLink{creds: c, expires: time.Now().Add(time.Minute)}
	return t
}

func (l *pmaLinks) take(t string) (appstore.PMACredentials, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.m[t]
	delete(l.m, t)
	if !ok || time.Now().After(v.expires) {
		return appstore.PMACredentials{}, false
	}
	return v.creds, true
}

func (l *pmaLinks) remember(session string, c appstore.PMACredentials) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]pmaLink{}
	}
	for k, v := range l.last {
		if time.Now().After(v.expires) {
			delete(l.last, k)
		}
	}
	l.last[pmaSessionKey(session)] = pmaLink{creds: c, expires: time.Now().Add(pmaRemember)}
}

func (l *pmaLinks) recall(session string) (appstore.PMACredentials, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.last[pmaSessionKey(session)]
	if !ok || time.Now().After(v.expires) {
		return appstore.PMACredentials{}, false
	}
	return v.creds, true
}

func pmaSessionKey(session string) string {
	h := sha256.Sum256([]byte("pma:" + session))
	return hex.EncodeToString(h[:])
}

// signonHeader is the header value the proxy adds on the sign-on script for
// a signed-in panel user: a valid one-time link, or (phpMyAdmin's own session
// expired) the credentials this panel session used last. "" = none.
func (s *Server) signonHeader(r *http.Request, session string) string {
	if r.URL.Path != appstore.PMASignon || r.URL.Query().Has("logout") {
		return ""
	}
	var c appstore.PMACredentials
	var ok bool
	if t := r.URL.Query().Get("t"); t != "" {
		if c, ok = s.pmaLinks.take(t); ok {
			s.pmaLinks.remember(session, c)
		}
	} else {
		c, ok = s.pmaLinks.recall(session)
	}
	if !ok {
		return ""
	}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func (s *Server) pmaURL(c appstore.PMACredentials) map[string]string {
	return map[string]string{"url": appstore.PMASignon + "?t=" + s.pmaLinks.issue(c)}
}

// handleDBPMALink signs phpMyAdmin in as the database's own user: only that
// database (and information_schema) is visible.
func (s *Server) handleDBPMALink(w http.ResponseWriter, r *http.Request) {
	d, err := s.DBs.Get(r.Context(), idParam(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, appstore.ErrNotFound)
		return
	}
	n := s.Apps.PMAServers(r.Context())[d.AppID]
	if n == 0 {
		writeErr(w, http.StatusNotFound, appstore.ErrNotInstalled)
		return
	}
	s.auditCat(r, "database", "db.phpmyadmin", d.Name)
	writeJSON(w, http.StatusOK, s.pmaURL(appstore.PMACredentials{User: d.Username, Password: d.Password, Port: d.Port, Server: n, DB: d.Name}))
}

// handleRootPMALink signs in as the engine's root (every database).
func (s *Server) handleRootPMALink(w http.ResponseWriter, r *http.Request) {
	c, err := s.Apps.PMARoot(r.Context(), idParam(r))
	if err != nil {
		writeAppErr(w, err)
		return
	}
	s.auditCat(r, "database", "db.phpmyadmin_root", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, s.pmaURL(c))
}

// ---- service ----

func (s *Server) handleAppService(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.ServiceInfo(r.Context(), idParam(r))
	s.respond(w, v, err)
}

func (s *Server) handleAppPort(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Port int `json:"port"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SetPort(r.Context(), idParam(r), in.Port)
	s.respondTask(w, r, "app.port", strconv.Itoa(in.Port), id, err)
}

func (s *Server) handleAppOOM(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SetOOMProtect(r.Context(), idParam(r), in.On)
	s.respondTask(w, r, "app.oom_protect", strconv.FormatBool(in.On), id, err)
}

// ---- MySQL / MariaDB ----

func (s *Server) handleMySQLStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.MySQLStatus(r.Context(), idParam(r))
	s.respond(w, v, err)
}

func (s *Server) handleMySQLTuning(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Values map[string]string `json:"values"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SaveMySQLTuning(r.Context(), idParam(r), in.Values)
	s.respondTask(w, r, "app.mysql_tuning", "", id, err)
}

func (s *Server) handleSlowLog(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.SlowLog(r.Context(), idParam(r))
	s.respond(w, map[string]string{"log": v}, err)
}

func (s *Server) handleSetSlowLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On      bool    `json:"on"`
		Seconds float64 `json:"seconds"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.Apps.SetSlowLog(r.Context(), idParam(r), in.On, in.Seconds)
	if err == nil {
		s.auditCat(r, "database", "app.slow_log", strconv.FormatBool(in.On))
	}
	s.respond(w, map[string]bool{"ok": true}, err)
}

func (s *Server) handleClearSlowLog(w http.ResponseWriter, r *http.Request) {
	s.respond(w, map[string]bool{"ok": true}, s.Apps.ClearSlowLog(r.Context(), idParam(r)))
}

func (s *Server) handleBinlogs(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.Binlogs(r.Context(), idParam(r))
	s.respond(w, v, err)
}

func (s *Server) handleSetBinlog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On         bool `json:"on"`
		ExpireDays int  `json:"expire_days"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SetBinlog(r.Context(), idParam(r), in.On, in.ExpireDays)
	s.respondTask(w, r, "app.binlog", strconv.FormatBool(in.On), id, err)
}

func (s *Server) handlePurgeBinlogs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Before string `json:"before"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.Apps.PurgeBinlogs(r.Context(), idParam(r), in.Before)
	if err == nil {
		s.auditCat(r, "database", "app.binlog_purge", in.Before)
	}
	s.respond(w, map[string]bool{"ok": true}, err)
}

// ---- Redis ----

func (s *Server) handleRedisSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.RedisSettings(r.Context(), idParam(r))
	s.respond(w, v, err)
}

func (s *Server) handleRedisPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Apps.SetRedisPassword(r.Context(), idParam(r), in.Password); err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	detail := "changed"
	if in.Password == "" {
		detail = "removed"
	}
	s.auditCat(r, "database", "redis.password", detail)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePutRedisSettings(w http.ResponseWriter, r *http.Request) {
	var in appstore.RedisSettings
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SaveRedisSettings(r.Context(), idParam(r), in)
	if err != nil {
		writeAppErr(w, err)
		return
	}
	s.auditCat(r, "database", "app.redis_settings", "")
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id}) // "" = applied live
}

func (s *Server) handleRedisInfo(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.RedisInfo(r.Context(), idParam(r))
	s.respond(w, v, err)
}

func (s *Server) handleRedisBGSave(w http.ResponseWriter, r *http.Request) {
	s.respond(w, map[string]bool{"ok": true}, s.Apps.RedisSaveNow(r.Context(), idParam(r)))
}

func (s *Server) handleRedisDBs(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.RedisDBs(r.Context(), idParam(r))
	s.respond(w, map[string]any{"items": v}, err)
}

func queryInt(r *http.Request, k string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(k))
	return n
}

func (s *Server) handleRedisKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("page") {
		v, err := s.Apps.RedisPage(r.Context(), idParam(r), queryInt(r, "db"), queryInt(r, "page"), queryInt(r, "page_size"), q.Get("match"))
		s.respond(w, v, err)
		return
	}
	v, err := s.Apps.RedisScan(r.Context(), idParam(r), queryInt(r, "db"), q.Get("cursor"), q.Get("match"), queryInt(r, "count"))
	s.respond(w, v, err)
}

func (s *Server) handleRedisKey(w http.ResponseWriter, r *http.Request) {
	v, err := s.Apps.RedisGet(r.Context(), idParam(r), queryInt(r, "db"), r.URL.Query().Get("key"))
	s.respond(w, v, err)
}

type redisKeyIn struct {
	DB    int    `json:"db"`
	Key   string `json:"key"`
	Value string `json:"value"`
	TTL   int64  `json:"ttl"`
}

func (s *Server) handleRedisSetKey(w http.ResponseWriter, r *http.Request) {
	var in redisKeyIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.Apps.RedisSet(r.Context(), idParam(r), in.DB, in.Key, in.Value, in.TTL)
	if err == nil {
		s.auditCat(r, "database", "redis.set", in.Key)
	}
	s.respond(w, map[string]bool{"ok": true}, err)
}

func (s *Server) handleRedisExpire(w http.ResponseWriter, r *http.Request) {
	var in redisKeyIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.respond(w, map[string]bool{"ok": true}, s.Apps.RedisExpire(r.Context(), idParam(r), in.DB, in.Key, in.TTL))
}

func (s *Server) handleRedisDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DB   int      `json:"db"`
		Keys []string `json:"keys"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	n, err := s.Apps.RedisDelete(r.Context(), idParam(r), in.DB, in.Keys)
	if err == nil {
		s.auditCat(r, "database", "redis.delete", strconv.Itoa(len(in.Keys))+" keys")
	}
	s.respond(w, map[string]int64{"deleted": n}, err)
}

func (s *Server) handleRedisFlush(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DB  int  `json:"db"`
		All bool `json:"all"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.Apps.RedisFlush(r.Context(), idParam(r), in.DB, in.All)
	if err == nil {
		s.auditCat(r, "database", "redis.flush", strconv.Itoa(in.DB)+" all="+strconv.FormatBool(in.All))
	}
	s.respond(w, map[string]bool{"ok": true}, err)
}

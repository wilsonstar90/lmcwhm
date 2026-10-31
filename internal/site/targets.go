package site

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// What a domain entry serves.
const (
	TargetSite        = "site"        // its own website files / project
	TargetLink        = "link"        // another website (TargetSite)
	TargetDocker      = "docker"      // a container's published port
	TargetURL         = "url"         // any http(s) URL
	TargetPassthrough = "passthrough" // the raw TLS stream to host:port
	TargetRedirect    = "redirect"    // a redirect to Redirect
)

var (
	ErrLinked     = errors.New("site.linked") // other domains still show this website
	ErrNoContent  = errors.New("site.no_content")
	ErrBadRedir   = errors.New("site.invalid_redirect")
	redirectURLRe = regexp.MustCompile(`^https?://([A-Za-z0-9.\-]+|\[[0-9a-fA-F:]+\])(:[0-9]{1,5})?(/[A-Za-z0-9._~%!&()*+,=:@/\-]*)?(\?[A-Za-z0-9._~%!&()*+,=:@/?\-]*)?$`)
)

// HasContent reports whether the entry has website files of its own (a
// pure domain entry is stored with type "proxy").
func (s Site) HasContent() bool { return s.Type != "proxy" }

func (s Site) target() string {
	if s.Target != "" {
		return s.Target
	}
	switch {
	case !s.HasContent() && s.Passthrough:
		return TargetPassthrough
	case !s.HasContent():
		return TargetURL
	}
	return TargetSite
}

func (sv *Service) lookup(id int64) (Site, bool) {
	s, err := sv.Get(context.Background(), id)
	return s, err == nil
}

// TargetInput changes what an entry serves.
type TargetInput struct {
	Target       string `json:"target"`
	TargetSite   int64  `json:"target_site"`
	Container    string `json:"container"`
	ProxyTarget  string `json:"proxy_target"` // url / docker: http(s) URL; passthrough: host:port
	Redirect     string `json:"redirect"`
	RedirectCode int    `json:"redirect_code"`
	RedirectPath bool   `json:"redirect_path"`
	TargetSubdir string `json:"target_subdir"` // link: serve this directory of the website
}

var containerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// applyTarget validates in and sets it on s.
func (sv *Service) applyTarget(ctx context.Context, s *Site, in TargetInput) error {
	switch in.Target {
	case TargetSite:
		if !s.HasContent() {
			return ErrNoContent
		}
	case TargetLink:
		t, err := sv.Get(ctx, in.TargetSite)
		if err != nil || t.ID == s.ID || !t.HasContent() {
			return ErrNoContent
		}
		s.TargetSite = t.ID
		d, err := cleanSubdir(in.TargetSubdir)
		if err != nil {
			return err
		}
		s.TargetSubdir = d
	case TargetDocker, TargetURL:
		target := strings.TrimRight(strings.TrimSpace(in.ProxyTarget), "/")
		if !validTarget(target, false) {
			return ErrBadTarget
		}
		if in.Target == TargetDocker && !containerRe.MatchString(in.Container) {
			return ErrBadTarget
		}
		s.ProxyTarget = target
	case TargetPassthrough:
		target := strings.TrimSpace(in.ProxyTarget)
		if !validTarget(target, true) {
			return ErrBadTarget
		}
		s.ProxyTarget = target
	case TargetRedirect:
		r := strings.TrimSpace(in.Redirect)
		if in.RedirectPath {
			r = strings.TrimRight(r, "/")
			if strings.Count(r, "/") < 2 || strings.Contains(r, "?") {
				return ErrBadRedir
			}
		}
		if len(r) > 512 || !redirectURLRe.MatchString(r) {
			return ErrBadRedir
		}
		s.Redirect, s.RedirectCode, s.RedirectPath = r, redirectCodeOf(in.RedirectCode), in.RedirectPath
	default:
		return ErrBadTarget
	}
	s.Target = in.Target
	s.Passthrough = in.Target == TargetPassthrough
	if in.Target == TargetDocker {
		s.Container = in.Container
	} else {
		s.Container = ""
	}
	if in.Target != TargetLink {
		s.TargetSite, s.TargetSubdir = 0, ""
	}
	return nil
}

func redirectCodeOf(c int) int {
	switch c {
	case 301, 302, 307, 308:
		return c
	}
	return 301
}

// SetTarget points an entry at something else (task: the stack reloads).
// Its website files, if any, stay and are served again when it targets its
// own website.
func (sv *Service) SetTarget(ctx context.Context, id int64, in TargetInput) (string, error) {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if err := sv.applyTarget(ctx, &s, in); err != nil {
		return "", err
	}
	return sv.tasks.Start("site.target:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		if sv.Env.Installed(ctx) {
			if err := sv.ensureStack(ctx, "", log); err != nil {
				return err
			}
			log("applying configuration")
			return sv.Apply(ctx)
		}
		return nil
	}), nil
}

// Linked lists the entries that show website id (target "link").
func (sv *Service) Linked(ctx context.Context, id int64) ([]Site, error) {
	all, err := sv.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []Site{}
	for _, s := range all {
		if s.target() == TargetLink && s.TargetSite == id {
			out = append(out, s)
		}
	}
	return out, nil
}

// linkedErr refuses deleting a website other domains still show.
func (sv *Service) linkedErr(ctx context.Context, id int64) error {
	ls, err := sv.Linked(ctx, id)
	if err != nil {
		return err
	}
	if len(ls) == 0 {
		return nil
	}
	names := make([]string, len(ls))
	for i, l := range ls {
		names[i] = l.Domain
	}
	return fmt.Errorf("%w: %s", ErrLinked, strings.Join(names, ", "))
}

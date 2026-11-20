package site

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

type poolAdvisorState struct {
	Target     string
	Streak     int
	LastSwitch int64
}

func autoPoolTarget(s Site, requests int64, status webenv.FPMStatus) (string, int) {
	current, _ := webenv.NormalizePHPPool(s.Advanced.PHPPool)
	if current == webenv.PHPPoolLow {
		if status.ListenQueue > 0 || status.MaxListenQueue > 0 || status.MaxChildrenReached > 0 {
			return webenv.PHPPoolBusy, 3
		}
		if requests >= 10000 {
			return webenv.PHPPoolBusy, 6
		}
	}
	if current == webenv.PHPPoolBusy && requests < 2000 && status.ListenQueue == 0 && status.ActiveProcesses <= 2 {
		return webenv.PHPPoolLow, 144
	}
	return current, 1
}

func (sv *Service) AutoPHPPools(ctx context.Context) error {
	return sv.tasks.RunSync(ctx, func(ctx context.Context) error {
		sites, err := sv.List(ctx)
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		statuses := map[string]webenv.FPMStatus{}
		for _, s := range sites {
			if s.Type != "php" || !s.Advanced.PHPPoolAuto || s.Stopped {
				continue
			}
			current, _ := webenv.NormalizePHPPool(s.Advanced.PHPPool)
			key := s.PHPVersion + "/" + current
			status, ok := statuses[key]
			if !ok {
				status, err = webenv.PHPStatusFor(ctx, s.PHPVersion, current)
				if err != nil {
					continue
				}
				statuses[key] = status
			}
			var requests int64
			if err := sv.st.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(pv), 0) FROM site_stats_hourly WHERE domain = ? AND hour >= ?`,
				s.Domain, time.Now().Add(-24*time.Hour).Unix()).Scan(&requests); err != nil {
				continue
			}
			target, required := autoPoolTarget(s, requests, status)
			state := poolAdvisorState{}
			err := sv.st.DB.QueryRowContext(ctx, `SELECT target, streak, last_switch FROM php_pool_advisor WHERE site_id = ?`, s.ID).
				Scan(&state.Target, &state.Streak, &state.LastSwitch)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if target == current || state.Target != target {
				state.Streak = 0
			}
			if target == current || now-state.LastSwitch < int64((6*time.Hour)/time.Second) {
				state.Target = ""
				state.Streak = 0
			} else {
				state.Target = target
				state.Streak++
			}
			if state.Streak < required {
				if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO php_pool_advisor(site_id, target, streak, updated, last_switch) VALUES(?,?,?,?,?)
					ON CONFLICT(site_id) DO UPDATE SET target=excluded.target, streak=excluded.streak, updated=excluded.updated`,
					s.ID, state.Target, state.Streak, now, state.LastSwitch); err != nil {
					continue
				}
				continue
			}
			old := s
			s.Advanced.PHPPool = target
			if err := sv.save(ctx, &s); err != nil {
				return err
			}
			specs, err := sv.specs(ctx)
			if err != nil {
				return err
			}
			if err := sv.Env.ApplyNginxGraceful(ctx, specs); err != nil {
				_ = sv.save(ctx, &old)
				oldSpecs, _ := sv.specs(ctx)
				_ = sv.Env.ApplyNginxGraceful(ctx, oldSpecs)
				return err
			}
			_, err = sv.st.DB.ExecContext(ctx, `INSERT INTO php_pool_advisor(site_id, target, streak, updated, last_switch) VALUES(?,?,0,?,?)
				ON CONFLICT(site_id) DO UPDATE SET target='', streak=0, updated=excluded.updated, last_switch=excluded.last_switch`,
				s.ID, now, now)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (sv *Service) RunPHPPoolAdvisor(ctx context.Context, log func(string, ...any)) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sv.AutoPHPPools(ctx); err != nil {
				log("PHP pool advisor: %v", err)
			}
		}
	}
}

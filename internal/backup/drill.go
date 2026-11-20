package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func (sv *Service) Drill(ctx context.Context, id int64) (err error) {
	rec, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	started := time.Now().Unix()
	result, err := sv.st.DB.ExecContext(ctx, `INSERT INTO backup_drills(backup_id, status, started) VALUES(?, 'running', ?)`, rec.ID, started)
	if err != nil {
		return err
	}
	drillID, _ := result.LastInsertId()
	defer func() {
		status := "success"
		message := ""
		if err != nil {
			status, message = "failed", err.Error()
			if len(message) > 500 {
				message = message[:500]
			}
		}
		_, _ = sv.st.DB.ExecContext(context.Background(), `UPDATE backup_drills SET status=?, error=?, finished=? WHERE id=?`,
			status, message, time.Now().Unix(), drillID)
	}()
	return sv.runDrill(ctx, rec)
}

func (sv *Service) runDrill(ctx context.Context, rec Record) error {
	if sv.Worker && rec.Dest == DestLocal {
		if err := sv.verifyWithWorker(ctx, rec.Location); err != nil {
			return err
		}
	}
	rc, err := sv.openVerified(ctx, rec)
	if err != nil {
		return err
	}
	defer rc.Close()
	if rec.Kind == KindDatabase {
		if sv.DBs == nil || sv.Apps == nil {
			return ErrNoRestore
		}
		original, err := sv.DBs.Get(ctx, rec.TargetID)
		if err != nil {
			return err
		}
		eng, err := sv.Apps.Get(ctx, original.AppID)
		if err != nil {
			return err
		}
		name := "drill_" + strings.ToLower(settings.RandomString(12, settings.Alnum))
		temp, err := sv.DBs.CreateNow(ctx, database.CreateInput{AppID: original.AppID, Name: name, Remark: "temporary restore drill"}, func(string, ...any) {})
		if err != nil {
			return err
		}
		defer func() { _ = sv.DBs.DeleteTemporary(context.Background(), temp.ID, func(string, ...any) {}) }()
		return restoreDB(ctx, eng, temp.Name, rc)
	}
	dir, err := os.MkdirTemp("", "lmcwhm-restore-drill-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	switch rec.Kind {
	case KindSite, KindPath, KindVolume:
		return extractTarGz(rc, dir, -1, -1)
	case KindPanel:
		if err := RestorePanel(dir, rc); err != nil {
			return err
		}
		return checkPanelDB(filepath.Join(dir, "data", "panel.db"))
	case KindSnapshot:
		if err := extractTarGz(rc, dir, -1, -1); err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
		if err != nil {
			return err
		}
		var manifest SnapshotManifest
		if err := json.Unmarshal(body, &manifest); err != nil || len(manifest.Parts) == 0 {
			return fmt.Errorf("%w: snapshot.json", ErrBadArchive)
		}
		return checkPanelDB(filepath.Join(dir, "home", "data", "panel.db"))
	default:
		return ErrNoRestore
	}
}

func checkPanelDB(path string) error {
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()
	var result string
	if err := st.DB.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("%w: SQLite %s", ErrBadArchive, result)
	}
	return nil
}

func (sv *Service) DrillStatus(ctx context.Context, id int64) (map[string]any, error) {
	var status, message string
	var drillID, started, finished int64
	err := sv.st.DB.QueryRowContext(ctx, `SELECT id, status, error, started, finished FROM backup_drills WHERE backup_id=? ORDER BY id DESC LIMIT 1`, id).
		Scan(&drillID, &status, &message, &started, &finished)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": drillID, "backup_id": id, "status": status, "error": message, "started": started, "finished": finished}, nil
}

func (sv *Service) verifyWithWorker(ctx context.Context, path string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, "worker", "--verify-archive", path)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		message := output.String()
		if len(message) > 500 {
			message = message[:500]
		}
		return fmt.Errorf("worker verification failed: %w: %s", err, strings.TrimSpace(message))
	}
	return nil
}

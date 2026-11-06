package database

// Table tools for MySQL / MariaDB (aaPanel's 工具箱): table list with engine,
// collation, rows and size; repair, optimize, engine conversion, comments and
// the CREATE TABLE statement.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

var (
	ErrBadTable  = errors.New("db.invalid_table")
	ErrBadAction = errors.New("db.invalid_action")
)

type Table struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Collation string `json:"collation"`
	Rows      int64  `json:"rows"`
	Size      int64  `json:"size"` // data + indexes
	Free      int64  `json:"free"` // reclaimable by OPTIMIZE
	Comment   string `json:"comment"`
}

var tableRe = regexp.MustCompile(`^[A-Za-z0-9_$\-]{1,64}$`)

// unescape undoes the mysql client's --batch escapes (\t \n \\ \0).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\t`, "\t", `\n`, "\n", `\\`, `\`, `\0`, "\x00").Replace(s)
}

// Tables lists the database's tables.
func (sv *Service) Tables(ctx context.Context, id int64) ([]Table, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !accessEngine(d.Engine) {
		return nil, ErrAccessUnsupported
	}
	eng, err := sv.engine(ctx, d.AppID)
	if err != nil {
		return nil, err
	}
	out, err := sv.apps.Query(ctx, eng, fmt.Sprintf(`SELECT TABLE_NAME, IFNULL(ENGINE,''), IFNULL(TABLE_COLLATION,''), IFNULL(TABLE_ROWS,0),
		IFNULL(DATA_LENGTH,0) + IFNULL(INDEX_LENGTH,0), IFNULL(DATA_FREE,0), IFNULL(TABLE_COMMENT,'')
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = '%s' AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME;`+"\n", d.Name))
	if err != nil {
		return nil, err
	}
	list := []Table{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 7 {
			continue
		}
		t := Table{Name: f[0], Engine: f[1], Collation: f[2], Comment: unescape(f[6])}
		t.Rows, _ = strconv.ParseInt(f[3], 10, 64)
		t.Size, _ = strconv.ParseInt(f[4], 10, 64)
		t.Free, _ = strconv.ParseInt(f[5], 10, 64)
		list = append(list, t)
	}
	return list, nil
}

// checkTables keeps names that exist in the database.
func (sv *Service) checkTables(ctx context.Context, id int64, names []string) ([]string, error) {
	all, err := sv.Tables(ctx, id)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, t := range all {
		have[t.Name] = true
	}
	if len(names) == 0 {
		return nil, ErrBadTable
	}
	for _, n := range names {
		if !tableRe.MatchString(n) || !have[n] {
			return nil, ErrBadTable
		}
	}
	return names, nil
}

// TableAction runs repair | optimize | innodb | myisam on tables (task).
func (sv *Service) TableAction(ctx context.Context, id int64, action string, names []string) (string, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	switch action {
	case "repair", "optimize", "innodb", "myisam":
	default:
		return "", ErrBadAction
	}
	names, err = sv.checkTables(ctx, id, names)
	if err != nil {
		return "", err
	}
	return sv.tasks.StartLong("db.tables:"+d.Name, tableTimeout, func(ctx context.Context, log tasks.Logf) error {
		eng, err := sv.engine(ctx, d.AppID)
		if err != nil {
			return err
		}
		failed := 0
		for _, n := range names {
			var q string
			switch action {
			case "repair":
				q = fmt.Sprintf("REPAIR TABLE `%s`.`%s`;\n", d.Name, n)
			case "optimize":
				q = fmt.Sprintf("OPTIMIZE TABLE `%s`.`%s`;\n", d.Name, n)
			case "innodb":
				q = fmt.Sprintf("ALTER TABLE `%s`.`%s` ENGINE = InnoDB;\n", d.Name, n)
			case "myisam":
				q = fmt.Sprintf("ALTER TABLE `%s`.`%s` ENGINE = MyISAM;\n", d.Name, n)
			}
			out, err := sv.apps.Query(ctx, eng, q)
			if err != nil {
				failed++
				log("%s: %v", n, err)
				continue
			}
			// REPAIR / OPTIMIZE answer "db.t  op  status|note|error  text" rows.
			msg := "OK"
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				if f := strings.Split(l, "\t"); len(f) >= 4 {
					msg = f[2] + ": " + f[3]
					if f[2] == "error" {
						failed++
					}
				}
			}
			log("%s: %s", n, msg)
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d tables failed", failed, len(names))
		}
		return nil
	}), nil
}

// tableTimeout: OPTIMIZE / ALTER copy big tables.
const tableTimeout = 6 * time.Hour

// SetTableComment changes a table's comment.
func (sv *Service) SetTableComment(ctx context.Context, id int64, name, comment string) error {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := sv.checkTables(ctx, id, []string{name}); err != nil {
		return err
	}
	if len([]rune(comment)) > 1024 {
		return ErrBadTable
	}
	eng, err := sv.engine(ctx, d.AppID)
	if err != nil {
		return err
	}
	esc := strings.NewReplacer(`\`, `\\`, `'`, `''`, "\x00", "").Replace(comment)
	_, err = sv.apps.Query(ctx, eng, fmt.Sprintf("ALTER TABLE `%s`.`%s` COMMENT = '%s';\n", d.Name, name, esc))
	return err
}

// TableSchema returns the CREATE TABLE statement.
func (sv *Service) TableSchema(ctx context.Context, id int64, name string) (string, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if _, err := sv.checkTables(ctx, id, []string{name}); err != nil {
		return "", err
	}
	eng, err := sv.engine(ctx, d.AppID)
	if err != nil {
		return "", err
	}
	out, err := sv.apps.Query(ctx, eng, fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s`;\n", d.Name, name))
	if err != nil {
		return "", err
	}
	_, stmt, ok := strings.Cut(strings.TrimRight(out, "\n"), "\t")
	if !ok {
		return "", ErrBadTable
	}
	return unescape(stmt) + ";\n", nil
}

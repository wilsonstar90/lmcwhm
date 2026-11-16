package webenv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func snapshotsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for path, body := range a {
		if !bytes.Equal(body, b[path]) {
			return false
		}
	}
	return true
}

func changedPaths(before, after map[string][]byte) []string {
	seen := map[string]bool{}
	for path := range before {
		seen[path] = true
	}
	for path := range after {
		seen[path] = true
	}
	out := []string{}
	for path := range seen {
		if !bytes.Equal(before[path], after[path]) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// syncRuntimeMaps applies existing map and ACL files without creating a new
// HAProxy worker. Any config, file-set or value outside this safe subset is
// left for the normal validated hot reload.
func (e *Env) syncRuntimeMaps(ctx context.Context, before, after map[string][]byte) (bool, error) {
	if _, err := os.Stat(e.adminSock()); err != nil {
		return false, nil
	}
	changed := changedPaths(before, after)
	if len(changed) == 0 {
		return true, nil
	}
	for _, path := range changed {
		rel, err := filepath.Rel(e.EdgeDir(), path)
		if err != nil || strings.HasPrefix(rel, "..") || before[path] == nil || after[path] == nil {
			return false, nil
		}
		if !strings.HasPrefix(rel, "maps/") || !(strings.HasSuffix(rel, ".map") || strings.HasSuffix(rel, ".lst")) {
			return false, nil
		}
		for _, body := range [][]byte{before[path], after[path]} {
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if strings.HasSuffix(rel, ".map") {
					fields := strings.Fields(line)
					if len(fields) != 2 || !runtimeValueSafe(fields[0]) || !runtimeValueSafe(fields[1]) {
						return false, nil
					}
				} else if !runtimeValueSafe(line) {
					return false, nil
				}
			}
		}
	}
	for _, path := range changed {
		var err error
		if strings.HasSuffix(path, ".map") {
			err = e.pushMap(ctx, path, parseRuntimeMap(before[path]), parseRuntimeMap(after[path]))
		} else {
			err = e.pushList(ctx, path, runtimeList(before[path]), runtimeLines(after[path]))
		}
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

func runtimeValueSafe(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || r == '\\' || r == '\'' || r == '"' || unicode.IsControl(r) {
			return false
		}
	}
	return s != ""
}

func runtimeLines(body []byte) []string {
	out := []string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

func runtimeList(body []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range runtimeLines(body) {
		out[line] = true
	}
	return out
}

func parseRuntimeMap(body []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 {
			out[fields[0]] = fields[1]
		}
	}
	return out
}

func (e *Env) pushMap(ctx context.Context, path string, old, next map[string]string) error {
	set, del := []string{}, []string{}
	for key, value := range next {
		if old[key] != value {
			set = append(set, key+" "+value)
		}
	}
	for key := range old {
		if _, ok := next[key]; !ok {
			del = append(del, key)
		}
	}
	if len(set)+len(del) == 0 {
		return nil
	}
	sort.Strings(set)
	sort.Strings(del)
	if len(set)+len(del) <= 500 {
		cmds := []string{}
		for _, entry := range set {
			cmds = append(cmds, "set map "+path+" "+entry)
		}
		for _, key := range del {
			cmds = append(cmds, "del map "+path+" "+key)
		}
		_, err := e.runtime(ctx, cmds)
		return err
	}
	out, err := e.runtime(ctx, []string{"prepare map " + path})
	if err != nil {
		return err
	}
	version := ""
	for _, answer := range out {
		answer = strings.TrimSpace(answer)
		if strings.HasPrefix(answer, "New version created:") {
			version = strings.TrimSpace(strings.TrimPrefix(answer, "New version created:"))
		}
	}
	if version == "" || strings.ContainsAny(version, " \t") {
		return fmt.Errorf("prepare map: %q", out)
	}
	cmds := []string{}
	for _, entry := range set {
		cmds = append(cmds, "add map @"+version+" "+path+" "+entry)
	}
	for _, key := range del {
		cmds = append(cmds, "del map @"+version+" "+path+" "+key)
	}
	cmds = append(cmds, "commit map @"+version+" "+path)
	_, err = e.runtime(ctx, cmds)
	return err
}

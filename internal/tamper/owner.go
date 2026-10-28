package tamper

import (
	"io/fs"
	"syscall"
)

func fileOwner(fi fs.FileInfo) (int, int, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid), true
	}
	return 0, 0, false
}

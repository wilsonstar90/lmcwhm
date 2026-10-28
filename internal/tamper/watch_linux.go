//go:build linux

package tamper

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

const watchMask = unix.IN_CLOSE_WRITE | unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_ONLYDIR

// watcher is one inotify instance covering a rule's directory tree.
type watcher struct {
	fd     int
	f      *os.File // fd in Go's poller: Close unblocks a pending Read
	mu     sync.Mutex
	dirs   map[int]string // watch descriptor -> directory
	files  int
	done   chan struct{}
	exited chan struct{} // closed when loop returns
}

func (w *watcher) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.files
}

// close stops the watcher and waits until no handler is running, so no
// restore races with whoever stopped it.
func (w *watcher) close() {
	select {
	case <-w.done:
	default:
		close(w.done)
		w.f.Close()
	}
	<-w.exited
}

func (w *watcher) add(dir string) {
	wd, err := unix.InotifyAddWatch(w.fd, dir, watchMask)
	if err == nil {
		w.mu.Lock()
		w.dirs[wd] = dir
		w.mu.Unlock()
	}
}

func (sv *Service) startWatch(r Rule) error {
	n, err := sv.baseline(r)
	if err != nil {
		return err
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	w := &watcher{fd: fd, f: os.NewFile(uintptr(fd), "inotify"), dirs: map[int]string{}, files: n, done: make(chan struct{}), exited: make(chan struct{})}
	r.targets(func(p string, dir bool) {
		if dir {
			w.add(p)
		}
	})
	sv.mu.Lock()
	if old := sv.watches[r.ID]; old != nil {
		old.close()
	}
	sv.watches[r.ID] = w
	sv.mu.Unlock()
	go sv.loop(r, w)
	return nil
}

func (sv *Service) loop(r Rule, w *watcher) {
	defer close(w.exited)
	buf := make([]byte, 64<<10)
	for {
		n, err := w.f.Read(buf)
		select {
		case <-w.done:
			return
		default:
		}
		if err != nil || n <= 0 {
			select {
			case <-w.done:
				return
			default:
			}
			sv.setStatus(r.ID, err)
			return
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			name := ""
			if ev.Len > 0 {
				b := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+int(ev.Len)]
				for i, c := range b {
					if c == 0 {
						b = b[:i]
						break
					}
				}
				name = string(b)
			}
			off += unix.SizeofInotifyEvent + int(ev.Len)
			w.mu.Lock()
			dir := w.dirs[int(ev.Wd)]
			w.mu.Unlock()
			if dir == "" || name == "" {
				continue
			}
			p := filepath.Join(dir, name)
			if ev.Mask&unix.IN_ISDIR != 0 {
				if ev.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
					rel, _ := filepath.Rel(r.Path, p)
					if !r.excluded(filepath.ToSlash(rel)) {
						w.add(p)
						// Files may already exist in a directory moved in.
						entries, _ := os.ReadDir(p)
						for _, e := range entries {
							if !e.IsDir() {
								sv.handle(r, filepath.Join(p, e.Name()), "create")
							}
						}
					}
				}
				continue
			}
			op := "modify"
			switch {
			case ev.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0:
				op = "create"
			case ev.Mask&unix.IN_DELETE != 0:
				op = "delete"
			case ev.Mask&unix.IN_MOVED_FROM != 0:
				op = "rename"
			case ev.Mask&unix.IN_ATTRIB != 0:
				op = "attrib"
			}
			if op == "create" && ev.Mask&unix.IN_CREATE != 0 {
				continue // wait for IN_CLOSE_WRITE so the content is complete
			}
			sv.handle(r, p, op)
		}
	}
}

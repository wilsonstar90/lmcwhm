//go:build !linux

package tamper

type watcher struct{}

func (w *watcher) count() int { return 0 }
func (w *watcher) close()     {}

func (sv *Service) startWatch(Rule) error { return ErrNoWatch }

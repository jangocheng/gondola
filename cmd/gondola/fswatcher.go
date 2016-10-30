package main

import (
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gnd.la/log"
	"gnd.la/util/generic"

	"gopkg.in/fsnotify.v1"
)

func watcherShouldUsePolling() bool {
	// Unfortunately, fsnotify uses one file descriptor per watched directory
	// in macOS. Coupled with the 256 max open files by default, it makes it
	// very easy to run into the limit, so we fall back to polling.
	return runtime.GOOS == "darwin"
}

type fsWatcher struct {
	// used for fsnotify
	watcher *fsnotify.Watcher
	// used for polling
	watched     map[string]time.Time
	stopPolling chan struct{}
	mu          sync.RWMutex
	Changed     func(string)
	IsValidFile func(string) bool
}

func newFSWatcher() (*fsWatcher, error) {
	if watcherShouldUsePolling() {
		watcher := &fsWatcher{
			watched:     make(map[string]time.Time),
			stopPolling: make(chan struct{}, 1),
		}
		go watcher.poll()
		return watcher, nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	watcher := &fsWatcher{watcher: w}
	go watcher.watch()
	return watcher, nil
}

func (w *fsWatcher) Add(path string) error {
	if w.watcher != nil {
		return w.watcher.Add(path)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.watched[path] = time.Time{}
	return nil
}

func (w *fsWatcher) Remove(path string) error {
	if w.watcher != nil {
		return w.watcher.Remove(path)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.watched, path)
	return nil
}

func (w *fsWatcher) Close() {
	if w.watcher != nil {
		w.watcher.Close()
		w.watcher = nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopPolling != nil {
		w.stopPolling <- struct{}{}
	}
	if w.watched != nil {
		w.watched = make(map[string]time.Time)
	}
}

func (w *fsWatcher) AddPackages(pkgs []*build.Package) error {
	paths := generic.Map(pkgs, func(pkg *build.Package) string { return pkg.Dir }).([]string)
	for _, p := range paths {
		if err := w.Add(p); err != nil {
			return err
		}
	}
	return nil
}

func (w *fsWatcher) watch() {
	var t *time.Timer
	for {
		select {
		case ev, ok := <-w.watcher.Events:
			if !ok {
				// Closed
				return
			}
			if ev.Op == fsnotify.Chmod {
				break
			}
			if ev.Op == fsnotify.Remove {
				// It seems the Watcher stops watching a file
				// if it receives a DELETE event for it. For some
				// reason, some editors generate a DELETE event
				// for a file when saving it, so we must watch the
				// file again. Since fsnotify is in exp/ and its
				// API might change, remove the watch first, just
				// in case.
				w.watcher.Remove(ev.Name)
				w.watcher.Add(ev.Name)
			}
			if w.isValidFile(ev.Name) {
				if t != nil {
					t.Stop()
				}
				name := ev.Name
				t = time.AfterFunc(50*time.Millisecond, func() {
					w.changed(name)
				})
			}
		case err := <-w.watcher.Errors:
			if err == nil {
				// Closed
				return
			}
			log.Errorf("Error watching: %s", err)
		}
	}
}

func (w *fsWatcher) poll() {
	ticker := time.NewTicker(time.Second)
	for {
		select {
		case <-ticker.C:
			w.doPolling()
		case <-w.stopPolling:
			ticker.Stop()
			return
		}
	}
}

func (w *fsWatcher) doPolling() {
	a := time.Now()
	fmt.Println("WILL POLL", a)
	defer func() {
		fmt.Println("DID POLL", time.Now().Sub(a))
	}()
	// Copy the map, since we might add entries to
	// it while iterating
	watched := make(map[string]time.Time)
	w.mu.RLock()
	for k, v := range w.watched {
		watched[k] = v
	}
	w.mu.RUnlock()
	for k, v := range watched {
		st, err := os.Stat(k)
		if err != nil {
			log.Errorf("error stat'ing %s: %v", k, err)
			continue
		}
		if st.IsDir() {
			if !v.IsZero() && st.ModTime().Equal(v) {
				// Nothing new in this dir
				continue
			}
			entries, err := ioutil.ReadDir(k)
			if err != nil {
				log.Errorf("error reading files in %s: %v", k, err)
				continue
			}
			if v.IsZero() {
				// 1st time we're polling this dir, add its files
				w.mu.Lock()
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					p := filepath.Join(k, e.Name())
					if !w.isValidFile(p) {
						continue
					}
					w.watched[p] = e.ModTime()
				}
				w.mu.Unlock()
			} else {
				var added []os.FileInfo
				w.mu.RLock()
				for _, e := range entries {
					p := filepath.Join(k, e.Name())
					if _, found := w.watched[p]; !found {
						added = append(added, e)
					}
				}
				w.mu.RUnlock()
				if len(added) > 0 {
					w.mu.Lock()
					for _, e := range added {
						w.watched[filepath.Join(k, e.Name())] = e.ModTime()
					}
					w.mu.Unlock()
					for _, e := range added {
						w.changed(filepath.Join(k, e.Name()))
					}
				}
			}
		} else if w.isValidFile(k) {
			if mt := st.ModTime(); !mt.Equal(v) {
				w.watched[k] = mt
				if !v.IsZero() {
					// File was changed
					w.changed(k)
				}
			}
		}
	}
}

func (w *fsWatcher) changed(path string) {
	if w.Changed != nil {
		w.Changed(path)
	}
}

func (w *fsWatcher) isValidFile(path string) bool {
	return w.IsValidFile != nil && w.IsValidFile(path)
}

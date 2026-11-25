// Package updates provides the release inventory shared by CLI, TUI and web.
package updates

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/versions"
)

const Repository = "ATCUSA/hamstack"
const PollInterval = time.Hour

// Version is set by release builds with -ldflags '-X hamstack/internal/updates.Version=vX.Y.Z'.
var Version = "dev"

func Current() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return "dev-" + s.Value[:min(12, len(s.Value))]
			}
		}
	}
	return "dev"
}

type Program struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Origin    string `json:"origin"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}
type Snapshot struct {
	Checked  time.Time `json:"checked"`
	Loading  bool      `json:"loading"`
	Programs []Program `json:"programs"`
}

var findInstalled = versions.FindInstalled
var latestVersion = versions.Latest
var releaseAssets = versions.ReleaseAssets
var locate = versions.Locate

var cache struct {
	sync.Mutex
	value Snapshot
	done  chan struct{}
	// print is the installed programs' fingerprint when the last check started; printAt is when Fresh last
	// compared it. A check runs every program to read its version, too slow to do on every page load; the
	// fingerprint only looks at the files, so a program updated after the check is noticed within seconds.
	print   string
	printAt time.Time
	stale   bool // Recheck: the last answer no longer counts, though the page keeps showing it until the next
}

// printEvery is how often Fresh looks at the files. A variable so that a test can look every time.
var printEvery = 5 * time.Second

// fingerprint is where each program is and its file's size and time: it changes when a program is installed,
// updated or removed, by hamstack or anything else.
func fingerprint() string {
	var b strings.Builder
	for _, p := range catalog.All {
		path := locate(p)
		b.WriteString(p.Name + "=" + path)
		if fi, err := os.Stat(path); path != "" && err == nil {
			fmt.Fprintf(&b, ":%d:%d", fi.Size(), fi.ModTime().UnixNano())
		}
		b.WriteByte(';')
	}
	return b.String()
}

// Fresh is State, and when a program has been installed, updated or removed since the last check, it starts
// another in the background (the page shows the result on its next refresh). The latest versions come from the
// cache on disk, so looking again does not ask GitHub again.
func Fresh() Snapshot {
	cache.Lock()
	due := cache.done == nil && !cache.value.Checked.IsZero() && time.Since(cache.printAt) >= printEvery
	if due {
		cache.printAt = time.Now()
	}
	known := cache.print
	cache.Unlock()
	if due && fingerprint() != known {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			_, _ = Recheck(ctx)
		}()
	}
	return State()
}

// Recheck looks again now, even within the hour: something changed what is installed (an install, update or
// uninstall finished). It waits for a check already under way, which may have read the versions before the change.
func Recheck(ctx context.Context) (Snapshot, error) {
	for {
		cache.Lock()
		if cache.done == nil {
			cache.stale = true
			cache.Unlock()
			return Check(ctx, false)
		}
		done := cache.done
		cache.Unlock()
		select {
		case <-ctx.Done():
			return State(), ctx.Err()
		case <-done:
		}
	}
}

func State() Snapshot {
	cache.Lock()
	defer cache.Unlock()
	s := cache.value
	s.Programs = append([]Program(nil), s.Programs...)
	return s
}
func Start() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		_, _ = Check(ctx, false)
	}()
}
func Check(ctx context.Context, force bool) (Snapshot, error) {
	if force { // asked for by a person: look again if the last answer is more than a few minutes old
		ctx = versions.WithRefresh(ctx)
	}
	cache.Lock()
	if cache.done != nil {
		done := cache.done
		cache.Unlock()
		select {
		case <-ctx.Done():
			return State(), ctx.Err()
		case <-done:
			return State(), nil
		}
	}
	if !force && !cache.stale && !cache.value.Checked.IsZero() && time.Since(cache.value.Checked) < PollInterval {
		s := cache.value
		cache.Unlock()
		return s, nil
	}
	cache.done = make(chan struct{})
	done := cache.done
	cache.stale = false
	cache.value.Loading = true
	cache.Unlock()
	print := fingerprint() // before the versions are read: a change during the check is noticed after it
	rows := make([]Program, len(catalog.All)+1)
	var wg sync.WaitGroup
	for i, p := range catalog.All {
		wg.Add(1)
		go func(i int, p catalog.Package) {
			defer wg.Done()
			row := Program{Name: p.Name, Status: "not installed"}
			in := findInstalled(p)
			if in == nil {
				rows[i] = row
				return
			}
			row.Installed, row.Origin = in.Version, in.Origin
			row.Status = "unchecked"
			latest, err := latestVersion(ctx, p)
			if err != nil {
				row.Error = err.Error()
			} else {
				row.Latest = latest
				row.Status = "up to date"
				if in.Version == "" {
					row.Status = "version unknown"
				} else if versions.Compare(in.Version, latest) < 0 {
					row.Status = "update available"
				}
			}
			rows[i] = row
		}(i, p)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		row := Program{Name: "hamstack", Installed: Current(), Origin: "hamstack release", Status: "unchecked"}
		tag, _, err := releaseAssets(ctx, Repository)
		if err != nil {
			if versions.IsRateLimit(err) {
				row.Error = err.Error()
			} else {
				row.Error = "No published release found or release lookup failed: " + err.Error()
			}
		} else {
			row.Latest = strings.TrimPrefix(tag, "v")
			if strings.HasPrefix(row.Installed, "dev") {
				row.Status = "development build; release available"
			} else if versions.Compare(strings.TrimPrefix(row.Installed, "v"), row.Latest) < 0 {
				row.Status = "update available"
			} else {
				row.Status = "up to date"
			}
		}
		rows[len(rows)-1] = row
	}()
	wg.Wait()
	value := Snapshot{Checked: time.Now(), Programs: rows}
	cache.Lock()
	cache.value = value
	cache.print, cache.printAt = print, time.Now()
	cache.done = nil
	close(done)
	cache.Unlock()
	if ctx.Err() != nil {
		return value, fmt.Errorf("some update checks timed out: %w", ctx.Err())
	}
	return value, nil
}

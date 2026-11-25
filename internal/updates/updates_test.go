package updates

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/versions"
)

func TestSharedInventoryChecksOnlyInstalledAppsAndCachesConcurrentReaders(t *testing.T) {
	oldFind, oldLatest, oldRelease := findInstalled, latestVersion, releaseAssets
	defer func() {
		findInstalled, latestVersion, releaseAssets = oldFind, oldLatest, oldRelease
		cache.Lock()
		cache.value = Snapshot{}
		cache.done = nil
		cache.Unlock()
	}()
	cache.Lock()
	cache.value = Snapshot{}
	cache.done = nil
	cache.Unlock()
	var appCalls, selfCalls atomic.Int32
	findInstalled = func(p catalog.Package) *versions.Installed {
		if p.Name == "pat" {
			return &versions.Installed{Version: "1.0.0", Origin: "user install"}
		}
		return nil
	}
	latestVersion = func(ctx context.Context, p catalog.Package) (string, error) {
		if p.Name != "pat" {
			t.Errorf("queried uninstalled app %s", p.Name)
		}
		appCalls.Add(1)
		return "2.0.0", nil
	}
	releaseAssets = func(ctx context.Context, repo string) (string, []versions.Asset, error) {
		if repo != "ATCUSA/hamstack" {
			t.Errorf("wrong source: %s", repo)
		}
		selfCalls.Add(1)
		return "v1.0.0", nil, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Check(context.Background(), false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if appCalls.Load() != 1 || selfCalls.Load() != 1 {
		t.Fatalf("duplicate release checks: apps=%d self=%d", appCalls.Load(), selfCalls.Load())
	}
	snapshot := State()
	found := false
	for _, p := range snapshot.Programs {
		if p.Name == "pat" {
			found = true
			if p.Status != "update available" || p.Latest != "2.0.0" {
				t.Fatalf("bad status: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("installed app omitted")
	}
	if _, err := Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if appCalls.Load() != 2 {
		t.Fatal("explicit refresh did not re-check")
	}
}

// After a program is updated (by a dashboard job, or from a terminal while the dashboard runs), the dashboard must
// stop saying an update is available without waiting for the hourly check.
func TestFreshNoticesAnUpdatedProgram(t *testing.T) {
	oldFind, oldLatest, oldRelease, oldLocate, oldEvery := findInstalled, latestVersion, releaseAssets, locate, printEvery
	defer func() {
		findInstalled, latestVersion, releaseAssets, locate, printEvery = oldFind, oldLatest, oldRelease, oldLocate, oldEvery
		resetCache()
	}()
	resetCache()
	printEvery = 0
	bin := filepath.Join(t.TempDir(), "pat")
	_ = os.WriteFile(bin, []byte("old"), 0o755)
	var mu sync.Mutex
	installed := "1.0.0"
	locate = func(p catalog.Package) string {
		if p.Name == "pat" {
			return bin
		}
		return ""
	}
	findInstalled = func(p catalog.Package) *versions.Installed {
		if p.Name != "pat" {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		return &versions.Installed{Path: bin, Version: installed, Origin: "user install"}
	}
	latestVersion = func(ctx context.Context, p catalog.Package) (string, error) { return "2.0.0", nil }
	releaseAssets = func(ctx context.Context, repo string) (string, []versions.Asset, error) { return "v1.0.0", nil, nil }

	if _, err := Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if st := patStatus(Fresh()); st != "update available" {
		t.Fatalf("before the update: %q", st)
	}
	// The update replaces the program.
	mu.Lock()
	installed = "2.0.0"
	mu.Unlock()
	_ = os.WriteFile(bin, []byte("new, longer"), 0o755)
	Fresh() // notices the change and looks again in the background
	deadline := time.Now().Add(5 * time.Second)
	for patStatus(State()) != "up to date" {
		if time.Now().After(deadline) {
			t.Fatalf("still %q after the program changed", patStatus(State()))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRecheckLooksAgainEvenWithinTheHour(t *testing.T) {
	oldFind, oldLatest, oldRelease := findInstalled, latestVersion, releaseAssets
	defer func() { findInstalled, latestVersion, releaseAssets = oldFind, oldLatest, oldRelease; resetCache() }()
	resetCache()
	installed := "1.0.0"
	findInstalled = func(p catalog.Package) *versions.Installed {
		if p.Name != "pat" {
			return nil
		}
		return &versions.Installed{Version: installed}
	}
	latestVersion = func(ctx context.Context, p catalog.Package) (string, error) { return "2.0.0", nil }
	releaseAssets = func(ctx context.Context, repo string) (string, []versions.Asset, error) { return "v1.0.0", nil, nil }
	_, _ = Check(context.Background(), false)
	installed = "2.0.0"
	s, _ := Recheck(context.Background())
	if patStatus(s) != "up to date" {
		t.Fatalf("Recheck kept the old answer: %q", patStatus(s))
	}
}

func patStatus(s Snapshot) string {
	for _, p := range s.Programs {
		if p.Name == "pat" {
			return p.Status
		}
	}
	return ""
}

func resetCache() {
	cache.Lock()
	cache.value, cache.done, cache.print, cache.printAt, cache.stale, cache.retryAt = Snapshot{}, nil, "", time.Time{}, false, time.Time{}
	cache.Unlock()
}

// A program from the distribution's packages with a newer upstream release that the distribution does not offer
// yet is not an update anyone can run: it must not be counted as "update available" (the dashboard banner would
// say so for ever).
func TestNewerUpstreamThatIsNotPackagedIsNotAnUpdate(t *testing.T) {
	oldFind, oldLatest, oldRelease, oldCand := findInstalled, latestVersion, releaseAssets, DistroCandidate
	defer func() {
		findInstalled, latestVersion, releaseAssets, DistroCandidate = oldFind, oldLatest, oldRelease, oldCand
		resetCache()
	}()
	resetCache()
	findInstalled = func(p catalog.Package) *versions.Installed {
		switch p.Name {
		case "vnc", "novnc":
			return &versions.Installed{Version: "1.15.0", Origin: "system package"}
		case "pat":
			return &versions.Installed{Version: "1.0.0", Origin: "user install"}
		}
		return nil
	}
	latestVersion = func(ctx context.Context, p catalog.Package) (string, error) { return "1.16.2", nil }
	releaseAssets = func(ctx context.Context, repo string) (string, []versions.Asset, error) { return "v1.0.0", nil, nil }
	DistroCandidate = func(p catalog.Package) string {
		if p.Name == "novnc" {
			return "1.16.0" // the distribution has a newer package ready: that is an update
		}
		return "1.15.0"
	}
	s, _ := Check(context.Background(), false)
	got := map[string]string{}
	for _, p := range s.Programs {
		got[p.Name] = p.Status
	}
	if got["vnc"] != NotPackaged {
		t.Errorf("vnc: %q", got["vnc"])
	}
	if got["novnc"] != "update available" {
		t.Errorf("novnc, with a newer package ready: %q", got["novnc"])
	}
	if got["pat"] != "update available" {
		t.Errorf("pat, installed by hamstack: %q", got["pat"])
	}
}

// At boot the check can run before the network is up. Those answers must not stand for an hour.
func TestAnOfflineCheckIsRetriedSoonAndSaysWhy(t *testing.T) {
	oldFind, oldLatest, oldRelease, oldRetry := findInstalled, latestVersion, releaseAssets, offlineRetry
	defer func() {
		findInstalled, latestVersion, releaseAssets, offlineRetry = oldFind, oldLatest, oldRelease, oldRetry
		resetCache()
	}()
	resetCache()
	offlineRetry = 0
	online := false
	findInstalled = func(p catalog.Package) *versions.Installed {
		if p.Name == "pat" {
			return &versions.Installed{Version: "1.0.0"}
		}
		return nil
	}
	latestVersion = func(ctx context.Context, p catalog.Package) (string, error) {
		if !online {
			return "", &net.OpError{Op: "dial", Net: "udp", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}
		}
		return "1.0.0", nil
	}
	releaseAssets = func(ctx context.Context, repo string) (string, []versions.Asset, error) { return "v1.0.0", nil, nil }
	s, _ := Check(context.Background(), false)
	var row Program
	for _, p := range s.Programs {
		if p.Name == "pat" {
			row = p
		}
	}
	if !strings.Contains(row.Error, "No network connection") {
		t.Errorf("the reason must be plain: %q", row.Error)
	}
	online = true
	Fresh()
	deadline := time.Now().Add(3 * time.Second)
	for patStatus(State()) != "up to date" {
		if time.Now().After(deadline) {
			t.Fatalf("an offline answer was kept: %q", patStatus(State()))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

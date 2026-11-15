package updates

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

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

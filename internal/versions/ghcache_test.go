package versions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
)

// githubStub stands in for api.github.com and github.com, and counts what reaches it.
type githubStub struct {
	srv       *httptest.Server
	api, web  atomic.Int32
	limited   atomic.Bool
	tag       atomic.Value
	sawToken  atomic.Value
	notModded atomic.Int32
}

func newStub(t *testing.T) *githubStub {
	t.Helper()
	g := &githubStub{}
	g.tag.Store("v1.2.0")
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest") && strings.HasPrefix(r.URL.Path, "/repos/"):
			g.api.Add(1)
			g.sawToken.Store(r.Header.Get("Authorization"))
			if g.limited.Load() {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(40*time.Minute).Unix()))
				http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
				return
			}
			etag := `"` + g.tag.Load().(string) + `"`
			if r.Header.Get("If-None-Match") == etag {
				g.notModded.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			fmt.Fprintf(w, `{"tag_name":%q}`, g.tag.Load())
		case strings.HasSuffix(r.URL.Path, "/releases/latest"): // github.com's page: a redirect to the tag
			g.web.Add(1)
			http.Redirect(w, r, strings.TrimSuffix(r.URL.Path, "latest")+"tag/"+g.tag.Load().(string), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.srv.Close)
	oldAPI, oldWeb, oldDir, oldCfg, oldNow, oldFresh := apiBase, webBase, CacheDir, ConfigDir, now, freshFor
	apiBase, webBase = g.srv.URL, g.srv.URL
	dir := t.TempDir()
	CacheDir, ConfigDir = func() string { return dir + "/cache" }, func() string { return dir + "/config" }
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Cleanup(func() {
		apiBase, webBase, CacheDir, ConfigDir, now, freshFor = oldAPI, oldWeb, oldDir, oldCfg, oldNow, oldFresh
	})
	return g
}

var tool = catalog.Package{Name: "tool", Repo: "owner/tool"}

func TestLatestIsAskedOnceAndThenRemembered(t *testing.T) {
	g := newStub(t)
	for i := 0; i < 5; i++ {
		v, err := Latest(context.Background(), tool)
		if err != nil || v != "v1.2.0" && v != "1.2.0" {
			t.Fatalf("%q %v", v, err)
		}
	}
	if n := g.api.Load(); n != 1 {
		t.Errorf("GitHub was asked %d times for the same thing; the answer is good for hours", n)
	}
}

func TestAnOlderAnswerIsCheckedWithItsETagAndCostsNothingWhenUnchanged(t *testing.T) {
	g := newStub(t)
	if _, err := Latest(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	now = func() time.Time { return t0.Add(freshFor + time.Minute) }
	if v, err := Latest(context.Background(), tool); err != nil || !strings.HasSuffix(v, "1.2.0") {
		t.Fatalf("%q %v", v, err)
	}
	if g.api.Load() != 2 || g.notModded.Load() != 1 {
		t.Errorf("want one conditional request answered 'not modified': api=%d 304s=%d", g.api.Load(), g.notModded.Load())
	}
	// And a new release is seen once the answer is old enough.
	g.tag.Store("v1.3.0")
	now = func() time.Time { return t0.Add(3*freshFor + time.Minute) }
	if v, _ := Latest(context.Background(), tool); !strings.HasSuffix(v, "1.3.0") {
		t.Errorf("a new release must be noticed: %q", v)
	}
}

func TestCheckNowLooksAgainAfterAFewMinutesNotHours(t *testing.T) {
	g := newStub(t)
	Latest(context.Background(), tool)
	t0 := time.Now()
	now = func() time.Time { return t0.Add(10 * time.Minute) }
	Latest(context.Background(), tool)
	if g.api.Load() != 1 {
		t.Fatalf("an ordinary lookup must not ask again after 10 minutes: %d", g.api.Load())
	}
	Latest(WithRefresh(context.Background()), tool)
	if g.api.Load() != 2 {
		t.Errorf("'check now' must ask: %d", g.api.Load())
	}
}

func TestWhenTheLimitIsReachedNothingMoreIsAskedAndTheLastAnswerIsUsed(t *testing.T) {
	g := newStub(t)
	Latest(context.Background(), tool) // a good answer is cached
	g.limited.Store(true)
	t0 := time.Now()
	now = func() time.Time { return t0.Add(freshFor + time.Minute) }
	for i := 0; i < 4; i++ {
		v, err := Latest(context.Background(), tool)
		if err != nil || !strings.HasSuffix(v, "1.2.0") {
			t.Fatalf("the last answer should serve while limited: %q %v", v, err)
		}
	}
	if n := g.api.Load(); n != 2 { // the first lookup, and the one that found the limit
		t.Errorf("after being told to wait GitHub was asked %d times in all, want 2", n)
	}
}

func TestWithNoCachedAnswerALimitedLookupUsesGithubsWebPage(t *testing.T) {
	g := newStub(t)
	g.limited.Store(true)
	v, err := Latest(context.Background(), tool)
	if err != nil || !strings.HasSuffix(v, "1.2.0") {
		t.Fatalf("%q %v", v, err)
	}
	if g.web.Load() != 1 {
		t.Errorf("web page lookups: %d", g.web.Load())
	}
	// The next one does not even try the API.
	api := g.api.Load()
	Latest(context.Background(), tool)
	if g.api.Load() != api {
		t.Error("the API must be left alone until the limit resets")
	}
	// Asking for assets needs the API, and says why it cannot.
	_, _, err = ReleaseAssets(context.Background(), "owner/other")
	if !IsRateLimit(err) || !strings.Contains(err.Error(), "github-token") {
		t.Errorf("assets while limited: %v", err)
	}
}

func TestATokenFromTheEnvironmentOrTheConfigFileIsSent(t *testing.T) {
	g := newStub(t)
	t.Setenv("GITHUB_TOKEN", "abc123")
	Latest(context.Background(), tool)
	if got, _ := g.sawToken.Load().(string); got != "Bearer abc123" {
		t.Errorf("authorization: %q", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if token() != "" {
		t.Fatal("no token expected")
	}
}

func TestAProjectWithNoReleasesIsNotAskedAboutAgainAndTagsAreTriedOnce(t *testing.T) {
	g := newStub(t)
	p := catalog.Package{Name: "x", Repo: "owner/norelease"}
	g.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.api.Add(1)
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `[{"name":"1.8-rc1"},{"name":"v1.7.0"},{"name":"v1.10.0"},{"name":"v1.9.2"}]`)
	})
	for i := 0; i < 3; i++ {
		v, err := Latest(context.Background(), p)
		if err != nil || v != "v1.10.0" && v != "1.10.0" {
			t.Fatalf("%q %v", v, err)
		}
	}
	if n := g.api.Load(); n != 2 {
		t.Errorf("one release lookup (a 404, remembered) and one tags lookup: %d", n)
	}
}

func TestBestTag(t *testing.T) {
	got, err := bestTag([]string{"v1.9.0", "1.10.0", "v2.0.0-rc1", "nightly", "v1.2.3"})
	if err != nil || got != "1.10.0" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := bestTag([]string{"nightly", "v2-rc"}); err == nil {
		t.Error("no release tag among these")
	}
}

// A lookup something is installed from always asks again: a remembered answer from before a release names the one
// before it (hamstack updating itself on the Pi installed v0.2.0 an hour after v0.3.0 came out).
func TestAFreshLookupSeesANewReleaseAtOnce(t *testing.T) {
	g := newStub(t)
	if _, err := Latest(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	g.tag.Store("v1.3.0")
	if v, _ := Latest(context.Background(), tool); strings.HasSuffix(v, "1.3.0") {
		t.Fatal("the stub should be remembered for an ordinary lookup")
	}
	if v, err := Latest(WithFresh(context.Background()), tool); err != nil || !strings.HasSuffix(v, "1.3.0") {
		t.Errorf("a fresh lookup: %q %v", v, err)
	}
}

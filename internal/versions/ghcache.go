package versions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GitHub lets an address make 60 API requests an hour without a token. Checking a dozen programs on every
// dashboard start used that up in a few restarts, so every lookup goes through a cache on disk:
//
//   - an answer is reused for a few hours without asking again;
//   - an older one is asked about with its ETag, and a "not modified" reply costs nothing against the limit;
//   - when GitHub says the limit is reached, nothing more is asked until it resets, and the last answer is
//     used meanwhile (or, for a version number, the github.com web page, which is not part of the API);
//   - a token in GITHUB_TOKEN, GH_TOKEN or ~/.config/hamstack/github-token raises the limit to 5000.

var (
	apiBase  = "https://api.github.com"
	webBase  = "https://github.com"
	freshFor = 6 * time.Hour
	now      = time.Now
	// CacheDir is where answers are kept; a variable for tests.
	CacheDir = func() string {
		d, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		return filepath.Join(d, "hamstack", "github")
	}
	// ConfigDir is where the optional token file is; a variable for tests.
	ConfigDir = func() string {
		d, err := os.UserConfigDir()
		if err != nil {
			return ""
		}
		return filepath.Join(d, "hamstack")
	}
)

type refreshKey struct{}

type freshKey struct{}

// WithFresh marks a lookup that something will be installed from (hamstack updating itself): the cached answer is
// always asked about again (cheaply, with its ETag), since an answer from before a release names the one before it.
func WithFresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshKey{}, true)
}

// WithRefresh marks a lookup as one the person asked for ("check now"): an answer more than a few minutes old
// is asked about again, instead of waiting out the usual hours.
func WithRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, refreshKey{}, true)
}

// RateLimitError means GitHub's request limit for this address has been reached.
type RateLimitError struct {
	Until     time.Time
	HaveToken bool
}

func (e *RateLimitError) Error() string {
	msg := "GitHub's request limit for this address is used up until " + e.Until.Local().Format("15:04")
	if !e.HaveToken {
		msg += " (60 an hour without a token: put one in ~/.config/hamstack/github-token to raise it)"
	}
	return msg
}

type cacheEntry struct {
	ETag    string    `json:"etag,omitempty"`
	Status  int       `json:"status"`
	Body    []byte    `json:"body,omitempty"`
	Fetched time.Time `json:"fetched"`
}

func cachePath(name string) string {
	d := CacheDir()
	if d == "" {
		return ""
	}
	h := sha256.Sum256([]byte(name))
	return filepath.Join(d, hex.EncodeToString(h[:8])+".json")
}

func readEntry(url string) *cacheEntry {
	p := cachePath(url)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var e cacheEntry
	if json.Unmarshal(b, &e) != nil || e.Status == 0 {
		return nil
	}
	return &e
}

func writeEntry(url string, e *cacheEntry) {
	p := cachePath(url)
	if p == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	b, _ := json.Marshal(e)
	tmp, err := os.CreateTemp(filepath.Dir(p), ".entry-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(tmp.Name(), p)
	}
}

func limitFile() string { return cachePath("rate-limit") }

func limitedUntil() time.Time {
	p := limitFile()
	if p == "" {
		return time.Time{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}
	}
	var t time.Time
	_ = json.Unmarshal(b, &t)
	return t
}

func setLimitedUntil(t time.Time) {
	if p := limitFile(); p != "" && os.MkdirAll(filepath.Dir(p), 0o700) == nil {
		b, _ := json.Marshal(t)
		_ = os.WriteFile(p, b, 0o600)
	}
}

func token() string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	if d := ConfigDir(); d != "" {
		if b, err := os.ReadFile(filepath.Join(d, "github-token")); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// githubGet fetches a GitHub API URL through the cache and returns the body of a 200 answer.
func githubGet(ctx context.Context, url string) ([]byte, error) {
	maxAge := freshFor
	if ctx.Value(refreshKey{}) != nil {
		maxAge = 5 * time.Minute
	}
	if ctx.Value(freshKey{}) != nil {
		maxAge = 0
	}
	entry := readEntry(url)
	answer := func(e *cacheEntry) ([]byte, error) {
		if e.Status != http.StatusOK {
			return nil, fmt.Errorf("%s: %d %s", url, e.Status, http.StatusText(e.Status))
		}
		return e.Body, nil
	}
	if entry != nil && now().Sub(entry.Fetched) < maxAge {
		return answer(entry)
	}
	tok := token()
	if until := limitedUntil(); until.After(now()) {
		if entry != nil {
			return answer(entry) // better a day-old answer than none while GitHub says wait
		}
		return nil, &RateLimitError{Until: until, HaveToken: tok != ""}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "hamstack")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if entry != nil && entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if entry != nil {
			return answer(entry) // no network: the last answer is still the best there is
		}
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusNotModified && entry != nil:
		entry.Fetched = now()
		writeEntry(url, entry)
		return answer(entry)
	case resp.StatusCode == http.StatusOK:
		e := &cacheEntry{ETag: resp.Header.Get("ETag"), Status: 200, Body: body, Fetched: now()}
		writeEntry(url, e)
		return body, nil
	case resp.StatusCode == http.StatusNotFound:
		e := &cacheEntry{Status: 404, Fetched: now()} // a project with no releases stays that way: do not ask again for hours
		writeEntry(url, e)
		return answer(e)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden && limitReached(resp, body):
		until := limitReset(resp)
		setLimitedUntil(until)
		if entry != nil {
			return answer(entry)
		}
		return nil, &RateLimitError{Until: until, HaveToken: tok != ""}
	}
	if entry != nil && resp.StatusCode >= 500 {
		return answer(entry)
	}
	return nil, fmt.Errorf("%s: %s", url, resp.Status)
}

func limitReached(resp *http.Response, body []byte) bool {
	return resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" ||
		strings.Contains(strings.ToLower(string(body)), "rate limit")
}

// limitReset is when GitHub says to try again: the end of the hour's window, or Retry-After, or a quarter of
// an hour when it says nothing.
func limitReset(resp *http.Response) time.Time {
	if v, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && v > now().Unix() {
		return time.Unix(v, 0)
	}
	if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && v > 0 {
		return now().Add(time.Duration(v) * time.Second)
	}
	return now().Add(15 * time.Minute)
}

// IsRateLimit reports whether err is GitHub's limit being reached.
func IsRateLimit(err error) bool {
	_, ok := err.(*RateLimitError)
	return ok
}

// latestFromWeb finds a repository's newest release or tag from github.com's own pages, which are not counted
// against the API limit: "releases/latest" redirects to the tag, and "tags.atom" lists the tags.
func latestFromWeb(ctx context.Context, repo string) (string, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 20 * time.Second}
	get := func(url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "hamstack")
		return client.Do(req)
	}
	if resp, err := get(webBase + "/" + repo + "/releases/latest"); err == nil {
		resp.Body.Close()
		if _, tag, ok := strings.Cut(resp.Header.Get("Location"), "/releases/tag/"); ok && tag != "" && !strings.ContainsAny(tag, "/?#") {
			return tag, nil
		}
	}
	resp, err := get(webBase + "/" + repo + "/tags.atom")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s/%s/tags.atom: %s", webBase, repo, resp.Status)
	}
	var feed struct {
		Entries []struct {
			Link struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&feed); err != nil {
		return "", err
	}
	var names []string
	for _, e := range feed.Entries {
		if _, t, ok := strings.Cut(e.Link.Href, "/releases/tag/"); ok {
			names = append(names, t)
		}
	}
	return bestTag(names)
}

// Package versions finds installed versions and the latest upstream releases.
package versions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/distro"
)

// Installed describes a locally installed binary.
type Installed struct {
	Path    string
	Version string // empty if the binary exists but its version could not be read
	Origin  string // "system package", "user install", "other"
}

// Which finds a program, preferring the copy hamstack installs in ~/.local/bin over one earlier
// on PATH. A newer program built from source must win over the distribution's older one even
// when ~/.local/bin is not first on PATH, so every part of hamstack resolves programs through this.
func Which(bin string) (string, error) {
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", bin)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return exec.LookPath(bin)
}

// FindInstalled looks pkg up (see Which) and asks it for its version.
// It returns nil if the binary is not installed.
//
// A program that cannot print its version without a display (a Qt application) has it read from the record
// hamstack keeps when it builds one into ~/.local, or from the distribution's package database.
func FindInstalled(pkg catalog.Package) *Installed {
	path, err := Which(pkg.Binary)
	if err != nil {
		if path, err = WhichSbin(pkg.Binary); err != nil {
			return nil
		}
	}
	in := &Installed{Path: path, Origin: origin(path)}
	if !pkg.NoVersionFlag && pkg.Version != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Some tools exit non-zero when asked for help or version; the text is what matters.
		out, _ := exec.CommandContext(ctx, path, pkg.Args...).CombinedOutput()
		if m := pkg.Version.FindStringSubmatch(string(out)); m != nil {
			in.Version = m[1]
		}
	}
	if in.Version == "" {
		if in.Origin == "user install" {
			in.Version = RecordedVersion(pkg.Name)
		} else if v := PackageVersion(pkg); v != "" {
			in.Version = v
		}
	}
	// Two programs that provide the same binary (WSJT-X and WSJT-X Improved both give "wsjtx") cannot be told
	// apart by finding it: only hamstack's own record, or the package database, says which one is here.
	if sharesBinary(pkg) && in.Version == "" {
		return nil
	}
	return in
}

// recordDir is where hamstack notes the version of what it built from source.
func recordDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "hamstack", "installed")
}

// Record notes that name was built from source at version.
func Record(name, version string) error {
	if err := os.MkdirAll(recordDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(recordDir(), name), []byte(version+"\n"), 0o644)
}

// RecordedVersion is the version Record noted, or "".
func RecordedVersion(name string) string {
	b, err := os.ReadFile(filepath.Join(recordDir(), name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var leadingVersion = regexp.MustCompile(`(\d+(?:\.\d+)+)`)

// UpstreamVersion extracts the upstream release from a distribution's package version: "1:20250502-1" has
// none worth comparing, "2.8.0+250501+repack-1" is 2.8.0, "3.0.1-2.fc45" is 3.0.1.
func UpstreamVersion(pkgVersion string) string {
	if i := strings.Index(pkgVersion, ":"); i >= 0 && i < 3 {
		pkgVersion = pkgVersion[i+1:] // drop an epoch
	}
	if m := leadingVersion.FindStringSubmatch(pkgVersion); m != nil {
		return m[1]
	}
	return ""
}

// PackageVersion asks the system's package database for the version of the first of pkg's distribution
// packages that is installed.
func PackageVersion(pkg catalog.Package) string {
	fam := distroFamily()
	for _, name := range pkg.Distro[fam] {
		var out []byte
		var err error
		switch fam {
		case "debian":
			out, err = exec.Command("dpkg-query", "-W", "-f=${Version}", name).Output()
		case "fedora":
			out, err = exec.Command("rpm", "-q", "--qf", "%{VERSION}", name).Output()
		case "arch":
			out, err = exec.Command("pacman", "-Q", name).Output()
			if f := strings.Fields(string(out)); len(f) == 2 {
				out = []byte(f[1])
			}
		}
		if err == nil {
			if v := UpstreamVersion(strings.TrimSpace(string(out))); v != "" {
				return v
			}
		}
	}
	return ""
}

// distroFamily is "arch", "debian" or "fedora" for this system, or "".
func distroFamily() string {
	if f := distro.Detect().Family; f != distro.Unknown {
		return string(f)
	}
	return ""
}

func origin(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	home, _ := os.UserHomeDir()
	switch {
	case strings.HasPrefix(real, "/usr/local/"), home != "" && strings.HasPrefix(real, home+"/"):
		return "user install"
	case strings.HasPrefix(real, "/usr/"), strings.HasPrefix(real, "/bin/"), strings.HasPrefix(real, "/opt/"):
		return "system package"
	}
	return "other"
}

// ErrNoUpstream means a program has no place hamstack can look up its newest release (gpsd lives on GitLab).
var ErrNoUpstream = errors.New("no upstream to check")

// Latest returns the newest upstream release version for pkg: from GitHub, or from its SourceForge listing.
func Latest(ctx context.Context, pkg catalog.Package) (string, error) {
	if pkg.SF != nil {
		return latestSourceForge(ctx, pkg.SF)
	}
	if pkg.Repo == "" {
		return "", ErrNoUpstream
	}
	tag, err := latestRelease(ctx, pkg.Repo)
	if err != nil {
		tag, err = latestTag(ctx, pkg.Repo)
		if err != nil {
			return "", err
		}
	}
	return strings.TrimPrefix(tag, pkg.TagPrefix), nil
}

// Asset is a downloadable file attached to a release.
type Asset struct {
	Name   string
	URL    string
	SHA256 string // hex digest published by GitHub; empty if unavailable
}

// ReleaseAssets returns the latest release tag of repo and its assets.
func ReleaseAssets(ctx context.Context, repo string) (string, []Asset, error) {
	var r struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := getJSON(ctx, "https://api.github.com/repos/"+repo+"/releases/latest", &r); err != nil {
		return "", nil, err
	}
	var out []Asset
	for _, a := range r.Assets {
		out = append(out, Asset{Name: a.Name, URL: a.URL, SHA256: strings.TrimPrefix(a.Digest, "sha256:")})
	}
	return r.Tag, out, nil
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "hamstack")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func latestRelease(ctx context.Context, repo string) (string, error) {
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := getJSON(ctx, "https://api.github.com/repos/"+repo+"/releases/latest", &r); err != nil {
		return "", err
	}
	if r.Tag == "" {
		return "", fmt.Errorf("no release tag")
	}
	return r.Tag, nil
}

// latestTag is the fallback for projects that tag versions without publishing releases.
func latestTag(ctx context.Context, repo string) (string, error) {
	var tags []struct {
		Name string `json:"name"`
	}
	if err := getJSON(ctx, "https://api.github.com/repos/"+repo+"/tags?per_page=30", &tags); err != nil {
		return "", err
	}
	best := ""
	for _, t := range tags {
		n := strings.TrimLeft(t.Name, "vV")
		if n == "" || n[0] < '0' || n[0] > '9' || strings.ContainsAny(n, "-_") {
			continue // skip pre-release tags such as 1.8-rc1
		}
		if best == "" || Compare(n, best) > 0 {
			best = t.Name
		}
	}
	if best == "" {
		return "", fmt.Errorf("no release tags found")
	}
	return best, nil
}

// Compare returns -1, 0 or 1 comparing dotted numeric versions a and b.
func Compare(a, b string) int {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parts(v string) []int {
	var out []int
	for _, s := range strings.Split(strings.TrimLeft(v, "vV"), ".") {
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		n, _ := strconv.Atoi(s[:end])
		out = append(out, n)
	}
	return out
}

// WhichSbin is Which, also looking in the system administration directories. gpsd, for one, installs to
// /usr/sbin on Debian, which is not on an ordinary user's PATH.
func WhichSbin(bin string) (string, error) {
	if p, err := Which(bin); err == nil {
		return p, nil
	}
	for _, d := range []string{"/usr/local/sbin", "/usr/sbin", "/sbin"} {
		p := filepath.Join(d, bin)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// latestSourceForge finds the newest version in a SourceForge project's listing: its RSS feed of files, or,
// when that only lists recent files, the folder names on its web page.
func latestSourceForge(ctx context.Context, sf *catalog.SourceForge) (string, error) {
	var url string
	if sf.HTML {
		url = "https://sourceforge.net/projects/" + sf.Project + "/files" + sf.Path + "/"
	} else {
		url = "https://sourceforge.net/projects/" + sf.Project + "/rss?path=" + sf.Path
	}
	body, err := getText(ctx, url)
	if err != nil {
		return "", err
	}
	v := newestIn(body, sf)
	if v == "" {
		return "", fmt.Errorf("no release found in %s", url)
	}
	return v, nil
}

var nameInListing = regexp.MustCompile(`(?:/files|<link>https://sourceforge\.net/projects/[^/]+/files)[^"<>\s]*?/([A-Za-z0-9_.\-]+)(?:/download|/")`)

// newestIn reads file and folder names out of a SourceForge page or feed and returns the highest version among
// those that match the project's pattern.
func newestIn(body string, sf *catalog.SourceForge) string {
	best := ""
	for _, m := range nameInListing.FindAllStringSubmatch(body, -1) {
		if g := sf.File.FindStringSubmatch(m[1]); g != nil && (best == "" || Compare(g[1], best) > 0) {
			best = g[1]
		}
	}
	return best
}

func getText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "hamstack")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err
}

// FindSourceForgeFile returns the download URL of the source archive in a SourceForge folder, for projects whose
// file names carry a build date as well as the version.
func FindSourceForgeFile(ctx context.Context, f *catalog.SourceFile, version string) (string, error) {
	dir := strings.ReplaceAll(f.Dir, "{version}", version)
	re, err := regexp.Compile(strings.ReplaceAll(f.File, "{version}", regexp.QuoteMeta(version)))
	if err != nil {
		return "", err
	}
	body, err := getText(ctx, "https://sourceforge.net/projects/"+f.Project+"/rss?path="+strings.ReplaceAll(dir, " ", "%20"))
	if err != nil {
		return "", err
	}
	var found []string
	for _, m := range regexp.MustCompile(`<link>(https://sourceforge\.net/projects/[^<]*?/([^/<]+)/download)</link>`).FindAllStringSubmatch(body, -1) {
		if re.MatchString(m[2]) {
			found = append(found, m[1])
		}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no file matching %s in %s", re, dir)
	}
	sort.Strings(found)
	return found[len(found)-1], nil // the latest build date sorts last
}

// sharesBinary reports whether another program in the catalog provides the same executable.
func sharesBinary(pkg catalog.Package) bool {
	for _, o := range catalog.All {
		if o.Name != pkg.Name && o.Binary == pkg.Binary {
			return true
		}
	}
	return false
}

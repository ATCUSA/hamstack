package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ATCUSA/hamstack/internal/versions"
)

// data2gArch maps Go's architecture names to the ones in Data2G's release file names.
var data2gArch = map[string]string{"amd64": "x86_64", "arm64": "aarch64", "arm": "armhf"}

// Data2GPlan downloads the upstream Data2G release, verifies its checksum and installs it. The release is a
// self-contained folder (the programs, their own Qt and Hamlib libraries, and a launcher script), so it goes to
// ~/.local/share/data2g whole, and the launcher is linked into ~/.local/bin as "data2g". The launcher finds its
// libraries from where it really is, so the link is enough. "data2g host" and "data2g web" run the other
// front ends; with no argument it opens the GUI.
func Data2GPlan(ctx context.Context, e Env) ([]Step, error) {
	arch, ok := data2gArch[e.Arch]
	if !ok {
		return nil, fmt.Errorf("no prebuilt Data2G for %s: see https://github.com/arodland/Data2G/releases", e.Arch)
	}
	tag, assets, err := versions.ReleaseAssets(ctx, "arodland/Data2G")
	if err != nil {
		return nil, fmt.Errorf("looking up the latest Data2G release: %w", err)
	}
	version := strings.TrimPrefix(tag, "v")
	want := fmt.Sprintf("data2g-%s-linux-%s.tar.gz", version, arch)
	var asset *versions.Asset
	for i := range assets {
		if assets[i].Name == want {
			asset = &assets[i]
		}
	}
	if asset == nil {
		return nil, fmt.Errorf("Data2G release %s has no %s", tag, want)
	}
	if asset.SHA256 == "" {
		return nil, fmt.Errorf("GitHub published no checksum for %s; refusing to install an unverified download", want)
	}
	dest := filepath.Join(e.Home, ".local", "share", "data2g")
	link := filepath.Join(e.prefix(), "bin", "data2g")
	return []Step{{
		Desc: fmt.Sprintf("Download Data2G %s (%s, with its own Qt libraries; needs glibc 2.39 or newer), verify sha256 %s…, install to %s and link %s", version, want, asset.SHA256[:12], dest, link),
		Do: func() error {
			tmp, err := os.MkdirTemp("", "hamstack-data2g-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			archive := filepath.Join(tmp, want)
			if err := Download(context.Background(), asset.URL, archive, asset.SHA256); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			// Unpack next to the destination so the final move is a rename on one filesystem.
			work, err := os.MkdirTemp(filepath.Dir(dest), ".data2g-new-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)
			top, err := ExtractTree(archive, work)
			if err != nil {
				return err
			}
			if top == "" {
				return fmt.Errorf("%s does not hold one folder, which is not what hamstack expects of a Data2G release", want)
			}
			src := filepath.Join(work, top)
			for _, f := range []string{"data2g", filepath.Join("bin", "data2g-host")} {
				if _, err := os.Stat(filepath.Join(src, f)); err != nil {
					return fmt.Errorf("%s has no %s: the release layout changed and hamstack's recipe needs updating", want, f)
				}
			}
			old := dest + ".old"
			_ = os.RemoveAll(old)
			if _, err := os.Lstat(dest); err == nil {
				if err := os.Rename(dest, old); err != nil {
					return err
				}
			}
			if err := os.Rename(src, dest); err != nil {
				_ = os.Rename(old, dest) // put the previous one back
				return err
			}
			_ = os.RemoveAll(old)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				return err
			}
			if fi, err := os.Lstat(link); err == nil {
				if fi.Mode()&os.ModeSymlink == 0 {
					return fmt.Errorf("%s exists and is not a link: not replacing it (Data2G is installed at %s)", link, dest)
				}
				_ = os.Remove(link)
			}
			if err := os.Symlink(filepath.Join(dest, "data2g"), link); err != nil {
				return err
			}
			fmt.Printf("installed: %s (launcher: %s)\n", dest, link)
			return versions.RecordAt(e.Home, Data2G, version)
		},
	}}, nil
}

package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/updates"
	"github.com/ATCUSA/hamstack/internal/versions"
)

func selfUpdateAsset(assets []versions.Asset, goos, arch string) (versions.Asset, error) {
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		token := arch
		if arch == "arm" {
			token = "armv7"
		}
		pattern := `^hamstack[-_]` + regexp.QuoteMeta(goos) + `[-_]` + regexp.QuoteMeta(token) + `(?:$|\.tar\.gz$|\.exe$)`
		if !regexp.MustCompile(pattern).MatchString(name) {
			continue
		}
		if strings.HasSuffix(name, ".sha256") || strings.Contains(name, "checksum") || strings.HasSuffix(name, ".sig") {
			continue
		}
		if strings.HasSuffix(name, ".tar.gz") || filepath.Ext(name) == "" || strings.HasSuffix(name, ".exe") {
			return asset, nil
		}
	}
	return versions.Asset{}, fmt.Errorf("no supported hamstack binary or .tar.gz asset for %s/%s", goos, arch)
}
func updateSelf(args []string) error {
	fs := flag.NewFlagSet("updates self", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm replacement")
	dry := fs.Bool("dry-run", false, "show release and asset only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tag, assets, err := versions.ReleaseAssets(ctx, updates.Repository)
	if err != nil {
		return fmt.Errorf("cannot find a published hamstack release at https://github.com/%s/releases: %w", updates.Repository, err)
	}
	asset, err := selfUpdateAsset(assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	fmt.Printf("hamstack %s -> %s\nSource: https://github.com/%s/releases\nAsset: %s\nReplace: %s\n", updates.Current(), tag, updates.Repository, asset.Name, exe)
	if strings.HasPrefix(updates.Current(), "dev") {
		fmt.Println("This is a development build; a published release may not contain its unpublished changes.")
	}
	if *dry {
		return nil
	}
	if !*yes && !(&asker{in: bufioStdin(), out: os.Stdout}).confirm("Install this release? A backup of the current executable is kept.", false) {
		return fmt.Errorf("cancelled")
	}

	dir, err := os.MkdirTemp("", "hamstack-self-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if _, e := hex.DecodeString(asset.SHA256); len(asset.SHA256) != 64 || e != nil {
		asset.SHA256 = ""
		for _, manifest := range assets {
			if manifest.Name != "SHA256SUMS" {
				continue
			}
			file := filepath.Join(dir, "SHA256SUMS")
			if err := install.Download(ctx, manifest.URL, file, manifest.SHA256); err != nil {
				return err
			}
			body, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(body), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset.Name {
					if _, err := hex.DecodeString(fields[0]); err == nil && len(fields[0]) == 64 {
						asset.SHA256 = fields[0]
					}
				}
			}
		}
		if asset.SHA256 == "" {
			return fmt.Errorf("release has no valid published SHA-256 digest or SHA256SUMS entry for %s; refusing an unverified replacement", asset.Name)
		}
	}
	archive := filepath.Join(dir, asset.Name)
	if err := install.Download(ctx, asset.URL, archive, asset.SHA256); err != nil {
		return err
	}
	binary := archive
	if strings.HasSuffix(asset.Name, ".tar.gz") {
		binary = filepath.Join(dir, "hamstack")
		if err := install.ExtractFile(archive, "hamstack", binary); err != nil {
			return err
		}
	}
	in, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".hamstack-update-*")
	if err != nil {
		return fmt.Errorf("cannot update this executable as the current user; install hamstack in ~/.local/bin: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link the previous inode as a rollback copy without interrupting running processes.
	backup := exe + ".previous"
	if fileExists(backup) {
		if err := os.Remove(backup); err != nil {
			return err
		}
	}
	if err := os.Link(exe, backup); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return err
	}
	fmt.Printf("Installed %s. Previous binary: %s\n", tag, backup)
	return offerMaintenance(&asker{in: bufioStdin(), out: os.Stdout}, false)
}

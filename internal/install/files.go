// Package install builds and runs install plans for the packages hamstack manages.
package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Download fetches url into dest. If wantSHA256 is non-empty the download is rejected
// unless its SHA-256 matches. The file is only moved into place once it is verified.
func Download(ctx context.Context, url, dest, wantSHA256 string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hamstack")
	resp, err := downloadClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxDownload+1))
	if err == nil && n > maxDownload {
		err = fmt.Errorf("download %s: larger than %d MB, refusing it", url, maxDownload>>20)
	}
	if err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if wantSHA256 != "" && !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, wantSHA256)
	}
	return os.Rename(tmp.Name(), dest)
}

// maxDownload bounds a download, so a wrong or hostile server cannot fill the disk.
var maxDownload int64 = 1 << 30

// downloadClient follows redirects (GitHub and SourceForge both redirect to their download hosts) but never
// from HTTPS to plain HTTP: several source archives have no published checksum, so the transport is all that
// vouches for them.
var downloadClient = &http.Client{CheckRedirect: noDowngrade}

func noDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing a redirect from HTTPS to %s://%s", req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// replaceFile writes data to dest through a temporary file renamed into place. Unlike writing in place it
// works while dest is a running program (which fails with "text file busy"), and a reader never sees half a
// file.
func replaceFile(dest string, data io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".hamstack-new-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode.Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// installProgram copies a built program to dest (mode 0755), replacing a running one safely.
func installProgram(built, dest string) error {
	f, err := os.Open(built)
	if err != nil {
		return fmt.Errorf("the build did not produce %s", built)
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("the build did not produce %s", built)
	}
	return replaceFile(dest, f, 0o755)
}

// fetchSource downloads a .tar.gz source archive into a new temporary folder and unpacks it. It returns that
// folder, which the caller removes when done, and the directory the source unpacked into. wantSHA256 may be
// empty when upstream publishes no checksum.
func fetchSource(name, url, wantSHA256 string) (work, src string, err error) {
	if work, err = os.MkdirTemp("", "hamstack-"+name+"-"); err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(work)
			work = ""
		}
	}()
	archive := filepath.Join(work, "src.tar.gz")
	if err = Download(context.Background(), url, archive, wantSHA256); err != nil {
		return work, "", err
	}
	out := filepath.Join(work, "src")
	if err = os.Mkdir(out, 0o755); err != nil {
		return work, "", err
	}
	top, err := ExtractAll(archive, out)
	if err != nil {
		return work, "", fmt.Errorf("unpacking the %s source: %w", name, err)
	}
	if top == "" {
		return work, "", fmt.Errorf("the %s source archive does not unpack into a single folder", name)
	}
	return work, filepath.Join(out, top), nil
}

// maxExtract bounds what we will unpack from an archive.
const maxExtract = 512 << 20

// ExtractFile pulls the single regular file whose base name is name out of a .tar.gz
// and writes it to dest with mode 0755. Archive paths are never used to build the output
// path, and macOS "._" metadata entries are ignored.
func ExtractFile(archive, name, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in %s", name, filepath.Base(archive))
		}
		if err != nil {
			return err
		}
		base := path.Base(h.Name)
		if h.Typeflag != tar.TypeReg || base != name || strings.HasPrefix(base, "._") {
			continue
		}
		tmp, err := os.CreateTemp(filepath.Dir(dest), ".extract-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, io.LimitReader(tr, maxExtract)); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), dest)
	}
}

// ExtractAll unpacks a .tar.gz into dir (which must exist). Entries that would land outside
// dir, links, and special files are skipped. It returns the name of the single top-level
// directory if the archive has one.
func ExtractAll(archive, dir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	tops := map[string]bool{}
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		clean := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || clean == ".." {
			continue
		}
		if first, _, _ := strings.Cut(clean, "/"); first != "pax_global_header" {
			tops[first] = true
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			total += h.Size
			if total > maxExtract {
				return "", fmt.Errorf("archive too large")
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
			// Keep the archive's timestamps, as tar does. Autotools tarballs contain generated files
			// (configure, Makefile.in) that must not look older than their sources, or make tries to
			// regenerate them with tools the user does not have.
			if !h.ModTime.IsZero() {
				if err := os.Chtimes(target, h.ModTime, h.ModTime); err != nil {
					return "", err
				}
			}
		}
	}
	if len(tops) == 1 {
		for t := range tops {
			return t, nil
		}
	}
	return "", nil
}

package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// AppPlan installs a catalog program that has no plan of its own (the fl suite, WSJT-X, JS8Call, Xastir...):
// the distribution's package when there is one, otherwise a build from the upstream source into ~/.local.
// fromSource forces the build, for the newest release when the distribution's package lags upstream. latest is
// the newest upstream version ("" when it could not be looked up, which a source build cannot do without).
// The notice is the disclaimer for a source build, or "" for a package.
func AppPlan(ctx context.Context, e Env, pkg catalog.Package, latest string, fromSource bool) (steps []Step, notice string, err error) {
	fam := string(e.Distro.Family)
	if names := pkg.Distro[fam]; len(names) > 0 && !fromSource {
		s, err := e.pkgSteps("Install "+pkg.Name+" with the system package manager", names...)
		return s, "", err
	}
	if pkg.Source == nil {
		if fam == "arch" && len(pkg.AUR) > 0 {
			return nil, "", ErrAUR{Pkgs: pkg.AUR}
		}
		if fromSource {
			return nil, "", fmt.Errorf("%s has no source recipe in hamstack yet", pkg.Name)
		}
		return nil, "", fmt.Errorf("%s has no package for %s and no source recipe in hamstack", pkg.Name, e.Distro.Name)
	}
	if latest == "" {
		return nil, "", fmt.Errorf("cannot look up the latest %s release (is the network up?): a source build needs it", pkg.Name)
	}
	src := pkg.Source
	if why := src.NotOn[fam]; why != "" {
		return nil, "", fmt.Errorf("%s cannot be built on %s: %s", pkg.Name, e.Distro.Name, why)
	}
	deps := src.Deps[fam]
	if deps == nil {
		return nil, "", fmt.Errorf("unsupported distribution %q: hamstack has no build dependencies for it", e.Distro.ID)
	}
	steps, err = e.pkgSteps("Install the build tools and libraries "+pkg.Name+" needs", deps...)
	if err != nil {
		return nil, "", err
	}
	tag := strings.ReplaceAll(src.Tag, "{version}", latest)
	if src.Tag == "" {
		tag = latest
	}
	url := strings.NewReplacer("{version}", latest, "{tag}", tag).Replace(src.URL)
	if src.Find != nil {
		if url, err = versions.FindSourceForgeFile(ctx, src.Find, latest); err != nil {
			return nil, "", fmt.Errorf("%s: finding the source archive: %w", pkg.Name, err)
		}
	}
	var work, dir string
	steps = append(steps,
		Step{Desc: fmt.Sprintf("Download %s %s source", pkg.Name, latest), Cleanup: func() { removeWork(work) }, Do: func() (err error) {
			work, dir, err = fetchSource(pkg.Name, url, "")
			return err
		}})
	if len(src.Patches) > 0 {
		steps = append(steps, Step{Desc: "Adjust the source for current build tools", Do: func() error {
			return applyPatches(dir, src.Patches)
		}})
	}
	switch src.Build {
	case "autotools":
		if src.Bootstrap {
			steps = append(steps, Step{Desc: "Generate the configure script", Do: func() error {
				return runIn(dir, "autoreconf", "-fi")
			}})
		}
		steps = append(steps,
			Step{Desc: "Configure " + pkg.Name, Do: func() error {
				return runIn(dir, append([]string{"./configure", "--prefix=" + e.prefix()}, src.Args...)...)
			}},
			Step{Desc: "Compile " + pkg.Name + " (this is the slow part)", Do: func() error {
				return runIn(dir, "make", fmt.Sprintf("-j%d", e.Jobs), "V=0") // V=0: one line per file, not the whole compiler command
			}},
			Step{Desc: fmt.Sprintf("Install %s to %s", pkg.Name, e.prefix()), Do: func() error {
				if err := runIn(dir, "make", "install"); err != nil {
					return err
				}
				return finishBuild(e, pkg, latest)
			}})
	case "make":
		// A plain Makefile that builds one program next to itself and has no install target.
		steps = append(steps,
			Step{Desc: "Compile " + pkg.Name, Do: func() error {
				return runIn(dir, append([]string{"make", fmt.Sprintf("-j%d", e.Jobs)}, src.Args...)...)
			}},
			Step{Desc: fmt.Sprintf("Install %s to %s", pkg.Name, filepath.Join(e.prefix(), "bin")), Do: func() error {
				if err := installProgram(filepath.Join(dir, pkg.Binary), filepath.Join(e.prefix(), "bin", pkg.Binary)); err != nil {
					return err
				}
				return finishBuild(e, pkg, latest)
			}})
	case "cmake":
		build := func() string { return filepath.Join(work, "build") }
		steps = append(steps,
			Step{Desc: "Configure " + pkg.Name, Do: func() error {
				args := append([]string{"-S", filepath.Join(dir, src.CMakeDir), "-B", build(), "-DCMAKE_BUILD_TYPE=Release", "-DCMAKE_INSTALL_PREFIX=" + e.prefix()}, src.Args...)
				return runIn("", append([]string{"cmake"}, args...)...)
			}},
			Step{Desc: "Compile " + pkg.Name + " (this is the slow part)", Do: func() error {
				return runIn("", "cmake", "--build", build(), "-j", fmt.Sprint(e.Jobs))
			}},
			Step{Desc: fmt.Sprintf("Install %s to %s", pkg.Name, e.prefix()), Do: func() error {
				// Some projects install files to fixed paths such as /usr/share/applications whatever the prefix. Install
				// into a staging folder instead, then move everything under the prefix: nothing is written outside it.
				stage := filepath.Join(work, "stage")
				fmt.Println("$ DESTDIR=" + stage + " cmake (install into the staging folder)")
				args := []string{"--install", build()}
				if src.InstallTarget != "" {
					args = []string{"--build", build(), "--target", src.InstallTarget, "-j", fmt.Sprint(e.Jobs)}
				}
				cmd := newCmd("cmake", args...)
				cmd.Env = append(os.Environ(), "DESTDIR="+stage)
				if err := cmd.Run(); err != nil {
					return err
				}
				n, err := InstallStaged(stage, e.prefix())
				if err != nil {
					return err
				}
				fmt.Printf("installed %d files under %s\n", n, e.prefix())
				return finishBuild(e, pkg, latest)
			}})
	default:
		return nil, "", fmt.Errorf("%s: unknown build kind %q", pkg.Name, src.Build)
	}
	return steps, SourceNotice(e, pkg, latest, url), nil
}

// finishBuild checks the program landed where it should and records its version, which Qt programs cannot print.
func finishBuild(e Env, pkg catalog.Package, version string) error {
	name := pkg.Binary
	if pkg.Source != nil && pkg.Source.Binary != "" {
		name = pkg.Source.Binary
	}
	bin := filepath.Join(e.prefix(), "bin", name)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("the build finished but %s is not at %s", name, bin)
	}
	if pkg.Source != nil {
		for link, target := range pkg.Source.Links { // the name the program is known by, so it can be found
			l := filepath.Join(e.prefix(), "bin", link)
			_ = os.Remove(l)
			if err := os.Symlink(target, l); err != nil {
				return err
			}
			fmt.Printf("linked: %s -> %s\n", l, target)
		}
	}
	fmt.Printf("installed: %s\n", bin)
	return versions.Record(pkg.Name, version)
}

func runIn(dir string, argv ...string) error {
	fmt.Println("$", strings.Join(argv, " "))
	cmd := newCmd(argv[0], argv[1:]...)
	cmd.Dir = dir
	return cmd.Run()
}

// SourceNotice is the disclaimer shown before a source build of a catalog program.
func SourceNotice(e Env, pkg catalog.Package, version, url string) string {
	deps := strings.Join(pkg.Source.Deps[string(e.Distro.Family)], ", ")
	var b strings.Builder
	fmt.Fprintf(&b, "Building %s %s from source\n", pkg.Name, version)
	fmt.Fprintf(&b, "  - It installs compilers and development libraries with your package manager (%s).\n", deps)
	fmt.Fprintf(&b, "  - It downloads the source from %s over HTTPS, builds it in a temporary folder and installs to %s.\n", hostOf(url), e.prefix())
	if pkg.Source.Notes != "" {
		fmt.Fprintf(&b, "  - %s\n", pkg.Source.Notes)
	}
	b.WriteString("  - Expect a few minutes, and much longer on a small board such as a Raspberry Pi.\n")
	b.WriteString("  - Those development packages stay installed afterwards; hamstack does not remove them.\n")
	return b.String()
}

func hostOf(url string) string {
	u := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}
	return u
}

// InstallStaged copies a staged install (a tree that holds files at their absolute install paths, as DESTDIR
// makes) under prefix. A file staged under prefix keeps its place; one staged under /usr/local or /usr (a fixed
// path the project ignored the prefix for) moves to the same place under prefix; anything else is skipped, so
// nothing can land outside prefix. Only regular files and symbolic links are copied (never a device or a
// pipe), without set-uid or set-gid bits. It returns how many files were copied.
func InstallStaged(stage, prefix string) (int, error) {
	stage, prefix = filepath.Clean(stage), filepath.Clean(prefix)
	under := func(p string) (string, bool) {
		r, err := filepath.Rel(stage, p)
		if err != nil {
			return "", false
		}
		abs := "/" + filepath.ToSlash(r)
		for _, root := range []string{prefix, "/usr/local", "/usr"} {
			if r, ok := strings.CutPrefix(abs, filepath.ToSlash(root)+"/"); ok {
				return r, true
			}
		}
		return "", false
	}
	n := 0
	err := filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		shown := strings.TrimPrefix(path, stage)
		r, ok := under(path)
		if !ok {
			fmt.Printf("skipping %s (outside %s)\n", shown, prefix)
			return nil
		}
		dest := filepath.Join(prefix, filepath.FromSlash(r))
		switch {
		case d.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			_ = os.Remove(dest)
			n++
			return os.Symlink(target, dest)
		case !d.Type().IsRegular():
			fmt.Printf("skipping %s (not a regular file)\n", shown)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		n++
		return replaceFile(dest, f, info.Mode().Perm()) // renamed into place: a running program can be replaced
	})
	return n, err
}

// applyPatches makes the recipe's exact text replacements in the extracted source. A replacement whose text is not
// found fails the build: it means upstream changed and the recipe needs looking at.
func applyPatches(dir string, patches []catalog.Patch) error {
	for _, p := range patches {
		path := filepath.Join(dir, p.File)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), p.Old) {
			return fmt.Errorf("the source no longer contains %q in %s: this recipe needs updating", p.Old, p.File)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(b), p.Old, p.New, 1)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

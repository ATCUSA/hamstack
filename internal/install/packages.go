package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// Names of what can be installed.
const (
	Hamlib  = "hamlib"
	Pat     = "pat"
	Mercury = "mercury"
)

// Env is what the plans need to know about the host.
type Env struct {
	Distro distro.Info
	Home   string // user's home; binaries go to Home/.local
	Root   bool   // running as root (no sudo needed)
	Sudo   bool   // sudo is available
	Arch   string // runtime.GOARCH
	Jobs   int    // parallel make jobs
}

// HostEnv describes the running system.
func HostEnv() Env {
	home, _ := os.UserHomeDir()
	_, err := exec.LookPath("sudo")
	return Env{Distro: distro.Detect(), Home: home, Root: os.Geteuid() == 0, Sudo: err == nil, Arch: runtime.GOARCH, Jobs: runtime.NumCPU()}
}

func (e Env) prefix() string { return filepath.Join(e.Home, ".local") }

// priv prefixes a command with sudo when not root.
func (e Env) priv(cmd ...string) ([]string, error) {
	if e.Root {
		return cmd, nil
	}
	if !e.Sudo {
		return nil, fmt.Errorf("need root to run %q and sudo is not installed: run hamstack as root or install sudo", cmd[0])
	}
	return append([]string{"sudo"}, cmd...), nil
}

// pkgInstall returns the privileged commands that install distro packages.
func (e Env) pkgInstall(pkgs ...string) ([][]string, error) {
	var cmds [][]string
	switch e.Distro.Family {
	case distro.Arch:
		cmds = [][]string{append([]string{"pacman", "-S", "--needed", "--noconfirm", "--disable-download-timeout"}, pkgs...)}
	case distro.Debian:
		cmds = [][]string{{"apt-get", "update"}, append([]string{"apt-get", "install", "-y"}, pkgs...)}
	case distro.Fedora:
		cmds = [][]string{append([]string{"dnf", "install", "-y"}, pkgs...)}
	default:
		return nil, fmt.Errorf("unsupported distribution %q: install %s manually", e.Distro.ID, strings.Join(pkgs, ", "))
	}
	out := make([][]string, 0, len(cmds))
	for _, c := range cmds {
		p, err := e.priv(c...)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (e Env) pkgSteps(desc string, pkgs ...string) ([]Step, error) {
	cmds, err := e.pkgInstall(pkgs...)
	if err != nil {
		return nil, err
	}
	var steps []Step
	for i, c := range cmds {
		d := desc
		if i < len(cmds)-1 {
			d = "Refresh package lists"
		}
		steps = append(steps, Step{Desc: d, Cmd: c})
	}
	return steps, nil
}

// HamlibPlan installs hamlib, which provides rigctld.
func HamlibPlan(e Env) ([]Step, error) {
	name := map[distro.Family]string{distro.Arch: "hamlib", distro.Debian: "libhamlib-utils", distro.Fedora: "hamlib"}[e.Distro.Family]
	if name == "" {
		return nil, fmt.Errorf("unsupported distribution %q: install hamlib (rigctld) manually", e.Distro.ID)
	}
	return e.pkgSteps("Install hamlib (rigctld) with the system package manager", name)
}

var patArch = map[string]string{"amd64": "amd64", "arm64": "arm64", "386": "i386"}

// Note: the ctx given to the plan builders is only for looking things up while building the plan.
// The steps run later, after the caller has long since cancelled it, so they must not use it.

// PatPlan downloads the upstream Pat release, verifies its checksum and installs it to ~/.local/bin.
func PatPlan(ctx context.Context, e Env) ([]Step, error) {
	arch, ok := patArch[e.Arch]
	if !ok {
		return nil, fmt.Errorf("no prebuilt Pat tarball for %s: see https://github.com/la5nta/pat/releases", e.Arch)
	}
	tag, assets, err := versions.ReleaseAssets(ctx, "la5nta/pat")
	if err != nil {
		return nil, fmt.Errorf("looking up the latest Pat release: %w", err)
	}
	want := fmt.Sprintf("pat_%s_linux_%s.tar.gz", strings.TrimPrefix(tag, "v"), arch)
	var asset *versions.Asset
	for i := range assets {
		if assets[i].Name == want {
			asset = &assets[i]
		}
	}
	if asset == nil {
		return nil, fmt.Errorf("Pat release %s has no %s", tag, want)
	}
	if asset.SHA256 == "" {
		return nil, fmt.Errorf("GitHub published no checksum for %s; refusing to install an unverified binary", want)
	}
	dest := filepath.Join(e.prefix(), "bin", "pat")
	return []Step{{
		Desc: fmt.Sprintf("Download Pat %s (%s), verify sha256 %s…, install to %s", tag, want, asset.SHA256[:12], dest),
		Do: func() error {
			tmp, err := os.MkdirTemp("", "hamstack-pat-")
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
			return ExtractFile(archive, "pat", dest)
		},
	}}, nil
}

// mercuryBuildDeps lists the build dependencies per distribution family.
var mercuryBuildDeps = map[distro.Family][]string{
	distro.Arch:   {"base-devel", "pkgconf", "alsa-lib", "libpulse", "hamlib", "hidapi", "openssl"},
	distro.Debian: {"build-essential", "pkg-config", "libasound2-dev", "libpulse-dev", "libhamlib-dev", "libhidapi-dev", "libssl-dev"},
	distro.Fedora: {"gcc", "make", "pkgconf-pkg-config", "alsa-lib-devel", "pulseaudio-libs-devel", "hamlib-devel", "hidapi-devel", "openssl-devel"},
}

// MercurySourceNotice is the disclaimer shown before Mercury is built.
func MercurySourceNotice(e Env, version string) string {
	deps := strings.Join(mercuryBuildDeps[e.Distro.Family], ", ")
	return fmt.Sprintf(`Building Mercury %s from source
  - It installs compilers and development libraries with your package manager (%s).
  - It downloads the tagged source from GitHub over HTTPS (there is no published checksum), builds it in a
    temporary folder and installs to %s.
  - Expect a few minutes, and much longer on a small board such as a Raspberry Pi.
  - Those development packages stay installed afterwards; hamstack does not remove them.
`, version, deps, e.prefix())
}

// MercuryPlan installs build dependencies, then builds the latest tagged Mercury into ~/.local.
func MercuryPlan(ctx context.Context, e Env, latest string) ([]Step, error) {
	deps := mercuryBuildDeps[e.Distro.Family]
	if deps == nil {
		return nil, fmt.Errorf("unsupported distribution %q: see https://github.com/Rhizomatica/mercury for build instructions", e.Distro.ID)
	}
	steps, err := e.pkgSteps("Install Mercury's build dependencies", deps...)
	if err != nil {
		return nil, err
	}
	tag := "v" + strings.TrimPrefix(latest, "v")
	url := "https://github.com/Rhizomatica/mercury/archive/refs/tags/" + tag + ".tar.gz"
	var work, src string
	steps = append(steps,
		Step{Desc: fmt.Sprintf("Download Mercury %s source (GitHub tag archive over HTTPS)", tag), Cleanup: func() { removeWork(work) }, Do: func() (err error) {
			work, src, err = fetchSource("mercury", url, "")
			return err
		}},
		Step{Desc: fmt.Sprintf("Build and install Mercury to %s", e.prefix()), Do: func() error {
			args := []string{"-C", src, fmt.Sprintf("-j%d", e.Jobs), "install", "prefix=" + e.prefix(), "GIT_HASH=" + tag}
			fmt.Println("$ make " + strings.Join(args, " "))
			return runStreaming("make", args...)
		}},
	)
	return steps, nil
}

// removeWork deletes a build's temporary folder, if the download step got as far as making one.
func removeWork(dir string) {
	if dir != "" {
		os.RemoveAll(dir)
	}
}

func runStreaming(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// hamlibBuildDeps lists what building hamlib from its release tarball needs. The tarball ships a
// generated configure script, so no autotools are required: a compiler, make and pkg-config,
// plus libusb for USB-attached radios.
var hamlibBuildDeps = map[distro.Family][]string{
	distro.Arch:   {"base-devel", "libusb"},
	distro.Debian: {"build-essential", "pkg-config", "libusb-1.0-0-dev"},
	distro.Fedora: {"gcc", "make", "pkgconf-pkg-config", "libusb1-devel"},
}

// HamlibSourceNotice is the disclaimer shown before a source build.
func HamlibSourceNotice(e Env, version string) string {
	deps := strings.Join(hamlibBuildDeps[e.Distro.Family], ", ")
	return fmt.Sprintf(`Building hamlib %s from source
  - It installs compilers and development libraries with your package manager (%s).
  - It downloads the release tarball, checks its checksum, builds it in a temporary folder and
    installs to %s. Your distribution's hamlib is left alone; hamstack will use the newer one.
  - The build took about a minute and a half on a 20-core PC (including the tools); expect much
    longer on slower hardware such as a Raspberry Pi.
  - Those development packages stay installed afterwards; hamstack does not remove them.
`, version, deps, e.prefix())
}

// HamlibSourcePlan builds the latest hamlib release from source into ~/.local. ctx is only used
// for looking up the release while building the plan.
func HamlibSourcePlan(ctx context.Context, e Env) ([]Step, string, error) {
	deps := hamlibBuildDeps[e.Distro.Family]
	if deps == nil {
		return nil, "", fmt.Errorf("unsupported distribution %q: see https://github.com/Hamlib/Hamlib for build instructions", e.Distro.ID)
	}
	tag, assets, err := versions.ReleaseAssets(ctx, "Hamlib/Hamlib")
	if err != nil {
		return nil, "", fmt.Errorf("looking up the latest hamlib release: %w", err)
	}
	want := fmt.Sprintf("hamlib-%s.tar.gz", strings.TrimPrefix(tag, "v"))
	var asset *versions.Asset
	for i := range assets {
		if assets[i].Name == want {
			asset = &assets[i]
		}
	}
	if asset == nil {
		return nil, "", fmt.Errorf("hamlib release %s has no %s", tag, want)
	}
	if asset.SHA256 == "" {
		return nil, "", fmt.Errorf("GitHub published no checksum for %s; refusing to build an unverified download", want)
	}
	steps, err := e.pkgSteps("Install the build tools hamlib needs", deps...)
	if err != nil {
		return nil, "", err
	}
	var work, src string
	steps = append(steps,
		Step{Desc: fmt.Sprintf("Download hamlib %s (%s) and verify sha256 %s…", tag, want, asset.SHA256[:12]), Cleanup: func() { removeWork(work) }, Do: func() (err error) {
			work, src, err = fetchSource("hamlib", asset.URL, asset.SHA256)
			return err
		}},
		Step{Desc: "Configure hamlib (no language bindings)", Do: func() error {
			cmd := exec.Command("sh", "./configure", "--prefix="+e.prefix(), "--disable-static",
				"--without-cxx-binding", "--without-perl-binding", "--without-python-binding", "--without-tcl-binding", "--without-lua-binding")
			cmd.Dir = src
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		}},
		Step{Desc: "Compile hamlib (this is the slow part)", Do: func() error {
			start := time.Now()
			if err := runStreaming("make", "-C", src, fmt.Sprintf("-j%d", e.Jobs)); err != nil {
				return err
			}
			fmt.Printf("compile took %s\n", time.Since(start).Round(time.Second))
			return nil
		}},
		Step{Desc: fmt.Sprintf("Install hamlib to %s", e.prefix()), Do: func() error {
			if err := runStreaming("make", "-C", src, "install"); err != nil {
				return err
			}
			rigctld := filepath.Join(e.prefix(), "bin", "rigctld")
			out, err := exec.Command(rigctld, "--version").CombinedOutput()
			if err != nil {
				return fmt.Errorf("installed %s but it does not run: %v: %s", rigctld, err, strings.TrimSpace(string(out)))
			}
			fmt.Printf("installed: %s", out)
			return nil
		}},
	)
	return steps, strings.TrimPrefix(tag, "v"), nil
}

// Direwolf is the package name of the VHF/UHF packet soundcard TNC.
const Direwolf = "direwolf"

var direwolfBuildDeps = map[distro.Family][]string{
	distro.Arch:   {"base-devel", "cmake", "alsa-lib", "hamlib"},
	distro.Debian: {"build-essential", "cmake", "libasound2-dev", "libudev-dev", "libhamlib-dev"},
	distro.Fedora: {"gcc", "gcc-c++", "make", "cmake", "alsa-lib-devel", "systemd-devel", "hamlib-devel"},
}

// DirewolfSourceNotice is the disclaimer shown before a source build.
func DirewolfSourceNotice(e Env, version string) string {
	deps := strings.Join(direwolfBuildDeps[e.Distro.Family], ", ")
	return fmt.Sprintf(`Building Direwolf %s from source
  - It installs compilers and development libraries with your package manager (%s).
  - It downloads the release source from GitHub, builds it in a temporary folder and installs the
    program to %s. Nothing else on the system is changed.
  - The source download has no published checksum; it comes from GitHub over HTTPS.
  - Expect a few minutes, and much longer on a small board such as a Raspberry Pi.
  - Those development packages stay installed afterwards; hamstack does not remove them.
`, version, deps, e.prefix())
}

// DirewolfPlan installs Direwolf. Debian and Fedora have a package, which is used unless fromSource is
// set (it can lag upstream: Debian 13 ships 1.7 against 1.8.1). Arch has no package, so it is built from
// source into ~/.local. The notice is the disclaimer for a source build, or "" for a package.
func DirewolfPlan(ctx context.Context, e Env, fromSource bool) (steps []Step, notice string, err error) {
	if pkg := map[distro.Family]string{distro.Debian: "direwolf", distro.Fedora: "direwolf"}[e.Distro.Family]; pkg != "" && !fromSource {
		s, err := e.pkgSteps("Install Direwolf with the system package manager", pkg)
		return s, "", err
	}
	deps := direwolfBuildDeps[e.Distro.Family]
	if deps == nil {
		return nil, "", fmt.Errorf("unsupported distribution %q: see https://github.com/wb2osz/direwolf for build instructions", e.Distro.ID)
	}
	tag, _, err := versions.ReleaseAssets(ctx, "wb2osz/direwolf")
	if err != nil {
		return nil, "", fmt.Errorf("looking up the latest Direwolf release: %w", err)
	}
	steps, err = e.pkgSteps("Install the build tools Direwolf needs", deps...)
	if err != nil {
		return nil, "", err
	}
	url := "https://github.com/wb2osz/direwolf/archive/refs/tags/" + tag + ".tar.gz"
	var work, src, build string
	steps = append(steps,
		Step{Desc: fmt.Sprintf("Download Direwolf %s source (GitHub tag archive over HTTPS)", tag), Cleanup: func() { removeWork(work) }, Do: func() (err error) {
			work, src, err = fetchSource("direwolf", url, "")
			build = filepath.Join(work, "build")
			return err
		}},
		Step{Desc: "Configure Direwolf", Do: func() error {
			return runStreaming("cmake", "-S", src, "-B", build, "-DCMAKE_BUILD_TYPE=Release")
		}},
		Step{Desc: "Compile Direwolf (this is the slow part)", Do: func() error {
			start := time.Now()
			if err := runStreaming("cmake", "--build", build, "--target", "direwolf", "-j", fmt.Sprint(e.Jobs)); err != nil {
				return err
			}
			fmt.Printf("compile took %s\n", time.Since(start).Round(time.Second))
			return nil
		}},
		Step{Desc: fmt.Sprintf("Install Direwolf to %s", filepath.Join(e.prefix(), "bin")), Do: func() error {
			dest := filepath.Join(e.prefix(), "bin", "direwolf")
			if err := installProgram(filepath.Join(build, "src", "direwolf"), dest); err != nil {
				return err
			}
			out, _ := exec.Command(dest, "-h").CombinedOutput() // prints its version, then usage
			if !strings.Contains(string(out), "Dire Wolf version") {
				return fmt.Errorf("installed %s but it does not report a version: %s", dest, strings.TrimSpace(string(out)))
			}
			fmt.Printf("installed: %s\n", strings.SplitN(string(out), "\n", 2)[0])
			return nil
		}},
	)
	return steps, DirewolfSourceNotice(e, tag), nil
}

// GPSD is gpsd's name for install.
const GPSD = "gpsd"

// GPSDPlan installs gpsd from the distribution's package manager. gpsd is not built from source: the
// distribution packages are recent enough, and it is a daemon that needs its udev rules and users.
func GPSDPlan(e Env) ([]Step, error) {
	if _, ok := map[distro.Family]bool{distro.Arch: true, distro.Debian: true, distro.Fedora: true}[e.Distro.Family]; !ok {
		return nil, fmt.Errorf("unsupported distribution %q: install gpsd manually", e.Distro.ID)
	}
	return e.pkgSteps("Install gpsd with the system package manager", "gpsd")
}

// GPSDNotice explains what installing gpsd does to the rest of the system. On Debian and Ubuntu the
// package also starts a system-wide gpsd and a udev rule that hands every GPS receiver you plug in to it,
// and that gpsd then holds the receiver, so hamstack's own gpsd cannot open it.
func GPSDNotice(e Env) string {
	if e.Distro.Family != distro.Debian {
		return ""
	}
	return "Note: on Debian and Ubuntu the gpsd package also starts a system-wide gpsd (gpsd.socket) that\n" +
		"takes over GPS receivers when they are plugged in. hamstack runs its own gpsd for your receiver,\n" +
		"and the two cannot both open it. 'hamstack doctor' tells you if the system one is in the way and\n" +
		"how to turn it off (sudo systemctl disable --now gpsd.socket gpsd.service)."
}

// Priv returns cmd prefixed with sudo when hamstack is not root.
func (e Env) Priv(cmd ...string) ([]string, error) { return e.priv(cmd...) }

// ErrAUR is returned for packages that exist only in the Arch User Repository. hamstack does not run AUR
// helpers or build AUR packages on its own: they are community-maintained and run arbitrary build scripts.
type ErrAUR struct{ Pkgs []string }

func (e ErrAUR) Error() string {
	return strings.Join(e.Pkgs, ", ") + " is only in the AUR (community-maintained, not in Arch's repositories). " +
		"Review and install it yourself, for example: yay -S " + strings.Join(e.Pkgs, " ")
}

// AX25ToolsPlan installs the classic AX.25 tools: kissattach and friends (ax25-tools) and axcall and
// axlisten (ax25-apps).
func AX25ToolsPlan(e Env) ([]Step, error) {
	switch e.Distro.Family {
	case distro.Debian, distro.Fedora:
		return e.pkgSteps("Install the AX.25 tools (ax25-tools, ax25-apps)", "ax25-tools", "ax25-apps")
	case distro.Arch:
		return nil, ErrAUR{Pkgs: []string{"ax25-tools", "ax25-apps"}}
	}
	return nil, fmt.Errorf("unsupported distribution %q: install ax25-tools and ax25-apps manually", e.Distro.ID)
}

// RootFileStep writes content to path as root, keeping a copy of any existing file as path.hamstack-bak.
// The content is shown in the plan exactly as it will be written.
func RootFileStep(e Env, desc, path, content string) (Step, error) {
	q := "'" + strings.ReplaceAll(content, "'", `'\''`) + "'"
	script := fmt.Sprintf("mkdir -p %[1]s && if [ -f %[2]s ]; then cp -a %[2]s %[2]s.hamstack-bak; fi && printf %%s %[3]s > %[2]s",
		shellQuote(dirOf(path)), shellQuote(path), q)
	cmd, err := e.priv("sh", "-c", script)
	return Step{Desc: desc, Cmd: cmd}, err
}

func dirOf(p string) string {
	switch i := strings.LastIndex(p, "/"); {
	case i > 0:
		return p[:i]
	case i == 0:
		return "/"
	}
	return "."
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func newCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd
}

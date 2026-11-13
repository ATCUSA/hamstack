// Package catalog lists the software hamstack knows how to detect and update.
package catalog

import "regexp"

// Package describes one piece of software.
type Package struct {
	Name     string
	Desc     string
	Category string         // what it is for: see the Categories
	Binary   string         // executable to look for in PATH
	Args     []string       // arguments that make Binary print its version
	Version  *regexp.Regexp // first capture group is the version number

	// Upstream release source: GitHub "owner/repo" (TagPrefix is stripped from the tag), or a SourceForge
	// feed. Some projects keep a stale GitHub mirror, so the real home has to be used for their versions.
	Repo      string
	TagPrefix string
	SF        *SourceForge

	// Requires must be installed for this one to work; Suggests are worth installing with it. Conflicts cannot
	// be installed together (they provide the same program).
	Requires, Suggests, Conflicts []string

	// Distro lists the distribution package names, by family ("arch", "debian", "fedora"). A family that is
	// missing has no package for this program: it is built from source, or on Arch taken from the AUR.
	Distro map[string][]string
	AUR    []string // the Arch User Repository names, shown as a hint (hamstack never builds AUR packages)

	// Source says how to build it from the upstream source; nil means a custom plan in the install package.
	Source *Source

	// NoVersionFlag is set for programs that cannot print their version without a display (Qt applications):
	// the version is then read from the package database, or from the record hamstack keeps for its own builds.
	NoVersionFlag bool
}

// SourceForge locates a release on SourceForge: a project's file listing, and the file names in it.
type SourceForge struct {
	Project string
	Path    string         // folder in the project's files ("/flrig"); "" for the top level
	File    *regexp.Regexp // matches a file name or folder; the first group is the version
	HTML    bool           // read the web page's folder list instead of the RSS feed (needed when the RSS lists only recent files)
}

// Source is a recipe for building a program: where its source is, how, and what it needs.
type Source struct {
	URL       string              // {version} and {tag} are replaced
	Tag       string              // the tag, with {version}: "v{version}"
	Build     string              // "autotools" or "cmake"
	Bootstrap bool                // run autoreconf first (a repository archive has no configure script)
	Args      []string            // extra configure or cmake arguments
	Deps      map[string][]string // build dependencies by distribution family
	CMakeDir  string              // subfolder of the source that holds CMakeLists.txt ("" for the top)
	Notes     string              // shown with the source-build disclaimer
	// Find locates the source archive by listing a SourceForge folder, for projects whose file names carry more than
	// the version (a build date). Dir and File may contain {version}; File's match is the file to fetch.
	Find *SourceFile
	// NotOn names a distribution family this cannot be built on, and why (say, a library it needs that the
	// distribution no longer ships).
	NotOn map[string]string
	// InstallTarget names a build target to install with ("cmake --build --target T") instead of "cmake --install",
	// for projects (the WSJT-X superbuild) whose install is a custom target that the plain command does not run.
	InstallTarget string
	// Binary is the program the build installs, when it is not the name the program is known by (WSJT-X Improved
	// builds "ws"); Links are extra names for it in the bin folder, so the known name finds it.
	Binary string
	Links  map[string]string
	// Patches are exact text replacements made in the extracted source before building. The build stops, saying
	// the recipe needs updating, if the text is not there: a release that changed must not be patched blindly.
	Patches []Patch
}

// Patch replaces Old with New in File (a path inside the extracted source).
type Patch struct{ File, Old, New string }

// SourceFile is a source archive found in a SourceForge folder.
type SourceFile struct {
	Project, Dir, File string
}

var ver = func(re string) *regexp.Regexp { return regexp.MustCompile(re) }

// Category names, in the order they are listed.
var Categories = []struct{ ID, Title string }{
	{"winlink", "Winlink"},
	{"modem", "Modems and rig control"},
	{"digital", "Digital modes"},
	{"aprs", "APRS and position"},
	{"tools", "Tools"},
}

var sfFldigi = func(name string) *SourceForge {
	return &SourceForge{Project: "fldigi", Path: "/" + name, File: ver(`^` + name + `-(\d+\.\d+\.\d+)\.tar\.gz$`)}
}

// flBuildDeps are what the fl suite (flrig, fldigi, flmsg, flamp) needs to build: a C++ compiler and FLTK, with
// the X libraries FLTK draws with, and fldigi additionally needs audio libraries.
var flBase = map[string][]string{
	"arch":   {"base-devel", "fltk", "libx11", "libxft", "libxinerama", "libxcursor", "libxfixes", "libpng", "libjpeg-turbo", "libxml2"},
	"debian": {"build-essential", "pkg-config", "libfltk1.3-dev", "libx11-dev", "libxft-dev", "libxinerama-dev", "libxcursor-dev", "libxfixes-dev", "libpng-dev", "libjpeg-dev", "libxml2-dev", "libudev-dev"},
	"fedora": {"gcc-c++", "make", "redhat-rpm-config", "pkgconf-pkg-config", "fltk-devel", "libX11-devel", "libXft-devel", "libXinerama-devel", "libXcursor-devel", "libXfixes-devel", "libpng-devel", "libjpeg-turbo-devel", "libxml2-devel", "systemd-devel"},
}
var flAudio = map[string][]string{
	"arch":   {"libsamplerate", "libsndfile", "portaudio", "libpulse", "alsa-lib", "hamlib"},
	"debian": {"libsamplerate0-dev", "libsndfile1-dev", "portaudio19-dev", "libpulse-dev", "libasound2-dev", "libhamlib-dev"},
	"fedora": {"libsamplerate-devel", "libsndfile-devel", "portaudio-devel", "pulseaudio-libs-devel", "alsa-lib-devel", "hamlib-devel"},
}

func concat(m ...map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, f := range []string{"arch", "debian", "fedora"} {
		for _, x := range m {
			out[f] = append(out[f], x[f]...)
		}
	}
	return out
}

func flSource(name string, deps map[string][]string) *Source {
	return &Source{
		URL:   "https://sourceforge.net/projects/fldigi/files/" + name + "/" + name + "-{version}.tar.gz/download",
		Build: "autotools", Deps: deps,
		// Newer compilers no longer pull in pthread.h on their own, which the fl suite relies on.
		Args:  []string{"CXXFLAGS=-O2 -include pthread.h"},
		Notes: "The source comes from SourceForge over HTTPS (the project's own site); there is no published checksum next to it.",
	}
}

// wsjtxDeps are what WSJT-X (and WSJT-X Improved) need to build: Qt 5, Fortran, FFTW, Boost, hamlib and libusb.
var wsjtxDeps = map[string][]string{
	"arch":   {"base-devel", "cmake", "gcc-fortran", "fftw", "boost", "qt5-base", "qt5-multimedia", "qt5-serialport", "qt5-tools", "hamlib", "libusb"},
	"debian": {"build-essential", "cmake", "gfortran", "git", "pkg-config", "libfftw3-dev", "libboost-log-dev", "qtbase5-dev", "qtmultimedia5-dev", "libqt5serialport5-dev", "qttools5-dev", "qttools5-dev-tools", "libqt5websockets5-dev", "libhamlib-dev", "libusb-1.0-0-dev", "libudev-dev"},
	"fedora": {"gcc-c++", "gcc-gfortran", "make", "cmake", "pkgconf-pkg-config", "fftw-devel", "boost-devel", "qt5-qtbase-devel", "qt5-qtmultimedia-devel", "qt5-qtserialport-devel", "qt5-linguist", "qt5-qtwebsockets-devel", "hamlib-devel", "libusb1-devel", "systemd-devel"},
}

// wsjtxQt6Deps are for the Qt 6 build of WSJT-X Improved. Its archive is a "superbuild": it builds the hamlib it
// ships first, then WSJT-X, so it also needs git, patch and the autotools' usual company.
var wsjtxQt6Deps = map[string][]string{
	"arch":   {"base-devel", "cmake", "gcc-fortran", "git", "patch", "fftw", "boost", "qt6-base", "qt6-multimedia", "qt6-serialport", "qt6-websockets", "qt6-tools", "libusb", "portaudio", "xcb-util-cursor"},
	"debian": {"build-essential", "cmake", "gfortran", "git", "patch", "pkg-config", "libfftw3-dev", "libboost-log-dev", "qt6-base-dev", "qt6-multimedia-dev", "qt6-serialport-dev", "qt6-websockets-dev", "qt6-tools-dev", "qt6-tools-dev-tools", "libgl-dev", "libusb-1.0-0-dev", "libudev-dev", "portaudio19-dev", "libasound2-dev", "libxcb-cursor0"},
	"fedora": {"gcc-c++", "gcc-gfortran", "make", "cmake", "git", "patch", "pkgconf-pkg-config", "fftw-devel", "boost-devel", "qt6-qtbase-devel", "qt6-qtmultimedia-devel", "qt6-qtserialport-devel", "qt6-qtwebsockets-devel", "qt6-qttools-devel", "libusb1-devel", "systemd-devel", "portaudio-devel", "alsa-lib-devel", "xcb-util-cursor"},
}

// js8Deps are what JS8Call needs: Qt 6.4 or newer (no Fortran: the decoder is C++), FFTW, Boost and hamlib.
var js8Deps = map[string][]string{
	"arch":   {"base-devel", "cmake", "fftw", "boost", "qt6-base", "qt6-multimedia", "qt6-serialport", "hamlib", "libusb"},
	"debian": {"build-essential", "cmake", "pkg-config", "libfftw3-dev", "libboost-dev", "qt6-base-dev", "qt6-multimedia-dev", "qt6-serialport-dev", "libgl-dev", "libhamlib-dev", "libusb-1.0-0-dev", "libudev-dev"},
	"fedora": {"gcc-c++", "make", "cmake", "pkgconf-pkg-config", "fftw-devel", "boost-devel", "qt6-qtbase-devel", "qt6-qtmultimedia-devel", "qt6-qtserialport-devel", "hamlib-devel", "libusb1-devel", "systemd-devel"},
}

// xastirDeps are what Xastir needs: Motif and the X libraries, with curl, pcre2, Berkeley DB, shapelib and
// the autotools (a GitHub archive has no configure script).
var xastirDeps = map[string][]string{
	"arch":   {"base-devel", "autoconf", "automake", "libtool", "openmotif", "libxt", "libxpm", "libxext", "curl", "pcre2", "db", "shapelib", "libgeotiff", "graphicsmagick"},
	"debian": {"build-essential", "autoconf", "automake", "libtool", "pkg-config", "libmotif-dev", "libxt-dev", "libxpm-dev", "libx11-dev", "libxext-dev", "libcurl4-openssl-dev", "libpcre2-dev", "libdb-dev", "libshp-dev", "libgeotiff-dev", "libgraphicsmagick1-dev"},
	"fedora": {"gcc-c++", "make", "autoconf", "automake", "libtool", "pkgconf-pkg-config", "motif-devel", "libXt-devel", "libXpm-devel", "libX11-devel", "libXext-devel", "libcurl-devel", "pcre2-devel", "libdb-devel", "shapelib-devel", "libgeotiff-devel", "GraphicsMagick-devel"},
}

func debFed(pkg ...string) map[string][]string {
	return map[string][]string{"debian": pkg, "fedora": pkg}
}

// All is every program hamstack knows how to detect, install and update.
var All = []Package{
	{
		Name: "pat", Desc: "Winlink client", Category: "winlink", Binary: "pat",
		Args: []string{"version"}, Version: ver(`(?i)pat\D*(\d+\.\d+(?:\.\d+)?)`),
		Repo: "la5nta/pat", TagPrefix: "v",
		Suggests: []string{"mercury", "direwolf", "ardopcf"},
	},
	{
		Name: "mercury", Desc: "HF modem (VARA-compatible)", Category: "modem", Binary: "mercury",
		Args: []string{"-V"}, Version: ver(`Version\s+(\d+\.\d+(?:\.\d+)?)`),
		Repo: "Rhizomatica/mercury", TagPrefix: "v",
		Requires: []string{"hamlib"}, Suggests: []string{"pat"},
	},
	{
		Name: "hamlib", Desc: "rig control (rigctld)", Category: "modem", Binary: "rigctld",
		Args: []string{"--version"}, Version: ver(`Hamlib\s+(\d+\.\d+(?:\.\d+)?)`),
		Repo: "Hamlib/Hamlib", TagPrefix: "",
	},
	{
		Name: "direwolf", Desc: "soundcard packet TNC (VHF/UHF)", Category: "modem", Binary: "direwolf",
		Args: []string{"-h"}, Version: ver(`(?i)dire wolf\s+(?:release|version)\s+(\d+\.\d+(?:\.\d+)?)`),
		Repo: "wb2osz/direwolf", TagPrefix: "",
		Suggests: []string{"hamlib", "pat", "ax25"},
	},
	{
		Name: "ardopcf", Desc: "ARDOP HF modem (Pat can use it: an alternative to Mercury and VARA)", Category: "modem", Binary: "ardopcf",
		Args: []string{"-h"}, Version: ver(`ardopcf Version (\d+(?:\.\d+)+)`),
		Repo: "pflarue/ardop", TagPrefix: "",
		Suggests: []string{"pat", "hamlib"},
		AUR:      []string{"ardopcf"},
		Source: &Source{
			URL: "https://github.com/pflarue/ardop/archive/refs/tags/{tag}.tar.gz", Tag: "{version}", Build: "make",
			// The release passes a pointer where an int is expected (its CM108 PTT code), which GCC 14 and newer reject.
			// Its own prebuilt binaries are built with an older compiler; this lets a new one build the same code.
			Args:  []string{"CFLAGS=-O2 -Wno-error=int-conversion -Wno-error=incompatible-pointer-types"},
			Notes: "The release's source has two type errors that GCC 14 and newer refuse; the build turns them back into warnings.",
			Deps: map[string][]string{
				"arch":   {"base-devel", "alsa-lib"},
				"debian": {"build-essential", "libasound2-dev"},
				"fedora": {"gcc", "make", "alsa-lib-devel"},
			},
		},
	},
	{
		Name: "flrig", Desc: "rig control for the fl suite and hamlib (FLRig)", Category: "modem", Binary: "flrig",
		Args: []string{"--version"}, Version: ver(`Version:\s*(\d+\.\d+\.\d+)`),
		SF: sfFldigi("flrig"), Suggests: []string{"fldigi"},
		Distro: debFed("flrig"), AUR: []string{"flrig"},
		Source: flSource("flrig", flBase),
	},
	{
		Name: "fldigi", Desc: "digital modes (PSK, RTTY, Olivia, MFSK...)", Category: "digital", Binary: "fldigi",
		Args: []string{"--version"}, Version: ver(`fldigi\s+(\d+\.\d+\.\d+)`),
		SF: sfFldigi("fldigi"), Suggests: []string{"flrig", "flmsg", "flamp"},
		Distro: debFed("fldigi"), AUR: []string{"fldigi"},
		Source: flSource("fldigi", concat(flBase, flAudio)),
	},
	{
		Name: "flmsg", Desc: "forms and messages over fldigi (ICS forms, radiograms)", Category: "digital", Binary: "flmsg",
		Args: []string{"--version"}, Version: ver(`Version:\s*(\d+\.\d+\.\d+)`),
		SF: sfFldigi("flmsg"), Suggests: []string{"fldigi"},
		Distro: debFed("flmsg"), AUR: []string{"flmsg"},
		Source: flSource("flmsg", flBase),
	},
	{
		Name: "flamp", Desc: "file transfer over fldigi (amateur multicast protocol)", Category: "digital", Binary: "flamp",
		Args: []string{"--version"}, Version: ver(`Version:\s*(\d+\.\d+\.\d+)`),
		SF: sfFldigi("flamp"), Suggests: []string{"fldigi"},
		Distro: debFed("flamp"), AUR: []string{"flamp"},
		Source: flSource("flamp", flBase),
	},
	{
		Name: "wsjtx", Desc: "WSJT-X: FT8, FT4, WSPR and other weak-signal modes", Category: "digital", Binary: "wsjtx",
		Version: ver(`(\d+\.\d+\.\d+)`), NoVersionFlag: true,
		SF:       &SourceForge{Project: "wsjt", File: ver(`^wsjtx-(\d+\.\d+\.\d+)$`), HTML: true},
		Suggests: []string{"hamlib", "flrig"}, Conflicts: []string{"wsjtx-improved"},
		Distro: debFed("wsjtx"), AUR: []string{"wsjtx"},
		Source: &Source{
			URL: "https://sourceforge.net/projects/wsjt/files/wsjtx-{version}/wsjtx-{version}-src.tar.gz/download", Build: "cmake",
			Args:  []string{"-DWSJT_GENERATE_DOCS=OFF", "-DWSJT_SKIP_MANPAGES=ON"},
			Deps:  wsjtxDeps,
			NotOn: map[string]string{"arch": "WSJT-X needs Qt 5's WebSockets module, which Arch no longer ships; WSJT-X Improved has a Qt 6 build: hamstack install wsjtx-improved"},
			Notes: "WSJT-X is large (Fortran and C++ with Qt): expect a long build. The source comes from SourceForge, the project's own site.",
		},
	},
	{
		Name: "wsjtx-improved", Desc: "WSJT-X Improved: WSJT-X with extra features (the usual choice)", Category: "digital", Binary: "wsjtx",
		Version: ver(`(\d+\.\d+\.\d+)`), NoVersionFlag: true,
		SF:       &SourceForge{Project: "wsjt-x-improved", File: ver(`^WS_v(\d+\.\d+\.\d+)$`), HTML: true},
		Suggests: []string{"hamlib", "flrig"}, Conflicts: []string{"wsjtx"},
		Distro: map[string][]string{"debian": {"wsjtx-improved"}}, AUR: []string{"wsjtx-improved"},
		Source: &Source{
			Find:  &SourceFile{Project: "wsjt-x-improved", Dir: "/WS_v{version}/Source code/Qt6", File: `^ws-{version}_\d+_qt6\.tgz$`},
			Build: "cmake",
			// -DCMAKE_POLICY_VERSION_MINIMUM: the superbuild asks for CMake 2.8, which CMake 4 (Arch, Fedora) refuses.
			Args: []string{"-DWSJT_GENERATE_DOCS=OFF", "-DWSJT_SKIP_MANPAGES=ON", "-DCMAKE_POLICY_VERSION_MINIMUM=3.5"},
			Deps: wsjtxQt6Deps,
			// The superbuild's own install and package targets are named like CMake's reserved ones, which CMake 4 treats as
			// an error. Its install is the wsjtx-install target, so the reserved ones can simply go.
			Patches: []Patch{
				{"CMakeLists.txt", "add_custom_target (install DEPENDS wsjtx-install)", ""},
				{"CMakeLists.txt", "add_custom_target (package DEPENDS wsjtx-package)", ""},
				// The hamlib it bundles is built static. Where the INDI libraries are installed (Arch) it would build INDI
				// rotator support in, which WSJT-X then cannot link: leave INDI out.
				{"CMakeLists.txt", "--without-cxx-binding ${EXTRA_FLAGS}", "--without-cxx-binding --without-indi ${EXTRA_FLAGS}"},
			},
			InstallTarget: "wsjtx-install",
			Binary:        "ws", Links: map[string]string{"wsjtx": "ws"},
			Notes: "WSJT-X Improved is large (Fortran and C++ with Qt): expect a long build. The source comes from SourceForge, the project's own site.",
		},
	},
	{
		Name: "js8call", Desc: "JS8Call: keyboard-to-keyboard messaging on HF", Category: "digital", Binary: "js8call",
		Version: ver(`(\d+\.\d+\.\d+)`), NoVersionFlag: true,
		Repo: "js8call/js8call", TagPrefix: "v",
		Suggests: []string{"hamlib", "flrig"},
		Distro:   debFed("js8call"), AUR: []string{"js8call"},
		Source: &Source{
			URL: "https://github.com/js8call/js8call/archive/refs/tags/{tag}.tar.gz", Tag: "v{version}", Build: "cmake",
			Deps:  js8Deps,
			Notes: "JS8Call needs Qt 6.4 or newer; on an older distribution the build stops at the configure step.",
		},
	},
	{
		Name: "xastir", Desc: "APRS client with maps", Category: "aprs", Binary: "xastir",
		Version: ver(`(\d+\.\d+\.\d+)`), NoVersionFlag: true,
		Repo: "Xastir/Xastir", TagPrefix: "Release-",
		Suggests: []string{"direwolf", "gpsd"},
		Distro:   debFed("xastir"), AUR: []string{"xastir"},
		Source: &Source{
			URL: "https://github.com/Xastir/Xastir/archive/refs/tags/{tag}.tar.gz", Tag: "Release-{version}", Build: "autotools", Bootstrap: true,
			Deps: xastirDeps,
		},
	},
	{
		Name: "gpsd", Desc: "GPS daemon (position and your grid square)", Category: "aprs", Binary: "gpsd",
		Args: []string{"-V"}, Version: ver(`gpsd:?\s+(?:release\s+)?(\d+\.\d+(?:\.\d+)?)`),
		Repo: "", Distro: map[string][]string{"arch": {"gpsd"}, "debian": {"gpsd"}, "fedora": {"gpsd"}},
	},
	{
		Name: "ax25", Desc: "Linux AX.25 tools: kissattach, axcall, axlisten (needs a kernel with AX.25)", Category: "tools", Binary: "kissattach",
		Version: ver(`(\d+\.\d+\.\d+)`), NoVersionFlag: true,
		Suggests: []string{"direwolf"},
		Distro:   debFed("ax25-tools", "ax25-apps"), AUR: []string{"ax25-tools", "ax25-apps"},
	},
}

// ByName finds a package.
func ByName(name string) (Package, bool) {
	for _, p := range All {
		if p.Name == name {
			return p, true
		}
	}
	return Package{}, false
}

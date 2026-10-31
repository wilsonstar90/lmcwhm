package appstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/shirou/gopsutil/v4/mem"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// Compile install (編譯安裝): instead of running the official pre-built
// image (極速安裝), the same version is compiled from the upstream source on
// this server. The build stage starts from the official image itself (same
// distribution, libraries and paths), compiles into /out with the paths the
// image already uses, and the result is the official image with the
// compiled files laid over it, so its entrypoint, users and data layout keep
// working and the two modes can be switched both ways on the same data.

const (
	ModeFast   = "fast"
	ModeSource = "source"
)

var (
	ErrNoSource  = errors.New("app.no_source_build")
	ErrBuildArgs = errors.New("app.invalid_build_args")
	ErrBuildMode = errors.New("app.invalid_build_mode")
)

// buildArgRe keeps configure options and compiler flags to plain words: they
// reach a shell inside the build, so no quotes, $ or command separators.
var buildArgRe = regexp.MustCompile(`^[A-Za-z0-9 _=+,./:@%-]{0,1000}$`)

// ValidBuildArgs checks extra configure options or compiler flags.
func ValidBuildArgs(s string) bool { return buildArgRe.MatchString(s) }

// Recipe is how one app is compiled. Deps installs the build tools in the
// official image; Build compiles $VERSION into /out ($CONFIGURE_ARGS and
// $EXTRA_CFLAGS hold the user's additions).
type Recipe struct {
	Minutes  int    // rough build time on 2 cores
	MinMemMB int    // the compiler needs at least this much memory
	Defaults string // the options always used (shown in the UI)
	Deps     string
	Build    string
}

// SourceUnsupported says why an app has no compile install.
var SourceUnsupported = map[string]string{
	"mongodb":    "huge",     // needs ~40 GB of disk and many hours
	"java":       "jdk",      // the JDK build needs another JDK and many hours
	"tomcat":     "bytecode", // Java bytecode: nothing to compile natively
	"sqlserver":  "closed",   // no public source
	"phpmyadmin": "script",   // PHP scripts
	"adminer":    "script",
}

const aptDeps = `RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget xz-utils bzip2 build-essential pkg-config `

// Recipes per catalog key ("php" is built by webenv with the same options).
var Recipes = map[string]Recipe{
	"redis": {
		Minutes: 3, MinMemMB: 512, Defaults: "BUILD_TLS=yes",
		Deps: aptDeps + `libssl-dev`,
		Build: `RUN wget -qO- "https://download.redis.io/releases/redis-${VERSION}.tar.gz" | tar xz \
 && cd "redis-${VERSION}" && make -j"$(nproc)" BUILD_TLS=yes REDIS_CFLAGS="${EXTRA_CFLAGS}" ${CONFIGURE_ARGS} \
 && make PREFIX=/out/usr/local install`,
	},
	"memcached": {
		Minutes: 2, MinMemMB: 512, Defaults: "--prefix=/usr/local --enable-tls",
		Deps: aptDeps + `libevent-dev libssl-dev perl`,
		Build: `RUN wget -qO- "https://memcached.org/files/memcached-${VERSION}.tar.gz" | tar xz \
 && cd "memcached-${VERSION}" && ./configure --prefix=/usr/local --enable-tls CFLAGS="-O2 ${EXTRA_CFLAGS}" ${CONFIGURE_ARGS} \
 && make -j"$(nproc)" && make DESTDIR=/out install`,
	},
	"postgres": {
		Minutes: 12, MinMemMB: 1024, Defaults: "--with-openssl --with-icu --with-lz4 --with-zstd --with-libxml --with-uuid=e2fs",
		Deps: aptDeps + `bison flex libreadline-dev zlib1g-dev libssl-dev libxml2-dev libxslt1-dev libicu-dev liblz4-dev libzstd-dev uuid-dev`,
		Build: `RUN M="${VERSION%%.*}" && wget -qO- "https://ftp.postgresql.org/pub/source/v${VERSION}/postgresql-${VERSION}.tar.bz2" | tar xj \
 && cd "postgresql-${VERSION}" \
 && ./configure --prefix="/usr/lib/postgresql/$M" --datadir="/usr/share/postgresql/$M" --sysconfdir=/etc/postgresql-common \
      --with-openssl --with-icu --with-lz4 --with-zstd --with-libxml --with-uuid=e2fs CFLAGS="-O2 ${EXTRA_CFLAGS}" ${CONFIGURE_ARGS} \
 && make -j"$(nproc)" && make -C contrib -j"$(nproc)" \
 && make DESTDIR=/out install && make -C contrib DESTDIR=/out install`,
	},
	"mariadb": {
		Minutes: 60, MinMemMB: 3000, Defaults: "-DINSTALL_LAYOUT=DEB -DWITH_SSL=system -DPLUGIN_ROCKSDB=NO -DPLUGIN_MROONGA=NO -DPLUGIN_SPIDER=NO -DPLUGIN_CONNECT=NO -DPLUGIN_S3=NO -DPLUGIN_COLUMNSTORE=NO",
		Deps: aptDeps + `cmake bison libncurses-dev libssl-dev zlib1g-dev libpcre2-dev libaio-dev liburing-dev libxml2-dev libcurl4-openssl-dev liblz4-dev libzstd-dev libbz2-dev liblzma-dev libfmt-dev`,
		Build: `RUN wget -qO- "https://archive.mariadb.org/mariadb-${VERSION}/source/mariadb-${VERSION}.tar.gz" | tar xz \
 && cd "mariadb-${VERSION}" \
 && cmake -S . -B b -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr -DINSTALL_LAYOUT=DEB -DWITH_SSL=system \
      -DPLUGIN_ROCKSDB=NO -DPLUGIN_MROONGA=NO -DPLUGIN_SPIDER=NO -DPLUGIN_CONNECT=NO -DPLUGIN_S3=NO -DPLUGIN_COLUMNSTORE=NO \
      -DWITH_UNIT_TESTS=OFF -DCMAKE_C_FLAGS="${EXTRA_CFLAGS}" -DCMAKE_CXX_FLAGS="${EXTRA_CFLAGS}" ${CONFIGURE_ARGS} \
 && cmake --build b -j"$(nproc)" && DESTDIR=/out cmake --install b \
 && rm -rf /out/etc /out/usr/share/mysql-test /out/usr/share/mariadb-test`,
	},
	"mysql": {
		Minutes: 90, MinMemMB: 4000, Defaults: "-DINSTALL_LAYOUT=RPM -DWITH_SSL=system -DWITH_ROUTER=OFF",
		Deps: `RUN microdnf install -y --enablerepo=ol9_codeready_builder gcc gcc-c++ make cmake bison wget tar gzip openssl-devel ncurses-devel libtirpc-devel rpcgen zlib-devel pkgconf-pkg-config`,
		Build: `RUN S="mysql-${VERSION}"; MM="$(echo "$VERSION" | cut -d. -f1-2)"; B=""; \
 if [ "$MM" = "8.0" ]; then S="mysql-boost-${VERSION}"; B="-DWITH_BOOST=boost"; fi \
 && wget -qO- "https://cdn.mysql.com/Downloads/MySQL-${MM}/${S}.tar.gz" | tar xz \
 && cd "mysql-${VERSION}" \
 && cmake -S . -B b -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr -DINSTALL_LAYOUT=RPM -DWITH_SSL=system -DWITH_ROUTER=OFF \
      -DWITH_UNIT_TESTS=OFF $B -DCMAKE_C_FLAGS="${EXTRA_CFLAGS}" -DCMAKE_CXX_FLAGS="${EXTRA_CFLAGS}" ${CONFIGURE_ARGS} \
 && cmake --build b -j"$(nproc)" && DESTDIR=/out cmake --install b \
 && rm -rf /out/etc /out/usr/mysql-test /out/usr/share/mysql-test`,
	},
	"node": {
		Minutes: 75, MinMemMB: 2000, Defaults: "--prefix=/usr/local",
		Deps: aptDeps + `python3 g++ make`,
		Build: `RUN wget -qO- "https://nodejs.org/dist/v${VERSION}/node-v${VERSION}.tar.xz" | tar xJ \
 && cd "node-v${VERSION}" && CFLAGS="${EXTRA_CFLAGS}" CXXFLAGS="${EXTRA_CFLAGS}" ./configure --prefix=/usr/local ${CONFIGURE_ARGS} \
 && make -j"$(nproc)" && make DESTDIR=/out install`,
	},
	"python": {
		Minutes: 10, MinMemMB: 1024, Defaults: "--prefix=/usr/local --enable-shared --enable-loadable-sqlite-extensions --with-ensurepip=install",
		Deps: aptDeps + `libssl-dev zlib1g-dev libbz2-dev libreadline-dev libsqlite3-dev libffi-dev liblzma-dev libgdbm-dev libgdbm-compat-dev uuid-dev libncursesw5-dev libexpat1-dev`,
		Build: `RUN wget -qO- "https://www.python.org/ftp/python/${VERSION}/Python-${VERSION}.tar.xz" | tar xJ \
 && cd "Python-${VERSION}" \
 && ./configure --prefix=/usr/local --enable-shared --enable-loadable-sqlite-extensions --with-ensurepip=install \
      LDFLAGS="-Wl,-rpath=/usr/local/lib" CFLAGS="${EXTRA_CFLAGS}" ${CONFIGURE_ARGS} \
 && make -j"$(nproc)" && make DESTDIR=/out install`,
	},
}

// unsupportedVersions are catalog versions too old for the recipes.
var unsupportedVersions = map[string]string{"mysql:5.7": "old"}

// SourceSupport says whether app version can be compiled and, if not, why.
func SourceSupport(app, version string) (Recipe, string) {
	if why, ok := unsupportedVersions[app+":"+version]; ok {
		return Recipe{}, why
	}
	if r, ok := Recipes[app]; ok {
		return r, ""
	}
	if why, ok := SourceUnsupported[app]; ok {
		return Recipe{}, why
	}
	return Recipe{}, "none"
}

// SourceImage is the local tag of a compiled version.
func SourceImage(app, version string) string { return "lmcwhm/src-" + app + ":" + version }

var dockerfileTmpl = template.Must(template.New("df").Parse(`FROM {{.Base}} AS build
USER root
ARG VERSION
ARG CONFIGURE_ARGS=""
ARG EXTRA_CFLAGS=""
{{.Deps}}
WORKDIR /usr/src/lmcwhm
{{.Build}}

FROM {{.Base}}
COPY --from=build /out/ /
LABEL lmcwhm.managed="true" lmcwhm.build="source"
`))

// SourceDockerfile renders the build of app on the official base image.
func SourceDockerfile(app, base string) (string, error) {
	r, why := SourceSupport(app, "")
	if why != "" {
		return "", ErrNoSource
	}
	var b strings.Builder
	err := dockerfileTmpl.Execute(&b, map[string]string{"Base": base, "Deps": r.Deps, "Build": r.Build})
	return b.String(), err
}

// buildSource compiles i's version (the official image must be present).
func (sv *Service) buildSource(ctx context.Context, i Installed, log tasks.Logf) error {
	df, err := SourceDockerfile(i.App, i.Image)
	if err != nil {
		return err
	}
	full := CurrentVersion(ctx, i.Image)
	if full == "" {
		return fmt.Errorf("the version of %s is unknown", i.Image)
	}
	r, _ := SourceSupport(i.App, i.Version)
	log("compiling %s %s from source (about %d minutes on 2 cores)", i.Name, full, r.Minutes)
	dir := filepath.Join(sv.Home, "logs", "build")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, i.App+"-"+i.Version+".log")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	log("full build log: %s", path)
	w := webenv.NewProgressWriter(f, log)
	err = dockerx.BuildStream(ctx, SourceImage(i.App, i.Version), df,
		map[string]string{"VERSION": full, "CONFIGURE_ARGS": i.BuildArgs, "EXTRA_CFLAGS": i.BuildCFlags}, w)
	if err != nil {
		w.DumpTail()
		return fmt.Errorf("the source build failed (see %s): %w", path, err)
	}
	log("compiled %s %s", i.Name, full)
	return nil
}

// buildTimeout bounds a compile (MySQL on a small server takes hours).
const buildTimeout = 6 * time.Hour

// checkBuildMemory refuses a compile the server cannot hold.
func checkBuildMemory(ctx context.Context, r Recipe) error {
	if r.MinMemMB == 0 {
		return nil
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		if sw, err := mem.SwapMemoryWithContext(ctx); err == nil && vm.Total+sw.Total < uint64(r.MinMemMB)<<20 {
			return fmt.Errorf("%w: %d", ErrLowMemory, r.MinMemMB)
		}
	}
	return nil
}

// SetBuild switches an installed version between the pre-built image and a
// compile here (or compiles it again with other options), keeping its data.
// The running version stays until the new one is ready; if it does not
// start, the previous image and settings come back (task).
func (sv *Service) SetBuild(ctx context.Context, id int64, opts BuildOptions) (string, error) {
	i, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if err := opts.normalize(i.App, i.Version); err != nil {
		return "", err
	}
	if opts.Mode == ModeSource {
		r, _ := SourceSupport(i.App, i.Version)
		if err := checkBuildMemory(ctx, r); err != nil {
			return "", err
		}
	}
	return sv.tasks.StartLong("app.build:"+i.App+"-"+i.Version, buildTimeout, func(ctx context.Context, log tasks.Logf) error {
		if !dockerx.Available(ctx) {
			return dockerx.ErrNoDocker
		}
		if !dockerx.ImageExists(ctx, i.Image) {
			log("pulling %s", i.Image)
			if _, err := dockerx.Run(ctx, "pull", "-q", i.Image); err != nil {
				return err
			}
		}
		prev := i
		next := i
		next.BuildMode, next.BuildArgs, next.BuildCFlags = opts.Mode, opts.Args, opts.CFlags
		// The current image is kept under the rollback tag until the switch works.
		keep := rollbackTag(i.App, i.Version)
		oldID := imageID(ctx, prev.RunImage())
		if oldID != "" {
			if _, err := dockerx.Run(ctx, "tag", oldID, keep); err != nil {
				return err
			}
		}
		if next.BuildMode == ModeSource {
			if err := sv.buildSource(ctx, next, log); err != nil {
				if oldID != "" {
					restoreTag(ctx, oldID, prev.RunImage(), keep)
				}
				return err
			}
		}
		if err := sv.saveBuild(ctx, next); err != nil {
			return err
		}
		v := CurrentVersion(ctx, i.Image)
		err := sv.restartOnSwitch(ctx, prev, next, oldID, keep, v, log)
		if err != nil {
			sv.saveBuild(ctx, prev)
			return err
		}
		if prev.BuildMode == ModeSource && next.BuildMode == ModeFast {
			dockerx.Run(ctx, "rmi", SourceImage(i.App, i.Version))
		}
		sv.refresh(ctx, appKey(i.ID))
		return nil
	}), nil
}

func (sv *Service) saveBuild(ctx context.Context, i Installed) error {
	_, err := sv.st.DB.ExecContext(ctx, `UPDATE apps SET build_mode = ?, build_args = ?, build_cflags = ? WHERE id = ?`,
		i.BuildMode, i.BuildArgs, i.BuildCFlags, i.ID)
	return err
}

// restartOnSwitch moves the container from prev's image to next's; when
// next does not start, prev runs again on the image kept as keep.
func (sv *Service) restartOnSwitch(ctx context.Context, prev, next Installed, oldID, keep, v string, log tasks.Logf) error {
	if next.Container == "" {
		if sv.RuntimeUpdated != nil {
			sv.RuntimeUpdated(ctx, next.App, next.Version, log)
		}
		if oldID != "" {
			dockerx.Run(ctx, "rmi", keep)
		}
		log("%s %s now runs the %s build", next.Name, next.Version, next.BuildMode)
		return nil
	}
	if sv.Backup != nil && next.Databases {
		log("backing up the databases of %s %s", next.Name, next.Version)
		if err := sv.Backup(ctx, next, log); err != nil {
			return fmt.Errorf("backup before the change failed: %w", err)
		}
	}
	log("recreating %s on the %s build", next.Container, next.BuildMode)
	if err := dockerx.Remove(ctx, next.Container); err != nil {
		return err
	}
	err := sv.start(ctx, next, log)
	if err == nil {
		if oldID != "" {
			dockerx.Run(ctx, "rmi", keep)
		}
		if pmaEngine(next.App) {
			if err := sv.RefreshPMA(ctx); err != nil {
				log("phpMyAdmin: %v", err)
			}
		}
		log("%s %s (%s) now runs the %s build", next.Name, next.Version, orUnknown(v), next.BuildMode)
		return nil
	}
	log("the new build did not start: %v; going back", err)
	if oldID != "" {
		restoreTag(ctx, oldID, prev.RunImage(), keep)
	}
	dockerx.Remove(ctx, prev.Container)
	if err2 := sv.start(ctx, prev, log); err2 != nil {
		return fmt.Errorf("switch failed (%v) and the previous build did not start: %w", err, err2)
	}
	return fmt.Errorf("app.build_failed: %v", err)
}

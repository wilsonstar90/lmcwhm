#!/usr/bin/env bash
# Build an offline install bundle (離線安裝包) on a server that has internet,
# ideally one already running lmcwhm, so the PHP images it built are included.
#
#   sudo bash make-offline.sh [--version vX.Y.Z] [--arch amd64|arm64] [--image NAME]...
#
# The bundle holds the lmcwhm binary, Docker's static binaries and the core
# images (HAProxy, nginx, the WAF image, Varnish, busybox, built PHP images and
# any --image). Install with:
#
#   sudo bash install.sh --offline lmcwhm-offline-<version>-<arch>.tar.gz
#
# Websites and the panel work offline; App Store apps, certificates and
# Cloudflare still need the internet (or images loaded with docker load).
set -euo pipefail

REPO=wilsonstar90/lmcwhm
DOCKER_VERSION=27.5.1
VERSION=latest
ARCH=""
EXTRA=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --arch)    ARCH="$2"; shift 2 ;;
    --image)   EXTRA+=("$2"); shift 2 ;;
    *) echo "unknown option $1" >&2; exit 1 ;;
  esac
done
if [[ -z "$ARCH" ]]; then
  case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; *) echo "unsupported arch" >&2; exit 1 ;; esac
fi
DARCH=x86_64; [[ $ARCH == arm64 ]] && DARCH=aarch64
command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT
mkdir -p "$OUT/images"

if [[ "$VERSION" == latest ]]; then BASE="https://github.com/${REPO}/releases/latest/download"; else BASE="https://github.com/${REPO}/releases/download/${VERSION}"; fi
echo ">> lmcwhm binary ($ARCH)"
curl -fL --retry 3 -o "$OUT/lmcwhm-linux-${ARCH}" "$BASE/lmcwhm-linux-${ARCH}"
curl -fsSL "$BASE/SHA256SUMS" -o "$OUT/SHA256SUMS"
(cd "$OUT" && grep " lmcwhm-linux-${ARCH}\$" SHA256SUMS | sha256sum -c -)
chmod 0755 "$OUT/lmcwhm-linux-${ARCH}"
VERSION_TAG="$("$OUT/lmcwhm-linux-${ARCH}" version 2>/dev/null | awk '{print $2}' || true)"
[[ -n "$VERSION_TAG" ]] || VERSION_TAG="$VERSION"
curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/deploy/install.sh" -o "$OUT/install.sh"

echo ">> Docker ${DOCKER_VERSION} static binaries"
curl -fL --retry 3 -o "$OUT/docker.tgz" "https://download.docker.com/linux/static/stable/${DARCH}/docker-${DOCKER_VERSION}.tgz"

IMAGES=(haproxy:3.0 nginx:stable busybox:1.37 varnish:7.6 "${EXTRA[@]}")
# Images this server built or pulled for the panel.
while read -r img; do IMAGES+=("$img"); done < <(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^(lmcwhm/php|owasp/modsecurity-crs):' || true)
for img in "${IMAGES[@]}"; do
  if [[ "$img" != lmcwhm/* ]] && ! docker image inspect "$img" >/dev/null 2>&1; then
    echo ">> pulling $img"
    docker pull -q --platform "linux/${ARCH}" "$img" >/dev/null
  fi
  echo ">> saving $img"
  docker save -o "$OUT/images/$(echo "$img" | tr '/:' '__').tar" "$img"
done

FILE="lmcwhm-offline-${VERSION_TAG}-${ARCH}.tar.gz"
tar -czf "$FILE" -C "$OUT" .
echo "bundle: $(pwd)/$FILE ($(du -h "$FILE" | cut -f1))"
echo "install: sudo bash install.sh --offline $FILE   (install.sh is inside the bundle too)"

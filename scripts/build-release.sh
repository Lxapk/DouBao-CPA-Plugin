#!/usr/bin/env bash
# Build the per-platform plugin archives that the CPA plugin store expects.
#
# The store resolves a release asset by exact name:
#
#   <id>_<version>_<goos>_<goarch>.zip
#
# and inside the archive it looks for, in order:
#
#   <goos>/<goarch>/<id>-v<version>.so
#   <goos>/<goarch>/<id>.so
#   <id>.so
#
# A checksums.txt asset is mandatory alongside the archives.
#
# Usage: ./scripts/build-release.sh [version]
set -euo pipefail

VERSION="${1:-0.1.0}"
PLUGIN_ID="doubao"
OUTDIR="dist"

rm -rf "$OUTDIR"
mkdir -p "$OUTDIR"

# c-shared needs a C toolchain per target. These are the conventional prefixes;
# when one is absent the target is reported as skipped rather than failing the
# whole build, because a release that covers some platforms is still useful and
# pretending to cover all of them would not be.
crossCC() {
  local goos="$1" goarch="$2"
  case "${goos}/${goarch}" in
    linux/arm64)  echo "${CC_LINUX_ARM64:-aarch64-linux-gnu-gcc}" ;;
    linux/amd64)  echo "${CC_LINUX_AMD64:-x86_64-linux-gnu-gcc}" ;;
    darwin/*)     echo "${CC_DARWIN:-o64-clang}" ;;
    *)            echo "cc" ;;
  esac
}

# The host target always uses the native compiler, whatever the defaults above say.
if [ "$(go env GOOS)/$(go env GOARCH)" = "linux/arm64" ]; then
  CC_LINUX_ARM64="$(command -v gcc || echo cc)"
fi
if [ "$(go env GOOS)/$(go env GOARCH)" = "linux/amd64" ]; then
  CC_LINUX_AMD64="$(command -v gcc || echo cc)"
fi
targets="linux/arm64 linux/amd64 darwin/arm64 darwin/amd64"
built=()
skipped=()

for target in $targets; do
  goos="${target%%/*}"
  goarch="${target##*/}"

  case "$goos" in
    darwin) ext="dylib" ;;
    *)      ext="so" ;;
  esac

  libname="${PLUGIN_ID}-v${VERSION}.${ext}"
  stage="$OUTDIR/stage-${goos}-${goarch}"
  mkdir -p "$stage/${goos}/${goarch}"

  printf '==> %s/%s\n' "$goos" "$goarch"
  if CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" CC="$(crossCC "$goos" "$goarch")" \
       go build -buildmode=c-shared -o "$stage/${goos}/${goarch}/${libname}" . 2>"$OUTDIR/err-${goos}-${goarch}.log"; then
    # The build emits a header next to the library; the store does not want it.
    rm -f "${stage}/${goos}/${goarch}/${PLUGIN_ID}-v${VERSION}.h"

    archive="${PLUGIN_ID}_${VERSION}_${goos}_${goarch}.zip"
    # Zip from inside the staging directory so the archive root is the layout
    # the store walks: <goos>/<goarch>/<lib>.
    (cd "$stage" && zip -qr "$OLDPWD/$OUTDIR/$archive" .)
    printf '    %s\n' "$archive"
    built+=("$archive")
  else
    reason=$(head -n 1 "$OUTDIR/err-${goos}-${goarch}.log" 2>/dev/null || echo 'unknown')
    printf '    跳过：%s\n' "$reason"
    skipped+=("${goos}/${goarch}")
  fi
  rm -rf "$stage" "$OUTDIR/err-${goos}-${goarch}.log"
done

printf '\n==> checksums.txt\n'
(cd "$OUTDIR" && shasum -a 256 ./*.zip > checksums.txt)
cat "$OUTDIR/checksums.txt"

printf '\n==> 完成\n'
printf '已构建 %d 个平台：%s\n' "${#built[@]}" "${built[*]:-无}"
if [ "${#skipped[@]}" -gt 0 ]; then
  printf '跳过 %d 个平台：%s\n' "${#skipped[@]}" "${skipped[*]}"
  printf '跳过的平台需要相应的 C 交叉编译工具链（如 aarch64-linux-gnu-gcc、o64-clang）。\n'
fi

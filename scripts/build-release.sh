#!/usr/bin/env bash
# Build the per-platform plugin archives that the CPA plugin store expects.
#
# The store resolves a release asset by exact name:
#
#   <id>_<version>_<goos>_<goarch>.zip
#
# and inside the archive the library must sit at the ZIP ROOT, named either
# <id><ext> or <id>-v<version><ext>:
#
#   doubao.so        or   doubao-v0.1.0.so
#
# A nested layout such as linux/amd64/doubao.so is rejected with
# "target dynamic library must be at zip root" — readTargetLibrary compares the
# entry's full cleaned path against the bare expected names, so any directory
# component fails the match.
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
  mkdir -p "$stage"

  printf '==> %s/%s\n' "$goos" "$goarch"
  if CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" CC="$(crossCC "$goos" "$goarch")" \
       go build -buildmode=c-shared -o "$stage/${libname}" . 2>"$OUTDIR/err-${goos}-${goarch}.log"; then
    # The build emits a header next to the library; the store does not want it,
    # and an unexpected .h would be ignored rather than rejected — but shipping
    # it invites the question.
    rm -f "${stage}/${PLUGIN_ID}-v${VERSION}.h"

    archive="${PLUGIN_ID}_${VERSION}_${goos}_${goarch}.zip"
    # The library must be at the archive root: zip the staging directory's
    # contents, not the directory itself.
    (cd "$stage" && zip -qj "$OLDPWD/$OUTDIR/$archive" ./*)
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
# The name field must be the bare asset name. `shasum` prefixes a path ("./")
# when given a glob, and the store looks the entry up by exact asset name, so a
# prefixed entry reads as "checksum not found" rather than as a format error.
(cd "$OUTDIR" && shasum -a 256 ./*.zip | sed 's|\./||' > checksums.txt)
cat "$OUTDIR/checksums.txt"

printf '\n==> 完成\n'
printf '已构建 %d 个平台：%s\n' "${#built[@]}" "${built[*]:-无}"
if [ "${#skipped[@]}" -gt 0 ]; then
  printf '跳过 %d 个平台：%s\n' "${#skipped[@]}" "${skipped[*]}"
  printf '跳过的平台需要相应的 C 交叉编译工具链（如 aarch64-linux-gnu-gcc、o64-clang）。\n'
fi

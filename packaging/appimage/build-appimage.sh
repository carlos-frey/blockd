#!/bin/sh
# Monta blockd-x86_64.AppImage. Use pelo Makefile (`make appimage`), que compila
# o daemon e a CLI sem cgo (rodam em qualquer distro) antes de chamar isto.
#
#   packaging/appimage/build-appimage.sh <pasta com blockd, blockctl, blockd-gui> <versão> [saída]
#
# appimagetool: $APPIMAGETOOL, o do PATH, ou baixado para .tools/.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
bin=$(cd "${1:?pasta com os binários}" && pwd)
version=${2:?versão}
out=${3:-$repo/dist}
mkdir -p "$out"
out=$(cd "$out" && pwd)

tool=${APPIMAGETOOL:-}
if [ -z "$tool" ]; then
    if command -v appimagetool >/dev/null 2>&1; then
        tool=$(command -v appimagetool)
    else
        tool=$repo/.tools/appimagetool
        if [ ! -x "$tool" ]; then
            mkdir -p "$repo/.tools"
            echo "baixando appimagetool para $tool" >&2
            curl -fsSL -o "$tool.part" \
                https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
            chmod 755 "$tool.part" && mv "$tool.part" "$tool"
        fi
    fi
fi

appdir=$(mktemp -d "${TMPDIR:-/tmp}/blockd-appdir.XXXXXX")
trap 'rm -rf "$appdir"' EXIT INT TERM
share=$appdir/usr/share/blockd

# Binários
mkdir -p "$appdir/usr/bin" "$share/units"
for f in blockd blockctl blockd-gui; do
    install -m755 "$bin/$f" "$appdir/usr/bin/$f"
done
strip --strip-unneeded "$appdir/usr/bin/blockd-gui" 2>/dev/null || true

# O que o install.sh copia para o sistema
install -m644 "$repo/packaging/blockd.service" "$repo/packaging/blockd-dns.service" "$share/units/"
install -m644 "$repo/packaging/blockd.sysusers" "$share/blockd.sysusers"
install -m644 "$repo/packaging/blockd-gui.desktop" "$share/blockd-gui.desktop"
install -m644 "$repo/packaging/blockd.svg" "$share/blockd.svg"
install -m755 "$here/install.sh" "$here/uninstall.sh" "$share/"
install -m644 "$repo/README.md" "$repo/LICENSE" "$share/"
printf '%s\n' "$version" > "$share/VERSION"

# Raiz do AppDir: AppRun, .desktop e ícone
install -m755 "$here/AppRun" "$appdir/AppRun"
rsvg-convert -w 256 -h 256 "$repo/packaging/blockd.svg" -o "$appdir/blockd.png"
mkdir -p "$appdir/usr/share/icons/hicolor/256x256/apps" "$appdir/usr/share/icons/hicolor/scalable/apps" \
    "$appdir/usr/share/applications"
cp "$appdir/blockd.png" "$appdir/usr/share/icons/hicolor/256x256/apps/blockd.png"
cp "$repo/packaging/blockd.svg" "$appdir/usr/share/icons/hicolor/scalable/apps/blockd.svg"
ln -s blockd.png "$appdir/.DirIcon"
sed -e 's#^Exec=.*#Exec=AppRun#' "$repo/packaging/blockd-gui.desktop" > "$appdir/blockd.desktop"
printf 'X-AppImage-Version=%s\n' "$version" >> "$appdir/blockd.desktop"
cp "$appdir/blockd.desktop" "$appdir/usr/share/applications/blockd.desktop"

chmod -R a+rX "$appdir"   # mktemp -d cria a pasta com 700
target=$out/blockd-x86_64.AppImage
# --appimage-extract-and-run: funciona também onde não há FUSE (CI, containers).
ARCH=x86_64 "$tool" --appimage-extract-and-run --no-appstream "$appdir" "$target" >&2
echo "$target"

#!/bin/sh
# Instala (ou atualiza) o blockd no sistema a partir do AppImage. Roda como root;
# o AppRun chama via pkexec/sudo a partir de uma cópia em /tmp (o root não enxerga
# o ponto de montagem FUSE do AppImage).
#
#   install.sh [--dry-run] <pasta com bin/ e share/> <usuário que entra no grupo blockd>
#
# Vai para /usr/local e /etc, para não brigar com o pacote da distro (/usr).
set -eu

dry=0
if [ "${1:-}" = --dry-run ]; then dry=1; shift; fi
src=${1:?pasta de origem}
user=${2:-}
[ -n "$user" ] || user=$(id -un "${PKEXEC_UID:-${SUDO_UID:-0}}" 2>/dev/null || true)

PREFIX=/usr/local
UNITDIR=/etc/systemd/system

run() {
    if [ "$dry" = 1 ]; then echo "+ $*"; else "$@"; fi
}
die() { echo "erro: $*" >&2; exit 1; }

[ "$dry" = 1 ] || [ "$(id -u)" = 0 ] || die "precisa rodar como root"
[ -d /run/systemd/system ] || die "o blockd precisa do systemd, e este sistema não está usando o systemd."

if [ -x /usr/bin/blockd ] && [ "$(readlink -f /usr/bin/blockd)" != "$PREFIX/bin/blockd" ]; then
    die "o blockd já está instalado pelo pacote da distro (/usr/bin/blockd). Atualize por ele, ou remova o pacote antes."
fi

# Dependências
missing=
for c in nft dnsmasq ip ss systemctl; do
    command -v "$c" >/dev/null 2>&1 || missing="$missing $c"
done
if [ -n "$missing" ]; then
    pkgs=$(echo "$missing" | sed -e 's/\bnft\b/nftables/' -e 's/\bip\b/iproute2/' -e 's/\bss\b/iproute2/')
    echo "Faltam programas que o blockd usa:$missing" >&2
    if command -v pacman >/dev/null 2>&1; then echo "  sudo pacman -S --needed$pkgs" >&2
    elif command -v apt-get >/dev/null 2>&1; then echo "  sudo apt install$(echo "$pkgs" | sed 's/\bdnsmasq\b/dnsmasq-base/')" >&2
    elif command -v dnf >/dev/null 2>&1; then echo "  sudo dnf install$(echo "$pkgs" | sed 's/\biproute2\b/iproute/')" >&2
    elif command -v zypper >/dev/null 2>&1; then echo "  sudo zypper install$(echo "$pkgs" | sed 's/\biproute2\b/iproute2/')" >&2
    fi
    exit 3
fi
if ! systemctl is-active --quiet systemd-resolved; then
    echo "O blockd bloqueia sites pelo systemd-resolved, que não está ativo." >&2
    echo "  sudo systemctl enable --now systemd-resolved" >&2
    echo "  sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf" >&2
    exit 4
fi

dnsmasq=$(command -v dnsmasq)
version=$(cat "$src/share/VERSION")
updating=0
[ -x "$PREFIX/bin/blockd" ] && updating=1

# Binários e arquivos
for f in blockd blockctl blockd-gui; do
    run install -Dm755 "$src/bin/$f" "$PREFIX/bin/$f"
done
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
sed "s#^ExecStart=/usr/bin/blockd#ExecStart=$PREFIX/bin/blockd#" "$src/share/units/blockd.service" > "$tmp"
run install -Dm644 "$tmp" "$UNITDIR/blockd.service"
sed "s#^ExecStart=/usr/bin/dnsmasq#ExecStart=$dnsmasq#" "$src/share/units/blockd-dns.service" > "$tmp"
run install -Dm644 "$tmp" "$UNITDIR/blockd-dns.service"
run install -Dm644 "$src/share/blockd.sysusers" /etc/sysusers.d/blockd.conf
sed "s#^Exec=.*#Exec=$PREFIX/bin/blockd-gui#" "$src/share/blockd-gui.desktop" > "$tmp"
run install -Dm644 "$tmp" "$PREFIX/share/applications/blockd-gui.desktop"
run install -Dm644 "$src/share/blockd.svg" "$PREFIX/share/icons/hicolor/scalable/apps/blockd.svg"
run install -Dm644 "$src/share/README.md" "$PREFIX/share/doc/blockd/README.md"
run install -Dm755 "$src/share/uninstall.sh" "$PREFIX/share/blockd/uninstall.sh"
printf '%s\n' "$version" > "$tmp"
run install -Dm644 "$tmp" "$PREFIX/share/blockd/VERSION"

# Serviço
run systemd-sysusers blockd.conf
run systemctl daemon-reload
run systemctl enable blockd.service blockd-dns.service
if [ "$updating" = 1 ]; then
    run systemctl restart blockd.service
else
    run systemctl start blockd.service
fi

newgroup=0
if [ -n "$user" ] && [ "$user" != root ]; then
    if ! id -nG "$user" 2>/dev/null | tr ' ' '\n' | grep -qx blockd; then
        run gpasswd -a "$user" blockd
        newgroup=1
    fi
fi

echo
if [ "$updating" = 1 ]; then
    echo "blockd atualizado para $version."
else
    echo "blockd $version instalado. O serviço já está rodando."
fi
if [ "$newgroup" = 1 ]; then
    echo "O usuário $user entrou no grupo blockd: saia e entre de novo na sessão para controlar o blockd sem sudo."
fi

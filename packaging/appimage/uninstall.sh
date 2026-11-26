#!/bin/sh
# Remove do sistema o blockd instalado pelo AppImage (o mesmo que `make uninstall`,
# mas em /usr/local). Roda como root. O estado fica em /var/lib/blockd.
#
#   uninstall.sh [--dry-run]
set -eu

dry=0
[ "${1:-}" = --dry-run ] && dry=1
run() {
    if [ "$dry" = 1 ]; then echo "+ $*"; else "$@"; fi
}

PREFIX=/usr/local
UNITDIR=/etc/systemd/system

[ "$dry" = 1 ] || [ "$(id -u)" = 0 ] || { echo "erro: precisa rodar como root" >&2; exit 1; }
[ -x "$PREFIX/bin/blockd" ] || { echo "O blockd não está instalado em $PREFIX." >&2; exit 0; }

# Parar o serviço não libera nada: o teardown tira as regras do firewall e do DNS.
run systemctl disable --now blockd.service || true
run "$PREFIX/bin/blockd" teardown || true
run systemctl disable --now blockd-dns.service || true

run rm -f "$PREFIX/bin/blockd" "$PREFIX/bin/blockctl" "$PREFIX/bin/blockd-gui" \
    "$UNITDIR/blockd.service" "$UNITDIR/blockd-dns.service" /etc/sysusers.d/blockd.conf \
    "$PREFIX/share/applications/blockd-gui.desktop" "$PREFIX/share/icons/hicolor/scalable/apps/blockd.svg"
run rm -rf "$PREFIX/share/doc/blockd" "$PREFIX/share/blockd"
run systemctl daemon-reload

echo
echo "blockd removido. O estado continua em /var/lib/blockd (apague à mão se quiser)."

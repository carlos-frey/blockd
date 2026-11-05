PREFIX  ?= /usr
DESTDIR ?=
GO      ?= go
BIN     := bin

.PHONY: all cli gui test test-netns install uninstall clean

all: cli gui

cli:
	$(GO) build -trimpath -o $(BIN)/ ./cmd/blockd ./cmd/blockctl

# cimgui-go compila o Dear ImGui via cgo: a primeira vez leva alguns minutos.
gui:
	$(GO) build -trimpath -o $(BIN)/ ./cmd/blockd-gui

test:
	$(GO) test ./...

# Testes que carregam regras reais no kernel, num namespace descartável (sem root).
test-netns:
	unshare -rn $(GO) test ./internal/backend/nft -run RealNft -v

# Rode `make` como usuário antes; `sudo make install` só copia arquivos.
install:
	install -Dm755 $(BIN)/blockd      $(DESTDIR)$(PREFIX)/bin/blockd
	install -Dm755 $(BIN)/blockctl    $(DESTDIR)$(PREFIX)/bin/blockctl
	test ! -f $(BIN)/blockd-gui || install -Dm755 $(BIN)/blockd-gui $(DESTDIR)$(PREFIX)/bin/blockd-gui
	install -Dm644 packaging/blockd.service     $(DESTDIR)$(PREFIX)/lib/systemd/system/blockd.service
	install -Dm644 packaging/blockd-dns.service $(DESTDIR)$(PREFIX)/lib/systemd/system/blockd-dns.service
	install -Dm644 packaging/blockd.sysusers    $(DESTDIR)$(PREFIX)/lib/sysusers.d/blockd.conf
	install -Dm644 packaging/blockd-gui.desktop $(DESTDIR)$(PREFIX)/share/applications/blockd-gui.desktop
	install -Dm644 README.md                    $(DESTDIR)$(PREFIX)/share/doc/blockd/README.md
ifeq ($(DESTDIR),)
	systemd-sysusers blockd.conf
	systemctl daemon-reload
	@echo
	@echo "Pronto. Próximos passos:"
	@echo "  sudo systemctl enable --now blockd blockd-dns"
	@echo "  sudo gpasswd -a $$SUDO_USER blockd   # e faça logout/login"
endif

uninstall:
	-systemctl disable --now blockd.service
	-$(PREFIX)/bin/blockd teardown
	-systemctl disable --now blockd-dns.service
	rm -f $(DESTDIR)$(PREFIX)/bin/blockd $(DESTDIR)$(PREFIX)/bin/blockctl $(DESTDIR)$(PREFIX)/bin/blockd-gui
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/system/blockd.service $(DESTDIR)$(PREFIX)/lib/systemd/system/blockd-dns.service
	rm -f $(DESTDIR)$(PREFIX)/lib/sysusers.d/blockd.conf $(DESTDIR)$(PREFIX)/share/applications/blockd-gui.desktop
	rm -rf $(DESTDIR)$(PREFIX)/share/doc/blockd
	-systemctl daemon-reload
	@echo "Estado mantido em /var/lib/blockd (apague à mão se quiser)."

clean:
	rm -rf $(BIN)

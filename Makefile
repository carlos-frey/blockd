PREFIX  ?= /usr
DESTDIR ?=
GO      ?= go
BIN     := bin

.PHONY: all cli gui windows windows-gui windows-setup test test-netns install uninstall clean

all: cli gui

cli:
	$(GO) build -trimpath -o $(BIN)/ ./cmd/blockd ./cmd/blockctl

# cimgui-go compila o Dear ImGui via cgo: a primeira vez leva alguns minutos.
gui:
	$(GO) build -trimpath -o $(BIN)/ ./cmd/blockd-gui

# Windows (amd64): daemon e CLI compilam cruzado sem cgo. Instale no Windows
# com packaging/windows/install.ps1 (veja o README).
windows:
	GOOS=windows GOARCH=amd64 $(GO) build -trimpath -o $(BIN)/windows/ ./cmd/blockd ./cmd/blockctl
	cp packaging/windows/*.ps1 $(BIN)/windows/

# Instalador único: embute o que `make windows` (e `make windows-gui`, se
# rodou antes) deixou em bin/windows, mais os scripts de packaging/windows.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
SETUP_PAYLOAD := cmd/blockd-setup/payload

windows-setup: windows
	rm -f $(SETUP_PAYLOAD)/*.exe $(SETUP_PAYLOAD)/*.ps1
	cp $(BIN)/windows/blockd.exe $(BIN)/windows/blockctl.exe packaging/windows/*.ps1 $(SETUP_PAYLOAD)/
	if [ -f $(BIN)/windows/blockd-gui.exe ]; then cp $(BIN)/windows/blockd-gui.exe $(SETUP_PAYLOAD)/; fi
	GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" \
		-o $(BIN)/windows/blockd-setup.exe ./cmd/blockd-setup
	rm -f $(SETUP_PAYLOAD)/*.exe $(SETUP_PAYLOAD)/*.ps1

# A GUI usa cgo: para compilar cruzado precisa do mingw-w64 (x86_64-w64-mingw32-gcc).
windows-gui:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
		$(GO) build -trimpath -ldflags "-H=windowsgui" -o $(BIN)/windows/ ./cmd/blockd-gui

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

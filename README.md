# blockd

Bloqueio de apps e sites que viciam, no Linux. Você escolhe apps num catálogo,
monta perfis (ou usa os prontos) e liga/desliga, à mão, por agenda semanal ou num
foco com hora para acabar. Por baixo, o blockd mantém
**nftables**, **DNS** e **cgroups** sempre sincronizados com o que você vê.

```
blockd-gui (ImGui)  ──exec──▶  blockctl --json  ──socket──▶  blockd (root)
                                                              ├─ nftables: table inet blockd
                                                              ├─ dnsmasq:  blockd-dns.service
                                                              └─ cgroup:   /sys/fs/cgroup/blockd.jail
```

- **blockd**: o daemon (root). Guarda o estado em `/var/lib/blockd/state.json`, aplica
  e **reconcilia a cada 15s**. Se alguém mexer no firewall/DNS por fora, ele reaplica
  e registra o *drift*.
- **blockctl**: a CLI, que funciona como API. Com `--json`, todo comando devolve
  `{"ok":true,"data":…}` ou `{"ok":false,"error":{"code","message","details"}}`.
- **blockd-gui**: a interface gráfica. Não fala com o daemon diretamente: tudo passa
  pelo `blockctl --json`.

## Instalação (Arch)

```sh
make                     # como usuário (a GUI leva ~3 min na 1ª vez: compila o Dear ImGui)
sudo make install
sudo systemctl enable --now blockd blockd-dns
sudo gpasswd -a $USER blockd   # depois faça logout/login
blockctl status
```

Também dá para empacotar: `cd packaging && makepkg -si`.

## Uso

```sh
blockctl profile ls                       # perfis (● = ativo)
blockctl profile on redes-sociais games   # ativa perfis (vários ao mesmo tempo)
blockctl app block youtube                # bloqueio avulso
blockctl status                           # o que está bloqueado e por quê
blockctl catalog ls -c Chat               # catálogo, com estado de cada app
blockctl profile create "Noite" --from-blocked --on   # parte do que já está bloqueado
blockctl profile clone trabalho --name "Meu trabalho" # perfis prontos são só-leitura
blockctl catalog add "Bet365" --domain bet365.com,bet365.bet.br --category Apostas
blockctl profile off redes-sociais
```

Regras de sincronização:

- O que fica bloqueado é **a união** dos perfis ativos com os bloqueios avulsos.
- Desativar um perfil não libera um app que outro perfil ativo, ou um bloqueio avulso,
  ainda segura.
- `app unblock` de um app preso a um perfil, agenda ou foco falha com
  `blocked_by_profile`, e os `details` dizem o que o segura (`held_by`). Na GUI o toggle
  aparece travado.
- Ao criar um perfil, os apps bloqueados no momento já vêm marcados
  (`--from-blocked` na CLI; é o padrão na GUI).

GUI: `blockd-gui` (`--page catalog|profiles|schedule`, `--open new-profile|new-app|new-schedule|focus`).

## Agenda e foco

Uma **agenda** liga perfis sozinha em janelas da semana; um **foco** liga perfis agora,
por um tempo, e desliga sozinho.

```sh
blockctl schedule create "Trabalho" --profiles redes-sociais,video \
    --window "seg-sex 09:00-12:00" --window "seg-sex 13:30-18:00" --mode friccao --delay 10
blockctl schedule create "Noite" --profiles games --window "todos 23:00-07:00" --mode travado
blockctl schedule ls                       # ● = valendo agora
blockctl schedule skip trabalho 25/12      # feriado: pula sem desligar (--undo desfaz)
blockctl schedule pause trabalho           # até o fim da janela atual (respeita o modo)
blockctl schedule resume trabalho          # cancela a pausa, mesmo a que ainda não começou
blockctl focus start redes-sociais --for 50m --mode travado
blockctl focus start games --until 18:00
blockctl focus stop f1                     # focus extend f1 --for 15m estende
```

- Dias: `seg`…`dom`, intervalos (`seg-sex`, `sex-seg`), listas (`sab,dom`), `uteis`, `fds`,
  `todos`. Uma janela com fim antes do início cruza a meia-noite (`23:00-07:00`), e
  `00:00-00:00` é o dia inteiro. Janelas encostadas viram um bloco só.
- O **modo** diz o quanto é difícil desligar *enquanto vale*:
  - `livre`: pausa na hora, até o fim da janela;
  - `friccao`: pede uma frase (o daemon confere) e a pausa só começa depois de N minutos.
    Dá para cancelar o pedido nesse meio tempo;
  - `travado`: nada que afrouxe o bloqueio é aceito até a janela acabar.
- Enquanto uma agenda ou foco em `friccao`/`travado` estiver valendo, o daemon recusa com
  `locked` qualquer mudança que afrouxe o que ele bloqueia: desligar, apagar, encurtar ou
  pular a janela atual, trocar por um modo mais fraco, tirar apps dos perfis dele, apagar
  esses perfis ou mudar os domínios dos apps custom. Acrescentar coisas sempre pode.
- O daemon muda o bloqueio no minuto exato da virada (e o reconcile de 15s cobre a volta
  de suspensão). `blockctl status` mostra a próxima mudança automática.
- Com root ainda dá para desmontar tudo (`blockd teardown`): as travas valem contra a
  impulsividade, não contra o administrador.

## Como cada coisa é bloqueada

| Item do catálogo | Mecanismo |
|---|---|
| Domínio | O dnsmasq responde `NXDOMAIN` para o domínio **e todos os subdomínios**. O resolved encaminha tudo para ele (drop-in com `Domains=~.`), que só existe enquanto houver domínio bloqueado. |
| Domínio com `ip_block` | Além do DNS, os IPs resolvidos no upstream real entram num set do nftables. Fica desligado por padrão porque Meta e Google compartilham IPs entre serviços. |
| Processo (Discord, Steam…) | O processo vai para o cgroup `blockd.jail`, que o nftables rejeita (`socket cgroupv2`). As conexões que ele já tinha abertas são derrubadas com `ss -K`. O app continua aberto, só que sem rede. Filhos (jogos abertos pela Steam) herdam o bloqueio. |
| IP fixo | Regra no nftables (opcionalmente só em algumas portas). É o que o "DoH público" usa. |

**Abas já abertas.** O bloqueio por DNS só vale para consultas novas, e os navegadores
guardam o próprio cache de DNS e mantêm abertas as sessões HTTP/2 e QUIC enquanto a aba
é usada. Sem mais nada, um x.com aberto antes do bloqueio continuaria funcionando. Por
isso, quando um domínio novo entra no bloqueio, o blockd faz um *kick*: derruba os sockets
UDP conectados na porta 443 (QUIC) e põe e tira um endereço temporário (`127.0.0.154`) no
`lo`. O Chromium/Brave e o Firefox tratam isso como troca de rede: fecham as sessões e
esvaziam o cache de DNS. As abas abertas recarregam a conexão por um instante. Para
fazer à mão: `sudo blockd kick`.

O app **DoH público** entra em todos os perfis prontos. Ele fecha os resolvers
DNS-over-HTTPS conhecidos e o canary `use-application-dns.net`, que faz o
Firefox desligar o DoH padrão. Sem isso, o navegador contornaria o DNS.

## Desfazer

- Parar o serviço **não** libera nada: as regras continuam valendo.
- Para limpar tudo do sistema sem perder o estado:
  `sudo systemctl stop blockd && sudo blockd teardown`.
- Para desinstalar: `sudo make uninstall`.

## Desenvolvimento

```sh
make test         # unitários (engine com backends falsos, ruleset golden, dnsmasq real numa porta alta)
make test-netns   # carrega o ruleset no kernel e testa rejeição, num netns descartável (sem root)

# daemon sem tocar no sistema:
./bin/blockd --dry-run --state-dir /tmp/bd --socket /tmp/bd/s.sock &
BLOCKD_SOCKET=/tmp/bd/s.sock ./bin/blockctl status
BLOCKD_SOCKET=/tmp/bd/s.sock ./bin/blockd-gui
BLOCKD_IT_SOCKET=/tmp/bd/s.sock go test ./internal/gui   # GUI controller ↔ blockctl ↔ daemon
```

O código é organizado em torno de interfaces injetadas no `engine.Service`
(`internal/backend`): `NetworkBlocker` (nft), `DNSBlocker` (dnsmasq),
`ProcessTracker` (cgroup), `Resolver` e `Store`. As implementações Linux ficam em
`internal/backend/*`, e os fakes em `internal/backend/fake`. A única montagem
concreta está em `cmd/blockd/main.go`. A GUI depende da interface `gui.Controller`.

Limitações conhecidas: VPNs que forçam o próprio DNS, apps que embutem IPs
fixos sem processo identificável, e root (o próprio usuário pode desfazer tudo;
a ideia é colocar fricção, não ser à prova de você).

## Licença

GPL-3.0-or-later. Veja [LICENSE](LICENSE).

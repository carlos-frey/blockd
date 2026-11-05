package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
)

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Mostra o que está bloqueado, por quê, e se o sistema está sincronizado",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return call(a, api.MStatus, nil, a.renderStatus)
		},
	}
}

func (a *app) syncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Força reaplicar firewall e DNS a partir do estado salvo",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return call(a, api.MSync, nil, func(s api.SyncView) {
				fmt.Fprintln(a.out, a.syncLine(s))
			})
		},
	}
}

// ── catalog ────────────────────────────────────────────────────────────

func (a *app) catalogCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "catalog", Aliases: []string{"cat"}, Short: "Catálogo de apps bloqueáveis"}

	var list api.CatalogListParams
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista o catálogo com o estado de cada app",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return call(a, api.MCatalogList, list, a.renderCatalog)
		},
	}
	ls.Flags().StringVarP(&list.Category, "category", "c", "", "filtra por categoria")
	ls.Flags().StringVarP(&list.Search, "search", "s", "", "busca por nome, id ou domínio")

	show := &cobra.Command{
		Use: "show <app>", Short: "Detalhes de um app", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MCatalogShow, api.IDParams{ID: args[0]}, a.renderApp)
		},
	}

	var f appFlags
	add := &cobra.Command{
		Use:   "add <nome>",
		Short: "Adiciona um app custom ao catálogo",
		Example: `  blockctl catalog add "Bet365" --domain bet365.com --category Apostas
  blockctl catalog add "Spotify" --process spotify --domain spotify.com,scdn.co`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app := domain.App{ID: f.id, Name: args[0], Category: f.category, Description: f.desc,
				Domains: f.domains(), Processes: splitList(f.processes), IPs: splitList(f.ips), Ports: f.portList()}
			return call(a, api.MCatalogAdd, api.CatalogAddParams{App: app}, a.renderApp)
		},
	}
	f.register(add, true)

	var ef appFlags
	edit := &cobra.Command{
		Use:   "edit <app>",
		Short: "Edita um app custom (listas passadas substituem as atuais)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := api.CatalogEditParams{ID: args[0]}
			ch := cmd.Flags().Changed
			if ch("name") {
				p.Name = &ef.name
			}
			if ch("category") {
				p.Category = &ef.category
			}
			if ch("desc") {
				p.Description = &ef.desc
			}
			if ch("domain") || ch("ip-domain") {
				d := ef.domains()
				p.Domains = &d
			}
			if ch("process") {
				pr := splitList(ef.processes)
				p.Processes = &pr
			}
			if ch("ip") {
				ips := splitList(ef.ips)
				p.IPs = &ips
			}
			if ch("port") {
				ports := ef.portList()
				p.Ports = &ports
			}
			return call(a, api.MCatalogEdit, p, a.renderApp)
		},
	}
	ef.register(edit, false)

	rm := &cobra.Command{
		Use: "rm <app>", Short: "Remove um app custom (e tira dos seus perfis)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MCatalogRemove, api.IDParams{ID: args[0]}, func(api.StatusView) {
				fmt.Fprintf(a.out, "app %s removido\n", args[0])
			})
		},
	}
	cmd.AddCommand(ls, show, add, edit, rm)
	return cmd
}

type appFlags struct {
	id, name, category, desc         string
	domain, ipDomain, processes, ips []string
	ports                            []uint
}

func (f *appFlags) register(cmd *cobra.Command, adding bool) {
	fl := cmd.Flags()
	if adding {
		fl.StringVar(&f.id, "id", "", "id (padrão: derivado do nome)")
	} else {
		fl.StringVar(&f.name, "name", "", "novo nome")
	}
	fl.StringVar(&f.category, "category", "", "categoria (padrão: Outros)")
	fl.StringVar(&f.desc, "desc", "", "descrição")
	fl.StringSliceVar(&f.domain, "domain", nil, "domínio (cobre subdomínios); repetível ou separado por vírgula")
	fl.StringSliceVar(&f.ipDomain, "ip-domain", nil, "domínio também bloqueado por IP (cuidado com CDNs)")
	fl.StringSliceVar(&f.processes, "process", nil, "nome do executável que fica sem rede")
	fl.StringSliceVar(&f.ips, "ip", nil, "IP ou CIDR fixo")
	fl.UintSliceVar(&f.ports, "port", nil, "restringe os IPs fixos a estas portas")
}

func (f *appFlags) portList() []uint16 {
	out := make([]uint16, 0, len(f.ports))
	for _, p := range f.ports {
		out = append(out, uint16(p))
	}
	return out
}

func (f *appFlags) domains() []domain.Domain {
	var out []domain.Domain
	for _, d := range splitList(f.domain) {
		out = append(out, domain.Domain{Name: d})
	}
	for _, d := range splitList(f.ipDomain) {
		out = append(out, domain.Domain{Name: d, IPBlock: true})
	}
	return out
}

// ── app ────────────────────────────────────────────────────────────────

func (a *app) appCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "app", Short: "Bloqueia ou libera apps avulsos (fora de perfis)"}
	done := func(verb string, ids []string) func(api.StatusView) {
		return func(s api.StatusView) {
			fmt.Fprintf(a.out, "%s %s\n", verb, strings.Join(ids, ", "))
			fmt.Fprintln(a.out, a.syncLine(s.Sync))
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use: "block <app>...", Short: "Bloqueia apps avulsos", Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MAppBlock, api.IDsParams{IDs: args}, done(a.paint("bloqueado:", red), args))
		},
	}, &cobra.Command{
		Use:   "unblock <app>...",
		Short: "Libera apps avulsos (falha se um perfil ativo ainda os bloqueia)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MAppUnblock, api.IDsParams{IDs: args}, done(a.paint("liberado:", green), args))
		},
	})
	return cmd
}

// ── profile ────────────────────────────────────────────────────────────

func (a *app) profileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Aliases: []string{"p"}, Short: "Perfis de bloqueio"}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista os perfis", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return call(a, api.MProfileList, nil, a.renderProfiles)
		},
	}
	show := &cobra.Command{
		Use: "show <perfil>", Short: "Detalhes de um perfil", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MProfileShow, api.IDParams{ID: args[0]}, a.renderProfile)
		},
	}

	var cp api.ProfileCreateParams
	var capps []string
	create := &cobra.Command{
		Use:   "create <nome>",
		Short: "Cria um perfil",
		Example: `  blockctl profile create "Noite" --apps youtube,twitch,reddit
  blockctl profile create "Agora" --from-blocked     # parte do que está bloqueado agora`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cp.Name, cp.Apps = args[0], splitList(capps)
			return call(a, api.MProfileCreate, cp, a.renderProfile)
		},
	}
	create.Flags().StringVar(&cp.ID, "id", "", "id (padrão: derivado do nome)")
	create.Flags().StringVar(&cp.Description, "desc", "", "descrição")
	create.Flags().StringSliceVar(&capps, "apps", nil, "apps do perfil")
	create.Flags().BoolVar(&cp.FromBlocked, "from-blocked", false, "inclui tudo que está bloqueado agora")
	create.Flags().BoolVar(&cp.Activate, "on", false, "já ativa o perfil")

	var name, desc string
	var set, add, remove []string
	edit := &cobra.Command{
		Use: "edit <perfil>", Short: "Edita um perfil seu", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := api.ProfileEditParams{ID: args[0], Add: splitList(add), Remove: splitList(remove)}
			if cmd.Flags().Changed("name") {
				p.Name = &name
			}
			if cmd.Flags().Changed("desc") {
				p.Description = &desc
			}
			if cmd.Flags().Changed("set") {
				s := splitList(set)
				p.SetApps = &s
			}
			return call(a, api.MProfileEdit, p, a.renderProfile)
		},
	}
	edit.Flags().StringVar(&name, "name", "", "novo nome")
	edit.Flags().StringVar(&desc, "desc", "", "nova descrição")
	edit.Flags().StringSliceVar(&set, "set", nil, "substitui a lista de apps")
	edit.Flags().StringSliceVar(&add, "add", nil, "adiciona apps")
	edit.Flags().StringSliceVar(&remove, "remove", nil, "remove apps")

	var cloneName string
	clone := &cobra.Command{
		Use: "clone <perfil>", Short: "Duplica um perfil (inclusive os prontos) para editar", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MProfileClone, api.ProfileCloneParams{ID: args[0], Name: cloneName}, a.renderProfile)
		},
	}
	clone.Flags().StringVar(&cloneName, "name", "", "nome da cópia")

	rm := &cobra.Command{
		Use: "rm <perfil>", Short: "Apaga um perfil seu (desativa antes, se preciso)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MProfileRemove, api.IDParams{ID: args[0]}, func(api.StatusView) {
				fmt.Fprintf(a.out, "perfil %s apagado\n", args[0])
			})
		},
	}
	toggle := func(method, verb, color string) func(*cobra.Command, []string) error {
		return func(_ *cobra.Command, args []string) error {
			return call(a, method, api.IDsParams{IDs: args}, func(s api.StatusView) {
				fmt.Fprintf(a.out, "%s %s\n", a.paint(verb, color), strings.Join(args, ", "))
				fmt.Fprintf(a.out, "%d apps bloqueados agora. %s\n", len(s.Blocked), a.syncLine(s.Sync))
			})
		}
	}
	on := &cobra.Command{Use: "on <perfil>...", Short: "Ativa perfis", Args: cobra.MinimumNArgs(1),
		RunE: toggle(api.MProfileOn, "ativado:", red)}
	off := &cobra.Command{Use: "off <perfil>...", Short: "Desativa perfis", Args: cobra.MinimumNArgs(1),
		RunE: toggle(api.MProfileOff, "desativado:", green)}

	cmd.AddCommand(ls, show, create, edit, clone, rm, on, off)
	return cmd
}

// ── rendering ──────────────────────────────────────────────────────────

func (a *app) syncLine(s api.SyncView) string {
	when := ""
	if !s.LastCheck.IsZero() {
		when = " (checado às " + s.LastCheck.Local().Format("15:04:05") + ")"
	}
	if s.InSync {
		line := a.paint("● sincronizado", green) + a.paint(when, dim)
		if s.LastDrift != nil {
			line += a.paint(fmt.Sprintf(" — drift corrigido às %s", s.LastDrift.Local().Format("15:04:05")), yellow)
		}
		return line
	}
	return a.paint("● fora de sincronia", red) + when + "\n  " + s.LastError
}

func reasons(by []string) string {
	var out []string
	for _, r := range by {
		if p, ok := strings.CutPrefix(r, "profile:"); ok {
			out = append(out, p)
		} else if id, ok := strings.CutPrefix(r, "schedule:"); ok {
			out = append(out, "agenda "+id)
		} else if id, ok := strings.CutPrefix(r, "focus:"); ok {
			out = append(out, "foco "+id)
		} else {
			out = append(out, "avulso")
		}
	}
	return strings.Join(out, ", ")
}

func (a *app) renderStatus(s api.StatusView) {
	if s.DryRun {
		fmt.Fprintln(a.out, a.paint("[dry-run: nada é aplicado no sistema]", yellow))
	}
	active := "nenhum"
	if len(s.ActiveProfiles) > 0 {
		active = strings.Join(s.ActiveProfiles, ", ")
	}
	fmt.Fprintf(a.out, "%s %s\n", a.paint("Perfis ativos:", bold), active)
	if len(s.ActiveSchedules) > 0 {
		fmt.Fprintf(a.out, "%s %s\n", a.paint("Agendas valendo:", bold), strings.Join(s.ActiveSchedules, ", "))
	}
	for _, f := range s.Focus {
		a.renderFocus(f)
	}
	if len(s.Blocked) == 0 {
		fmt.Fprintln(a.out, a.paint("Nada bloqueado.", dim))
	} else {
		fmt.Fprintf(a.out, "%s %d apps · %d domínios · %d processos · %d IPs fixos\n",
			a.paint("Bloqueado:", bold), len(s.Blocked), len(s.Plan.DNSDomains), len(s.Plan.Processes), len(s.Plan.Static))
		w := maxLen(s.Blocked)
		for _, v := range s.Blocked {
			fmt.Fprintf(a.out, "  %s %-*s %s\n", a.paint("✕", red), w, v.Name, a.paint(reasons(v.BlockedBy), dim))
		}
	}
	if len(s.Jailed) > 0 {
		var names []string
		for _, j := range s.Jailed {
			names = append(names, fmt.Sprintf("%s[%d]", j.Name, j.PID))
		}
		fmt.Fprintf(a.out, "%s %s\n", a.paint("Sem rede agora:", bold), strings.Join(names, " "))
	}
	if s.Sync.DynamicIPs > 0 {
		fmt.Fprintf(a.out, "%s %d\n", a.paint("IPs resolvidos bloqueados:", bold), s.Sync.DynamicIPs)
	}
	if s.NextChange != nil {
		fmt.Fprintf(a.out, "%s %s\n", a.paint("Próxima mudança automática:", bold), domain.When(*s.NextChange, time.Now()))
	}
	fmt.Fprintln(a.out, a.syncLine(s.Sync))
}

func maxLen(apps []api.AppView) int {
	w := 0
	for _, a := range apps {
		w = max(w, len([]rune(a.Name)))
	}
	return w
}

func (a *app) renderCatalog(apps []api.AppView) {
	cat := ""
	w := maxLen(apps)
	for _, v := range apps {
		if v.Category != cat {
			cat = v.Category
			fmt.Fprintf(a.out, "\n%s\n", a.paint(cat, bold+";"+cyan))
		}
		mark := a.paint("○", green)
		if v.Blocked {
			mark = a.paint("✕", red)
		}
		extra := ""
		if v.Blocked {
			extra = a.paint(" bloqueado por: "+reasons(v.BlockedBy), dim)
		}
		custom := ""
		if !v.Builtin {
			custom = a.paint(" [custom]", yellow)
		}
		fmt.Fprintf(a.out, "  %s %-14s %-*s%s%s\n", mark, v.ID, w, v.Name, custom, extra)
	}
}

func (a *app) renderApp(v api.AppView) {
	fmt.Fprintf(a.out, "%s %s\n", a.paint(v.Name, bold), a.paint("("+v.ID+")", dim))
	fmt.Fprintf(a.out, "  categoria: %s\n", v.Category)
	if v.Description != "" {
		fmt.Fprintf(a.out, "  %s\n", v.Description)
	}
	if v.Blocked {
		fmt.Fprintf(a.out, "  estado:    %s por %s\n", a.paint("bloqueado", red), reasons(v.BlockedBy))
	} else {
		fmt.Fprintf(a.out, "  estado:    %s\n", a.paint("liberado", green))
	}
	var doms []string
	for _, d := range v.Domains {
		s := d.Name
		if d.IPBlock {
			s += " (+IP)"
		}
		doms = append(doms, s)
	}
	printList(a, "domínios", doms)
	printList(a, "processos", v.Processes)
	printList(a, "IPs", v.IPs)
	printList(a, "perfis", v.Profiles)
}

func printList(a *app, label string, items []string) {
	if len(items) > 0 {
		fmt.Fprintf(a.out, "  %-10s %s\n", label+":", strings.Join(items, ", "))
	}
}

func (a *app) renderProfiles(ps []api.ProfileView) {
	for _, p := range ps {
		mark := a.paint("○", dim)
		if p.Active {
			mark = a.paint("●", red)
		}
		tag := ""
		if p.Builtin {
			tag = a.paint(" pronto", dim)
		}
		fmt.Fprintf(a.out, "%s %-18s %-22s %2d apps%s\n", mark, p.ID, p.Name, len(p.Apps), tag)
	}
}

func (a *app) renderProfile(p api.ProfileView) {
	state := a.paint("inativo", dim)
	if p.Active {
		state = a.paint("ativo", red)
	}
	kind := "seu"
	if p.Builtin {
		kind = "pronto (use clone para editar)"
	}
	fmt.Fprintf(a.out, "%s %s — %s, %s\n", a.paint(p.Name, bold), a.paint("("+p.ID+")", dim), state, kind)
	if p.Description != "" {
		fmt.Fprintf(a.out, "  %s\n", p.Description)
	}
	apps := slices.Clone(p.Apps)
	fmt.Fprintf(a.out, "  apps (%d): %s\n", len(apps), strings.Join(apps, ", "))
}

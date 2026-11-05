package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/carlos-frey/blockd/internal/api"
	"github.com/carlos-frey/blockd/internal/domain"
	"github.com/carlos-frey/blockd/internal/ipc"
)

// ── schedule ───────────────────────────────────────────────────────────

func (a *app) scheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "schedule", Aliases: []string{"agenda", "sched"},
		Short: "Agendas: ligam perfis sozinhas em horários da semana",
		Long: `Uma agenda liga perfis nas janelas da semana definidas (ex.: seg-sex 09:00-18:00).
Janelas com fim antes do início cruzam a meia-noite (22:00-02:00).

O modo diz o quanto é difícil desligar enquanto ela vale:
  livre     pausa na hora, até o fim da janela atual
  friccao   pede uma frase e só pausa depois de N minutos
  travado   não dá para pausar, editar, pular nem apagar até a janela acabar`,
	}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista as agendas", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return call(a, api.MScheduleList, nil, a.renderSchedules)
		},
	}
	show := &cobra.Command{
		Use: "show <agenda>", Short: "Detalhes de uma agenda", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MScheduleShow, api.IDParams{ID: args[0]}, a.renderSchedule)
		},
	}

	var cp api.ScheduleCreateParams
	var cprofiles, cwindows []string
	var cg guardFlags
	create := &cobra.Command{
		Use:   "create <nome>",
		Short: "Cria uma agenda",
		Example: `  blockctl schedule create "Trabalho" --profiles redes-sociais,video \
      --window "seg-sex 09:00-12:00" --window "seg-sex 13:30-18:00" --mode friccao --delay 10
  blockctl schedule create "Noite" --profiles games --window "todos 23:00-07:00" --mode travado`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := parseWindows(cwindows)
			if err != nil {
				return a.fail(err)
			}
			g, err := cg.guard()
			if err != nil {
				return a.fail(err)
			}
			cp.Name, cp.Profiles, cp.Windows, cp.Guard = args[0], splitList(cprofiles), ws, g
			return call(a, api.MScheduleCreate, cp, a.renderSchedule)
		},
	}
	create.Flags().StringVar(&cp.ID, "id", "", "id (padrão: derivado do nome)")
	create.Flags().StringSliceVar(&cprofiles, "profiles", nil, "perfis que a agenda liga")
	create.Flags().StringArrayVar(&cwindows, "window", nil, `janela "dias HH:MM-HH:MM"; repetível (dias: seg-sex, sab,dom, uteis, fds, todos)`)
	create.Flags().BoolVar(&cp.Disabled, "off", false, "cria desligada")
	cg.register(create.Flags())

	var name string
	var eprofiles, ewindows []string
	var eg guardFlags
	edit := &cobra.Command{
		Use: "edit <agenda>", Short: "Edita uma agenda (listas passadas substituem as atuais)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := api.ScheduleEditParams{ID: args[0]}
			ch := cmd.Flags().Changed
			if ch("name") {
				p.Name = &name
			}
			if ch("profiles") {
				ps := splitList(eprofiles)
				p.Profiles = &ps
			}
			if ch("window") {
				ws, err := parseWindows(ewindows)
				if err != nil {
					return a.fail(err)
				}
				p.Windows = &ws
			}
			if ch("mode") || ch("delay") || ch("phrase") {
				cur, err := a.showSchedule(args[0])
				if err != nil {
					return a.fail(err)
				}
				g, err := eg.merge(cmd.Flags(), cur.Guard)
				if err != nil {
					return a.fail(err)
				}
				p.Guard = &g
			}
			return call(a, api.MScheduleEdit, p, a.renderSchedule)
		},
	}
	edit.Flags().StringVar(&name, "name", "", "novo nome")
	edit.Flags().StringSliceVar(&eprofiles, "profiles", nil, "substitui os perfis")
	edit.Flags().StringArrayVar(&ewindows, "window", nil, "substitui as janelas; repetível")
	eg.register(edit.Flags())

	rm := &cobra.Command{
		Use: "rm <agenda>", Short: "Apaga uma agenda", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MScheduleRemove, api.IDParams{ID: args[0]}, func(api.StatusView) {
				fmt.Fprintf(a.out, "agenda %s apagada\n", args[0])
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
	on := &cobra.Command{Use: "on <agenda>...", Short: "Liga agendas", Args: cobra.MinimumNArgs(1),
		RunE: toggle(api.MScheduleOn, "ligada:", red)}
	off := &cobra.Command{Use: "off <agenda>...", Short: "Desliga agendas (respeita o modo enquanto valem)", Args: cobra.MinimumNArgs(1),
		RunE: toggle(api.MScheduleOff, "desligada:", green)}

	var phrase string
	pause := &cobra.Command{
		Use:   "pause <agenda>",
		Short: "Pausa a agenda até o fim da janela atual (no modo fricção, pede a frase e espera)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := api.PauseParams{ID: args[0], Phrase: phrase}
			if !cmd.Flags().Changed("phrase") {
				cur, err := a.showSchedule(args[0])
				if err == nil && cur.Guard.Mode == domain.GuardFriction {
					if p.Phrase, err = a.askPhrase(cur.Guard); err != nil {
						return a.fail(err)
					}
				}
			}
			return call(a, api.MSchedulePause, p, a.renderPaused)
		},
	}
	pause.Flags().StringVar(&phrase, "phrase", "", "frase do modo fricção (sem ela, é pedida no terminal)")

	resume := &cobra.Command{
		Use: "resume <agenda>", Short: "Cancela a pausa (pedida ou valendo)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MScheduleResume, api.IDParams{ID: args[0]}, a.renderSchedule)
		},
	}

	var undo bool
	skip := &cobra.Command{
		Use:     "skip <agenda> <data>",
		Short:   "Pula a agenda numa data (hoje, amanha, AAAA-MM-DD ou DD/MM)",
		Example: "  blockctl schedule skip trabalho 25/12\n  blockctl schedule skip trabalho amanha --undo",
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			d, err := parseDate(args[1], time.Now())
			if err != nil {
				return a.fail(err)
			}
			return call(a, api.MScheduleSkip, api.ScheduleSkipParams{ID: args[0], Date: d, Undo: undo}, a.renderSchedule)
		},
	}
	skip.Flags().BoolVar(&undo, "undo", false, "volta a valer nessa data")

	cmd.AddCommand(ls, show, create, edit, rm, on, off, pause, resume, skip)
	return cmd
}

// ── focus ──────────────────────────────────────────────────────────────

func (a *app) focusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "focus", Aliases: []string{"foco"},
		Short: "Foco temporário: liga perfis por um tempo e desliga sozinho",
	}

	var dur, until string
	var g guardFlags
	start := &cobra.Command{
		Use:   "start <perfil>...",
		Short: "Liga perfis por um tempo (--for) ou até um horário (--until)",
		Example: `  blockctl focus start redes-sociais video --for 2h
  blockctl focus start games --until 18:00 --mode travado`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p := api.FocusStartParams{Profiles: splitList(args)}
			var err error
			if p.Minutes, p.Until, err = parseSpan(dur, until, time.Now()); err != nil {
				return a.fail(err)
			}
			if p.Guard, err = g.guard(); err != nil {
				return a.fail(err)
			}
			return call(a, api.MFocusStart, p, a.renderFocus)
		},
	}
	start.Flags().StringVar(&dur, "for", "", "duração (25m, 2h, 1h30m ou minutos)")
	start.Flags().StringVar(&until, "until", "", "horário de fim (HH:MM; se já passou, é amanhã)")
	g.register(start.Flags())

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista os focos", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return call(a, api.MStatus, nil, func(s api.StatusView) {
				if len(s.Focus) == 0 {
					fmt.Fprintln(a.out, a.paint("Nenhum foco agora.", dim))
				}
				for _, f := range s.Focus {
					a.renderFocus(f)
				}
			})
		},
	}

	var phrase string
	stop := &cobra.Command{
		Use: "stop <id>", Short: "Encerra um foco (respeita o modo dele)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := api.PauseParams{ID: args[0], Phrase: phrase}
			if !cmd.Flags().Changed("phrase") {
				if f, ok := a.findFocus(args[0]); ok && f.Guard.Mode == domain.GuardFriction {
					var err error
					if p.Phrase, err = a.askPhrase(f.Guard); err != nil {
						return a.fail(err)
					}
				}
			}
			return call(a, api.MFocusStop, p, func(s api.StatusView) {
				for _, f := range s.Focus {
					if f.ID == args[0] && f.Pause != nil {
						fmt.Fprintf(a.out, "%s o foco %s termina às %s\n", a.paint("pedido:", yellow), f.ID, f.Pause.From.Local().Format("15:04"))
						return
					}
				}
				fmt.Fprintf(a.out, "%s foco %s\n", a.paint("encerrado:", green), args[0])
			})
		},
	}
	stop.Flags().StringVar(&phrase, "phrase", "", "frase do modo fricção (sem ela, é pedida no terminal)")

	resume := &cobra.Command{
		Use: "resume <id>", Short: "Cancela o pedido de encerrar", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call(a, api.MFocusResume, api.IDParams{ID: args[0]}, a.renderFocus)
		},
	}

	var edur, euntil string
	extend := &cobra.Command{
		Use: "extend <id>", Short: "Estende um foco (--for soma ao fim atual)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p := api.FocusExtendParams{ID: args[0]}
			var err error
			if p.Minutes, p.Until, err = parseSpan(edur, euntil, time.Now()); err != nil {
				return a.fail(err)
			}
			return call(a, api.MFocusExtend, p, a.renderFocus)
		},
	}
	extend.Flags().StringVar(&edur, "for", "", "quanto a mais (30m, 1h)")
	extend.Flags().StringVar(&euntil, "until", "", "novo horário de fim (HH:MM)")

	cmd.AddCommand(start, ls, stop, resume, extend)
	return cmd
}

// ── flags and parsing ──────────────────────────────────────────────────

type guardFlags struct {
	mode, phrase string
	delay        int
}

func (g *guardFlags) register(fl *pflag.FlagSet) {
	fl.StringVar(&g.mode, "mode", "livre", "livre, friccao ou travado")
	fl.IntVar(&g.delay, "delay", domain.DefaultDelayMinutes, "fricção: minutos de espera até a pausa valer")
	fl.StringVar(&g.phrase, "phrase", "", "fricção: frase a digitar para pausar (padrão: \""+domain.DefaultPhrase+"\")")
}

func (g *guardFlags) guard() (domain.Guard, error) {
	m, err := parseMode(g.mode)
	if err != nil {
		return domain.Guard{}, err
	}
	out := domain.Guard{Mode: m, DelayMinutes: g.delay, Phrase: g.phrase}
	return out, out.Normalize()
}

// merge applies only the guard flags that were set on top of cur.
func (g *guardFlags) merge(fl *pflag.FlagSet, cur domain.Guard) (domain.Guard, error) {
	if fl.Changed("mode") {
		m, err := parseMode(g.mode)
		if err != nil {
			return cur, err
		}
		cur.Mode = m
	}
	if fl.Changed("delay") {
		cur.DelayMinutes = g.delay
	}
	if fl.Changed("phrase") {
		cur.Phrase = g.phrase
	}
	return cur, cur.Normalize()
}

func parseMode(s string) (domain.GuardMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "livre", "free":
		return domain.GuardFree, nil
	case "friccao", "fricção", "friction":
		return domain.GuardFriction, nil
	case "travado", "travada", "locked", "lock":
		return domain.GuardLocked, nil
	}
	return "", domain.Errorf(domain.CodeInvalidArgument, "modo %q inválido: use livre, friccao ou travado", s)
}

func parseWindows(specs []string) ([]domain.Window, error) {
	var out []domain.Window
	for _, s := range specs {
		w, err := domain.ParseWindow(s)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// parseSpan reads --for (minutes) or --until (a time today, else tomorrow).
func parseSpan(dur, until string, now time.Time) (int, *time.Time, error) {
	switch {
	case dur != "" && until != "":
		return 0, nil, domain.Errorf(domain.CodeInvalidArgument, "use --for ou --until, não os dois")
	case dur != "":
		if n, err := strconv.Atoi(dur); err == nil && n > 0 {
			return n, nil, nil
		}
		d, err := time.ParseDuration(dur)
		if err != nil || d < time.Minute {
			return 0, nil, domain.Errorf(domain.CodeInvalidArgument, "duração inválida %q (ex.: 25m, 2h, 1h30m)", dur)
		}
		return int(d.Round(time.Minute) / time.Minute), nil, nil
	case until != "":
		w, err := domain.ParseWindow(until + "-" + until)
		if err != nil {
			return 0, nil, domain.Errorf(domain.CodeInvalidArgument, "horário inválido %q (use HH:MM)", until)
		}
		hh, _ := strconv.Atoi(w.Start[:2])
		mm, _ := strconv.Atoi(w.Start[3:])
		t := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return 0, &t, nil
	}
	return 0, nil, domain.Errorf(domain.CodeInvalidArgument, "diga por quanto tempo (--for 2h) ou até quando (--until 18:00)")
}

// parseDate accepts hoje, amanha, AAAA-MM-DD and DD/MM (this year, or next
// year when the date already passed).
func parseDate(s string, now time.Time) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hoje", "today":
		return now.Format(domain.DateLayout), nil
	case "amanha", "amanhã", "tomorrow":
		return now.AddDate(0, 0, 1).Format(domain.DateLayout), nil
	}
	if t, err := time.Parse(domain.DateLayout, s); err == nil {
		return t.Format(domain.DateLayout), nil
	}
	if t, err := time.ParseInLocation("02/01", s, now.Location()); err == nil {
		t = time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
		if t.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())) {
			t = t.AddDate(1, 0, 0)
		}
		return t.Format(domain.DateLayout), nil
	}
	return "", domain.Errorf(domain.CodeInvalidArgument, "data inválida %q (use hoje, amanha, AAAA-MM-DD ou DD/MM)", s)
}

// ── helpers that talk to the daemon ────────────────────────────────────

func (a *app) client() *ipc.Client {
	c := ipc.NewClient()
	c.Path = a.socket
	return c
}

func (a *app) showSchedule(id string) (api.ScheduleView, error) {
	var v api.ScheduleView
	err := a.client().Call(context.Background(), api.MScheduleShow, api.IDParams{ID: id}, &v)
	return v, err
}

func (a *app) findFocus(id string) (api.FocusView, bool) {
	var s api.StatusView
	if err := a.client().Call(context.Background(), api.MStatus, nil, &s); err != nil {
		return api.FocusView{}, false
	}
	for _, f := range s.Focus {
		if f.ID == id {
			return f, true
		}
	}
	return api.FocusView{}, false
}

// askPhrase prompts for the friction phrase. With --json or without a
// terminal there is nobody to ask: the daemon then rejects the empty phrase.
func (a *app) askPhrase(g domain.Guard) (string, error) {
	if a.json || !isTerminal(os.Stdin) {
		return "", nil
	}
	fmt.Fprintf(a.out, "Modo fricção: digite a frase abaixo para pedir a pausa (ela vale em %d min).\n  %s\n> ",
		g.DelayMinutes, a.paint(g.Phrase, bold))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", domain.Errorf(domain.CodeInvalidArgument, "pausa cancelada")
	}
	return strings.TrimSpace(line), nil
}

// fail reports a local error (bad flag value) the same way call reports
// daemon errors.
func (a *app) fail(err error) error {
	de := domain.AsError(err)
	if a.json {
		a.printJSON(api.Response{Error: de})
	} else {
		fmt.Fprintf(os.Stderr, "%s %s\n", a.paint("erro:", red), de.Message)
	}
	return exitError{ExitUsage}
}

// ── rendering ──────────────────────────────────────────────────────────

func modeLabel(g domain.Guard) string {
	switch g.Mode {
	case domain.GuardFriction:
		return fmt.Sprintf("fricção (%d min)", g.DelayMinutes)
	case domain.GuardLocked:
		return "travado"
	}
	return "livre"
}

func (a *app) scheduleState(v api.ScheduleView) string {
	now := time.Now()
	switch {
	case !v.Enabled:
		return a.paint("desligada", dim)
	case v.Active && v.Pause != nil:
		return a.paint("valendo até "+domain.When(v.Current.End, now), red) +
			a.paint(" · pausa às "+domain.When(v.Pause.From, now), yellow)
	case v.Active:
		return a.paint("valendo até "+domain.When(v.Current.End, now), red)
	case v.Current != nil && v.Pause != nil:
		return a.paint("pausada até "+domain.When(v.Pause.Until, now), yellow)
	case v.Next != nil:
		return "próxima: " + domain.When(v.Next.Start, now)
	}
	return a.paint("sem próximas janelas", dim)
}

func windowsLabel(ws []domain.Window) string {
	var out []string
	for _, w := range ws {
		out = append(out, w.String())
	}
	return strings.Join(out, "; ")
}

func (a *app) renderSchedules(vs []api.ScheduleView) {
	if len(vs) == 0 {
		fmt.Fprintln(a.out, a.paint("Nenhuma agenda. Crie com: blockctl schedule create \"Trabalho\" --profiles ... --window \"seg-sex 09:00-18:00\"", dim))
		return
	}
	for _, v := range vs {
		mark := a.paint("○", dim)
		if v.Active {
			mark = a.paint("●", red)
		}
		fmt.Fprintf(a.out, "%s %-16s %-20s %-16s %s\n", mark, v.ID, v.Name, modeLabel(v.Guard), a.scheduleState(v))
		fmt.Fprintf(a.out, "  %s\n", a.paint(windowsLabel(v.Windows)+" · "+strings.Join(v.Profiles, ", "), dim))
	}
}

func (a *app) renderSchedule(v api.ScheduleView) {
	fmt.Fprintf(a.out, "%s %s — %s\n", a.paint(v.Name, bold), a.paint("("+v.ID+")", dim), a.scheduleState(v))
	printList(a, "perfis", v.Profiles)
	for i, w := range v.Windows {
		label := ""
		if i == 0 {
			label = "janelas:"
		}
		fmt.Fprintf(a.out, "  %-10s %s\n", label, w.String())
	}
	fmt.Fprintf(a.out, "  %-10s %s\n", "modo:", modeLabel(v.Guard))
	if v.Guard.Mode == domain.GuardFriction {
		fmt.Fprintf(a.out, "  %-10s %s\n", "frase:", v.Guard.Phrase)
	}
	printList(a, "pula", v.Skip)
}

func (a *app) renderPaused(v api.ScheduleView) {
	now := time.Now()
	if v.Pause != nil && v.Pause.From.After(now) {
		fmt.Fprintf(a.out, "%s %s pausa às %s, até %s\n", a.paint("pedido:", yellow), v.Name,
			domain.When(v.Pause.From, now), domain.When(v.Pause.Until, now))
		return
	}
	a.renderSchedule(v)
}

func (a *app) renderFocus(f api.FocusView) {
	now := time.Now()
	state := a.paint("até "+domain.When(f.Until, now), red)
	if f.Pause != nil {
		state += a.paint(" · encerra às "+domain.When(f.Pause.From, now), yellow)
	}
	fmt.Fprintf(a.out, "%s foco %s: %s %s · %s\n", a.paint("●", red), a.paint(f.ID, bold),
		strings.Join(f.Profiles, ", "), state, modeLabel(f.Guard))
}

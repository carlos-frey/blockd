// blockd-gui is the graphical front-end of blockd. It never talks to the
// daemon directly: every read and action goes through `blockctl --json`.
package main

import (
	"context"
	"flag"

	"github.com/carlos-frey/blockd/internal/gui"
)

func main() {
	bin := flag.String("blockctl", gui.FindBlockctl(), "caminho do blockctl")
	socket := flag.String("socket", "", "socket do daemon (padrão: o do blockctl)")
	var o gui.Options
	flag.StringVar(&o.Page, "page", "overview", "tela inicial: overview, catalog, profiles ou schedule")
	flag.StringVar(&o.Open, "open", "", "abre um diálogo ao iniciar: new-profile, new-app, new-schedule ou focus")
	flag.Parse()

	app := gui.NewApp(&gui.CLIController{Bin: *bin, Socket: *socket}, o)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.Run(ctx)
}

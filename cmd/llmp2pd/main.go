package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/log0u7/llmp2p/internal/daemon"
	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/store"
	"github.com/log0u7/llmp2p/internal/version"
)

func main() {
	fs := flag.NewFlagSet("llmp2pd", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`llmp2pd: keep stored llmp2p models seeding and serve a local status API.

Usage:
  llmp2pd [flags]

Flags:`)
		fs.PrintDefaults()
		fmt.Println(`
API:
  GET /api/v1/status     daemon summary
  GET /api/v1/models     model ids in store
  GET /api/v1/torrents   swarm statuses`)
	}
	addr := fs.String("addr", daemon.DefaultAddr, "status API listen address")
	dir := fs.String("dir", store.DefaultDir(), "model store directory")
	listenPort := fs.Int("listen-port", 0, "BitTorrent listen port, 0 picks a random one")
	var showVersion bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "v", false, "print version and exit (shorthand)")
	_ = fs.Parse(os.Args[1:])
	if showVersion {
		fmt.Println("llmp2pd version", version.Version)
		return
	}

	st, err := store.Open(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llmp2pd:", err)
		os.Exit(1)
	}

	// systemd sends SIGTERM; graceful shutdown must trigger on both.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, daemon.Options{
		Store:      st,
		ListenAddr: *addr,
		Log:        slog.Default(),
		Version:    version.Version,
		EngineOverrides: func(c *engine.Config) {
			c.ListenPort = *listenPort
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, "llmp2pd:", err)
		os.Exit(1)
	}
}

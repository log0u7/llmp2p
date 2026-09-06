package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/log0u7/llmp2p/internal/daemon"
	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/store"
)

var version = "0.0.0"

func main() {
	addr := daemon.DefaultAddr
	dir := store.DefaultDir()
	listenPort := 0
	if len(os.Args) > 1 {
		for i := 1; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--addr", "-addr":
				i++
				if i < len(os.Args) {
					addr = os.Args[i]
				}
			case "--dir", "-dir":
				i++
				if i < len(os.Args) {
					dir = os.Args[i]
				}
			case "--listen-port", "-listen-port":
				i++
				if i < len(os.Args) {
					if _, err := fmt.Sscanf(os.Args[i], "%d", &listenPort); err != nil {
						fmt.Fprintln(os.Stderr, "llmp2pd: --listen-port wants a number")
						os.Exit(1)
					}
				}
			case "--version", "-v":
				fmt.Println("llmp2pd version", version)
				return
			case "--help", "-h":
				fmt.Println(`llmp2pd: keep stored llmp2p models seeding and serve a local status API.

Usage: llmp2pd [--addr 127.0.0.1:8347] [--dir ~/.local/share/llmp2p] [--listen-port 0]

API:
  GET /api/v1/status     daemon summary
  GET /api/v1/models     model ids in store
  GET /api/v1/torrents   swarm statuses`)
				return
			}
		}
	}

	st, err := store.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llmp2pd:", err)
		os.Exit(1)
	}

	// systemd sends SIGTERM; graceful shutdown must trigger on both.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, daemon.Options{
		Store:      st,
		ListenAddr: addr,
		Log:        slog.Default(),
		Version:    version,
		EngineOverrides: func(c *engine.Config) {
			c.ListenPort = listenPort
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, "llmp2pd:", err)
		os.Exit(1)
	}
}

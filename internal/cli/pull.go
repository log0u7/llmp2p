package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/hf"
	"github.com/log0u7/llmp2p/internal/pull"
	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/store"
)

type pullFlags struct {
	httpOnly   bool
	bootstrap  []string
	listenPort int
	grace      time.Duration
	token      string
	json       bool
}

func newPullCmd() *cobra.Command {
	var f pullFlags
	cmd := &cobra.Command{
		Use:   "pull <hf:owner/repo[@rev][#/path]>",
		Short: "Pull a model repository via P2P with Hub fallback",
		Args:  cobra.ExactArgs(1),
		Example: `  llmp2p pull hf:Qwen/Qwen3-Coder-30B-A3B-GGUF
  llmp2p pull hf:org/repo@1a2b3c#model-Q4_K_M.gguf
  llmp2p pull hf:org/repo --http-only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			st, err := store.Open(flagStoreDir)
			if err != nil {
				return err
			}
			token := f.token
			if token == "" {
				token = os.Getenv("HF_TOKEN")
			}
			hfc := hf.New()
			hfc.Token = token

			bootstraps := f.bootstrap
			if len(bootstraps) == 0 {
				bootstraps = []string{DefaultBootstrapURL}
			}

			if !f.json {
				fmt.Fprintf(os.Stderr, "pulling %s\n", r)
			}
			opts := pull.Options{
				Store:         st,
				HF:            hfc,
				BootstrapURLs: bootstraps,
				HTTPOnly:      f.httpOnly,
				P2PGrace:      f.grace,
				EngineCfg:     engine.Config{ListenPort: f.listenPort},
				Log:           slog.Default(),
			}
			var progressShown bool
			if !f.json {
				opts.OnProgress = progressPrinter(os.Stderr, &progressShown)
			}
			res, err := pull.Run(cmd.Context(), r, opts)
			if err != nil {
				return err
			}
			if progressShown {
				fmt.Fprintln(os.Stderr)
			}
			if f.json {
				return json.NewEncoder(os.Stdout).Encode(res)
			}
			mode := res.Mode
			fmt.Printf("pulled %s @%s via %s: %d files, %.1f MiB\n",
				res.Model, shortRev(res.Revision), mode, res.Files, float64(res.Size)/(1<<20))
			fmt.Printf("manifest %s\ninfohash %s\n", res.ManifestSHA256, res.InfoHash)
			if mode != pull.ModeCache {
				fmt.Printf("to keep sharing: llmp2p seed hf:%s\n", res.Model)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&f.httpOnly, "http-only", false, "skip P2P and download from the Hub directly")
	cmd.Flags().StringSliceVar(&f.bootstrap, "bootstrap", nil, "bootstrap index base URLs (default: project index)")
	cmd.Flags().IntVar(&f.listenPort, "listen-port", 0, "BitTorrent listen port (0: random)")
	cmd.Flags().DurationVar(&f.grace, "grace", pull.DefaultP2PGrace, "wait for swarm data before falling back to HTTP")
	cmd.Flags().StringVar(&f.token, "token", "", "Hugging Face access token (default: $HF_TOKEN)")
	cmd.Flags().BoolVar(&f.json, "json", false, "print machine-readable result")
	return cmd
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// progressPrinter renders a single self-rewriting stderr line per progress
// mode. The returned function is safe for repeated ticks; a newline must be
// printed by the caller when the operation completes.
func progressPrinter(w io.Writer, shown *bool) func(string, engine.Progress) {
	return func(mode string, p engine.Progress) {
		if p.Total <= 0 {
			return
		}
		var line string
		switch mode {
		case pull.ModeP2P:
			pct := 100 * p.Completed / p.Total
			line = fmt.Sprintf("swarm %3d%% | %s / %s | peers %d",
				pct, humanBytes(p.Completed), humanBytes(p.Total), p.Peers)
		case pull.ModeHTTP:
			line = fmt.Sprintf("http %d/%d files fetched", p.Completed, p.Total)
		default:
			return
		}
		fmt.Fprintf(w, "\r\033[K%s", line)
		*shown = true
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	switch {
	case n >= unit*unit*unit:
		return fmt.Sprintf("%.1f GiB", float64(n)/(unit*unit*unit))
	case n >= unit*unit:
		return fmt.Sprintf("%.1f MiB", float64(n)/(unit*unit))
	case n >= unit:
		return fmt.Sprintf("%.0f KiB", float64(n)/unit)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

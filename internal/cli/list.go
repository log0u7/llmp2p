package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/store"
)

// contextWithSignal cancels the returned context on SIGINT/SIGTERM.
func contextWithSignal(ctx context.Context) (context.Context, func()) {
	nctx, cancel := context.WithCancel(ctx)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(ch)
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return nctx, cancel
}

type modelInfo struct {
	Model    string `json:"model"`
	Revision string `json:"revision"`
	Files    int    `json:"files"`
	Size     int64  `json:"size"`
}

func newListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List models in the store",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(flagStoreDir)
			if err != nil {
				return err
			}
			models, err := st.Models()
			if err != nil {
				return err
			}
			infos := make([]modelInfo, 0, len(models))
			for _, id := range models {
				mfile, err := st.ModelManifestFile(id)
				if err != nil {
					continue
				}
				m, err := manifest.Load(mfile)
				if err != nil {
					continue
				}
				var size int64
				for _, f := range m.Files {
					size += f.Size
				}
				infos = append(infos, modelInfo{Model: id, Revision: m.Revision, Files: len(m.Files), Size: size})
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(infos)
			}
			for _, mi := range infos {
				fmt.Printf("%-40s @%-12s %3d files  %8.1f MiB\n",
					mi.Model, mi.Revision, mi.Files, float64(mi.Size)/(1<<20))
			}
			fmt.Fprintf(os.Stderr, "%d model(s)\n", len(infos))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

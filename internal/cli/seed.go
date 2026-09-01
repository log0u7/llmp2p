package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/store"
)

func newSeedCmd() *cobra.Command {
	var (
		listenPort int
		dataDir    string
	)
	cmd := &cobra.Command{
		Use:   "seed <hf:owner/repo|path/to.torrent>",
		Short: "Seed a stored model or a torrent file until interrupted",
		Args:  cobra.ExactArgs(1),
		Example: `  llmp2p seed hf:Qwen/Qwen3-Coder-30B-A3B-GGUF
  llmp2p seed ~/models/model-Q4_K_M.gguf.torrent --data-dir ~/models`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if daemonUp(DefaultDaemonURL) {
				fmt.Println("a daemon is already running and seeds the whole store")
				fmt.Printf("status: %s/api/v1/status\n", DefaultDaemonURL)
				return nil
			}
			arg := args[0]
			torrentPath, dataRoot, err := resolveSeedTarget(arg, dataDir)
			if err != nil {
				return err
			}
			eng, err := engine.New(engine.Config{
				DataDir:    dataRoot,
				Seed:       true,
				ListenPort: listenPort,
			}, nil)
			if err != nil {
				return err
			}
			defer func() { _ = eng.Close() }()

			seedCtx, cancel := contextWithSignal(cmd.Context())
			defer cancel()
			if err := eng.SeedTorrentFile(seedCtx, torrentPath); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "seeding %s (ctrl-c to stop)\n", torrentPath)

			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-seedCtx.Done():
					return nil
				case <-ticker.C:
					for _, st := range eng.TorrentStatuses() {
						fmt.Printf("%s peers=%d up=%d down=%d\n",
							st.Name, st.Peers, st.Uploaded, st.Downloaded)
					}
				}
			}
		},
	}
	cmd.Flags().IntVar(&listenPort, "listen-port", 0, "BitTorrent listen port (0: random)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "data directory when seeding a torrent file (default: cwd)")
	return cmd
}

// resolveSeedTarget maps the seed argument to a torrent path and the data
// directory the torrent data lives under.
func resolveSeedTarget(arg, dataDir string) (string, string, error) {
	if filepath.Ext(arg) == ".torrent" {
		if dataDir == "" {
			dataDir, _ = os.Getwd()
		}
		return arg, dataDir, nil
	}
	r, err := ref.Parse(arg)
	if err != nil {
		return "", "", fmt.Errorf("seed target must be an hf: reference or a .torrent path: %w", err)
	}
	st, err := store.Open(flagStoreDir)
	if err != nil {
		return "", "", err
	}
	m, err := loadModelManifest(st, r.ID())
	if err != nil {
		return "", "", err
	}
	tpath, err := st.TorrentPath(m.InfoHash)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(tpath); err != nil {
		return "", "", fmt.Errorf("seed: torrent %s missing: %w", m.InfoHash, err)
	}
	modelDir, err := st.ModelDir(r.ID())
	if err != nil {
		return "", "", err
	}
	return tpath, filepath.Dir(modelDir), nil
}

// loadModelManifest reads the manifest pointer stored inside a model dir.
func loadModelManifest(st *store.Store, modelID string) (*manifest.Manifest, error) {
	mfile, err := st.ModelManifestFile(modelID)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(mfile)
	if err != nil {
		return nil, fmt.Errorf("seed: model %s not found in store: %w", modelID, err)
	}
	return m, nil
}

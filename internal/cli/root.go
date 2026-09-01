// Package cli implements the llmp2p command line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/store"
)

// version is injected at build time with -ldflags.
var version = "0.0.0"

// DefaultBootstrapURL is the project-maintained index origin.
const DefaultBootstrapURL = "https://raw.githubusercontent.com/log0u7/llmp2p/main"

var flagStoreDir string

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "llmp2p",
		Short:   "P2P distribution of LLM model artifacts",
		Version: version,
		Long: `llmp2p pulls Hugging Face model repositories through a BitTorrent
swarm when peers exist, falling back to HTTPS from the Hub.
Content is verified with sha256 end to end.

Use llmp2pd to keep your models seeding in the background.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagStoreDir, "dir", store.DefaultDir(), "model store directory")
	root.AddCommand(
		newPullCmd(),
		newSeedCmd(),
		newListCmd(),
		newImportCmd(),
		newRemoveCmd(),
		newVerifyCmd(),
		newKeygenCmd(),
	)
	return root
}

// Execute runs the CLI and exits with a status code.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "llmp2p:", err)
		os.Exit(1)
	}
}

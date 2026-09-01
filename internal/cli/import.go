package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/ollama"
	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/store"
)

func newImportCmd() *cobra.Command {
	var (
		name string
		host string
		bin  string
	)
	cmd := &cobra.Command{
		Use:   "import <hf:owner/repo|path/to/model.gguf>",
		Short: "Import a stored GGUF model into Ollama",
		Example: `  llmp2p import hf:Qwen/Qwen3-Coder-30B-A3B-GGUF
  llmp2p import hf:org/repo --name qwen3:30b --host tcp://localhost:11434`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := args[0]
			var gguf, modelID string
			if strings.EqualFold(filepath.Ext(arg), ".gguf") {
				gguf = arg
			} else {
				id := arg
				if r, err := ref.Parse(arg); err == nil {
					id = r.ID()
				} else if !ref.ValidModelID(arg) {
					return fmt.Errorf("import target must be an hf: reference or a .gguf path")
				}
				st, err := store.Open(flagStoreDir)
				if err != nil {
					return err
				}
				dir, err := st.ModelDir(id)
				if err != nil {
					return err
				}
				modelID = id
				gguf, err = ollama.FindGGUF(dir)
				if err != nil {
					return err
				}
			}
			if name == "" {
				label := modelID
				if label == "" {
					label = strings.TrimSuffix(filepath.Base(gguf), filepath.Ext(gguf))
				}
				name = ollama.DefaultName(label)
			}
			if err := ollama.Create(cmd.Context(), bin, gguf, name, host, os.Stdout); err != nil {
				return err
			}
			fmt.Printf("imported as %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Ollama model name (default: repo basename)")
	cmd.Flags().StringVar(&host, "host", "", "Ollama host (OLLAMA_HOST)")
	cmd.Flags().StringVar(&bin, "ollama", ollama.Bin, "ollama binary path")
	return cmd
}

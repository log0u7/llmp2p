package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/store"
)

func newVerifyCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "verify <hf:owner/repo>",
		Short: "Re-verify the integrity of a stored model",
		Args:  cobra.ExactArgs(1),
		Example: `  llmp2p verify hf:Qwen/Qwen2.5-0.5B-Instruct-GGUF
  llmp2p verify hf:org/repo --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			if r.Path != "" {
				return fmt.Errorf("verify operates on whole models, not single artifacts")
			}
			st, err := store.Open(flagStoreDir)
			if err != nil {
				return err
			}
			m, err := loadModelManifest(st, r.ID())
			if err != nil {
				return err
			}
			modelDir, err := st.ModelDir(r.ID())
			if err != nil {
				return err
			}

			var size int64
			for _, f := range m.Files {
				size += f.Size
			}
			verifyErr := m.VerifyDir(modelDir)
			if asJSON {
				out := struct {
					Model    string `json:"model"`
					Revision string `json:"revision"`
					Files    int    `json:"files"`
					Size     int64  `json:"size"`
					OK       bool   `json:"ok"`
					Error    string `json:"error,omitempty"`
				}{r.ID(), m.Revision, len(m.Files), size, verifyErr == nil, ""}
				if verifyErr != nil {
					out.Error = verifyErr.Error()
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
			} else if verifyErr != nil {
				return fmt.Errorf("verify %s: %w", r.ID(), verifyErr)
			} else {
				fmt.Printf("ok %s @%s: %d files, %.1f MiB verified\n",
					r.ID(), m.Revision, len(m.Files), float64(size)/(1<<20))
			}
			return verifyErr
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable result")
	return cmd
}

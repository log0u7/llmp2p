package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/store"
)

func newRemoveCmd() *cobra.Command {
	var (
		yes    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "remove <hf:owner/repo>",
		Short: "Remove a model from the store (files, torrent, manifest, index entry)",
		Args:  cobra.ExactArgs(1),
		Example: `  llmp2p remove hf:Qwen/Qwen2.5-0.5B-Instruct-GGUF
  llmp2p remove hf:org/repo --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := ref.Parse(args[0])
			if err != nil {
				return err
			}
			if r.Path != "" {
				return fmt.Errorf("remove operates on whole models, not single artifacts")
			}
			st, err := store.Open(flagStoreDir)
			if err != nil {
				return err
			}

			if !asJSON && !yes {
				if m, merr := loadModelManifest(st, r.ID()); merr == nil {
					var size int64
					for _, f := range m.Files {
						size += f.Size
					}
					fmt.Fprintf(os.Stderr, "remove %s (%d files, %.1f MiB)? [y/N] ",
						r.ID(), len(m.Files), float64(size)/(1<<20))
					line, rerr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if (rerr != nil && line == "") ||
						!strings.Contains(strings.ToLower(strings.TrimSpace(line)), "y") {
						return fmt.Errorf("aborted")
					}
				}
			}

			summary, err := st.Remove(r.ID())
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(summary)
			}
			var bits []string
			if summary.DirRemoved {
				bits = append(bits, "files")
			}
			if summary.TorrentRemoved {
				bits = append(bits, "torrent")
			}
			if summary.ManifestRemoved {
				bits = append(bits, "manifest")
			}
			if summary.IndexEntryRemoved {
				bits = append(bits, "index entry")
			}
			out := fmt.Sprintf("removed %s: %d files (%.1f MiB)",
				summary.Model, summary.Files, float64(summary.Size)/(1<<20))
			if len(bits) > 0 {
				out += ": " + strings.Join(bits, ", ")
			}
			fmt.Println(out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable summary")
	return cmd
}

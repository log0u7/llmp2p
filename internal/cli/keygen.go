package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/log0u7/llmp2p/internal/signing"
	"github.com/log0u7/llmp2p/internal/store"
)

func newKeygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Generate the publisher key used to sign manifests",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(flagStoreDir)
			if err != nil {
				return err
			}
			keyPath := filepath.Join(st.Root(), signing.DefaultKeyFile)
			priv, created, err := signing.LoadOrCreate(keyPath)
			if err != nil {
				return err
			}
			if created {
				fmt.Printf("publisher key created: %s\n", keyPath)
			} else {
				fmt.Printf("publisher key already exists: %s\n", keyPath)
			}
			fmt.Printf("public key: %s\n", signing.PublicKeyHex(priv))
			return nil
		},
	}
}

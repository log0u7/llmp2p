//go:build !windows

package cli

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestContextWithSignalCancel pins the seed signal contract: SIGINT and
// SIGTERM cancel the returned context (seeding stops cleanly). Separate
// !windows file: syscall.Kill does not exist there (the runtime.GOOS
// skip is not enough).
func TestContextWithSignalCancel(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, cancel := contextWithSignal(context.Background())
			defer cancel()
			if err := syscall.Kill(os.Getpid(), sig.(syscall.Signal)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("context not canceled by %s", sig)
			}
		})
	}
}

// Package version holds the binary version, injected at build time
// with -ldflags "-X github.com/log0u7/llmp2p/internal/version.Version=...".
package version

// Version is the release version; dev builds report the default.
var Version = "0.0.0"

// Package buildinfo holds build metadata injected via -ldflags -X.
package buildinfo

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func IsDevelopment() bool { return Version == "" || Version == "dev" }

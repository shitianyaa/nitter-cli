// Package buildinfo holds build metadata injected via -ldflags -X.
package buildinfo

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
	// Repo is the GitHub slug of this project's repository — the query
	// target of `twitter update --check`. Overridable via
	// -ldflags -X so forks point their builds at their own releases.
	Repo = "shitianyaa/twitter-cli"
)

func IsDevelopment() bool { return Version == "" || Version == "dev" }

package version

// Version is the provider version. It is injected at release build time via
// -ldflags and defaults to "dev" for local and CI builds.
var Version = "dev"

// String returns the provider version, falling back to "dev" when Version has
// been blanked (for example, by an incomplete release -ldflags invocation).
func String() string {
	if Version == "" {
		return "dev"
	}
	return Version
}

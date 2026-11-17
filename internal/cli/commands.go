package cli

// commands lists every subcommand the dispatcher knows. Each feature adds its
// Command here; the dispatcher itself does not change.
var commands = []Command{
	versionCommand,
}

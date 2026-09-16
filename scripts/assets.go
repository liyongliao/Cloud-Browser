package scriptassets

import "embed"

// Files contains the firewall program installed by the standalone manager.
//
//go:embed firewall.sh
var Files embed.FS

package browserassets

import "embed"

// Files contains the host isolation files required by the standalone manager.
//
//go:embed apparmor.profile chrome-seccomp.json
var Files embed.FS

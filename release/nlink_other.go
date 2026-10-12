//go:build !unix

package release

import "os"

// Go has no portable link-count API. A platform that cannot prove the
// single-link invariant refuses regular inputs instead of weakening it.
func hasMultipleLinks(info os.FileInfo) bool { return true }

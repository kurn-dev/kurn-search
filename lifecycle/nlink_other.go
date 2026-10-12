//go:build !unix

package lifecycle

import "os"

// Refuse where a single-link invariant cannot be proved portably.
func hasMultipleLinks(info os.FileInfo) bool { return true }

//go:build !unix

package fleet

import "os"

func hasMultipleLinks(info os.FileInfo) bool { return true }

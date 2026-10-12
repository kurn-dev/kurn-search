// Command kurn-search-local prepares the checked private local example.
package main

import (
	"os"

	"github.com/kurn-dev/kurn-search/localsetup"
	"github.com/kurn-dev/kurn-search/localsetup/preparedsetup"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "prepare-artifact" || os.Args[1] == "verify-version") {
		os.Exit(preparedsetup.Run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(localsetup.Run(os.Args[1:], os.Stdout, os.Stderr))
}

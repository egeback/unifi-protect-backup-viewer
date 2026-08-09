// Command hashpw prints a bcrypt hash for AUTH_PASSWORD_HASH, so operators
// don't need a separate htpasswd/python toolchain to set up login.
//
// Usage: hashpw <password>
package main

import (
	"fmt"
	"os"

	"github.com/egeback/unifi-protect-backup-viewer/internal/auth"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hashpw <password>")
		os.Exit(1)
	}
	hash, err := auth.HashPassword(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(hash)
}

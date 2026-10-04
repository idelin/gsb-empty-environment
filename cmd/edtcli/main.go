// edtcli serves the edtgrid JSON command protocol on stdin/stdout.
package main

import (
	"fmt"
	"os"

	"edtgrid/cli"
)

func main() {
	if err := cli.Run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "edtcli:", err)
		os.Exit(1)
	}
}

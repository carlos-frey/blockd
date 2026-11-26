//go:build !windows

// blockd-setup is the Windows installer; there is nothing to do elsewhere.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "blockd-setup é o instalador do Windows; no Linux use `make install`")
	os.Exit(1)
}

//go:build !windows

package cli

import "os"

func enableVT(*os.File) bool { return true }

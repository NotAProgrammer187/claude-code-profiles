//go:build !windows

package main

import (
	"context"
	"os/exec"
)

// batchCommand only means something on Windows; elsewhere claude is a real
// executable and this is never reached, but the symbol must still compile.
func batchCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}

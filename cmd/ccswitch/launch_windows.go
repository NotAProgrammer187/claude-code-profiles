//go:build windows

package main

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
)

// batchCommand runs a .cmd/.bat through cmd.exe — CreateProcess cannot execute
// batch files directly. Letting exec.Command quote the arguments is not enough:
// cmd.exe parses its command line by its own rules, and with `/c "path" args...`
// it strips the first and last quote on the line, so a spaced install path plus
// one quoted argument breaks the launch. The `/s /c "..."` idiom hands cmd.exe
// the whole payload in one outer pair of quotes, which /s makes it strip
// verbatim. (Literal `"` or `%` inside an argument still degrades — that is
// batch's own limit, not a quoting bug here.)
func batchCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	parts := []string{syscall.EscapeArg(bin)}
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /s /c "` + strings.Join(parts, " ") + `"`,
	}
	return cmd
}

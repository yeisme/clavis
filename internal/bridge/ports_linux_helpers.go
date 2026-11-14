//go:build linux

package bridge

import (
	"context"
	"fmt"
	"github.com/yeisme/clavis/pkg/core/host"
	"os/exec"
	"syscall"
)

func listUnixPorts(ctx context.Context) ([]host.ListeningPort, error) {
	out, e := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN").Output()
	if e != nil {
		return nil, e
	}
	return ParseLsofListeningPorts(string(out)), nil
}
func killUnix(ctx context.Context, pid int, force bool) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}

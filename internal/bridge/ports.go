package bridge

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/yeisme/clavis/pkg/core/host"
)

// ParseLsofListeningPorts parses lsof -nP -iTCP -sTCP:LISTEN output.
func ParseLsofListeningPorts(out string) []host.ListeningPort {
	var result []host.ListeningPort
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 9 {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		address := f[8]
		colon := strings.LastIndex(address, ":")
		if colon < 0 {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSuffix(address[colon+1:], ")"))
		if err != nil {
			continue
		}
		result = append(result, host.ListeningPort{Port: port, Proto: "tcp", PID: pid, ProcessName: f[0], Command: strings.Join(f[8:], " ")})
	}
	return result
}

// ParseNetstatListeningPorts parses netstat -ano output and resolves optional names from tasklist.
func ParseNetstatListeningPorts(out, tasklist string) []host.ListeningPort {
	names := make(map[int]string)
	for _, line := range strings.Split(tasklist, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			if p, e := strconv.Atoi(f[1]); e == nil {
				names[p] = f[0]
			}
		}
	}
	var result []host.ListeningPort
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.EqualFold(f[0], "TCP") {
			continue
		}
		if !strings.EqualFold(f[len(f)-2], "LISTENING") {
			continue
		}
		pid, e := strconv.Atoi(f[len(f)-1])
		if e != nil {
			continue
		}
		addr := f[1]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		port, e := strconv.Atoi(strings.Trim(addr[i+1:], "[]"))
		if e != nil {
			continue
		}
		result = append(result, host.ListeningPort{Port: port, Proto: "tcp", PID: pid, ProcessName: names[pid], Command: strings.Join(f, " ")})
	}
	return result
}

func unsupported(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return errors.ErrUnsupported
}

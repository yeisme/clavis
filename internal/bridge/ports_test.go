package bridge

import "testing"

func TestParseListeningPorts(t *testing.T) {
	lsof := "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\nclavis 123 me 7u IPv4 0x0 0t0 TCP 127.0.0.1:8080 (LISTEN)\n"
	got := ParseLsofListeningPorts(lsof)
	if len(got) != 1 || got[0].Port != 8080 || got[0].PID != 123 {
		t.Fatalf("unexpected lsof result: %+v", got)
	}
	netstat := "Active Connections\n\n  Proto  Local Address          Foreign Address        State           PID\n  TCP    0.0.0.0:3000         0.0.0.0:0              LISTENING       77\n"
	tasks := "Image Name                     PID Session Name        Session#    Mem Usage\napp.exe                      77 Console                    1     1,000 K\n"
	got = ParseNetstatListeningPorts(netstat, tasks)
	if len(got) != 1 || got[0].Port != 3000 || got[0].PID != 77 || got[0].ProcessName != "app.exe" {
		t.Fatalf("unexpected netstat result: %+v", got)
	}
}

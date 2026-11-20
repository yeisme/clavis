package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentPlistKeepsAliveOnlyOnBadExit(t *testing.T) {
	p := launchAgentPlist("/Applications/Clavis.app/Contents/MacOS/Clavis", "/tmp/x.log")
	for _, want := range []string{
		"<string>" + LaunchAgentLabel + "</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>SuccessfulExit</key><false/>",
		"/Applications/Clavis.app/Contents/MacOS/Clavis",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("plist missing %q:\n%s", want, p)
		}
	}
	// A deliberate Quit must not be undone: that is what SuccessfulExit=false encodes.
	if strings.Contains(p, "KeepAlive</key><true/>") {
		t.Fatal("KeepAlive must be conditional, not unconditional")
	}
}

func TestLaunchAgentPathIsUnderUserLaunchAgents(t *testing.T) {
	path, err := LaunchAgentPath()
	if err != nil {
		t.Skip("no home directory")
	}
	if !strings.HasSuffix(path, filepath.Join("Library", "LaunchAgents", LaunchAgentLabel+".plist")) {
		t.Fatalf("unexpected path %q", path)
	}
	dir, _ := LaunchAgentDir()
	if _, statErr := os.Stat(dir); statErr != nil && !os.IsNotExist(statErr) {
		t.Fatalf("LaunchAgents dir: %v", statErr)
	}
}

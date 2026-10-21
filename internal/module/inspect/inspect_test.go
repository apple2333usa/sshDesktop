package inspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOtherPanels(t *testing.T) {
	root := t.TempDir()
	if p := otherPanels(root); len(p) != 0 {
		t.Fatalf("%v", p)
	}
	for _, f := range []string{"/usr/local/bin/1pctl", "/etc/systemd/system/1panel-core.service"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(root, f), nil, 0o644)
	}
	if p := otherPanels(root); strings.Join(p, ",") != "1Panel" {
		t.Fatalf("%v", p)
	}
}

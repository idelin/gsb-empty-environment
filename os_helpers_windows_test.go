//go:build windows

package netsec

import (
	"os"
	"strings"
	"testing"
)

func joinPath(parts ...string) string {
	return strings.Join(parts, `\`)
}

func createFile(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(name, nil, 0o600); err != nil {
		t.Fatalf("create %q: %v", name, err)
	}
}

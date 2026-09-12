package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkAuditQueryTypesVersionAsBigInt(t *testing.T) {
	path := filepath.Join("store_postgres.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(source), "'version', $8::bigint") {
		t.Fatal("network audit query must cast its version parameter to bigint")
	}
}

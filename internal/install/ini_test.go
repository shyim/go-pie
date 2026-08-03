package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reinstalling an extension that is not currently loaded must not stack a
// second copy of the same block in a shared php.ini.
func TestAppendIniReplacingBlockDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(target, []byte("; hand written\nextension=gd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	block := GpieMarkerPrefix + "phpredis/phpredis extension\n; priority=20\nextension=redis\n"

	for range 3 {
		if err := appendIniReplacingBlock(t.Context(), target, block, "redis"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), "extension=redis"); n != 1 {
		t.Errorf("extension=redis appears %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(string(got), "extension=gd") {
		t.Errorf("pre-existing content was lost:\n%s", got)
	}
}

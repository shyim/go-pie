package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shyim/go-pie/internal/resolver"
)

func TestIsDedicatedIniFile(t *testing.T) {
	m := &ManagedExtension{ExtensionName: "redis", Priority: 20}
	cases := []struct {
		path string
		want bool
	}{
		{"/etc/php/conf.d/20-redis.ini", true},
		{"/etc/php/conf.d/20-REDIS.INI", true},
		{"/etc/php/conf.d/05-redis.ini", false},
		{"/etc/php/php.ini", false},
		{"20-redis.ini", true},
	}
	for _, c := range cases {
		if got := isDedicatedIniFile(c.path, m); got != c.want {
			t.Errorf("isDedicatedIniFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	// Zero-padded to at least 2 digits; wider numbers unpadded.
	m100 := &ManagedExtension{ExtensionName: "redis", Priority: 100}
	if !isDedicatedIniFile("100-redis.ini", m100) {
		t.Error("priority 100 filename should match")
	}
	m5 := &ManagedExtension{ExtensionName: "redis", Priority: 5}
	if !isDedicatedIniFile("05-redis.ini", m5) {
		t.Error("priority 5 filename should be zero-padded to 05")
	}
}

func TestWithExtension(t *testing.T) {
	cases := []struct{ in, ext, want string }{
		{"foo.dll", "pdb", "foo.pdb"},
		{"/a/b/php_redis.dll", "pdb", "/a/b/php_redis.pdb"},
		{"noext", "pdb", "noext.pdb"},
	}
	for _, c := range cases {
		if got := withExtension(c.in, c.ext); got != c.want {
			t.Errorf("withExtension(%q, %q) = %q, want %q", c.in, c.ext, got, c.want)
		}
	}
}

func TestIniContents(t *testing.T) {
	pkg := &resolver.ResolvedPackage{
		Name:          "phpredis/phpredis",
		Version:       "6.1.0",
		ExtensionName: "redis",
		Priority:      20,
	}
	got := iniContents(pkg, "extension")
	want := "\n; rpie automatically added this to enable the phpredis/phpredis extension\n" +
		"; priority=20\n; version=6.1.0\nextension=redis\n"
	if got != want {
		t.Errorf("iniContents mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestManagedKeysSorted(t *testing.T) {
	m := map[string]ManagedExtension{
		"redis":  {ExtensionName: "redis"},
		"xdebug": {ExtensionName: "xdebug"},
		"apcu":   {ExtensionName: "apcu"},
	}
	keys := ManagedKeys(m)
	want := []string{"apcu", "redis", "xdebug"}
	if len(keys) != len(want) {
		t.Fatalf("got %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], want[i])
		}
	}
}

func TestRustLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a\nb\n", []string{"a", "b"}},
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb\r\n", []string{"a", "b"}},
		{"\n", []string{""}},
	}
	for _, c := range cases {
		got := rustLines(c.in)
		if len(got) != len(c.want) {
			t.Errorf("rustLines(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("rustLines(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestWriteFilePlain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.ini")
	if err := writeFile(path, "hello\n"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Errorf("content = %q", string(data))
	}
	// Truncating: a second write replaces, not appends.
	if err := writeFile(path, "bye\n"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "bye\n" {
		t.Errorf("content after rewrite = %q", string(data))
	}
}

func TestCopyContentsPreservesMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(src, []byte("data"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := copyContents(src, dst); err != nil {
		t.Fatalf("copyContents: %v", err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "data" {
		t.Errorf("content = %q", string(data))
	}
	info, _ := os.Stat(dst)
	if info.Mode().Perm() != 0640 {
		t.Errorf("mode = %o, want 0640", info.Mode().Perm())
	}
}

func TestWalkFilesBoundedDepth(t *testing.T) {
	dir := t.TempDir()
	// depth 0: top-level file (collected)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	// depth 1
	os.MkdirAll(filepath.Join(dir, "d1"), 0755)
	os.WriteFile(filepath.Join(dir, "d1", "b.txt"), []byte("b"), 0644)
	// depth 2
	os.MkdirAll(filepath.Join(dir, "d1", "d2"), 0755)
	os.WriteFile(filepath.Join(dir, "d1", "d2", "c.txt"), []byte("c"), 0644)
	// depth 3 (beyond bound: not collected)
	os.MkdirAll(filepath.Join(dir, "d1", "d2", "d3"), 0755)
	os.WriteFile(filepath.Join(dir, "d1", "d2", "d3", "d.txt"), []byte("d"), 0644)

	got := walkFiles(dir)
	names := map[string]bool{}
	for _, g := range got {
		names[filepath.Base(g)] = true
	}
	for _, want := range []string{"a.txt", "b.txt", "c.txt"} {
		if !names[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if names["d.txt"] {
		t.Errorf("d.txt should be beyond depth bound: %v", got)
	}
}

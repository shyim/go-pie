// Package install performs the final pipeline stage: place the compiled
// artifact into PHP's extension_dir and enable it via an INI marker snippet.
// It mirrors PIE's Php\Pie\Installing. The INI marker comments it writes ARE
// the managed-extension state store (there is no separate metadata file).
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shyim/go-pie/internal/buildpkg"
	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/procutil"
	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/sudo"
)

// InstallOutcome reports where the .so/.dll landed and what happened to the INI.
type InstallOutcome struct {
	InstalledSo string
	Ini         IniOutcome
}

// UninstallOutcome reports what an uninstall did. RemovedSo is "" when no .so
// existed.
type UninstallOutcome struct {
	RemovedSo         string
	RemovedIniFiles   []string
	RewrittenIniFiles []string
}

// Install copies the built .so into the PHP extension dir and (optionally)
// enables it via INI, escalating to sudo when the extension dir is not writable.
func Install(pkg *resolver.ResolvedPackage, built *buildpkg.BuiltExtension,
	plat *platform.TargetPlatform, setupIniFlag bool) (*InstallOutcome, error) {
	target := plat.PHP.SharedObjectPath(pkg.ExtensionName)

	useSudo := sudo.PathNeedsSudo(target) && sudo.IsAvailable()
	if err := copySo(built.BinaryPath, target, useSudo); err != nil {
		return nil, err
	}

	ini := IniOutcome{Kind: IniSkipped}
	if setupIniFlag {
		out, err := setupIni(pkg, plat)
		if err != nil {
			return nil, err
		}
		ini = out
	}

	return &InstallOutcome{InstalledSo: target, Ini: ini}, nil
}

// InstallWindows installs a pre-compiled Windows extension: copy the extension
// DLL into the PHP extension dir, its .pdb alongside if present, and any
// dependency DLLs next to php.exe, then enable it via INI. No sudo logic.
func InstallWindows(pkg *resolver.ResolvedPackage, dll, extractedDir string,
	plat *platform.TargetPlatform, setupIniFlag bool) (*InstallOutcome, error) {
	target := plat.PHP.DllPath(pkg.ExtensionName)
	if err := copyContents(dll, target); err != nil {
		return nil, fmt.Errorf("copying %s -> %s: %w", dll, target, err)
	}

	// Copy the matching .pdb (debug symbols) if the package ships one.
	pdbSrc := withExtension(dll, "pdb")
	if _, err := os.Stat(pdbSrc); err == nil {
		pdbTarget := withExtension(target, "pdb")
		_ = copyContents(pdbSrc, pdbTarget)
	}

	// Copy dependency DLLs (everything in the archive that is not the extension
	// DLL/PDB itself) next to php.exe.
	if phpDir, ok := plat.PHP.PhpDir(); ok {
		dllName := filepath.Base(dll)
		for _, entry := range walkFiles(extractedDir) {
			isSelf := filepath.Base(entry) == dllName
			isDll := strings.EqualFold(strings.TrimPrefix(filepath.Ext(entry), "."), "dll")
			if isDll && !isSelf {
				dest := filepath.Join(phpDir, filepath.Base(entry))
				if _, err := os.Stat(dest); err != nil {
					_ = copyContents(entry, dest)
				}
			}
		}
	}

	ini := IniOutcome{Kind: IniSkipped}
	if setupIniFlag {
		out, err := setupIni(pkg, plat)
		if err != nil {
			return nil, err
		}
		ini = out
	}

	return &InstallOutcome{InstalledSo: target, Ini: ini}, nil
}

// Uninstall disables and removes an rpie-managed extension: delete its .so from
// the extension dir and strip its enabling INI marker (deleting the INI file if
// it becomes effectively empty and was a dedicated file). managed comes from
// DiscoverManaged.
func Uninstall(managed *ManagedExtension, plat *platform.TargetPlatform) (*UninstallOutcome, error) {
	outcome := &UninstallOutcome{}

	so := plat.PHP.SharedObjectPath(managed.ExtensionName)
	if _, err := os.Stat(so); err == nil {
		if err := removeFile(so); err != nil {
			return nil, fmt.Errorf("removing %s: %w", so, err)
		}
		outcome.RemovedSo = so
	}

	iniPath := managed.IniFile
	contentsBytes, err := os.ReadFile(iniPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", iniPath, err)
	}
	contents := string(contentsBytes)
	rewritten := RemoveMarkerBlock(contents, managed.ExtensionName)

	if IsEffectivelyEmpty(rewritten) && isDedicatedIniFile(iniPath, managed) {
		if err := removeFile(iniPath); err != nil {
			return nil, fmt.Errorf("removing %s: %w", iniPath, err)
		}
		outcome.RemovedIniFiles = append(outcome.RemovedIniFiles, iniPath)
	} else if rewritten != contents {
		if err := writeFile(iniPath, rewritten); err != nil {
			return nil, fmt.Errorf("rewriting %s: %w", iniPath, err)
		}
		outcome.RewrittenIniFiles = append(outcome.RewrittenIniFiles, iniPath)
	}

	return outcome, nil
}

// isDedicatedIniFile reports whether the file's basename equals
// "<%02d-priority>-<ext>.ini" case-insensitively — a file rpie created solely
// for this extension, safe to delete wholesale.
func isDedicatedIniFile(iniPath string, managed *ManagedExtension) bool {
	name := filepath.Base(iniPath)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return false
	}
	expected := fmt.Sprintf("%02d-%s.ini", managed.Priority, managed.ExtensionName)
	return strings.EqualFold(name, expected)
}

// removeFile deletes a file, escalating to sudo when it is not directly writable.
func removeFile(path string) error {
	if sudo.PathNeedsSudo(path) && sudo.IsAvailable() {
		return procutil.Run("sudo", []string{"rm", "-f", path}, ".", "removing file (sudo)")
	}
	return os.Remove(path)
}

// writeFile writes contents to path (truncating), escalating to sudo when the
// path is not writable.
func writeFile(path, contents string) error {
	if !sudo.PathNeedsSudo(path) {
		return os.WriteFile(path, []byte(contents), 0644)
	}
	if !sudo.IsAvailable() {
		return fmt.Errorf(
			"cannot rewrite %s (needs elevated privileges and sudo is unavailable / non-interactive)",
			path)
	}
	tmp, err := os.CreateTemp("", "")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(contents); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	tmp.Close()
	return procutil.Run("sudo", []string{"cp", tmpPath, path}, ".", "rewriting INI (sudo)")
}

// copySo copies the built .so into place, via `sudo cp -f` when use_sudo is set.
func copySo(from, to string, useSudo bool) error {
	if useSudo {
		return procutil.Run("sudo", []string{"cp", "-f", from, to}, ".", "copying extension into place (sudo)")
	}
	if err := copyContents(from, to); err != nil {
		return fmt.Errorf("copying %s -> %s: %w", from, to, err)
	}
	return nil
}

// copyContents mirrors std::fs::copy: copy file contents AND the source's
// permission bits, overwriting the destination.
func copyContents(from, to string) error {
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to, data, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chmod(to, info.Mode().Perm())
}

// withExtension replaces the final path extension with ext (no leading dot),
// mirroring Rust Path::with_extension.
func withExtension(path, ext string) string {
	base := path[:len(path)-len(filepath.Ext(path))]
	return base + "." + ext
}

// walkFiles collects regular files under dir with bounded recursion (initial
// depth 3), silently tolerating unreadable directories.
func walkFiles(dir string) []string {
	var out []string
	collectFiles(dir, 3, &out)
	return out
}

func collectFiles(dir string, depth int, out *[]string) {
	if depth == 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		info, err := os.Stat(p) // Stat follows symlinks, matching Rust is_dir/is_file.
		if err != nil {
			continue
		}
		if info.IsDir() {
			collectFiles(p, depth-1, out)
		} else if info.Mode().IsRegular() {
			*out = append(*out, p)
		}
	}
}

// Package download downloads and extracts a resolved package's artifact,
// mirroring PIE's Php\Pie\Downloading namespace.
package download

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/style"
	"github.com/shyim/go-pie/internal/version"
)

var httpClient = &http.Client{}

// VerifyPolicy controls how a missing checksum is handled. A checksum mismatch
// is always fatal regardless of policy.
type VerifyPolicy int

const (
	VerifyWarn VerifyPolicy = iota
	VerifyEnforce
	VerifyAttest
	VerifySkip
)

// ArtifactKind discriminates the three artifact variants.
type ArtifactKind int

const (
	ArtifactSource ArtifactKind = iota
	ArtifactBinary
	ArtifactWindowsDll
)

// Artifact is what a download produced.
type Artifact struct {
	Kind         ArtifactKind
	Path         string
	ExtractedDir string
}

// DownloadedPackage is a package whose artifact has been downloaded and
// extracted to disk.
type DownloadedPackage struct {
	Artifact Artifact
	root     string
	keep     bool
}

// SourcePath returns the buildable source directory, if this is a source download.
func (d *DownloadedPackage) SourcePath() (string, bool) {
	if d.Artifact.Kind == ArtifactSource {
		return d.Artifact.Path, true
	}
	return "", false
}

// BinaryPath returns the pre-built Unix .so path, if this is a Unix binary download.
func (d *DownloadedPackage) BinaryPath() (string, bool) {
	if d.Artifact.Kind == ArtifactBinary {
		return d.Artifact.Path, true
	}
	return "", false
}

// WindowsDll returns the Windows DLL and its extraction dir, if this is a
// Windows download.
func (d *DownloadedPackage) WindowsDll() (dll, extractedDir string, ok bool) {
	if d.Artifact.Kind == ArtifactWindowsDll {
		return d.Artifact.Path, d.Artifact.ExtractedDir, true
	}
	return "", "", false
}

// Close removes the extraction temp dir (no-op after Keep).
func (d *DownloadedPackage) Close() error {
	if d.keep || d.root == "" {
		return nil
	}
	return os.RemoveAll(d.root)
}

// Keep disables cleanup, the Go equivalent of std::mem::forget on the temp dir.
func (d *DownloadedPackage) Keep() {
	d.keep = true
}

// Download downloads the resolved package honouring its declared download-method
// preference, returning the extracted artifact.
func Download(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform, policy VerifyPolicy) (*DownloadedPackage, error) {
	var errs []string

	var methods []resolver.DownloadUrlMethod
	if plat.OS == platform.OSWindows {
		methods = []resolver.DownloadUrlMethod{resolver.WindowsBinary}
	} else {
		methods = append(methods, pkg.Metadata.DownloadUrlMethods...)
	}

	for _, method := range methods {
		downloaded, err := tryDownloadWith(pkg, plat, method, policy)
		if err == nil {
			return downloaded, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %s", method.String(), err.Error()))
	}

	return nil, fmt.Errorf("could not download `%s:%s` by any declared method:\n  %s",
		pkg.Name, pkg.Version, strings.Join(errs, "\n  "))
}

func tryDownloadWith(pkg *resolver.ResolvedPackage, plat *platform.TargetPlatform, method resolver.DownloadUrlMethod, policy VerifyPolicy) (*DownloadedPackage, error) {
	switch method {
	case resolver.ComposerDefault:
		if pkg.DistURL == nil {
			return nil, fmt.Errorf("`%s:%s` has no dist archive", pkg.Name, pkg.Version)
		}
		url := *pkg.DistURL
		distType := "zip"
		if pkg.DistType != nil {
			distType = *pkg.DistType
		}
		b, err := fetch(url)
		if err != nil {
			return nil, err
		}
		expected := ExpectedFromPackagistShasum(pkg.DistShasum)
		if err := applyVerification(b, expected, policy, url, method, "", ""); err != nil {
			return nil, err
		}
		root, err := extract(b, distType, url)
		if err != nil {
			return nil, err
		}
		source, err := locateSourceDir(root, pkg)
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		return &DownloadedPackage{
			Artifact: Artifact{Kind: ArtifactSource, Path: source},
			root:     root,
		}, nil

	case resolver.PrePackagedSource:
		names := sourceAssetNames(pkg)
		asset, err := findReleaseAsset(pkg, names)
		if err != nil {
			return nil, err
		}
		distType := distTypeFromURL(asset.DownloadURL)
		b, err := fetch(asset.DownloadURL)
		if err != nil {
			return nil, err
		}
		expected := ExpectedFromGithubDigest(asset.Digest)
		if err := applyVerification(b, expected, policy, asset.DownloadURL, method, "", ""); err != nil {
			return nil, err
		}
		root, err := extract(b, distType, asset.DownloadURL)
		if err != nil {
			return nil, err
		}
		source, err := locateSourceDir(root, pkg)
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		return &DownloadedPackage{
			Artifact: Artifact{Kind: ArtifactSource, Path: source},
			root:     root,
		}, nil

	case resolver.PrePackagedBinary:
		names := binaryAssetNames(pkg, plat)
		asset, err := findReleaseAsset(pkg, names)
		if err != nil {
			return nil, err
		}
		distType := distTypeFromURL(asset.DownloadURL)
		b, err := fetch(asset.DownloadURL)
		if err != nil {
			return nil, err
		}
		expected := ExpectedFromGithubDigest(asset.Digest)
		root, err := extract(b, distType, asset.DownloadURL)
		if err != nil {
			return nil, err
		}
		attestFile, attestRepo := stashAttestAsset(root, b, policy, asset.Repo)
		if err := applyVerification(b, expected, policy, asset.DownloadURL, method, attestFile, attestRepo); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		if attestFile != "" {
			_ = os.Remove(attestFile)
		}
		binary, err := locateSharedObject(root, pkg.ExtensionName)
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		return &DownloadedPackage{
			Artifact: Artifact{Kind: ArtifactBinary, Path: binary},
			root:     root,
		}, nil

	case resolver.WindowsBinary:
		names := windowsAssetNames(pkg, plat)
		if len(names) == 0 {
			return nil, fmt.Errorf("could not determine the Windows build tag (compiler) for the target PHP; is this a Windows PHP build?")
		}
		asset, err := findReleaseAsset(pkg, names)
		if err != nil {
			return nil, err
		}
		b, err := fetch(asset.DownloadURL)
		if err != nil {
			return nil, err
		}
		expected := ExpectedFromGithubDigest(asset.Digest)
		root, err := extract(b, "zip", asset.DownloadURL)
		if err != nil {
			return nil, err
		}
		attestFile, attestRepo := stashAttestAsset(root, b, policy, asset.Repo)
		if err := applyVerification(b, expected, policy, asset.DownloadURL, method, attestFile, attestRepo); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		if attestFile != "" {
			_ = os.Remove(attestFile)
		}
		dll, err := locateWindowsDll(root, pkg.ExtensionName)
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		extractedDir := filepath.Dir(dll)
		if extractedDir == "" {
			extractedDir = root
		}
		return &DownloadedPackage{
			Artifact: Artifact{Kind: ArtifactWindowsDll, Path: dll, ExtractedDir: extractedDir},
			root:     root,
		}, nil

	default:
		return nil, fmt.Errorf("unknown download method")
	}
}

// stashAttestAsset writes the raw bytes to <root>/__rpie_asset__ when the policy
// is Attest, returning the file path and repo when the write succeeded.
func stashAttestAsset(root string, b []byte, policy VerifyPolicy, repo string) (file, attestRepo string) {
	if policy != VerifyAttest {
		return "", ""
	}
	f := filepath.Join(root, "__rpie_asset__")
	if err := os.WriteFile(f, b, 0o644); err != nil {
		return "", ""
	}
	return f, repo
}

func locateWindowsDll(root, extensionName string) (string, error) {
	exact := fmt.Sprintf("php_%s.dll", extensionName)
	if found, ok := findFile(root, exact, 3); ok {
		return found, nil
	}
	candidates := findFilesByExtension(root, "dll", 3)
	var filtered []string
	for _, p := range candidates {
		base := strings.ToLower(filepath.Base(p))
		if strings.HasPrefix(base, "php_") {
			filtered = append(filtered, p)
		}
	}
	switch len(filtered) {
	case 1:
		return filtered[0], nil
	case 0:
		return "", fmt.Errorf("no `php_%s.dll` found in the Windows package", extensionName)
	default:
		return "", fmt.Errorf("found multiple `php_*.dll` files in the Windows package; could not identify the extension DLL for `%s`", extensionName)
	}
}

func applyVerification(b []byte, expected Expected, policy VerifyPolicy, url string, method resolver.DownloadUrlMethod, attestFile, attestRepo string) error {
	if policy == VerifySkip {
		return nil
	}

	verified, err := VerifyBytes(b, expected)
	if err != nil {
		return fmt.Errorf("verifying %s: %w", url, err)
	}

	st := style.ForStderr()
	if verified {
		fmt.Fprintf(os.Stderr, "  %s %s checksum verified\n", st.Green("✔"), expected.Describe())
	} else {
		msg := fmt.Sprintf("no checksum published for this %s artifact; integrity relies on HTTPS to the origin", method.Label())
		if policy == VerifyEnforce {
			return fmt.Errorf("%s (refusing under --verify=enforce)", msg)
		}
		fmt.Fprintf(os.Stderr, "  %s %s\n", st.Yellow("warning:"), msg)
	}

	if policy == VerifyAttest && attestFile != "" {
		ok, aerr := VerifyGithubAttestation(attestFile, attestRepo)
		switch {
		case aerr != nil:
			return fmt.Errorf("GitHub attestation verification: %w", aerr)
		case ok:
			fmt.Fprintf(os.Stderr, "  %s GitHub attestation verified for %s\n", st.Green("✔"), attestRepo)
		default:
			fmt.Fprintf(os.Stderr, "  %s attestation support not built in; skipping attestation check\n", st.Yellow("warning:"))
		}
	}

	return nil
}

// extract extracts already-downloaded bytes into a fresh temp dir, returning its path.
func extract(b []byte, distType, url string) (string, error) {
	root, err := os.MkdirTemp("", "rpie-dl-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir for extraction: %w", err)
	}
	if err := extractArchive(b, distType, root); err != nil {
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("extracting %s: %w", url, err)
	}
	return root, nil
}

func distTypeFromURL(url string) string {
	lower := strings.ToLower(url)
	if strings.HasSuffix(lower, ".zip") {
		return "zip"
	}
	return "tar"
}

func fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	req.Header.Set("User-Agent", version.UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body of %s: %w", url, err)
	}
	return buf, nil
}

// locateSharedObject finds the compiled <extension>.so inside an extracted binary archive.
func locateSharedObject(root, extensionName string) (string, error) {
	exact := extensionName + ".so"
	if found, ok := findFile(root, exact, 3); ok {
		return found, nil
	}
	sos := findFilesByExtension(root, "so", 3)
	switch len(sos) {
	case 1:
		return sos[0], nil
	case 0:
		return "", fmt.Errorf("no `.so` found in the pre-packaged binary for `%s`", extensionName)
	default:
		return "", fmt.Errorf("expected exactly one `.so` in the pre-packaged binary for `%s`, found %d: %s",
			extensionName, len(sos), strings.Join(sos, ", "))
	}
}

// locateSourceDir finds the directory containing config.m4, honouring an explicit build-path.
func locateSourceDir(root string, pkg *resolver.ResolvedPackage) (string, error) {
	top := root
	if sub, ok := singleSubdir(root); ok {
		top = sub
	}

	if pkg.Metadata.BuildPath != nil {
		resolved := strings.ReplaceAll(*pkg.Metadata.BuildPath, "{version}", pkg.Version)
		candidate := filepath.Join(top, resolved)
		canonical, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolving build-path `%s`: %w", resolved, err)
		}
		topCanonical, err := filepath.EvalSymlinks(top)
		if err != nil {
			topCanonical = top
		}
		if !pathStartsWith(canonical, topCanonical) {
			return "", fmt.Errorf("build-path `%s` escapes the extracted source tree", resolved)
		}
		return canonical, nil
	}

	if fileExists(filepath.Join(top, "config.m4")) {
		return top, nil
	}
	if found, ok := findConfigM4(top, 2); ok {
		return found, nil
	}
	return "", fmt.Errorf("could not find config.m4 under the extracted source of `%s`", pkg.Name)
}

func pathStartsWith(path, prefix string) bool {
	if path == prefix {
		return true
	}
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// singleSubdir returns the sole subdirectory of dir when dir contains exactly one
// directory entry and that entry is a directory.
func singleSubdir(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	if len(entries) != 1 {
		return "", false
	}
	first := entries[0]
	path := filepath.Join(dir, first.Name())
	if isDir(path) {
		return path, true
	}
	return "", false
}

func findConfigM4(dir string, depth int) (string, bool) {
	return findFile(dir, "config.m4", depth)
}

// findFile searches depth levels (including the starting dir) for a file with the
// exact name; first pre-order match wins.
func findFile(dir, name string, depth int) (string, bool) {
	if depth == 0 {
		return "", false
	}
	candidate := filepath.Join(dir, name)
	if pathExists(candidate) {
		return candidate, true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if isDir(p) {
			if found, ok := findFile(p, name, depth-1); ok {
				return found, true
			}
		}
	}
	return "", false
}

// findFilesByExtension collects every non-directory file whose path extension
// equals ext exactly (case-sensitive), within depth levels.
func findFilesByExtension(dir, ext string, depth int) []string {
	var out []string
	collectByExtension(dir, ext, depth, &out)
	return out
}

func collectByExtension(dir, ext string, depth int, out *[]string) {
	if depth == 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if isDir(p) {
			collectByExtension(p, ext, depth-1, out)
		} else if fileExtEquals(p, ext) {
			*out = append(*out, p)
		}
	}
}

// fileExtEquals reports whether the file's extension component equals ext exactly,
// matching Rust Path::extension semantics (no leading dot, case-sensitive).
func fileExtEquals(path, ext string) bool {
	base := filepath.Base(path)
	dot := strings.LastIndex(base, ".")
	if dot <= 0 {
		return false
	}
	return base[dot+1:] == ext
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

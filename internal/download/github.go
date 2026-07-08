package download

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/version"
)

const githubAPI = "https://api.github.com"

// releaseAsset is a matched GitHub release asset.
type releaseAsset struct {
	DownloadURL string
	Digest      *string
	Repo        string
}

// findReleaseAsset finds the release asset matching one of candidateNames.
func findReleaseAsset(pkg *resolver.ResolvedPackage, candidateNames []string) (*releaseAsset, error) {
	repo, err := githubOrgAndRepo(pkg)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", githubAPI, repo, pkg.Version)

	response, err := getJSON(url)
	if err != nil {
		return nil, fmt.Errorf("looking up GitHub release `%s` for `%s`: %w", pkg.Version, pkg.Name, err)
	}

	assetsRaw, ok := response["assets"].([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub release %s has no assets list", pkg.Version)
	}

	wanted := make(map[string]struct{}, len(candidateNames))
	for _, n := range candidateNames {
		wanted[n] = struct{}{}
	}

	for _, a := range assetsRaw {
		asset, ok := a.(map[string]any)
		if !ok {
			continue
		}
		name, _ := asset["name"].(string)
		name = strings.ToLower(name)
		if _, hit := wanted[name]; !hit {
			continue
		}
		dl, ok := asset["browser_download_url"].(string)
		if !ok {
			continue
		}
		var digest *string
		if d, ok := asset["digest"].(string); ok {
			digest = &d
		}
		return &releaseAsset{DownloadURL: dl, Digest: digest, Repo: repo}, nil
	}

	return nil, fmt.Errorf("no release asset for `%s` %s matched any of: %s",
		pkg.Name, pkg.Version, strings.Join(candidateNames, ", "))
}

// githubOrgAndRepo derives org/repo from the package's source/dist URL, falling
// back to the composer package name.
func githubOrgAndRepo(pkg *resolver.ResolvedPackage) (string, error) {
	for _, url := range []*string{pkg.SourceURL, pkg.DistURL} {
		if url == nil {
			continue
		}
		if repo, ok := parseGithubRepo(*url); ok {
			return repo, nil
		}
	}

	if strings.Count(pkg.Name, "/") == 1 {
		return pkg.Name, nil
	}

	return "", fmt.Errorf("could not determine the GitHub repository for `%s` (source: %s)",
		pkg.Name, debugOptionString(pkg.SourceURL))
}

// debugOptionString renders an *string as Rust {:?} on Option<String>:
// None or Some("...").
func debugOptionString(s *string) string {
	if s == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%q)", *s)
}

// parseGithubRepo extracts org/repo from a GitHub URL.
func parseGithubRepo(url string) (string, bool) {
	var rest string
	var ok bool
	for _, prefix := range []string{
		"https://api.github.com/repos/",
		"https://github.com/",
		"git@github.com:",
		"ssh://git@github.com/",
	} {
		if rest, ok = strings.CutPrefix(url, prefix); ok {
			break
		}
	}
	if !ok {
		return "", false
	}

	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return "", false
	}
	org := strings.TrimSpace(parts[0])
	repo := trimEndMatches(strings.TrimSpace(parts[1]), ".git")
	if org == "" || repo == "" {
		return "", false
	}
	return org + "/" + repo, true
}

// trimEndMatches strips all repeated occurrences of suffix, like Rust's
// trim_end_matches.
func trimEndMatches(s, suffix string) string {
	for suffix != "" && strings.HasSuffix(s, suffix) {
		s = s[:len(s)-len(suffix)]
	}
	return s
}

func getJSON(url string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("requesting GitHub API: %w", err)
	}
	req.Header.Set("User-Agent", version.UserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no GitHub release found for that version tag (HTTP 404)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("requesting GitHub API: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing GitHub API response: %w", err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("parsing GitHub API response: %w", err)
	}
	return value, nil
}

func githubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

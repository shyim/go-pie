package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"sort"
	"strings"
	"time"

	"github.com/shyim/go-pie/internal/version"
)

const packagistP2 = "https://repo.packagist.org/p2"

const maxPackagistResponseBytes int64 = 32 << 20

var httpClient = &http.Client{Timeout: 30 * time.Second}

type PackageVersion struct {
	Version           string
	VersionNormalized string
	PackageType       string
	DistURL           *string
	DistType          *string
	DistShasum        *string
	SourceURL         *string
	LibRequires       []string
	Requires          map[string]string
	PhpExt            json.RawMessage
}

type PackageSource interface {
	PackageVersions(ctx context.Context, pkg string) ([]PackageVersion, error)
}

type PackagistClient struct {
	base string
}

func NewPackagistClient() *PackagistClient {
	return &PackagistClient{base: packagistP2}
}

func (c *PackagistClient) PackageVersions(ctx context.Context, pkg string) ([]PackageVersion, error) {
	parts := strings.Split(pkg, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("package name `%s` must be in vendor/name form", pkg)
	}
	// Escape each segment so a name with reserved characters cannot add path
	// segments or a query/fragment to the request.
	url := fmt.Sprintf("%s/%s/%s.json", c.base, neturl.PathEscape(parts[0]), neturl.PathEscape(parts[1]))

	body, err := c.fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", url, err)
	}

	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("parsing Packagist response for %s: %w", pkg, err)
	}

	return ParseVersionsJSON(body, pkg)
}

func (c *PackagistClient) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version.PackagistUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: status code %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPackagistResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxPackagistResponseBytes {
		return nil, fmt.Errorf("Packagist response exceeds the %d-byte safety limit", maxPackagistResponseBytes) //nolint:staticcheck // Packagist is a proper noun.
	}
	return body, nil
}

// ParseVersionsJSON parses a Packagist p2 body.
func ParseVersionsJSON(body []byte, pkg string) ([]PackageVersion, error) {
	var doc struct {
		Packages map[string]json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("Packagist returned no versions for `%s`", pkg) //nolint:staticcheck // Packagist is a proper noun.
	}
	raw, ok := doc.Packages[pkg]
	if !ok {
		return nil, fmt.Errorf("Packagist returned no versions for `%s`", pkg) //nolint:staticcheck // Packagist is a proper noun.
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("Packagist returned no versions for `%s`", pkg) //nolint:staticcheck // Packagist is a proper noun.
	}

	out := make([]PackageVersion, 0, len(arr))
	for _, el := range arr {
		out = append(out, parsePackageVersion(el))
	}
	return out, nil
}

func parsePackageVersion(raw json.RawMessage) PackageVersion {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)

	strAt := func(k string) string {
		if s, ok := jsonString(obj[k]); ok {
			return s
		}
		return ""
	}
	nested := func(parent, key string) *string {
		p, ok := obj[parent]
		if !ok {
			return nil
		}
		var pm map[string]json.RawMessage
		if json.Unmarshal(p, &pm) != nil {
			return nil
		}
		if s, ok := jsonString(pm[key]); ok {
			return &s
		}
		return nil
	}

	requires := parseRequires(obj["require"])

	libKeys := make([]string, 0)
	for k := range requires {
		if strings.HasPrefix(k, "lib-") {
			libKeys = append(libKeys, k)
		}
	}
	sort.Strings(libKeys)
	libRequires := make([]string, 0, len(libKeys))
	for _, k := range libKeys {
		libRequires = append(libRequires, strings.ToLower(strings.TrimPrefix(k, "lib-")))
	}

	var phpExt json.RawMessage
	if v, ok := obj["php-ext"]; ok {
		phpExt = v
	}

	return PackageVersion{
		Version:           strAt("version"),
		VersionNormalized: strAt("version_normalized"),
		PackageType:       strAt("type"),
		DistURL:           nested("dist", "url"),
		DistType:          nested("dist", "type"),
		DistShasum:        nested("dist", "shasum"),
		SourceURL:         nested("source", "url"),
		LibRequires:       libRequires,
		Requires:          requires,
		PhpExt:            phpExt,
	}
}

func parseRequires(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return out
	}
	for k, v := range obj {
		if s, ok := jsonString(v); ok {
			out[k] = s
		}
	}
	return out
}

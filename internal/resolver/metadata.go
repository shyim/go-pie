package resolver

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/shyim/go-pie/internal/platform"
)

type ExtensionType int

const (
	PhpModule ExtensionType = iota
	ZendExtension
)

func (t ExtensionType) String() string {
	switch t {
	case PhpModule:
		return "PhpModule"
	case ZendExtension:
		return "ZendExtension"
	default:
		return "PhpModule"
	}
}

func (t ExtensionType) IniDirective() string {
	if t == ZendExtension {
		return "zend_extension"
	}
	return "extension"
}

func (t ExtensionType) ComposerType() string {
	if t == ZendExtension {
		return "php-ext-zend"
	}
	return "php-ext"
}

func ExtensionTypeFromComposerType(s string) (ExtensionType, bool) {
	switch s {
	case "php-ext":
		return PhpModule, true
	case "php-ext-zend":
		return ZendExtension, true
	default:
		return 0, false
	}
}

type DownloadUrlMethod int

const (
	ComposerDefault DownloadUrlMethod = iota
	PrePackagedSource
	PrePackagedBinary
	WindowsBinary
)

func (m DownloadUrlMethod) String() string {
	switch m {
	case ComposerDefault:
		return "ComposerDefault"
	case PrePackagedSource:
		return "PrePackagedSource"
	case PrePackagedBinary:
		return "PrePackagedBinary"
	case WindowsBinary:
		return "WindowsBinary"
	default:
		return "ComposerDefault"
	}
}

func (m DownloadUrlMethod) Label() string {
	switch m {
	case ComposerDefault:
		return "composer-default"
	case PrePackagedSource:
		return "pre-packaged-source"
	case PrePackagedBinary:
		return "pre-packaged-binary"
	case WindowsBinary:
		return "windows-binary"
	default:
		return "composer-default"
	}
}

func downloadMethodFromToken(token string) (DownloadUrlMethod, bool) {
	switch token {
	case "composer-default":
		return ComposerDefault, true
	case "pre-packaged-source":
		return PrePackagedSource, true
	case "pre-packaged-binary":
		return PrePackagedBinary, true
	case "windows-binary":
		return WindowsBinary, true
	default:
		return 0, false
	}
}

type ConfigureOption struct {
	Name        string
	NeedsValue  bool
	Description *string
}

type PhpExtMetadata struct {
	ExtensionName      string
	ConfigureOptions   []ConfigureOption
	BuildPath          *string
	SupportZts         bool
	SupportNts         bool
	OsFamilies         []string
	OsFamiliesExclude  []string
	Priority           uint32
	DownloadUrlMethods []DownloadUrlMethod
}

func MetadataFromValue(phpExt json.RawMessage, packageName string) PhpExtMetadata {
	var obj map[string]json.RawMessage
	if len(phpExt) > 0 {
		_ = json.Unmarshal(phpExt, &obj)
	}
	get := func(k string) (json.RawMessage, bool) {
		if obj == nil {
			return nil, false
		}
		v, ok := obj[k]
		return v, ok
	}

	extensionName := deriveExtensionNameFromPackage(packageName)
	if raw, ok := get("extension-name"); ok {
		if s, ok := jsonString(raw); ok {
			extensionName = normaliseExtensionName(s)
		}
	}

	var configureOptions []ConfigureOption
	if raw, ok := get("configure-options"); ok {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil {
			for _, el := range arr {
				if co, ok := parseConfigureOption(el); ok {
					configureOptions = append(configureOptions, co)
				}
			}
		}
	}

	var buildPath *string
	if raw, ok := get("build-path"); ok {
		if s, ok := jsonString(raw); ok {
			buildPath = &s
		}
	}

	rawZts, okZts := get("support-zts")
	supportZts := jsonBoolDefault(rawZts, okZts)
	rawNts, okNts := get("support-nts")
	supportNts := jsonBoolDefault(rawNts, okNts)

	rawOsF, okOsF := get("os-families")
	osFamilies := stringList(rawOsF, okOsF)
	rawOsX, okOsX := get("os-families-exclude")
	osFamiliesExclude := stringList(rawOsX, okOsX)

	priority := uint32(80)
	if raw, ok := get("priority"); ok {
		if n, ok := jsonU64(raw); ok && n <= math.MaxUint32 {
			priority = uint32(n)
		}
	}

	rawDl, okDl := get("download-url-method")
	methods := parseDownloadMethods(rawDl, okDl)

	return PhpExtMetadata{
		ExtensionName:      extensionName,
		ConfigureOptions:   configureOptions,
		BuildPath:          buildPath,
		SupportZts:         supportZts,
		SupportNts:         supportNts,
		OsFamilies:         osFamilies,
		OsFamiliesExclude:  osFamiliesExclude,
		Priority:           priority,
		DownloadUrlMethods: methods,
	}
}

func (m *PhpExtMetadata) SupportsThreadSafety(ts platform.ThreadSafety) bool {
	if ts == platform.ThreadSafe {
		return m.SupportZts
	}
	return m.SupportNts
}

func (m *PhpExtMetadata) SupportsOsFamily(f platform.OperatingSystemFamily) bool {
	token := f.Token()
	// An exclude list only rules families out; if an include list is also
	// present the family must still appear in it.
	if len(m.OsFamiliesExclude) > 0 {
		for _, e := range m.OsFamiliesExclude {
			if strings.EqualFold(e, token) {
				return false
			}
		}
		if len(m.OsFamilies) == 0 {
			return true
		}
	}
	if len(m.OsFamilies) > 0 {
		for _, e := range m.OsFamilies {
			if strings.EqualFold(e, token) {
				return true
			}
		}
		return false
	}
	return true
}

func parseConfigureOption(raw json.RawMessage) (ConfigureOption, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ConfigureOption{}, false
	}
	name, ok := jsonString(obj["name"])
	if !ok {
		return ConfigureOption{}, false
	}
	co := ConfigureOption{Name: name}
	if b, ok := jsonBool(obj["needs-value"]); ok {
		co.NeedsValue = b
	}
	if d, ok := jsonString(obj["description"]); ok {
		co.Description = &d
	}
	return co, true
}

func parseDownloadMethods(raw json.RawMessage, present bool) []DownloadUrlMethod {
	var methods []DownloadUrlMethod
	if present {
		if s, ok := jsonString(raw); ok {
			if m, ok := downloadMethodFromToken(s); ok {
				methods = append(methods, m)
			}
		} else {
			var arr []json.RawMessage
			if json.Unmarshal(raw, &arr) == nil {
				for _, el := range arr {
					if s, ok := jsonString(el); ok {
						if m, ok := downloadMethodFromToken(s); ok {
							methods = append(methods, m)
						}
					}
				}
			}
		}
	}
	if len(methods) == 0 {
		return []DownloadUrlMethod{ComposerDefault}
	}
	return methods
}

func stringList(raw json.RawMessage, present bool) []string {
	if !present {
		return nil
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var out []string
		for _, el := range arr {
			if s, ok := jsonString(el); ok {
				out = append(out, s)
			}
		}
		return out
	}
	if s, ok := jsonString(raw); ok {
		return []string{s}
	}
	return nil
}

func normaliseExtensionName(name string) string {
	if s, ok := strings.CutPrefix(name, "ext-"); ok {
		return s
	}
	return name
}

// ValidateExtensionName rejects values that could be interpreted as paths or
// inject additional INI directives. PHP extension module names are identifiers,
// not filenames: the common portable character set is ASCII alphanumeric plus
// underscore.
func ValidateExtensionName(name string) error {
	if name == "" {
		return errors.New("extension name must not be empty")
	}
	for _, c := range name {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' {
			continue
		}
		return fmt.Errorf("invalid extension name %q: only ASCII letters, digits, and underscores are allowed", name)
	}
	return nil
}

func deriveExtensionNameFromPackage(packageName string) string {
	tail := packageName
	if i := strings.LastIndex(packageName, "/"); i >= 0 {
		tail = packageName[i+1:]
	}
	return strings.ReplaceAll(tail, "-", "_")
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func jsonBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var b bool
	if json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

func jsonBoolDefault(raw json.RawMessage, present bool) bool {
	if !present {
		return true
	}
	if b, ok := jsonBool(raw); ok {
		return b
	}
	return true
}

func jsonU64(raw json.RawMessage) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n uint64
	if json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	return n, true
}

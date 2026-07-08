package platform

import "runtime"

type OperatingSystem int

const (
	OSLinux OperatingSystem = iota
	OSDarwin
	OSWindows
	OSOther
)

type OperatingSystemFamily int

const (
	FamilyLinux OperatingSystemFamily = iota
	FamilyDarwin
	FamilyWindows
	FamilyBsd
	FamilySolaris
	FamilyUnknown
)

func (f OperatingSystemFamily) Token() string {
	switch f {
	case FamilyLinux:
		return "linux"
	case FamilyDarwin:
		return "darwin"
	case FamilyWindows:
		return "windows"
	case FamilyBsd:
		return "bsd"
	case FamilySolaris:
		return "solaris"
	default:
		return "unknown"
	}
}

type ThreadSafety int

const (
	ThreadSafe ThreadSafety = iota
	NonThreadSafe
)

func (t ThreadSafety) Token() string {
	if t == ThreadSafe {
		return "zts"
	}
	return "nts"
}

func (t ThreadSafety) String() string {
	if t == ThreadSafe {
		return "ThreadSafe"
	}
	return "NonThreadSafe"
}

type TargetPlatform struct {
	OS               OperatingSystem
	OSFamily         OperatingSystemFamily
	PHP              PhpBinary
	Architecture     Architecture
	ThreadSafety     ThreadSafety
	MakeParallelJobs int
	Phpize           *PhpizePath
	PhpConfig        string
}

// TargetPlatformFromPhp builds the platform from an already-introspected PHP
// binary. makeJobs nil → runtime.NumCPU(). phpize nil → GuessPhpize (error
// swallowed).
func TargetPlatformFromPhp(php *PhpBinary, makeJobs *int, phpize *PhpizePath) (*TargetPlatform, error) {
	os := currentOS()
	threadSafety := NonThreadSafe
	if php.ThreadSafe {
		threadSafety = ThreadSafe
	}
	phpConfig := guessPhpConfig(php)
	if phpize == nil {
		if guessed, err := GuessPhpize(php); err == nil {
			phpize = guessed
		}
	}
	jobs := runtime.NumCPU()
	if makeJobs != nil {
		jobs = *makeJobs
	}
	return &TargetPlatform{
		OS:               os,
		OSFamily:         osToFamily(os),
		PHP:              *php,
		Architecture:     php.Architecture,
		ThreadSafety:     threadSafety,
		MakeParallelJobs: jobs,
		Phpize:           phpize,
		PhpConfig:        phpConfig,
	}, nil
}

// TargetPlatformFixture builds a synthetic platform for unit tests.
func TargetPlatformFixture(os OperatingSystem, ts ThreadSafety, php *PhpBinary) *TargetPlatform {
	return &TargetPlatform{
		OS:               os,
		OSFamily:         osToFamily(os),
		PHP:              *php,
		Architecture:     php.Architecture,
		ThreadSafety:     ts,
		MakeParallelJobs: 1,
		Phpize:           nil,
		PhpConfig:        "",
	}
}

func currentOS() OperatingSystem {
	switch runtime.GOOS {
	case "linux":
		return OSLinux
	case "darwin":
		return OSDarwin
	case "windows":
		return OSWindows
	default:
		return OSOther
	}
}

func osToFamily(os OperatingSystem) OperatingSystemFamily {
	switch os {
	case OSLinux:
		return FamilyLinux
	case OSDarwin:
		return FamilyDarwin
	case OSWindows:
		return FamilyWindows
	default:
		switch runtime.GOOS {
		case "freebsd", "openbsd", "netbsd", "dragonfly":
			return FamilyBsd
		case "solaris", "illumos":
			return FamilySolaris
		default:
			return FamilyUnknown
		}
	}
}

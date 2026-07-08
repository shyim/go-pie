package commands

import (
	"github.com/shyim/go-pie/internal/platform"
)

// PhpTargetArgs holds the shared --with-php-path / --with-phpize-path /
// --make-jobs flags. A nil pointer means the flag was not supplied.
type PhpTargetArgs struct {
	PhpPath    *string // --with-php-path
	PhpizePath *string // --with-phpize-path
	MakeJobs   *int    // --make-jobs / -j
}

// Resolve introspects the target PHP binary and produces the full platform
// descriptor used throughout the pipeline.
func (a *PhpTargetArgs) Resolve() (*platform.TargetPlatform, error) {
	var (
		php *platform.PhpBinary
		err error
	)
	if a.PhpPath != nil {
		php, err = platform.PhpBinaryFromPath(*a.PhpPath)
	} else {
		php, err = platform.PhpBinaryFromPathEnv()
	}
	if err != nil {
		return nil, err
	}

	var phpize *platform.PhpizePath
	if a.PhpizePath != nil {
		phpize, err = platform.ExplicitPhpize(*a.PhpizePath)
		if err != nil {
			return nil, err
		}
	}

	return platform.TargetPlatformFromPhp(php, a.MakeJobs, phpize)
}

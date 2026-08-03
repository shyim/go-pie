package commands

import (
	"context"
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
func (a *PhpTargetArgs) Resolve(ctx context.Context) (*platform.TargetPlatform, error) {
	var (
		php *platform.PhpBinary
		err error
	)
	if a.PhpPath != nil {
		php, err = platform.PhpBinaryFromSelector(ctx, *a.PhpPath)
	} else {
		php, err = platform.PhpBinaryFromPathEnv(ctx)
	}
	if err != nil {
		return nil, err
	}

	var phpize *platform.PhpizePath
	if a.PhpizePath != nil {
		phpize, err = platform.ExplicitPhpize(ctx, *a.PhpizePath)
		if err != nil {
			return nil, err
		}
	}

	return platform.TargetPlatformFromPhp(ctx, php, a.MakeJobs, phpize)
}

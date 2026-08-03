package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shyim/go-pie/internal/download"
	"github.com/shyim/go-pie/internal/style"
	"github.com/shyim/go-pie/internal/version"
)

// ranCommand is set true at the top of every subcommand RunE, so Execute can
// tell a runtime error (a command ran and returned an error → exit 1) from a
// usage/parse error (cobra rejected the args before any RunE → exit 2).
var ranCommand bool

// VerifyArg is the pflag.Value for --verify. An invalid value is a parse error
// (exit 2), matching clap.
type VerifyArg string

func (v *VerifyArg) Set(s string) error {
	switch s {
	case "warn", "enforce", "attest", "skip":
		*v = VerifyArg(s)
		return nil
	default:
		return fmt.Errorf("invalid value '%s' for '--verify <WHEN>'", s)
	}
}

func (v *VerifyArg) String() string { return string(*v) }

func (v *VerifyArg) Type() string { return "WHEN" }

func (v *VerifyArg) Policy() download.VerifyPolicy {
	switch *v {
	case "enforce":
		return download.VerifyEnforce
	case "attest":
		return download.VerifyAttest
	case "skip":
		return download.VerifySkip
	default:
		return download.VerifyWarn
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "gpie",
		Short:         "🐹🥧 gpie — install PHP extensions (a Go port of PIE, Docker-aware)",
		Version:       version.Version,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `gpie` is a usage error (a subcommand is required). Leaving
			// ranCommand false routes this to the exit-2 path in Execute.
			return errors.New("a subcommand is required")
		},
	}
	root.SetVersionTemplate("{{.DisplayName}} {{.Version}}\n")

	var phpPath, phpizePath string
	var makeJobs int
	pf := root.PersistentFlags()
	pf.StringVar(&phpPath, "with-php-path", "", "Path to the target `php` binary, or a version such as 8.3 (defaults to the first on $PATH)")
	pf.StringVar(&phpizePath, "with-phpize-path", "", "Path to a matching `phpize` (auto-detected when omitted)")
	pf.IntVarP(&makeJobs, "make-jobs", "j", 0, "Number of parallel `make` jobs (defaults to CPU count)")

	// phpTargetFromCmd builds a PhpTargetArgs honoring flag-presence semantics.
	phpTargetFromCmd := func(cmd *cobra.Command) PhpTargetArgs {
		var a PhpTargetArgs
		fs := cmd.Flags()
		if fs.Changed("with-php-path") {
			v := phpPath
			a.PhpPath = &v
		}
		if fs.Changed("with-phpize-path") {
			v := phpizePath
			a.PhpizePath = &v
		}
		if fs.Changed("make-jobs") {
			v := makeJobs
			a.MakeJobs = &v
		}
		return a
	}

	root.AddCommand(newInfoCommand(phpTargetFromCmd))
	root.AddCommand(newInstallCommand("install", "Download, build, and install a PIE-compatible PHP extension", ModeInstall, phpTargetFromCmd))
	root.AddCommand(newInstallCommand("download", "Download an extension's source without building it", ModeDownloadOnly, phpTargetFromCmd))
	root.AddCommand(newInstallCommand("build", "Build (but do not install) an already-downloaded extension", ModeBuildOnly, phpTargetFromCmd))
	root.AddCommand(newShowCommand(phpTargetFromCmd))
	root.AddCommand(newUninstallCommand(phpTargetFromCmd))
	root.AddCommand(newPhpListCommand())

	return root
}

// Execute parses os.Args[1:], dispatches, prints errors, and returns the process
// exit code: 0 success/help/version, 1 runtime error, 2 usage error.
func Execute() int {
	ranCommand = false
	root := newRootCommand()

	// Cancel the command tree on SIGINT/SIGTERM so in-flight downloads, registry
	// requests, and child processes (phpize, make, apt-get) are torn down
	// instead of being orphaned by an abrupt exit. A second signal restores the
	// default behaviour and kills the process outright.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	// A cancelled run is an interrupt, not a usage or runtime error. Test the
	// context rather than the returned error: commands aggregate per-package
	// failures into their own error values, so context.Canceled is usually not
	// in the chain by the time it reaches here.
	if ctx.Err() != nil {
		fmt.Fprintf(os.Stderr, "\n%s interrupted\n", style.ForStderr().RedBold("error:"))
		return 130
	}

	if !ranCommand {
		// Usage / parse error: cobra already knows the failing command via
		// FindTarget; print Error + usage to stderr and exit 2.
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if usageCmd, _, ferr := root.Find(os.Args[1:]); ferr == nil && usageCmd != nil {
			fmt.Fprint(os.Stderr, usageCmd.UsageString())
		} else {
			fmt.Fprint(os.Stderr, root.UsageString())
		}
		return 2
	}

	fmt.Fprintf(os.Stderr, "%s %v\n", style.ForStderr().RedBold("error:"), err)
	return 1
}

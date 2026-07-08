package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shyim/go-pie/internal/install"
	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/style"
)

type UninstallArgs struct {
	Packages []string
	Php      PhpTargetArgs
}

func newUninstallCommand(phpTarget func(*cobra.Command) PhpTargetArgs) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall PACKAGE...",
		Short: "Disable and remove one or more RPIE-installed extensions",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ranCommand = true
			return RunUninstall(&UninstallArgs{
				Packages: args,
				Php:      phpTarget(cmd),
			})
		},
	}
	return cmd
}

func RunUninstall(args *UninstallArgs) error {
	plat, err := args.Php.Resolve()
	if err != nil {
		return err
	}

	if !platform.IsRunningAsRoot() {
		fmt.Fprintln(os.Stderr, "This command may need elevated privileges, and may prompt you for your password.")
	}

	managed, err := install.DiscoverManaged(plat)
	if err != nil {
		return fmt.Errorf("scanning RPIE-managed extensions: %w", err)
	}

	multiple := len(args.Packages) > 1
	var failures []string
	se := style.ForStderr()
	so := style.ForStdout()

	for _, spec := range args.Packages {
		found := findManaged(managed, spec)
		if found == nil {
			fmt.Fprintf(os.Stderr, "%s `%s` is not managed by RPIE (not found in any RPIE INI marker)\n",
				se.Red("not found:"), spec)
			failures = append(failures, spec)
			continue
		}

		outcome, err := install.Uninstall(found, plat)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", se.Red("failed:"), spec, err)
			failures = append(failures, spec)
			continue
		}

		if outcome.RemovedSo != "" {
			fmt.Printf("%s %s\n", so.Green("Removed:"), outcome.RemovedSo)
		} else {
			fmt.Printf("%s no .so found for `%s` (already gone)\n",
				so.Yellow("note:"), found.ExtensionName)
		}
		for _, f := range outcome.RemovedIniFiles {
			fmt.Printf("%s %s\n", so.Green("Removed INI:"), f)
		}
		for _, f := range outcome.RewrittenIniFiles {
			fmt.Printf("%s %s\n", so.Green("Disabled in:"), f)
		}

		loaded, err := plat.PHP.ExtensionIsLoaded(found.ExtensionName)
		if err == nil && loaded {
			fmt.Fprintf(os.Stderr, "%s `%s` still reports as loaded — a restart or another INI may re-enable it\n",
				se.Yellow("warning:"), found.ExtensionName)
		} else {
			fmt.Printf("%s %s uninstalled\n", so.Green("✅"), found.PackageName)
		}
	}

	if multiple {
		ok := len(args.Packages) - len(failures)
		fmt.Printf("\n%s %d/%d extensions removed.\n",
			so.Bold("Finished."), ok, len(args.Packages))
	}

	if len(failures) > 0 {
		return fmt.Errorf("failed to uninstall: %s", strings.Join(failures, ", "))
	}
	return nil
}

func findManaged(managed map[string]install.ManagedExtension, spec string) *install.ManagedExtension {
	specLower := strings.ToLower(spec)
	if m, ok := managed[specLower]; ok {
		return &m
	}
	for _, m := range managed {
		if strings.EqualFold(m.PackageName, spec) {
			return &m
		}
	}
	return nil
}

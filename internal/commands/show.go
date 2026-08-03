package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shyim/go-pie/internal/install"
	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/style"
)

type ShowArgs struct {
	All          bool
	CheckUpdates bool
	Php          PhpTargetArgs
}

func newShowCommand(phpTarget func(*cobra.Command) PhpTargetArgs) *cobra.Command {
	var all, checkUpdates bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "List installed extensions and the GPIE packages they came from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ranCommand = true
			return RunShow(cmd.Context(), &ShowArgs{All: all, CheckUpdates: checkUpdates, Php: phpTarget(cmd)})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Show all loaded extensions, even those GPIE does not manage")
	cmd.Flags().BoolVar(&checkUpdates, "check-updates", false, "Check Packagist for newer versions of managed extensions")
	return cmd
}

func RunShow(ctx context.Context, args *ShowArgs) error {
	plat, err := args.Php.Resolve(ctx)
	if err != nil {
		return err
	}

	loaded, err := plat.PHP.LoadedExtensionsWithVersions(ctx)
	if err != nil {
		return fmt.Errorf("listing loaded extensions: %w", err)
	}

	managed, err := install.DiscoverManaged(ctx, plat)
	if err != nil {
		return fmt.Errorf("scanning GPIE-managed INI files: %w", err)
	}

	var client *resolver.PackagistClient
	if args.CheckUpdates {
		client = resolver.NewPackagistClient()
	}

	so := style.ForStdout()
	header := "Loaded GPIE extensions:"
	if args.All {
		header = "All loaded extensions:"
	}
	fmt.Println(so.BoldUnderlined(header))

	var matchedPackages []string
	anyShown := false

	for _, ev := range loaded {
		name := ev.Name
		version := ev.Version
		if m, ok := managed[toLowerASCII(name)]; ok {
			anyShown = true
			matchedPackages = append(matchedPackages, m.PackageName)
			suffix := ""
			if client != nil {
				suffix = updateNotice(ctx, client, &m, plat)
			}
			fmt.Printf("  %s (from 🦀🥧 %s)%s\n", so.Green(name+":"+version), m.PackageName, suffix)
		} else if args.All {
			anyShown = true
			fmt.Printf("  %s\n", so.Dim(name+":"+version))
		}
	}

	if !anyShown {
		fmt.Println("  (none)")
	}

	reportNotLoaded(so, managed, loaded, matchedPackages)

	if !args.All {
		fmt.Printf("\nTip: use %s to include extensions GPIE does not manage.\n", so.Cyan("--all"))
	}

	return nil
}

func reportNotLoaded(so style.Styler, managed map[string]install.ManagedExtension, loaded []platform.ExtensionVersion, matchedPackages []string) {
	loadedNames := make(map[string]struct{}, len(loaded))
	for _, ev := range loaded {
		loadedNames[toLowerASCII(ev.Name)] = struct{}{}
	}
	matched := make(map[string]struct{}, len(matchedPackages))
	for _, p := range matchedPackages {
		matched[p] = struct{}{}
	}

	var notLoaded []install.ManagedExtension
	for _, key := range install.ManagedKeys(managed) {
		m := managed[key]
		if _, ok := loadedNames[toLowerASCII(m.ExtensionName)]; ok {
			continue
		}
		if _, ok := matched[m.PackageName]; ok {
			continue
		}
		notLoaded = append(notLoaded, m)
	}

	if len(notLoaded) == 0 {
		return
	}

	fmt.Printf("\n⚠ %s\n", so.BoldUnderlined("GPIE extensions not loaded:"))
	fmt.Println("These were set up with GPIE but are not currently enabled.")
	fmt.Println()
	for _, m := range notLoaded {
		fmt.Printf(" - %s (ext-%s) — in %s but not loaded\n", m.PackageName, m.ExtensionName, m.IniFile)
	}
}

// updateNotice best-effort resolves the recorded package on Packagist and
// returns the styled update suffix. Any failure yields the empty string.
func updateNotice(ctx context.Context, client *resolver.PackagistClient, managed *install.ManagedExtension, plat *platform.TargetPlatform) string {
	request, err := resolver.ParseRequest(managed.PackageName)
	if err != nil {
		return ""
	}
	resolved, err := resolver.Resolve(ctx, client, request, plat)
	if err != nil {
		return ""
	}
	latest := resolved.Version
	so := style.ForStdout()

	if managed.Version != nil {
		installed := *managed.Version
		switch {
		case installed == latest:
			return " — " + so.Green("up to date")
		case resolver.VersionIsNewer(latest, installed):
			return fmt.Sprintf(" — %s %s (installed %s)", so.Yellow("upgradable to"), so.Yellow(latest), installed)
		default:
			return " — " + so.Green("up to date")
		}
	}
	return ", latest available is " + so.Yellow(latest)
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

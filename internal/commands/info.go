package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shyim/go-pie/internal/docker"
	"github.com/shyim/go-pie/internal/resolver"
	"github.com/shyim/go-pie/internal/style"
)

type InfoArgs struct {
	Package *string // optional positional
	Php     PhpTargetArgs
}

func newInfoCommand(phpTarget func(*cobra.Command) PhpTargetArgs) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info [PACKAGE]",
		Short: "Show information about the target PHP and a resolved extension",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ranCommand = true
			a := &InfoArgs{Php: phpTarget(cmd)}
			if len(args) == 1 {
				a.Package = &args[0]
			}
			return RunInfo(a)
		},
	}
	return cmd
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func RunInfo(args *InfoArgs) error {
	plat, err := args.Php.Resolve()
	if err != nil {
		return err
	}
	php := &plat.PHP
	so := style.ForStdout()

	fmt.Println(so.BoldCyan("Target PHP"))
	fmt.Printf("  binary        : %s\n", php.Path)
	fmt.Printf("  version       : %s\n", php.Version.String())
	ts := "NTS"
	if php.ThreadSafe {
		ts = "ZTS"
	}
	fmt.Printf("  thread safety : %s\n", ts)
	fmt.Printf("  debug build   : %s\n", yesNo(php.DebugBuild))
	fmt.Printf("  architecture  : %s\n", php.Architecture.String())
	fmt.Printf("  PHP API       : %s\n", php.APIVersion)
	fmt.Printf("  extension_dir : %s\n", php.ExtensionDir)
	fmt.Printf("  os family     : %s\n", plat.OSFamily.Token())
	if php.WindowsCompiler != nil {
		fmt.Printf("  win compiler  : %s\n", php.WindowsCompiler.Token())
	}
	if plat.Phpize != nil {
		fmt.Printf("  phpize        : %s\n", plat.Phpize.Path)
	} else {
		fmt.Printf("  phpize        : %s\n", "(not found)")
	}
	if plat.PhpConfig != "" {
		fmt.Printf("  php-config    : %s\n", plat.PhpConfig)
	} else {
		fmt.Printf("  php-config    : %s\n", "(not found)")
	}
	fmt.Printf("  make -j       : %d\n", plat.MakeParallelJobs)

	if distro := docker.DetectDistro(); distro != nil {
		fmt.Println()
		fmt.Println(so.BoldCyan("Detected Linux distro"))
		fmt.Printf("  distro        : %s\n", distro.Label())
		fmt.Printf("  package mgr   : %s\n", docker.PackageManagerForDistro(distro).String())
		fmt.Printf("  official PHP image : %s\n", yesNo(docker.InOfficialPHPImage()))
	}

	if args.Package != nil {
		request, err := resolver.ParseRequest(*args.Package)
		if err != nil {
			return err
		}
		client := resolver.NewPackagistClient()
		resolved, err := resolver.Resolve(client, request, plat)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(so.BoldCyan("Resolved extension"))
		fmt.Printf("  package       : %s:%s\n", resolved.Name, resolved.Version)
		fmt.Printf("  extension     : %s\n", resolved.ExtensionName)
		fmt.Printf("  type          : %s\n", resolved.ExtensionType.String())
		fmt.Printf("  priority      : %d\n", resolved.Priority)
		if len(resolved.Metadata.ConfigureOptions) > 0 {
			fmt.Println("  configure options:")
			for _, opt := range resolved.Metadata.ConfigureOptions {
				value := ""
				if opt.NeedsValue {
					value = "=<value>"
				}
				desc := ""
				if opt.Description != nil {
					desc = fmt.Sprintf("  (%s)", *opt.Description)
				}
				fmt.Printf("    --%s%s%s\n", opt.Name, value, desc)
			}
		}
		if resolved.DistURL != nil {
			fmt.Printf("  dist          : %s\n", *resolved.DistURL)
		}
	}

	return nil
}

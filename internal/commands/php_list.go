package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shyim/go-pie/internal/platform"
	"github.com/shyim/go-pie/internal/style"
)

type PhpListArgs struct {
	Paths bool
}

func newPhpListCommand() *cobra.Command {
	var paths bool
	cmd := &cobra.Command{
		Use:   "php:list",
		Short: "List the PHP installations found on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ranCommand = true
			return RunPhpList(cmd.Context(), &PhpListArgs{Paths: paths})
		},
	}
	cmd.Flags().BoolVar(&paths, "paths", false, "Print only the binary paths, one per line")
	return cmd
}

func RunPhpList(ctx context.Context, args *PhpListArgs) error {
	found := platform.DiscoverPhp(ctx)

	if args.Paths {
		for _, php := range found {
			fmt.Println(php.Path)
		}
		return nil
	}

	so := style.ForStdout()
	if len(found) == 0 {
		fmt.Println("No PHP installation found.")
		fmt.Printf("Pass %s to point GPIE at a `php` binary explicitly.\n", so.Cyan("--with-php-path"))
		return nil
	}

	fmt.Println(so.BoldUnderlined("Installed PHP versions:"))
	defaultPhp := platform.DefaultPhp(found)
	for i := range found {
		php := &found[i]
		marker := " "
		if php == defaultPhp {
			marker = so.Green("*")
		}
		labels := php.Source
		if php.IsSystem {
			labels += ", system"
		}
		fmt.Printf(" %s %s  %s %s\n",
			marker,
			so.Bold(php.Version.String()),
			php.Path,
			so.Dim("("+labels+")"))
	}

	fmt.Printf("\n%s is the default target. Select another with %s or %s.\n",
		so.Green("*"), so.Cyan("--with-php-path <version>"), so.Cyan("--with-php-path <path>"))
	return nil
}

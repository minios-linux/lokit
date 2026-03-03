package cli

import (
	"fmt"

	. "github.com/minios-linux/lokit/i18n"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: T("Show version information"),
		Long:  T(`Display version, commit hash, and build date.`),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf(T("lokit version %s")+"\n", version)
			fmt.Printf("  %s %s\n", T("commit:"), commit)
			fmt.Printf("  %s %s\n", T("built:"), date)
		},
	}

	return cmd
}

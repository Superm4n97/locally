package cmd

import (
	"github.com/Superm4n97/whoserve/pkg/expose"
	"github.com/spf13/cobra"
)

var port int

var exposeCmd = &cobra.Command{
	Use:   "expose",
	Short: "Expose a server serving your current directory",
	Run: func(cmd *cobra.Command, args []string) {
		if err := expose.StartExposeServer(port); err != nil {
			panic(err)
		}
	},
}

func addExposeFlags() {
	exposeCmd.Flags().IntVarP(&port, "port", "p", 8000, "port number where the target server is running on")
}

func init() {
	rootCmd.AddCommand(exposeCmd)
	addExposeFlags()
}

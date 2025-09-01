package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var exposeCmd = &cobra.Command{
	Use:   "expose",
	Short: "Expose a running servers (listening ports)",
	Run: func(cmd *cobra.Command, args []string) {
		// Try `ss -tulnp` (works on most modern Linux distros)
		out, err := exec.Command("ss", "-tulnp").CombinedOutput()
		if err != nil {
			fmt.Println("Error running ss command:", err)
			return
		}

		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "LISTEN") {
				fmt.Println(line)
			}
		}
	},
}

func addFlags() {
	var port string
	exposeCmd.Flags().StringVarP(&port, "port", "p", "", "port number where the server is running on")
	exposeCmd.MarkFlagRequired("port")
}

func init() {
	rootCmd.AddCommand(exposeCmd)
	addFlags()
}

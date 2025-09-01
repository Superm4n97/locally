package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all running  servers (listening ports)",
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

func init() {

	rootCmd.AddCommand(listCmd)
}

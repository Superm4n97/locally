package cmd

import (
	"github.com/Superm4n97/whoserve/pkg/proxy"
	"github.com/spf13/cobra"
)

var (
	targetPort, proxyPort int
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Run a proxy server for any running servers (listening ports)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := proxy.StartProxyServer(proxyPort, targetPort); err != nil {
			panic(err)
		}
	},
}

func addProxyFlags() {
	proxyCmd.Flags().IntVarP(&targetPort, "target-port", "t", 0, "port number where the target server is running on")
	proxyCmd.MarkFlagRequired("target-port")

	proxyCmd.Flags().IntVarP(&proxyPort, "proxy-port", "p", 0, "proxy server port number")
	proxyCmd.MarkFlagRequired("proxy-port")
}

func init() {
	rootCmd.AddCommand(proxyCmd)
	addProxyFlags()
}

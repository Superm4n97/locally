package proxy

import (
	"fmt"
	"github.com/Superm4n97/whoserve/pkg/util"
	"k8s.io/klog/v2"
	_ "k8s.io/klog/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func StartProxyServer(listenPort, targetPort int) error {
	if err := util.ValidatePort(util.PortInput{
		Name: "proxy port",
		Port: listenPort,
	}, util.PortInput{
		Name: "target port",
		Port: targetPort,
	}); err != nil {
		return err
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://localhost:%d", targetPort))
	if err != nil {
		return fmt.Errorf("failed to parse target URL: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	addr := fmt.Sprintf("0.0.0.0:%d", listenPort)
	myip, err := util.MyIP()
	if err != nil {
		return err
	}
	klog.Infof("Starting proxy server on %s:%d forwarding to %s", myip, listenPort, targetURL)

	if err = util.PrintQRCode(fmt.Sprintf("http://%s:%d", myip, listenPort)); err != nil {
		return err
	}

	if err = http.ListenAndServe(addr, proxy); err != nil {
		return fmt.Errorf("server failed: %v", err)
	}
	return nil
}

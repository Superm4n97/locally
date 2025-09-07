package expose

import (
	"fmt"
	"github.com/Superm4n97/whoserve/pkg/util"
	"k8s.io/klog/v2"
	"net/http"
)

func StartExposeServer(port int) error {
	if err := util.ValidatePort(util.PortInput{
		Name: "proxy port",
		Port: port,
	}); err != nil {
		return err
	}

	// Serve current directory (".")
	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)

	myip, err := util.MyIP()
	if err != nil {
		return err
	}
	klog.Infof("Starting exposing server on %s:%d", myip, port)

	if err = util.PrintQRCode(fmt.Sprintf("http://%s:%d", myip, port)); err != nil {
		return err
	}

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	if err = http.ListenAndServe(addr, nil); err != nil {
		panic(err)
	}

	return nil
}

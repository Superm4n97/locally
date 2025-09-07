package util

import (
	"fmt"
	"k8s.io/klog/v2"
)

type PortInput struct {
	Name string
	Port int
}

func ValidatePort(ports ...PortInput) error {
	for _, p := range ports {
		if p.Port <= 0 || p.Port > 65535 {
			return fmt.Errorf("invalid port %d", p.Port)
		}
		if p.Port < 1023 {
			klog.Warningf("%s trying to use privileged port: %d", p.Port, p.Port)
		}
	}
	return nil
}

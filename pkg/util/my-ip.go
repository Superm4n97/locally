package util

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

func MyIP() (string, error) {
	// Step 1: Get default network interface name
	out, err := exec.Command("sh", "-c", "ip route | grep '^default' | awk '{print $5}'").Output()
	if err != nil {
		fmt.Println("Error getting default interface:", err)
		return "", err
	}
	ifaceName := strings.TrimSpace(string(out))

	// Step 2: Get interface by name
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		fmt.Println("Error finding interface:", err)
		return "", err
	}

	// Step 3: Get IP addresses from interface
	addrs, err := iface.Addrs()
	if err != nil {
		fmt.Println("Error getting addresses:", err)
		return "", err
	}

	// Step 4: Filter IPv4 address
	var ipAddr string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.To4() != nil {
			ipAddr = ipNet.IP.String()
			break
		}
	}

	if ipAddr == "" {
		err = fmt.Errorf("No IPv4 address found for", ifaceName)
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Println("Your interface IP:", ipAddr)
	return ipAddr, nil
}

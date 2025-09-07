package util

import (
	"fmt"
	"github.com/skip2/go-qrcode"
)

func PrintQRCode(url string) error {
	qr, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		fmt.Println("failed to generate qr code, ", err)
		return err
	}
	qrString := qr.ToString(false)
	fmt.Println(qrString)
	return nil
}

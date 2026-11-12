package netsec

import "net"

func linkText(addr [16]byte) string {
	return net.IP(addr[:]).String()
}

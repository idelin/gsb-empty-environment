package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

const (
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

var (
	// ErrInvalidDatagram marks a datagram that does not pass SOCKS5 UDP
	// request-header validation.
	ErrInvalidDatagram = errors.New("socks5: invalid UDP datagram")
	// ErrDatagramTooShort means a required header field is missing.
	ErrDatagramTooShort = errors.New("socks5: UDP datagram is too short")
	// ErrReservedNonZero means RSV is not 0x0000.
	ErrReservedNonZero = errors.New("socks5: reserved field is not zero")
	// ErrFragmented means FRAG is not zero. A relay must not reassemble or
	// forward fragmented SOCKS5 UDP datagrams.
	ErrFragmented = errors.New("socks5: fragmented UDP datagrams are not supported")
	// ErrUnsupportedAddressType means ATYP is not IPv4, domain, or IPv6.
	ErrUnsupportedAddressType = errors.New("socks5: unsupported address type")
	// ErrInvalidDomainLength means a domain is empty or its length byte
	// extends beyond the datagram.
	ErrInvalidDomainLength = errors.New("socks5: invalid domain length")
)

// Address is the destination carried in a SOCKS5 UDP request header. Host is
// either an IP literal or a domain name. Domain names are encoded with ATYP
// 0x03; IP literals are encoded with ATYP 0x01 or 0x04.
type Address struct {
	Host string
	Port uint16
}

// String returns the address in host:port form.
func (a Address) String() string {
	return net.JoinHostPort(a.Host, strconv.Itoa(int(a.Port)))
}

// IsIP reports whether Host is an IP literal rather than a domain name.
func (a Address) IsIP() bool {
	_, err := netip.ParseAddr(a.Host)
	return err == nil
}

// IP returns Host as a net.IP when Host is an IP literal.
func (a Address) IP() net.IP {
	ip, err := netip.ParseAddr(a.Host)
	if err != nil {
		return nil
	}
	return net.IP(ip.Unmap().AsSlice())
}

// AddressFromUDPAddr converts a UDP address into a SOCKS5 address.
func AddressFromUDPAddr(addr *net.UDPAddr) Address {
	if addr == nil {
		return Address{}
	}
	host := ""
	if ip, ok := netip.AddrFromSlice(addr.IP); ok {
		host = ip.Unmap().String()
	}
	port := 0
	if addr.Port >= 0 && addr.Port <= 65535 {
		port = addr.Port
	}
	return Address{Host: host, Port: uint16(port)}
}

// UDPAddr resolves the address to a UDP address. Domain names are resolved
// with the system resolver.
func (a Address) UDPAddr() (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp", a.String())
}

// ParseDatagram validates a SOCKS5 UDP request header and returns its target
// and payload. RSV must be zero, FRAG must be zero, and the target address
// must be completely contained in packet.
func ParseDatagram(packet []byte) (Address, []byte, error) {
	if len(packet) < 4 {
		return Address{}, nil, fmt.Errorf("%w: %w", ErrInvalidDatagram, ErrDatagramTooShort)
	}
	if packet[0] != 0 || packet[1] != 0 {
		return Address{}, nil, fmt.Errorf("%w: %w", ErrInvalidDatagram, ErrReservedNonZero)
	}
	if packet[2] != 0 {
		return Address{}, nil, fmt.Errorf("%w: %w", ErrInvalidDatagram, ErrFragmented)
	}

	target, consumed, err := parseAddress(packet[3:])
	if err != nil {
		return Address{}, nil, fmt.Errorf("%w: %w", ErrInvalidDatagram, err)
	}
	return target, packet[3+consumed:], nil
}

// ParseUDPDatagram is an alias for ParseDatagram.
func ParseUDPDatagram(packet []byte) (Address, []byte, error) {
	return ParseDatagram(packet)
}

func parseAddress(packet []byte) (Address, int, error) {
	if len(packet) < 1 {
		return Address{}, 0, ErrDatagramTooShort
	}

	switch packet[0] {
	case atypIPv4:
		if len(packet) < 1+4+2 {
			return Address{}, 0, ErrDatagramTooShort
		}
		var raw [4]byte
		copy(raw[:], packet[1:5])
		return Address{
			Host: netip.AddrFrom4(raw).String(),
			Port: binary.BigEndian.Uint16(packet[5:7]),
		}, 7, nil

	case atypIPv6:
		if len(packet) < 1+16+2 {
			return Address{}, 0, ErrDatagramTooShort
		}
		var raw [16]byte
		copy(raw[:], packet[1:17])
		return Address{
			Host: netip.AddrFrom16(raw).String(),
			Port: binary.BigEndian.Uint16(packet[17:19]),
		}, 19, nil

	case atypDomain:
		if len(packet) < 2 {
			return Address{}, 0, ErrDatagramTooShort
		}
		length := int(packet[1])
		if length == 0 || len(packet) < 2+length {
			return Address{}, 0, ErrInvalidDomainLength
		}
		if len(packet) < 2+length+2 {
			return Address{}, 0, ErrDatagramTooShort
		}
		return Address{
			Host: string(packet[2 : 2+length]),
			Port: binary.BigEndian.Uint16(packet[2+length : 4+length]),
		}, 4 + length, nil

	default:
		return Address{}, 0, ErrUnsupportedAddressType
	}
}

// EncodeDatagram encodes a complete SOCKS5 UDP request.
func EncodeDatagram(target Address, payload []byte) ([]byte, error) {
	return AppendDatagram(nil, target, payload)
}

// EncodeUDPDatagram is an alias for EncodeDatagram.
func EncodeUDPDatagram(target Address, payload []byte) ([]byte, error) {
	return EncodeDatagram(target, payload)
}

// AppendDatagram appends a complete SOCKS5 UDP request to dst.
func AppendDatagram(dst []byte, target Address, payload []byte) ([]byte, error) {
	dst = append(dst, 0, 0, 0)
	dst, err := appendAddress(dst, target)
	if err != nil {
		return nil, err
	}
	return append(dst, payload...), nil
}

func appendAddress(dst []byte, target Address) ([]byte, error) {
	ip, err := netip.ParseAddr(target.Host)
	if err == nil {
		if ip.Zone() != "" {
			return nil, fmt.Errorf("%w: IPv6 zones are not encodable", ErrUnsupportedAddressType)
		}
		ip = ip.Unmap()
		if ip.Is4() {
			raw := ip.As4()
			dst = append(dst, atypIPv4)
			dst = append(dst, raw[:]...)
		} else {
			raw := ip.As16()
			dst = append(dst, atypIPv6)
			dst = append(dst, raw[:]...)
		}
	} else {
		if len(target.Host) == 0 || len(target.Host) > 255 {
			return nil, ErrInvalidDomainLength
		}
		dst = append(dst, atypDomain, byte(len(target.Host)))
		dst = append(dst, target.Host...)
	}

	var port [2]byte
	binary.BigEndian.PutUint16(port[:], target.Port)
	return append(dst, port[:]...), nil
}

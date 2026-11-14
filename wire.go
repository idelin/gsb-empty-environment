package netsec

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ErrTruncated is returned when a wire-format message ends prematurely.
var ErrTruncated = errors.New("netsec: truncated DNS message")

// unpackName decodes a possibly compressed domain name at off. It returns
// the canonical name and the offset of the first byte after the name in
// its (possibly pointer-terminated) on-wire form. Pointer loops are
// rejected.
func unpackName(msg []byte, off int) (string, int, error) {
	var labels []string
	next := -1
	jumps := 0
	for {
		if off >= len(msg) {
			return "", 0, ErrTruncated
		}
		length := int(msg[off])
		switch length & 0xC0 {
		case 0xC0:
			if off+1 >= len(msg) {
				return "", 0, ErrTruncated
			}
			ptr := int(length&0x3F)<<8 | int(msg[off+1])
			if next < 0 {
				next = off + 2
			}
			jumps++
			if jumps > 32 {
				return "", 0, errors.New("netsec: compression pointer loop")
			}
			off = ptr
		case 0x00:
			if length == 0 {
				if next < 0 {
					next = off + 1
				}
				if len(labels) == 0 {
					return ".", next, nil
				}
				return Canonical(strings.Join(labels, ".")), next, nil
			}
			if off+1+length > len(msg) {
				return "", 0, ErrTruncated
			}
			labels = append(labels, string(msg[off+1:off+1+length]))
			off += 1 + length
		default:
			return "", 0, errors.New("netsec: reserved label type")
		}
	}
}

// ParseMessage decodes a wire-format DNS message into a Response. All
// owner names and names embedded in well-known RDATA are decompressed
// and canonicalized, so records that differ only in case or in the use
// of compression pointers compare equal downstream.
func ParseMessage(msg []byte) (*Response, error) {
	if len(msg) < 12 {
		return nil, ErrTruncated
	}
	flags := binary.BigEndian.Uint16(msg[2:4])
	resp := &Response{
		Authoritative: flags&0x0400 != 0,
		RCode:         int(flags & 0x000F),
	}
	counts := []int{
		int(binary.BigEndian.Uint16(msg[4:6])),
		int(binary.BigEndian.Uint16(msg[6:8])),
		int(binary.BigEndian.Uint16(msg[8:10])),
		int(binary.BigEndian.Uint16(msg[10:12])),
	}
	off := 12
	for i := 0; i < counts[0]; i++ {
		name, noff, err := unpackName(msg, off)
		if err != nil {
			return nil, err
		}
		off = noff
		if off+4 > len(msg) {
			return nil, ErrTruncated
		}
		if i == 0 {
			resp.Question = Question{
				Name:  name,
				Type:  binary.BigEndian.Uint16(msg[off:]),
				Class: binary.BigEndian.Uint16(msg[off+2:]),
			}
		}
		off += 4
	}
	sections := []struct {
		count int
		dst   *[]RR
	}{
		{counts[1], &resp.Answer},
		{counts[2], &resp.Authority},
		{counts[3], &resp.Additional},
	}
	for _, sec := range sections {
		for i := 0; i < sec.count; i++ {
			rr, noff, err := parseRR(msg, off)
			if err != nil {
				return nil, err
			}
			off = noff
			*sec.dst = append(*sec.dst, rr)
		}
	}
	return resp, nil
}

func parseRR(msg []byte, off int) (RR, int, error) {
	name, noff, err := unpackName(msg, off)
	if err != nil {
		return RR{}, 0, err
	}
	off = noff
	if off+10 > len(msg) {
		return RR{}, 0, ErrTruncated
	}
	typ := binary.BigEndian.Uint16(msg[off:])
	class := binary.BigEndian.Uint16(msg[off+2:])
	ttl := binary.BigEndian.Uint32(msg[off+4:])
	rdlen := int(binary.BigEndian.Uint16(msg[off+8:]))
	off += 10
	if off+rdlen > len(msg) {
		return RR{}, 0, ErrTruncated
	}
	rdata, err := parseRData(msg, typ, off, rdlen)
	if err != nil {
		return RR{}, 0, err
	}
	return RR{Owner: name, Type: typ, Class: class, TTL: ttl, RData: rdata}, off + rdlen, nil
}

// parseRData renders RDATA in presentation form. Names embedded in
// well-known types are decompressed against the whole message and
// canonicalized; unknown types use RFC 3597 generic syntax.
func parseRData(msg []byte, typ uint16, off, rdlen int) (string, error) {
	end := off + rdlen
	name := func(o int) (string, int, error) {
		n, next, err := unpackName(msg, o)
		if err != nil {
			return "", 0, err
		}
		if next > end {
			return "", 0, errors.New("netsec: name in RDATA exceeds rdlength")
		}
		return n, next, nil
	}
	switch typ {
	case TypeA:
		if rdlen != 4 {
			return "", errors.New("netsec: bad A rdlength")
		}
		return net.IP(msg[off:end]).String(), nil
	case TypeAAAA:
		if rdlen != 16 {
			return "", errors.New("netsec: bad AAAA rdlength")
		}
		return net.IP(msg[off:end]).String(), nil
	case TypeNS, TypeCNAME, TypePTR, TypeDNAME:
		n, _, err := name(off)
		return n, err
	case TypeSOA:
		mname, o, err := name(off)
		if err != nil {
			return "", err
		}
		rname, o, err := name(o)
		if err != nil {
			return "", err
		}
		if o+20 > end {
			return "", ErrTruncated
		}
		var nums [5]uint32
		for i := range nums {
			nums[i] = binary.BigEndian.Uint32(msg[o+4*i:])
		}
		return fmt.Sprintf("%s %s %d %d %d %d %d", mname, rname,
			nums[0], nums[1], nums[2], nums[3], nums[4]), nil
	case TypeMX:
		if off+2 > end {
			return "", ErrTruncated
		}
		pref := binary.BigEndian.Uint16(msg[off:])
		exch, _, err := name(off + 2)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %s", pref, exch), nil
	case TypeSRV:
		if off+6 > end {
			return "", ErrTruncated
		}
		target, _, err := name(off + 6)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %d %d %s",
			binary.BigEndian.Uint16(msg[off:]),
			binary.BigEndian.Uint16(msg[off+2:]),
			binary.BigEndian.Uint16(msg[off+4:]), target), nil
	case TypeTXT:
		var parts []string
		for o := off; o < end; {
			l := int(msg[o])
			if o+1+l > end {
				return "", ErrTruncated
			}
			parts = append(parts, strconv.Quote(string(msg[o+1:o+1+l])))
			o += 1 + l
		}
		return strings.Join(parts, " "), nil
	case TypeDS:
		if rdlen < 4 {
			return "", ErrTruncated
		}
		return fmt.Sprintf("%d %d %d %s",
			binary.BigEndian.Uint16(msg[off:]), msg[off+2], msg[off+3],
			strings.ToUpper(hex.EncodeToString(msg[off+4:end]))), nil
	case TypeDNSKEY:
		if rdlen < 4 {
			return "", ErrTruncated
		}
		return fmt.Sprintf("%d %d %d %s",
			binary.BigEndian.Uint16(msg[off:]), msg[off+2], msg[off+3],
			base64.StdEncoding.EncodeToString(msg[off+4:end])), nil
	case TypeRRSIG:
		if rdlen < 18 {
			return "", ErrTruncated
		}
		signer, o, err := name(off + 18)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %d %d %d %d %d %d %s %s",
			binary.BigEndian.Uint16(msg[off:]),
			msg[off+2], msg[off+3],
			binary.BigEndian.Uint32(msg[off+4:]),
			binary.BigEndian.Uint32(msg[off+8:]),
			binary.BigEndian.Uint32(msg[off+12:]),
			binary.BigEndian.Uint16(msg[off+16:]),
			signer,
			base64.StdEncoding.EncodeToString(msg[o:end])), nil
	default:
		return fmt.Sprintf(`\# %d %s`, rdlen,
			strings.ToUpper(hex.EncodeToString(msg[off:end]))), nil
	}
}

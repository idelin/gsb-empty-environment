package netsec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	dnsTypeCAA = 257
	dnsClassIN = 1
	dnsRcodeOK = 0
)

// DNSResolver is a small recursive-resolver client for CAA queries. It does
// not itself validate DNSSEC signatures; it reports the AD bit from the
// resolver as DNSSECSecure. Configure a validating recursive resolver and set
// Validating to distinguish unsigned zones from unvalidated answers.
//
// The zero value uses the name servers from /etc/resolv.conf (or 127.0.0.1)
// and a five-second timeout.
type DNSResolver struct {
	// Servers are DNS server addresses. A port defaults to 53.
	Servers []string
	// Timeout bounds each transport exchange. Zero uses five seconds.
	Timeout time.Duration
	// Validating indicates that Servers perform DNSSEC validation. When true,
	// a response without the AD bit is reported as DNSSECInsecure.
	Validating bool
}

// NewDNSResolver returns a DNSResolver using the supplied servers. With no
// servers, the system resolver configuration is used.
func NewDNSResolver(servers ...string) *DNSResolver {
	return &DNSResolver{Servers: append([]string(nil), servers...)}
}

// LookupCAA performs a CAA query. NXDOMAIN, SERVFAIL, REFUSED, malformed
// responses, and transport errors are returned as errors.
func (r *DNSResolver) LookupCAA(ctx context.Context, name string) (LookupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := NormalizeName(name)
	if err != nil {
		return LookupResult{}, err
	}
	return r.lookupAt(ctx, normalized, r.servers())
}

// LookupCAAVia continues a CAA query at the name servers in a delegation
// referral.
func (r *DNSResolver) LookupCAAVia(ctx context.Context, name string, referral Delegation) (LookupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := NormalizeName(name)
	if err != nil {
		return LookupResult{}, err
	}
	servers, err := r.referralServers(ctx, referral)
	if err != nil {
		return LookupResult{}, err
	}
	return r.lookupAt(ctx, normalized, servers)
}

func (r *DNSResolver) lookupAt(ctx context.Context, name string, servers []string) (LookupResult, error) {
	if len(servers) == 0 {
		return LookupResult{}, errors.New("no DNS servers configured")
	}

	var lastErr error
	for _, server := range servers {
		query, id, err := buildCAAQuery(name)
		if err != nil {
			return LookupResult{}, err
		}
		response, err := r.exchange(ctx, server, query, id)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", server, err)
			continue
		}
		result, err := r.parseResponse(response, id, name)
		if err != nil {
			var rcodeError *DNSRcodeError
			if errors.As(err, &rcodeError) {
				rcodeError.Server = server
				return LookupResult{}, err
			}
			lastErr = fmt.Errorf("%s: %w", server, err)
			continue
		}
		return result, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no DNS servers responded")
	}
	return LookupResult{}, lastErr
}

func (r *DNSResolver) servers() []string {
	if len(r.Servers) > 0 {
		servers := make([]string, 0, len(r.Servers))
		for _, server := range r.Servers {
			if address := addressWithPort(server, "53"); address != "" {
				servers = append(servers, address)
			}
		}
		return servers
	}
	return systemNameServers()
}

func (r *DNSResolver) referralServers(ctx context.Context, referral Delegation) ([]string, error) {
	if len(referral.NameServers) == 0 {
		return nil, fmt.Errorf("delegation %q has no name servers", referral.Zone)
	}
	var servers []string
	for _, nameServer := range referral.NameServers {
		nameServer = strings.TrimSpace(nameServer)
		if nameServer == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(nameServer); err == nil {
			servers = append(servers, nameServer)
			continue
		}
		if ip := net.ParseIP(nameServer); ip != nil {
			servers = append(servers, net.JoinHostPort(ip.String(), "53"))
			continue
		}
		if strings.Contains(nameServer, ":") {
			// A bracketed IPv6 address without a port.
			host := strings.Trim(nameServer, "[]")
			if net.ParseIP(host) != nil {
				servers = append(servers, net.JoinHostPort(host, "53"))
				continue
			}
			return nil, fmt.Errorf("invalid delegation name server %q", nameServer)
		}
		addresses, err := net.DefaultResolver.LookupHost(ctx, nameServer)
		if err != nil {
			return nil, fmt.Errorf("resolve delegation name server %q: %w", nameServer, err)
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("delegation name server %q has no addresses", nameServer)
		}
		servers = append(servers, net.JoinHostPort(addresses[0], "53"))
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("delegation %q has no usable name servers", referral.Zone)
	}
	return servers, nil
}

func (r *DNSResolver) exchange(ctx context.Context, server string, query []byte, id uint16) ([]byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response, err := exchangeUDP(ctx, server, query)
	if err != nil {
		return nil, err
	}
	if len(response) < 12 {
		return nil, errors.New("short DNS response")
	}
	flags := binary.BigEndian.Uint16(response[2:4])
	if flags&0x8000 == 0 {
		return nil, errors.New("DNS response is not a response")
	}
	if binary.BigEndian.Uint16(response[0:2]) != id {
		return nil, errors.New("DNS response ID mismatch")
	}
	if flags&0x0200 != 0 {
		return exchangeTCP(ctx, server, query)
	}
	return response, nil
}

func exchangeUDP(ctx context.Context, server string, query []byte) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	buffer := make([]byte, 65535)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	return buffer[:n], nil
}

func exchangeTCP(ctx context.Context, server string, query []byte) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	message := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(message[:2], uint16(len(query)))
	copy(message[2:], query)
	if _, err := conn.Write(message); err != nil {
		return nil, err
	}

	var length uint16
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	response := make([]byte, int(length))
	if _, err := io.ReadFull(conn, response); err != nil {
		return nil, err
	}
	if len(response) < 12 {
		return nil, errors.New("short DNS response")
	}
	if binary.BigEndian.Uint16(response[0:2]) != binary.BigEndian.Uint16(query[0:2]) {
		return nil, errors.New("DNS response ID mismatch")
	}
	return response, nil
}

// DNSRcodeError reports a DNS error response such as NXDOMAIN or SERVFAIL.
type DNSRcodeError struct {
	Name   string
	Server string
	Rcode  int
}

func (e *DNSRcodeError) Error() string {
	if e.Server != "" {
		return fmt.Sprintf("DNS query for %s at %s returned %s", e.Name, e.Server, rcodeName(e.Rcode))
	}
	return fmt.Sprintf("DNS query for %s returned %s", e.Name, rcodeName(e.Rcode))
}

func (r *DNSResolver) parseResponse(response []byte, id uint16, queryName string) (LookupResult, error) {
	if len(response) < 12 {
		return LookupResult{}, errors.New("short DNS response")
	}
	if binary.BigEndian.Uint16(response[0:2]) != id {
		return LookupResult{}, errors.New("DNS response ID mismatch")
	}
	flags := binary.BigEndian.Uint16(response[2:4])
	if flags&0x8000 == 0 {
		return LookupResult{}, errors.New("DNS response is not a response")
	}
	rcode := int(flags & 0x000f)
	if rcode != dnsRcodeOK {
		return LookupResult{}, &DNSRcodeError{Name: queryName, Rcode: rcode}
	}

	qdcount := int(binary.BigEndian.Uint16(response[4:6]))
	ancount := int(binary.BigEndian.Uint16(response[6:8]))
	nscount := int(binary.BigEndian.Uint16(response[8:10]))
	arcount := int(binary.BigEndian.Uint16(response[10:12]))

	offset := 12
	for i := 0; i < qdcount; i++ {
		qname, next, err := decodeDNSName(response, offset)
		if err != nil {
			return LookupResult{}, fmt.Errorf("parse question: %w", err)
		}
		offset = next
		if offset+4 > len(response) {
			return LookupResult{}, errors.New("short DNS question")
		}
		qtype := binary.BigEndian.Uint16(response[offset : offset+2])
		qclass := binary.BigEndian.Uint16(response[offset+2 : offset+4])
		offset += 4
		if i == 0 {
			if !strings.EqualFold(qname, queryName) {
				return LookupResult{}, fmt.Errorf("DNS question name %q does not match %q", qname, queryName)
			}
			if qtype != dnsTypeCAA || qclass != dnsClassIN {
				return LookupResult{}, fmt.Errorf("unexpected DNS question type %d class %d", qtype, qclass)
			}
		}
	}

	var (
		records       []Record
		ownedCAA      []ownedCAARecord
		aliases       = map[string]string{}
		dnames        []DNAME
		nsOwners      = map[string][]string{}
		authoritative = flags&0x0400 != 0
		hasSOA        bool
	)

	for section := 0; section < 3; section++ {
		count := ancount
		switch section {
		case 1:
			count = nscount
		case 2:
			count = arcount
		}
		for i := 0; i < count; i++ {
			owner, next, err := decodeDNSName(response, offset)
			if err != nil {
				return LookupResult{}, fmt.Errorf("parse RR owner: %w", err)
			}
			offset = next
			if offset+10 > len(response) {
				return LookupResult{}, errors.New("short DNS RR header")
			}
			rrType := binary.BigEndian.Uint16(response[offset : offset+2])
			rrClass := binary.BigEndian.Uint16(response[offset+2 : offset+4])
			rdlength := int(binary.BigEndian.Uint16(response[offset+8 : offset+10]))
			offset += 10
			if offset+rdlength > len(response) {
				return LookupResult{}, errors.New("DNS RR exceeds message")
			}
			rdata := response[offset : offset+rdlength]
			offset += rdlength

			if section == 0 {
				switch rrType {
				case dnsTypeCAA:
					if rrClass != dnsClassIN {
						continue
					}
					record, err := parseCAARData(rdata)
					if err != nil {
						return LookupResult{}, fmt.Errorf("parse CAA RR: %w", err)
					}
					ownedCAA = append(ownedCAA, ownedCAARecord{owner: strings.ToLower(owner), record: record})
				case 5: // CNAME
					if rrClass != dnsClassIN {
						continue
					}
					alias, _, err := decodeDNSName(response, offset-rdlength)
					if err != nil {
						return LookupResult{}, fmt.Errorf("parse CNAME: %w", err)
					}
					aliases[strings.ToLower(owner)] = alias
				case 39: // DNAME
					if rrClass != dnsClassIN {
						continue
					}
					target, _, err := decodeDNSName(response, offset-rdlength)
					if err != nil {
						return LookupResult{}, fmt.Errorf("parse DNAME: %w", err)
					}
					dnames = append(dnames, DNAME{Owner: owner, Target: target})
				}
			}
			if section == 1 && rrType == 2 && rrClass == dnsClassIN { // NS
				nsName, _, err := decodeDNSName(response, offset-rdlength)
				if err != nil {
					return LookupResult{}, fmt.Errorf("parse NS: %w", err)
				}
				nsOwners[strings.ToLower(owner)] = append(nsOwners[strings.ToLower(owner)], nsName)
			}
			if section == 1 && rrType == 6 && rrClass == dnsClassIN { // SOA
				hasSOA = true
			}
		}
	}

	var cnameTarget string
	var hasCNAME bool
	if len(aliases) > 0 {
		cnameTarget, hasCNAME = followCNAME(queryName, aliases)
	}
	if len(ownedCAA) > 0 {
		owner := queryName
		if hasCNAME {
			owner = strings.ToLower(cnameTarget)
		}
		owners := map[string]struct{}{owner: {}}
		for _, candidate := range ownedCAA {
			if _, ok := owners[candidate.owner]; ok {
				records = append(records, candidate.record)
			}
		}
	}

	result := LookupResult{Records: records}
	if len(records) > 0 {
		result.AnswerName = queryName
		if hasCNAME {
			result.AnswerName = cnameTarget
		}
	}
	if flags&0x0020 != 0 {
		result.DNSSEC = DNSSECSecure
	} else if r.Validating {
		result.DNSSEC = DNSSECInsecure
	} else {
		result.DNSSEC = DNSSECUnknown
	}

	if len(records) == 0 && hasCNAME {
		result.CNAME = cnameTarget
	}
	if len(records) == 0 && result.CNAME == "" {
		for _, dname := range dnames {
			owner := strings.ToLower(strings.TrimSuffix(dname.Owner, "."))
			if owner != "" && strings.HasSuffix(queryName, "."+owner) {
				candidate := dname
				result.DNAME = &candidate
				break
			}
		}
	}

	if len(records) == 0 && result.CNAME == "" && result.DNAME == nil && !authoritative && !hasSOA && len(nsOwners) > 0 {
		bestLength := -1
		for zone, nameServers := range nsOwners {
			if isAncestorOrSelf(zone, queryName) && len(zone) > bestLength {
				bestLength = len(zone)
				result.Delegation = &Delegation{Zone: zone, NameServers: nameServers}
			}
		}
	}
	return result, nil
}

func parseCAARData(rdata []byte) (Record, error) {
	if len(rdata) < 2 {
		return Record{}, errors.New("CAA RDATA is shorter than 2 bytes")
	}
	tagLength := int(rdata[1])
	if tagLength == 0 || 2+tagLength > len(rdata) {
		return Record{}, errors.New("invalid CAA tag length")
	}
	return Record{
		Flags: rdata[0],
		Tag:   string(rdata[2 : 2+tagLength]),
		Value: string(rdata[2+tagLength:]),
	}, nil
}

type ownedCAARecord struct {
	owner  string
	record Record
}

func followCNAME(name string, aliases map[string]string) (string, bool) {
	current := strings.ToLower(name)
	found := false
	for i := 0; i < 16; i++ {
		next, ok := aliases[current]
		if !ok {
			break
		}
		found = true
		current = strings.ToLower(next)
	}
	return current, found
}

func isAncestorOrSelf(zone, name string) bool {
	if zone == name {
		return true
	}
	return strings.HasSuffix(name, "."+zone)
}

func buildCAAQuery(name string) ([]byte, uint16, error) {
	if len(name) > 253 {
		return nil, 0, errors.New("DNS name too long")
	}
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, 0, err
	}
	id := binary.BigEndian.Uint16(idBytes[:])

	var buffer bytes.Buffer
	buffer.Write(idBytes[:])
	buffer.Write([]byte{0x01, 0x00}) // RD
	buffer.Write([]byte{0x00, 0x01}) // QDCOUNT
	buffer.Write([]byte{0x00, 0x00}) // ANCOUNT
	buffer.Write([]byte{0x00, 0x00}) // NSCOUNT
	buffer.Write([]byte{0x00, 0x00}) // ARCOUNT

	labels := strings.Split(name, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return nil, 0, fmt.Errorf("invalid DNS label %q", label)
		}
		buffer.WriteByte(byte(len(label)))
		buffer.WriteString(label)
	}
	buffer.WriteByte(0)
	buffer.Write([]byte{0x01, 0x01}) // TYPE CAA (257)
	buffer.Write([]byte{0x00, 0x01}) // CLASS IN
	return buffer.Bytes(), id, nil
}

func decodeDNSName(message []byte, offset int) (string, int, error) {
	if offset < 0 || offset >= len(message) {
		return "", 0, errors.New("DNS name offset out of range")
	}
	var labels []string
	next := offset
	position := offset
	jumped := false
	visited := map[int]struct{}{}

	for {
		if position >= len(message) {
			return "", 0, errors.New("truncated DNS name")
		}
		if _, ok := visited[position]; ok {
			return "", 0, errors.New("DNS name compression loop")
		}
		visited[position] = struct{}{}

		length := int(message[position])
		switch length & 0xc0 {
		case 0x00:
			position++
			if length == 0 {
				if !jumped {
					next = position
				}
				if len(labels) == 0 {
					return ".", next, nil
				}
				return strings.ToLower(strings.Join(labels, ".")), next, nil
			}
			if length > 63 || position+length > len(message) {
				return "", 0, errors.New("invalid DNS label length")
			}
			labels = append(labels, string(message[position:position+length]))
			position += length
		case 0xc0:
			if position+1 >= len(message) {
				return "", 0, errors.New("truncated DNS compression pointer")
			}
			pointer := int(message[position]&0x3f)<<8 | int(message[position+1])
			if pointer >= len(message) {
				return "", 0, errors.New("DNS compression pointer out of range")
			}
			if !jumped {
				next = position + 2
				jumped = true
			}
			position = pointer
		default:
			return "", 0, errors.New("invalid DNS label type")
		}
	}
}

func addressWithPort(address, defaultPort string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		if port == "" {
			return net.JoinHostPort(host, defaultPort)
		}
		return address
	}
	if ip := net.ParseIP(address); ip != nil {
		return net.JoinHostPort(ip.String(), defaultPort)
	}
	host := strings.Trim(address, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return net.JoinHostPort(host, defaultPort)
	}
	return net.JoinHostPort(address, defaultPort)
}

func systemNameServers() []string {
	content, err := os.ReadFile("/etc/resolv.conf")
	if err == nil {
		var servers []string
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.EqualFold(fields[0], "nameserver") {
				if address := addressWithPort(fields[1], "53"); address != "" {
					servers = append(servers, address)
				}
			}
		}
		if len(servers) > 0 {
			return servers
		}
	}
	return []string{"127.0.0.1:53"}
}

func rcodeName(rcode int) string {
	switch rcode {
	case 1:
		return "FORMERR"
	case 2:
		return "SERVFAIL"
	case 3:
		return "NXDOMAIN"
	case 4:
		return "NOTIMP"
	case 5:
		return "REFUSED"
	default:
		return "RCODE" + strconv.Itoa(rcode)
	}
}

package spf

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type record struct {
	terms    []term
	redirect string
	exp      string
	hasAll   bool
	raw      string
}

type term struct {
	kind   string
	qual   byte
	domain string
	addr   netip.Addr
	prefix netip.Prefix
	cidr4  int
	cidr6  int
}

func parseRecord(raw string) (*record, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "v=spf1") {
		return nil, errors.New("SPF record does not start with v=spf1")
	}
	parsed := &record{raw: raw}
	seenRedirect := false
	seenExp := false
	for _, token := range fields[1:] {
		if token == "" {
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, ":") {
			name, value, _ := strings.Cut(token, "=")
			switch strings.ToLower(name) {
			case "redirect":
				if seenRedirect {
					return nil, errors.New("duplicate redirect modifier")
				}
				if value == "" {
					return nil, errors.New("empty redirect modifier")
				}
				seenRedirect = true
				parsed.redirect = value
			case "exp":
				if seenExp {
					return nil, errors.New("duplicate exp modifier")
				}
				seenExp = true
				parsed.exp = value
			default:
				// Unknown modifiers are ignored by RFC 7208.
			}
			continue
		}

		item, err := parseMechanism(token)
		if err != nil {
			return nil, err
		}
		if item.kind == "all" {
			parsed.hasAll = true
		}
		parsed.terms = append(parsed.terms, item)
	}
	return parsed, nil
}

func parseMechanism(token string) (term, error) {
	item := term{qual: '+'}
	if len(token) == 0 {
		return item, errors.New("empty SPF mechanism")
	}
	switch token[0] {
	case '+', '-', '~', '?':
		item.qual = token[0]
		token = token[1:]
	}
	if token == "" {
		return item, errors.New("empty SPF mechanism")
	}
	name, value, hasValue := strings.Cut(token, ":")
	item.kind = strings.ToLower(name)
	if !hasValue {
		if base, cidr, ok := strings.Cut(name, "/"); ok && cidr != "" {
			item.kind = strings.ToLower(base)
			value = "/" + cidr
			hasValue = true
		}
	}

	switch item.kind {
	case "all":
		if hasValue && value != "" {
			return item, errors.New("all mechanism cannot have a domain")
		}
	case "include", "exists":
		if !hasValue || value == "" {
			return item, fmt.Errorf("%s mechanism requires a domain", item.kind)
		}
		item.domain = value
	case "a", "mx":
		if hasValue {
			domain, cidr4, cidr6, hasCIDR, err := splitDomainCIDR(value)
			if err != nil {
				return item, fmt.Errorf("invalid %s mechanism: %w", item.kind, err)
			}
			item.domain = domain
			item.cidr4, item.cidr6 = cidr4, cidr6
			if !hasCIDR {
				item.cidr4, item.cidr6 = 32, 128
			}
		} else {
			item.cidr4, item.cidr6 = 32, 128
		}
		if item.cidr4 < 0 || item.cidr4 > 32 || item.cidr6 < 0 || item.cidr6 > 128 {
			return item, fmt.Errorf("invalid %s CIDR length", item.kind)
		}
	case "ptr":
		item.domain = value
	case "ip4":
		if !hasValue || value == "" {
			return item, errors.New("ip4 mechanism requires an address")
		}
		addr, prefix, err := parseIPMechanism(value, 4)
		if err != nil {
			return item, err
		}
		item.addr, item.prefix = addr, prefix
	case "ip6":
		if !hasValue || value == "" {
			return item, errors.New("ip6 mechanism requires an address")
		}
		addr, prefix, err := parseIPMechanism(value, 6)
		if err != nil {
			return item, err
		}
		item.addr, item.prefix = addr, prefix
	default:
		return item, fmt.Errorf("unknown SPF mechanism %q", name)
	}
	return item, nil
}

func splitDomainCIDR(value string) (domain string, cidr4, cidr6 int, hasCIDR bool, err error) {
	cidr4, cidr6 = 32, 128
	if value == "" {
		return "", cidr4, cidr6, false, nil
	}
	slash := strings.IndexByte(value, '/')
	if slash < 0 {
		return value, cidr4, cidr6, false, nil
	}
	domain = value[:slash]
	cidrText := value[slash+1:]
	hasCIDR = true
	if cidrText == "" {
		return "", 0, 0, false, errors.New("empty CIDR length")
	}
	if strings.HasPrefix(cidrText, "/") {
		cidr6, err = strconv.Atoi(strings.TrimPrefix(cidrText, "/"))
		if err != nil {
			return "", 0, 0, false, errors.New("invalid IPv6 CIDR length")
		}
		return domain, cidr4, cidr6, true, nil
	}
	if before, after, ok := strings.Cut(cidrText, "//"); ok {
		if before != "" {
			cidr4, err = strconv.Atoi(before)
			if err != nil {
				return "", 0, 0, false, errors.New("invalid IPv4 CIDR length")
			}
		}
		if after != "" {
			cidr6, err = strconv.Atoi(after)
			if err != nil {
				return "", 0, 0, false, errors.New("invalid IPv6 CIDR length")
			}
		}
		return domain, cidr4, cidr6, true, nil
	}
	cidr4, err = strconv.Atoi(cidrText)
	if err != nil {
		return "", 0, 0, false, errors.New("invalid CIDR length")
	}
	// A single length is interpreted for IPv4. IPv6 keeps its default /128.
	cidr6 = 128
	return domain, cidr4, cidr6, true, nil
}

func parseIPMechanism(value string, family int) (netip.Addr, netip.Prefix, error) {
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Addr{}, netip.Prefix{}, fmt.Errorf("invalid ip%d network: %w", family, err)
		}
		addr := prefix.Addr()
		if family == 4 && !addr.Is4() {
			return netip.Addr{}, netip.Prefix{}, errors.New("ip4 mechanism contains an IPv6 address")
		}
		if family == 6 && !addr.Is6() {
			return netip.Addr{}, netip.Prefix{}, errors.New("ip6 mechanism contains an IPv4 address")
		}
		return addr, prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, netip.Prefix{}, fmt.Errorf("invalid ip%d address: %w", family, err)
	}
	addr = addr.Unmap()
	if family == 4 && !addr.Is4() {
		return netip.Addr{}, netip.Prefix{}, errors.New("ip4 mechanism contains an IPv6 address")
	}
	if family == 6 && !addr.Is6() {
		return netip.Addr{}, netip.Prefix{}, errors.New("ip6 mechanism contains an IPv4 address")
	}
	bits := 128
	if addr.Is4() {
		bits = 32
	}
	return addr, netip.PrefixFrom(addr, bits), nil
}

func (s *evalState) evaluateRecord(domain string, rec *record) (Result, string) {
	for _, item := range rec.terms {
		switch item.kind {
		case "all":
			return qualifierResult(item.qual), "matched all"
		case "include":
			if err := s.addTerm(); err != nil {
				return PermError, err.Error()
			}
			target, err := s.expand(item.domain, domain)
			if err != nil {
				return PermError, err.Error()
			}
			result, _, explanation := s.evaluateDomain(target, true)
			switch result {
			case Pass:
				return qualifierResult(item.qual), "include " + target + " passed"
			case TempError, PermError:
				return result, explanation
			default:
				// FAIL, SOFTFAIL, NEUTRAL and NONE continue evaluation.
			}
		case "a":
			if err := s.addTerm(); err != nil {
				return PermError, err.Error()
			}
			target, err := s.expand(item.domain, domain)
			if err != nil {
				return PermError, err.Error()
			}
			if target == "" {
				target = domain
			}
			matched, result, explanation := s.matchA(target, item.cidr4, item.cidr6)
			if result != "" {
				return result, explanation
			}
			if matched {
				return qualifierResult(item.qual), "matched a:" + target
			}
		case "mx":
			if err := s.addTerm(); err != nil {
				return PermError, err.Error()
			}
			target, err := s.expand(item.domain, domain)
			if err != nil {
				return PermError, err.Error()
			}
			if target == "" {
				target = domain
			}
			matched, result, explanation := s.matchMX(target, item.cidr4, item.cidr6)
			if result != "" {
				return result, explanation
			}
			if matched {
				return qualifierResult(item.qual), "matched mx:" + target
			}
		case "ptr":
			if err := s.addTerm(); err != nil {
				return PermError, err.Error()
			}
			target, err := s.expand(item.domain, domain)
			if err != nil {
				return PermError, err.Error()
			}
			matched, result, explanation := s.matchPTR(target, domain)
			if result != "" {
				return result, explanation
			}
			if matched {
				return qualifierResult(item.qual), "matched ptr:" + target
			}
		case "exists":
			if err := s.addTerm(); err != nil {
				return PermError, err.Error()
			}
			target, err := s.expand(item.domain, domain)
			if err != nil {
				return PermError, err.Error()
			}
			// RFC 7208 defines exists in terms of an A query.
			_, void, err := s.lookupIP(target, "ip4")
			if err != nil {
				if errors.Is(err, errVoidLimit) {
					return PermError, err.Error()
				}
				return TempError, err.Error()
			}
			if !void {
				return qualifierResult(item.qual), "matched exists:" + target
			}
		case "ip4", "ip6":
			if item.prefix.Contains(s.ip) {
				return qualifierResult(item.qual), "matched " + item.kind + ":" + item.addr.String()
			}
		}
	}

	if rec.redirect != "" && !rec.hasAll {
		if err := s.addTerm(); err != nil {
			return PermError, err.Error()
		}
		target, err := s.expand(rec.redirect, domain)
		if err != nil {
			return PermError, err.Error()
		}
		result, _, explanation := s.evaluateDomain(target, true)
		return result, "redirect " + target + ": " + explanation
	}
	return Neutral, "no SPF mechanism matched"
}

func (s *evalState) matchA(domain string, cidr4, cidr6 int) (bool, Result, string) {
	network := "ip6"
	bits := cidr6
	if s.ip.Is4() {
		network = "ip4"
		bits = cidr4
	}
	ips, void, err := s.lookupIP(domain, network)
	if err != nil {
		if errors.Is(err, errVoidLimit) {
			return false, PermError, err.Error()
		}
		return false, TempError, err.Error()
	}
	if void {
		return false, "", ""
	}
	for _, ip := range ips {
		prefix := netip.PrefixFrom(ip, bits).Masked()
		if prefix.Contains(s.ip) {
			return true, "", ""
		}
	}
	return false, "", ""
}

func (s *evalState) matchMX(domain string, cidr4, cidr6 int) (bool, Result, string) {
	records, void, err := s.lookupMX(domain)
	if err != nil {
		if errors.Is(err, errVoidLimit) {
			return false, PermError, err.Error()
		}
		return false, TempError, err.Error()
	}
	if void {
		return false, "", ""
	}
	for _, mx := range records {
		matched, result, explanation := s.matchA(strings.TrimSuffix(mx.Host, "."), cidr4, cidr6)
		if result != "" {
			return false, result, explanation
		}
		if matched {
			return true, "", ""
		}
	}
	return false, "", ""
}

func (s *evalState) matchPTR(domain, currentDomain string) (bool, Result, string) {
	names, void, err := s.lookupAddr(s.ip.String())
	if err != nil {
		if errors.Is(err, errVoidLimit) {
			return false, PermError, err.Error()
		}
		return false, TempError, err.Error()
	}
	if void {
		return false, "", ""
	}
	if domain == "" {
		domain = currentDomain
	}
	domain = normalizeDomain(domain)
	for _, name := range names {
		name = normalizeDomain(name)
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return true, "", ""
		}
	}
	return false, "", ""
}

func qualifierResult(qual byte) Result {
	switch qual {
	case '-':
		return Fail
	case '~':
		return SoftFail
	case '?':
		return Neutral
	default:
		return Pass
	}
}

func (s *evalState) expand(spec, domain string) (string, error) {
	if spec == "" {
		return "", nil
	}
	if !strings.Contains(spec, "%") {
		return normalizeDomain(spec), nil
	}
	local, senderDomain := splitSender(s.sender)
	_ = senderDomain
	var out strings.Builder
	for i := 0; i < len(spec); i++ {
		if spec[i] != '%' {
			out.WriteByte(spec[i])
			continue
		}
		if i+1 >= len(spec) {
			return "", errors.New("trailing % in SPF macro")
		}
		next := spec[i+1]
		switch next {
		case '%':
			out.WriteByte('%')
			i++
		case '_':
			out.WriteByte(' ')
			i++
		case '-':
			out.WriteString("%20")
			i++
		case '{':
			end := strings.IndexByte(spec[i+2:], '}')
			if end < 0 {
				return "", errors.New("unterminated SPF macro")
			}
			body := spec[i+2 : i+2+end]
			value, err := s.macroValue(body, domain, local)
			if err != nil {
				return "", err
			}
			out.WriteString(value)
			i += end + 2
		default:
			return "", fmt.Errorf("invalid SPF macro %%%c", next)
		}
	}
	return normalizeDomain(out.String()), nil
}

func (s *evalState) macroValue(body, domain, local string) (string, error) {
	if body == "" {
		return "", errors.New("empty SPF macro")
	}
	letter := body[0]
	transform := body[1:]
	var value string
	switch letter {
	case 's':
		value = s.sender
	case 'l':
		value = local
	case 'o':
		if local != "" {
			value = local
		} else {
			value = "postmaster"
		}
	case 'd':
		value = domain
	case 'h':
		value = s.helo
	case 'i':
		if s.ip.Is4() {
			value = s.ip.String()
		} else {
			value = ipv6Nibbles(s.ip)
		}
	case 'v':
		if s.ip.Is4() {
			value = "in-addr"
		} else {
			value = "ip6"
		}
	case 't':
		value = strconv.FormatInt(s.now.Unix(), 10)
	case 'p':
		value = "unknown"
	default:
		return "", fmt.Errorf("unknown SPF macro letter %%%c", letter)
	}

	if strings.Contains(transform, "r") {
		labels := strings.Split(value, ".")
		for left, right := 0, len(labels)-1; left < right; left, right = left+1, right-1 {
			labels[left], labels[right] = labels[right], labels[left]
		}
		value = strings.Join(labels, ".")
		transform = strings.ReplaceAll(transform, "r", "")
	}
	if transform != "" {
		count, err := strconv.Atoi(transform)
		if err != nil || count < 0 {
			return "", errors.New("invalid SPF macro transformer")
		}
		labels := strings.Split(value, ".")
		if count < len(labels) {
			value = strings.Join(labels[len(labels)-count:], ".")
		}
	}
	return value, nil
}

func splitSender(sender string) (local, domain string) {
	at := strings.LastIndexByte(sender, '@')
	if at < 0 {
		return "", ""
	}
	return sender[:at], sender[at+1:]
}

func ipv6Nibbles(addr netip.Addr) string {
	bytes := addr.As16()
	const hex = "0123456789abcdef"
	var out strings.Builder
	for i, b := range bytes {
		if i > 0 {
			out.WriteByte('.')
		}
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&0x0f])
	}
	return out.String()
}

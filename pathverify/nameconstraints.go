package pathverify

import (
	"crypto/x509"
	"net"
	"net/url"
	"strings"
)

// ncLayer 收集一层 CA 施加的名称约束；多层之间 permitted 取交集、
// excluded 取并集，由各 check* 函数在遍历 layers 时体现。
type ncLayer struct {
	permittedDNS   []string
	excludedDNS    []string
	permittedIPs   []*net.IPNet
	excludedIPs    []*net.IPNet
	permittedEmail []string
	excludedEmail  []string
	permittedURIs  []string
	excludedURIs   []string
}

func buildNCLayer(ca *x509.Certificate) ncLayer {
	return ncLayer{
		permittedDNS:   ca.PermittedDNSDomains,
		excludedDNS:    ca.ExcludedDNSDomains,
		permittedIPs:   ca.PermittedIPRanges,
		excludedIPs:    ca.ExcludedIPRanges,
		permittedEmail: ca.PermittedEmailAddresses,
		excludedEmail:  ca.ExcludedEmailAddresses,
		permittedURIs:  ca.PermittedURIDomains,
		excludedURIs:   ca.ExcludedURIDomains,
	}
}

func verifyNameConstraints(chain []*x509.Certificate) *Error {
	leaf := chain[len(chain)-1]

	var layers []ncLayer
	for _, ca := range chain[:len(chain)-1] {
		layers = append(layers, buildNCLayer(ca))
	}

	dnsNames := leaf.DNSNames
	// 无任何 SAN 时的传统回退：仅当 CN 本身是合法 DNS 名称时才参与约束/主机名匹配。
	if len(dnsNames) == 0 && len(leaf.IPAddresses) == 0 &&
		len(leaf.EmailAddresses) == 0 && len(leaf.URIs) == 0 &&
		looksLikeDNSCN(leaf.Subject.CommonName) {
		dnsNames = []string{leaf.Subject.CommonName}
	}

	for _, name := range dnsNames {
		if err := checkDNSConstraints(name, layers); err != nil {
			return err
		}
	}
	for _, ip := range leaf.IPAddresses {
		if err := checkIPConstraints(ip, layers); err != nil {
			return err
		}
	}
	for _, mail := range leaf.EmailAddresses {
		if err := checkEmailConstraints(mail, layers); err != nil {
			return err
		}
	}
	for _, u := range leaf.URIs {
		if err := checkURIConstraints(u.String(), layers); err != nil {
			return err
		}
	}
	return nil
}

// dnsInConstraint 严格遵循 RFC 5280 4.2.1.10 的 DNS 约束边界：
//   - "host.example.com" 仅精确匹配该主机；
//   - ".example.com" 匹配 example.com 的任意子域（如 a.example.com、a.b.example.com），
//     但不匹配 example.com 自身。
func dnsInConstraint(name, constraint string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	constraint = strings.ToLower(strings.TrimSuffix(constraint, "."))
	if constraint == "" {
		return true
	}
	if strings.HasPrefix(constraint, ".") {
		suffix := constraint[1:]
		return strings.HasSuffix(name, "."+suffix)
	}
	return name == constraint
}

func checkDNSConstraints(name string, layers []ncLayer) *Error {
	for _, l := range layers {
		for _, ex := range l.excludedDNS { // excluded 逐层并集：命中任一即拒绝。
			if dnsInConstraint(name, ex) {
				return fail(ReasonNameConstraint,
					"DNS name %q matches excluded subtree %q", name, ex)
			}
		}
		if len(l.permittedDNS) > 0 {
			matched := false
			for _, p := range l.permittedDNS { // permitted 逐层交集：每层都必须放行。
				if dnsInConstraint(name, p) {
					matched = true
					break
				}
			}
			if !matched {
				return fail(ReasonNameConstraint,
					"DNS name %q is not within permitted subtrees %v of a chain CA",
					name, l.permittedDNS)
			}
		}
	}
	return nil
}

func checkIPConstraints(ip net.IP, layers []ncLayer) *Error {
	for _, l := range layers {
		for _, ex := range l.excludedIPs {
			if ex.Contains(ip) {
				return fail(ReasonNameConstraint,
					"IP %q matches excluded range %s", ip.String(), ex.String())
			}
		}
		if len(l.permittedIPs) > 0 {
			matched := false
			for _, p := range l.permittedIPs {
				if p.Contains(ip) {
					matched = true
					break
				}
			}
			if !matched {
				return fail(ReasonNameConstraint,
					"IP %q is not within permitted CIDR ranges of a chain CA", ip.String())
			}
		}
	}
	return nil
}

// emailInConstraint 实现 RFC 5280 邮件约束：
//   - "user@example.com" 精确匹配；
//   - "example.com" 匹配该域及其任意子域邮箱；
//   - ".example.com" 仅匹配子域邮箱，不含 example.com 自身。
func emailInConstraint(mail, constraint string) bool {
	mail = strings.ToLower(mail)
	constraint = strings.ToLower(strings.TrimSpace(constraint))
	if constraint == "" {
		return true
	}
	if strings.Contains(constraint, "@") {
		return mail == constraint
	}
	at := strings.LastIndex(mail, "@")
	if at < 0 {
		return false
	}
	domain := mail[at+1:]
	if strings.HasPrefix(constraint, ".") {
		return strings.HasSuffix(domain, constraint)
	}
	return domain == constraint || strings.HasSuffix(domain, "."+constraint)
}

func checkEmailConstraints(mail string, layers []ncLayer) *Error {
	for _, l := range layers {
		for _, ex := range l.excludedEmail {
			if emailInConstraint(mail, ex) {
				return fail(ReasonNameConstraint,
					"email %q matches excluded subtree %q", mail, ex)
			}
		}
		if len(l.permittedEmail) > 0 {
			matched := false
			for _, p := range l.permittedEmail {
				if emailInConstraint(mail, p) {
					matched = true
					break
				}
			}
			if !matched {
				return fail(ReasonNameConstraint,
					"email %q is not within permitted email subtrees of a chain CA", mail)
			}
		}
	}
	return nil
}

// uriInConstraint 对 URI 的 host 部分施加与 DNS 相同的子域边界规则。
func uriInConstraint(rawURI, constraint string) bool {
	u, err := url.Parse(rawURI)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return dnsInConstraint(host, constraint)
}

func checkURIConstraints(rawURI string, layers []ncLayer) *Error {
	for _, l := range layers {
		for _, ex := range l.excludedURIs {
			if uriInConstraint(rawURI, ex) {
				return fail(ReasonNameConstraint,
					"URI %q matches excluded subtree %q", rawURI, ex)
			}
		}
		if len(l.permittedURIs) > 0 {
			matched := false
			for _, p := range l.permittedURIs {
				if uriInConstraint(rawURI, p) {
					matched = true
					break
				}
			}
			if !matched {
				return fail(ReasonNameConstraint,
					"URI %q is not within permitted URI subtrees of a chain CA", rawURI)
			}
		}
	}
	return nil
}

// looksLikeDNSCN 判断 CN 是否为传统主机名（不含空格/逗号等 DN 特征字符），
// 仅在完全没有 SAN 时才回退使用 CN。
func looksLikeDNSCN(cn string) bool {
	cn = strings.TrimSpace(cn)
	if cn == "" || strings.ContainsAny(cn, " ,@/") {
		return false
	}
	return strings.Contains(cn, ".")
}

// verifyHostname 实现“SAN 压倒 CN”：只要叶证书包含至少一个 DNS SAN，
// 主机名匹配就只在 SAN 集合内进行，彻底忽略 Subject CN；
// 完全没有 SAN 时才回退到 CN。
func verifyHostname(leaf *x509.Certificate, dnsName string) *Error {
	if len(leaf.DNSNames) > 0 {
		for _, san := range leaf.DNSNames {
			if hostMatch(strings.ToLower(dnsName), strings.ToLower(san)) {
				return nil
			}
		}
		return fail(ReasonHostname,
			"hostname %q not matched by any DNS SAN %v (Subject CN ignored)",
			dnsName, leaf.DNSNames)
	}
	if looksLikeDNSCN(leaf.Subject.CommonName) &&
		hostMatch(strings.ToLower(dnsName), strings.ToLower(leaf.Subject.CommonName)) {
		return nil
	}
	return fail(ReasonHostname,
		"hostname %q not matched (no DNS SAN and no usable Subject CN)", dnsName)
}

// hostMatch 支持最左侧标签通配符（*.example.com），通配符恰好匹配一个标签；
// 其余名称按小写精确匹配。
func hostMatch(want, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return want == pattern
	}
	labels := strings.Split(pattern, ".")
	if labels[0] != "*" || strings.Contains(strings.Join(labels[1:], "."), "*") {
		return want == pattern // 非最左标签通配不符合规范，按精确匹配处理。
	}
	wantLabels := strings.Split(want, ".")
	if len(wantLabels) != len(labels) {
		return false // 通配符只匹配一个标签，标签数量必须一致。
	}
	for i := 1; i < len(labels); i++ {
		if wantLabels[i] != labels[i] {
			return false
		}
	}
	return wantLabels[0] != ""
}

#include "pki/names.hpp"

#include <algorithm>
#include <arpa/inet.h>
#include <cctype>
#include <netinet/in.h>

namespace pki::names {

namespace {

bool ends_with(const std::string& value, const std::string& suffix) {
    return value.size() >= suffix.size() &&
           value.compare(value.size() - suffix.size(), suffix.size(), suffix) == 0;
}

std::string strip_trailing_dots(std::string value) {
    while (value.size() > 1 && value.back() == '.') {
        value.pop_back();
    }
    return value;
}

struct DnsConstraintParts {
    std::string zone;
    bool include_base = true;
};

DnsConstraintParts parse_dns_constraint(const std::string& constraint) {
    std::string normalized = ascii_lower(constraint);
    DnsConstraintParts parts;
    if (!normalized.empty() && normalized[0] == '.') {
        parts.include_base = false;
        normalized.erase(normalized.begin());
    }
    parts.zone = strip_trailing_dots(std::move(normalized));
    return parts;
}

bool dns_zone_contains(const std::string& outer, const std::string& inner) {
    if (outer == inner) {
        return true;
    }
    const std::string suffix = "." + outer;
    return inner.size() > suffix.size() && ends_with(inner, suffix);
}

bool ip_mask_covers(const der::Bytes& outer_mask, const der::Bytes& inner_mask) {
    if (outer_mask.size() != inner_mask.size()) {
        return false;
    }
    for (std::size_t i = 0; i < outer_mask.size(); ++i) {
        if ((inner_mask[i] & outer_mask[i]) != outer_mask[i]) {
            return false;
        }
    }
    return true;
}

std::optional<GeneralName> intersect_dns(const GeneralName& left, const GeneralName& right) {
    DnsConstraintParts a = parse_dns_constraint(left.text);
    DnsConstraintParts b = parse_dns_constraint(right.text);
    if (a.zone.empty() || b.zone.empty()) {
        return std::nullopt;
    }

    GeneralName result;
    result.kind = GeneralNameKind::Dns;

    if (a.zone == b.zone) {
        const bool include_base = a.include_base && b.include_base;
        result.text = include_base ? a.zone : "." + a.zone;
        return result;
    }
    if (dns_zone_contains(a.zone, b.zone)) {
        return right;
    }
    if (dns_zone_contains(b.zone, a.zone)) {
        return left;
    }
    return std::nullopt;
}

bool email_constraint_covers(const std::string& outer_text, const GeneralName& inner) {
    if (inner.kind == GeneralNameKind::Rfc822) {
        if (inner.text.find('@') != std::string::npos) {
            return email_matches_constraint(inner.text, outer_text);
        }

        const std::string inner_host = parse_dns_constraint(inner.text).zone;
        const DnsConstraintParts outer = parse_dns_constraint(outer_text);
        if (outer_text.find('@') != std::string::npos) {
            return false;
        }
        if (outer.include_base) {
            return ascii_lower(inner.text) == outer.zone;
        }
        return dns_zone_contains(outer.zone, inner_host) && inner_host != outer.zone;
    }
    return false;
}

std::optional<GeneralName> intersect_email(const GeneralName& left, const GeneralName& right) {
    if (email_constraint_covers(left.text, right)) {
        return right;
    }
    if (email_constraint_covers(right.text, left)) {
        return left;
    }
    return std::nullopt;
}

std::optional<GeneralName> intersect_ip(const GeneralName& left, const GeneralName& right) {
    if (left.ip_address.size() != right.ip_address.size() ||
        left.ip_mask.size() != right.ip_mask.size() || left.ip_mask.empty()) {
        return std::nullopt;
    }

    const bool left_covers_right =
        ip_mask_covers(left.ip_mask, right.ip_mask) &&
        ip_matches_constraint(right.ip_address, left.ip_address, left.ip_mask);
    if (left_covers_right) {
        return right;
    }

    const bool right_covers_left =
        ip_mask_covers(right.ip_mask, left.ip_mask) &&
        ip_matches_constraint(left.ip_address, right.ip_address, right.ip_mask);
    if (right_covers_left) {
        return left;
    }
    return std::nullopt;
}

std::optional<GeneralName> intersect_directory(const GeneralName& left,
                                               const GeneralName& right) {
    if (directory_matches_constraint(right.directory_name, left.directory_name)) {
        return left;
    }
    if (directory_matches_constraint(left.directory_name, right.directory_name)) {
        return right;
    }
    return std::nullopt;
}

} // namespace

std::string ascii_lower(std::string value) {
    for (char& ch : value) {
        if (ch >= 'A' && ch <= 'Z') {
            ch = static_cast<char>(ch - 'A' + 'a');
        }
    }
    return value;
}

bool looks_like_dns_name(const std::string& value) {
    if (value.empty() || value.size() > 253 || value.find_first_of(" \t\r\n/@:?#") != std::string::npos) {
        return false;
    }
    std::string normalized = value;
    if (!normalized.empty() && normalized.back() == '.') {
        normalized.pop_back();
    }
    if (normalized.empty()) {
        return false;
    }
    for (char ch : normalized) {
        const bool ok = (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
                        (ch >= '0' && ch <= '9') || ch == '-' || ch == '.' || ch == '*' || ch == '_';
        if (!ok) {
            return false;
        }
    }
    return true;
}

bool dns_matches_constraint(const std::string& name, const std::string& constraint) {
    const std::string normalized_name = strip_trailing_dots(ascii_lower(name));
    DnsConstraintParts rule = parse_dns_constraint(constraint);
    if (normalized_name.empty() || rule.zone.empty()) {
        return false;
    }
    if (!rule.include_base) {
        const std::string suffix = "." + rule.zone;
        return normalized_name.size() > suffix.size() && ends_with(normalized_name, suffix);
    }
    return normalized_name == rule.zone || dns_zone_contains(rule.zone, normalized_name);
}

bool email_matches_constraint(const std::string& email, const std::string& constraint) {
    const std::size_t email_at = email.find('@');
    if (email_at == std::string::npos || email.rfind('@') != email_at || email_at == 0 ||
        email_at + 1 >= email.size()) {
        return false;
    }
    const std::string email_local = email.substr(0, email_at);
    const std::string email_domain = ascii_lower(email.substr(email_at + 1));

    const std::size_t constraint_at = constraint.find('@');
    if (constraint_at != std::string::npos) {
        if (constraint.rfind('@') != constraint_at || constraint_at == 0 ||
            constraint_at + 1 >= constraint.size()) {
            return false;
        }
        const std::string constraint_local = constraint.substr(0, constraint_at);
        const std::string constraint_domain = ascii_lower(constraint.substr(constraint_at + 1));
        return email_local == constraint_local && email_domain == constraint_domain;
    }

    if (!constraint.empty() && constraint[0] == '.') {
        return dns_matches_constraint(email_domain, constraint);
    }
    return email_domain == ascii_lower(constraint);
}

bool ip_matches_constraint(const std::vector<std::uint8_t>& address,
                           const std::vector<std::uint8_t>& constraint_address,
                           const std::vector<std::uint8_t>& mask) {
    if (address.size() != constraint_address.size() || address.size() != mask.size()) {
        return false;
    }
    for (std::size_t i = 0; i < address.size(); ++i) {
        if ((address[i] & mask[i]) != (constraint_address[i] & mask[i])) {
            return false;
        }
    }
    return true;
}

bool ip_matches_constraint(const der::Bytes& address, const GeneralName& constraint) {
    der::Bytes constraint_address = constraint.ip_address;
    der::Bytes mask = constraint.ip_mask;
    if (mask.empty() && constraint.ip.size() == address.size() * 2) {
        const std::size_t half = constraint.ip.size() / 2;
        constraint_address.assign(constraint.ip.begin(),
                                  constraint.ip.begin() + static_cast<std::ptrdiff_t>(half));
        mask.assign(constraint.ip.begin() + static_cast<std::ptrdiff_t>(half), constraint.ip.end());
    }
    return ip_matches_constraint(address, constraint_address, mask);
}

bool uri_matches_constraint(const std::string& uri, const std::string& constraint) {
    std::string authority = uri;
    const std::size_t scheme = authority.find("://");
    if (scheme != std::string::npos) {
        authority = authority.substr(scheme + 3);
    }
    const std::size_t path = authority.find_first_of("/?#");
    if (path != std::string::npos) {
        authority = authority.substr(0, path);
    }
    const std::size_t userinfo = authority.rfind('@');
    if (userinfo != std::string::npos) {
        authority = authority.substr(userinfo + 1);
    }

    std::string host;
    if (!authority.empty() && authority[0] == '[') {
        const std::size_t close = authority.find(']');
        if (close == std::string::npos) {
            return false;
        }
        host = authority.substr(1, close - 1);
    } else {
        const std::size_t port = authority.rfind(':');
        if (port != std::string::npos && authority.find(':') == port) {
            authority = authority.substr(0, port);
        }
        host = authority;
    }
    return dns_matches_constraint(host, constraint);
}

bool directory_matches_constraint(const der::Bytes& directory_name,
                                  const der::Bytes& constraint_name) {
    try {
        der::Element name = der::parse_one(directory_name);
        der::Element prefix = der::parse_one(constraint_name);
        std::vector<der::Element> name_rdns = der::parse_children(name);
        std::vector<der::Element> prefix_rdns = der::parse_children(prefix);
        if (prefix_rdns.size() > name_rdns.size()) {
            return false;
        }
        for (std::size_t i = 0; i < prefix_rdns.size(); ++i) {
            if (prefix_rdns[i].raw.to_bytes() != name_rdns[i].raw.to_bytes()) {
                return false;
            }
        }
        return true;
    } catch (...) {
        return false;
    }
}

bool general_name_matches(const GeneralName& name, const GeneralName& constraint) {
    if (name.kind != constraint.kind) {
        return false;
    }
    switch (name.kind) {
        case GeneralNameKind::Dns:
            return dns_matches_constraint(name.text, constraint.text);
        case GeneralNameKind::Rfc822:
            return email_matches_constraint(name.text, constraint.text);
        case GeneralNameKind::Ip:
            return ip_matches_constraint(name.ip, constraint);
        case GeneralNameKind::Uri:
            return uri_matches_constraint(name.text, constraint.text);
        case GeneralNameKind::Directory:
            return directory_matches_constraint(name.directory_name, constraint.directory_name);
        case GeneralNameKind::RegisteredId:
            return name.text == constraint.text;
        default:
            return false;
    }
}

std::optional<GeneralName> intersect_general_constraints(const GeneralName& left,
                                                          const GeneralName& right) {
    if (left.kind != right.kind) {
        return std::nullopt;
    }
    switch (left.kind) {
        case GeneralNameKind::Dns:
            return intersect_dns(left, right);
        case GeneralNameKind::Rfc822:
            return intersect_email(left, right);
        case GeneralNameKind::Ip:
            return intersect_ip(left, right);
        case GeneralNameKind::Directory:
            return intersect_directory(left, right);
        case GeneralNameKind::RegisteredId:
            return left.text == right.text ? std::optional<GeneralName>(left) : std::nullopt;
        default:
            return std::nullopt;
    }
}

bool is_permitted(const std::vector<GeneralName>& names,
                  const std::vector<GeneralName>& permitted) {
    for (const GeneralName& name : names) {
        bool has_same_kind_rule = false;
        bool matched = false;
        for (const GeneralName& rule : permitted) {
            if (rule.kind != name.kind) {
                continue;
            }
            has_same_kind_rule = true;
            if (general_name_matches(name, rule)) {
                matched = true;
                break;
            }
        }
        if (has_same_kind_rule && !matched) {
            return false;
        }
    }
    return true;
}

bool is_excluded(const std::vector<GeneralName>& names,
                 const std::vector<GeneralName>& excluded) {
    for (const GeneralName& name : names) {
        for (const GeneralName& rule : excluded) {
            if (general_name_matches(name, rule)) {
                return true;
            }
        }
    }
    return false;
}

der::Bytes parse_ip_address(const std::string& text) {
    der::Bytes result;
    if (text.find(':') != std::string::npos) {
        struct in6_addr address {};
        if (inet_pton(AF_INET6, text.c_str(), &address) != 1) {
            throw std::invalid_argument("invalid IPv6 address");
        }
        const auto* bytes = reinterpret_cast<const std::uint8_t*>(&address);
        result.assign(bytes, bytes + sizeof(address));
        return result;
    }

    struct in_addr address {};
    if (inet_pton(AF_INET, text.c_str(), &address) != 1) {
        throw std::invalid_argument("invalid IPv4 address");
    }
    const auto* bytes = reinterpret_cast<const std::uint8_t*>(&address);
    result.assign(bytes, bytes + sizeof(address));
    return result;
}

der::Bytes ip_mask(std::size_t address_bytes, int prefix) {
    const int max_prefix = static_cast<int>(address_bytes * 8U);
    if (prefix < 0 || prefix > max_prefix) {
        throw std::out_of_range("CIDR prefix is outside address family range");
    }
    der::Bytes mask(address_bytes, 0);
    for (int bit = 0; bit < prefix; ++bit) {
        const std::size_t byte_index = static_cast<std::size_t>(bit / 8);
        const unsigned bit_index = static_cast<unsigned>(7 - (bit % 8));
        mask[byte_index] = static_cast<der::Byte>(mask[byte_index] | (1U << bit_index));
    }
    return mask;
}

IpCidr parse_ip_cidr(const std::string& cidr) {
    const std::size_t slash = cidr.find('/');
    if (slash == std::string::npos || slash + 1 >= cidr.size()) {
        throw std::invalid_argument("CIDR requires /prefix");
    }
    const std::string address_text = cidr.substr(0, slash);
    const std::string prefix_text = cidr.substr(slash + 1);
    if (!std::all_of(prefix_text.begin(), prefix_text.end(),
                     [](char ch) { return ch >= '0' && ch <= '9'; })) {
        throw std::invalid_argument("CIDR prefix must be decimal");
    }
    const int prefix = std::stoi(prefix_text);
    der::Bytes address = parse_ip_address(address_text);
    const int max_prefix = static_cast<int>(address.size() * 8U);
    if (prefix < 0 || prefix > max_prefix) {
        throw std::out_of_range("CIDR prefix is outside address family range");
    }

    IpCidr result;
    result.address = address;
    result.prefix = prefix;
    result.mask = ip_mask(address.size(), prefix);
    return result;
}

} // namespace pki::names

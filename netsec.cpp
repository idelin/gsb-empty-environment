#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <limits>
#include <utility>

namespace netsec {
namespace {

constexpr std::size_t ipv4_bytes = 4;
constexpr std::size_t ipv6_bytes = 16;

bool is_digit(char c) noexcept {
    return c >= '0' && c <= '9';
}

bool is_hex_digit(char c) noexcept {
    return is_digit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
}

std::optional<std::uint32_t> parse_decimal(std::string_view text,
                                           std::uint32_t maximum) {
    if (text.empty() || text.size() > 10) {
        return std::nullopt;
    }
    std::uint64_t value = 0;
    for (char c : text) {
        if (!is_digit(c)) {
            return std::nullopt;
        }
        value = value * 10 + static_cast<std::uint64_t>(c - '0');
        if (value > maximum) {
            return std::nullopt;
        }
    }
    return static_cast<std::uint32_t>(value);
}

std::vector<std::string_view> split(std::string_view text, char delimiter) {
    std::vector<std::string_view> parts;
    std::size_t begin = 0;
    while (true) {
        const std::size_t end = text.find(delimiter, begin);
        if (end == std::string_view::npos) {
            parts.push_back(text.substr(begin));
            break;
        }
        parts.push_back(text.substr(begin, end - begin));
        begin = end + 1;
    }
    return parts;
}

std::string_view trim(std::string_view text) {
    while (!text.empty() &&
           (text.front() == ' ' || text.front() == '\t' ||
            text.front() == '\r' || text.front() == '\n')) {
        text.remove_prefix(1);
    }
    while (!text.empty() &&
           (text.back() == ' ' || text.back() == '\t' ||
            text.back() == '\r' || text.back() == '\n')) {
        text.remove_suffix(1);
    }
    return text;
}

std::optional<std::array<std::uint8_t, ipv4_bytes>> parse_ipv4(
    std::string_view text) {
    const auto parts = split(text, '.');
    if (parts.size() != ipv4_bytes) {
        return std::nullopt;
    }

    std::array<std::uint8_t, ipv4_bytes> result{};
    for (std::size_t i = 0; i < ipv4_bytes; ++i) {
        const auto value = parse_decimal(parts[i], 255);
        if (!value) {
            return std::nullopt;
        }
        result[i] = static_cast<std::uint8_t>(*value);
    }
    return result;
}

std::optional<std::uint16_t> parse_hextet(std::string_view text) {
    if (text.empty() || text.size() > 4) {
        return std::nullopt;
    }
    std::uint32_t value = 0;
    for (char c : text) {
        if (!is_hex_digit(c)) {
            return std::nullopt;
        }
        value <<= 4;
        if (c >= '0' && c <= '9') {
            value |= static_cast<std::uint32_t>(c - '0');
        } else if (c >= 'a' && c <= 'f') {
            value |= static_cast<std::uint32_t>(c - 'a' + 10);
        } else {
            value |= static_cast<std::uint32_t>(c - 'A' + 10);
        }
    }
    return static_cast<std::uint16_t>(value);
}

bool append_ipv6_part(std::string_view text,
                      std::vector<std::uint16_t>& words) {
    if (text.empty()) {
        return true;
    }

    const auto pieces = split(text, ':');
    for (std::size_t i = 0; i < pieces.size(); ++i) {
        const auto& piece = pieces[i];
        if (piece.empty()) {
            return false;
        }
        if (piece.find('.') != std::string_view::npos) {
            if (i + 1 != pieces.size()) {
                return false;
            }
            const auto v4 = parse_ipv4(piece);
            if (!v4) {
                return false;
            }
            words.push_back(static_cast<std::uint16_t>(
                (static_cast<std::uint16_t>((*v4)[0]) << 8) | (*v4)[1]));
            words.push_back(static_cast<std::uint16_t>(
                (static_cast<std::uint16_t>((*v4)[2]) << 8) | (*v4)[3]));
        } else {
            const auto word = parse_hextet(piece);
            if (!word) {
                return false;
            }
            words.push_back(*word);
        }
    }
    return true;
}

std::optional<std::array<std::uint8_t, ipv6_bytes>> parse_ipv6(
    std::string_view text) {
    if (text.size() >= 2 && text.front() == '[' && text.back() == ']') {
        text.remove_prefix(1);
        text.remove_suffix(1);
    }
    if (text.empty() || text.find('%') != std::string_view::npos) {
        return std::nullopt;
    }

    const std::size_t compression = text.find("::");
    if (compression != std::string_view::npos &&
        text.find("::", compression + 2) != std::string_view::npos) {
        return std::nullopt;
    }

    std::vector<std::uint16_t> words;
    if (compression == std::string_view::npos) {
        if (!append_ipv6_part(text, words) || words.size() != 8) {
            return std::nullopt;
        }
    } else {
        std::vector<std::uint16_t> left;
        std::vector<std::uint16_t> right;
        if (!append_ipv6_part(text.substr(0, compression), left) ||
            !append_ipv6_part(text.substr(compression + 2), right)) {
            return std::nullopt;
        }
        if (left.size() + right.size() >= 8) {
            return std::nullopt;
        }
        words.reserve(8);
        words.insert(words.end(), left.begin(), left.end());
        words.resize(8 - right.size(), 0);
        words.insert(words.end(), right.begin(), right.end());
    }

    std::array<std::uint8_t, ipv6_bytes> result{};
    for (std::size_t i = 0; i < 8; ++i) {
        result[i * 2] = static_cast<std::uint8_t>(words[i] >> 8);
        result[i * 2 + 1] = static_cast<std::uint8_t>(words[i] & 0xff);
    }
    return result;
}

std::array<std::uint8_t, 4> v4_bytes(const IpAddress& address) {
    return {address.bytes[0], address.bytes[1], address.bytes[2],
            address.bytes[3]};
}

std::uint32_t ipv4_value(const IpAddress& address) noexcept {
    return (static_cast<std::uint32_t>(address.bytes[0]) << 24) |
           (static_cast<std::uint32_t>(address.bytes[1]) << 16) |
           (static_cast<std::uint32_t>(address.bytes[2]) << 8) |
           static_cast<std::uint32_t>(address.bytes[3]);
}

bool ipv4_in(std::uint32_t value, std::uint32_t network,
             unsigned prefix) noexcept {
    if (prefix == 0) {
        return true;
    }
    const std::uint32_t mask =
        prefix == 32 ? 0xffffffffu : (0xffffffffu << (32 - prefix));
    return (value & mask) == (network & mask);
}

AddressScope classify_ipv4(std::uint32_t value) noexcept {
    if (ipv4_in(value, 0x00000000u, 8)) {  // 0.0.0.0/8, "this network"
        return AddressScope::unspecified;
    }
    if (ipv4_in(value, 0x0a000000u, 8)) {  // 10.0.0.0/8
        return AddressScope::private_network;
    }
    if (ipv4_in(value, 0x64400000u, 10)) {  // 100.64.0.0/10, CGNAT
        return AddressScope::shared_address;
    }
    if (ipv4_in(value, 0x7f000000u, 8)) {  // 127.0.0.0/8
        return AddressScope::loopback;
    }
    if (ipv4_in(value, 0xa9fe0000u, 16)) {  // 169.254.0.0/16
        return AddressScope::link_local;
    }
    if (ipv4_in(value, 0xac100000u, 12)) {  // 172.16.0.0/12
        return AddressScope::private_network;
    }
    if (ipv4_in(value, 0xc0a80000u, 16)) {  // 192.168.0.0/16
        return AddressScope::private_network;
    }
    if (ipv4_in(value, 0xe0000000u, 4)) {  // 224.0.0.0/4
        return AddressScope::multicast;
    }
    if (ipv4_in(value, 0xf0000000u, 4)) {  // 240.0.0.0/4
        return AddressScope::reserved;
    }
    return AddressScope::global;
}

bool bytes_are_zero(const std::uint8_t* data, std::size_t count) noexcept {
    for (std::size_t i = 0; i < count; ++i) {
        if (data[i] != 0) {
            return false;
        }
    }
    return true;
}

IpAddress masked_network(const IpAddress& address, std::uint8_t prefix) {
    IpAddress result = address;
    const std::size_t bytes = address.is_v4() ? ipv4_bytes : ipv6_bytes;
    const std::size_t bits = bytes * 8;
    const std::size_t clipped = std::min<std::size_t>(prefix, bits);
    for (std::size_t i = 0; i < bytes; ++i) {
        const std::size_t byte_start = i * 8;
        if (clipped >= byte_start + 8) {
            continue;
        }
        if (clipped <= byte_start) {
            result.bytes[i] = 0;
        } else {
            const unsigned keep = static_cast<unsigned>(clipped - byte_start);
            result.bytes[i] &= static_cast<std::uint8_t>(0xffu << (8 - keep));
        }
    }
    return result;
}

std::optional<std::uint16_t> parse_port(std::string_view text) {
    const auto value = parse_decimal(text, 65535);
    if (!value) {
        return std::nullopt;
    }
    return static_cast<std::uint16_t>(*value);
}

std::string ipv6_to_string(const std::array<std::uint8_t, 16>& bytes) {
    std::array<std::uint16_t, 8> words{};
    for (std::size_t i = 0; i < words.size(); ++i) {
        words[i] = static_cast<std::uint16_t>(
            (static_cast<std::uint16_t>(bytes[i * 2]) << 8) | bytes[i * 2 + 1]);
    }

    std::size_t best_start = 0;
    std::size_t best_length = 0;
    for (std::size_t i = 0; i < words.size();) {
        if (words[i] != 0) {
            ++i;
            continue;
        }
        std::size_t end = i;
        while (end < words.size() && words[end] == 0) {
            ++end;
        }
        if (end - i > best_length) {
            best_start = i;
            best_length = end - i;
        }
        i = end;
    }

    auto hex = [](std::uint16_t word) {
        static constexpr char digits[] = "0123456789abcdef";
        std::string out;
        bool started = false;
        for (int shift = 12; shift >= 0; shift -= 4) {
            const unsigned nibble = (word >> shift) & 0xfu;
            if (nibble != 0 || started || shift == 0) {
                out.push_back(digits[nibble]);
                started = true;
            }
        }
        return out;
    };

    auto join = [&](std::size_t begin, std::size_t end) {
        std::string out;
        for (std::size_t i = begin; i < end; ++i) {
            if (!out.empty()) {
                out.push_back(':');
            }
            out += hex(words[i]);
        }
        return out;
    };

    if (best_length < 2) {
        return join(0, words.size());
    }

    const std::string left = join(0, best_start);
    const std::string right = join(best_start + best_length, words.size());
    if (left.empty() && right.empty()) {
        return "::";
    }
    if (left.empty()) {
        return "::" + right;
    }
    if (right.empty()) {
        return left + "::";
    }
    return left + "::" + right;
}

}  // namespace

// ---------------------------------------------------------------------------
// Address primitives
// ---------------------------------------------------------------------------

IpAddress IpAddress::from_v4(std::uint8_t a, std::uint8_t b, std::uint8_t c,
                             std::uint8_t d) noexcept {
    IpAddress address;
    address.family = IpFamily::v4;
    address.bytes[0] = a;
    address.bytes[1] = b;
    address.bytes[2] = c;
    address.bytes[3] = d;
    return address;
}

IpAddress IpAddress::from_v6(const std::array<std::uint8_t, 16>& raw) noexcept {
    IpAddress address;
    address.family = IpFamily::v6;
    address.bytes = raw;
    return address;
}

std::optional<IpAddress> IpAddress::parse(std::string_view text) {
    text = trim(text);
    if (text.empty()) {
        return std::nullopt;
    }

    if (text.find(':') == std::string_view::npos) {
        const auto parsed = parse_ipv4(text);
        if (!parsed) {
            return std::nullopt;
        }
        return from_v4((*parsed)[0], (*parsed)[1], (*parsed)[2], (*parsed)[3]);
    }

    const auto parsed = parse_ipv6(text);
    if (!parsed) {
        return std::nullopt;
    }
    return from_v6(*parsed);
}

std::string IpAddress::to_string() const {
    if (family == IpFamily::v4) {
        return std::to_string(bytes[0]) + "." + std::to_string(bytes[1]) + "." +
               std::to_string(bytes[2]) + "." + std::to_string(bytes[3]);
    }
    return ipv6_to_string(bytes);
}

bool IpAddress::is_v4() const noexcept {
    return family == IpFamily::v4;
}

std::optional<std::array<std::uint8_t, 4>> IpAddress::as_v4() const noexcept {
    if (family != IpFamily::v4) {
        return std::nullopt;
    }
    return std::array<std::uint8_t, 4>{bytes[0], bytes[1], bytes[2], bytes[3]};
}

std::optional<Subnet> Subnet::parse(std::string_view text) {
    text = trim(text);
    if (text.empty()) {
        return std::nullopt;
    }

    std::string_view address_text = text;
    std::optional<std::uint32_t> prefix;
    const std::size_t slash = text.rfind('/');
    if (slash != std::string_view::npos) {
        address_text = text.substr(0, slash);
        prefix = parse_decimal(text.substr(slash + 1), 128);
        if (!prefix) {
            return std::nullopt;
        }
    }

    const auto address = IpAddress::parse(address_text);
    if (!address) {
        return std::nullopt;
    }

    const std::size_t maximum =
        address->is_v4() ? ipv4_bytes * 8 : ipv6_bytes * 8;
    const std::size_t bits =
        prefix ? static_cast<std::size_t>(*prefix) : maximum;
    if (bits > maximum) {
        return std::nullopt;
    }

    Subnet subnet;
    subnet.network = masked_network(*address, static_cast<std::uint8_t>(bits));
    subnet.prefix_length = static_cast<std::uint8_t>(bits);
    return subnet;
}

bool Subnet::contains(const IpAddress& address) const noexcept {
    if (address.family != network.family) {
        return false;
    }
    const std::size_t bytes = address.is_v4() ? ipv4_bytes : ipv6_bytes;
    const std::size_t whole = prefix_length / 8;
    const unsigned remainder = prefix_length % 8;

    for (std::size_t i = 0; i < whole && i < bytes; ++i) {
        if (address.bytes[i] != network.bytes[i]) {
            return false;
        }
    }
    if (remainder != 0 && whole < bytes) {
        const std::uint8_t mask =
            static_cast<std::uint8_t>(0xffu << (8 - remainder));
        if ((address.bytes[whole] & mask) != (network.bytes[whole] & mask)) {
            return false;
        }
    }
    return true;
}

std::string Subnet::to_string() const {
    return network.to_string() + "/" +
           std::to_string(static_cast<unsigned>(prefix_length));
}

std::optional<Endpoint> Endpoint::parse(std::string_view text) {
    text = trim(text);
    if (text.empty()) {
        return std::nullopt;
    }

    std::string_view address_text;
    std::string_view port_text;
    if (text.front() == '[') {
        const std::size_t close = text.find(']');
        if (close == std::string_view::npos || close + 2 > text.size() ||
            text[close + 1] != ':') {
            return std::nullopt;
        }
        address_text = text.substr(1, close - 1);
        port_text = text.substr(close + 2);
    } else {
        const std::size_t colon = text.rfind(':');
        if (colon == std::string_view::npos) {
            return std::nullopt;
        }
        address_text = text.substr(0, colon);
        port_text = text.substr(colon + 1);
        if (address_text.find(':') != std::string_view::npos) {
            return std::nullopt;
        }
    }

    const auto address = IpAddress::parse(address_text);
    const auto port = parse_port(port_text);
    if (!address || !port) {
        return std::nullopt;
    }
    return Endpoint{*address, *port};
}

std::string Endpoint::to_string() const {
    if (address.is_v4()) {
        return address.to_string() + ":" + std::to_string(port);
    }
    return "[" + address.to_string() + "]:" + std::to_string(port);
}

AddressScope classify_address(const IpAddress& address) noexcept {
    if (address.is_v4()) {
        return classify_ipv4(ipv4_value(address));
    }

    if (bytes_are_zero(address.bytes.data(), ipv6_bytes)) {
        return AddressScope::unspecified;
    }

    bool loopback = true;
    for (std::size_t i = 0; i < 15; ++i) {
        if (address.bytes[i] != 0) {
            loopback = false;
            break;
        }
    }
    if (loopback && address.bytes[15] == 1) {
        return AddressScope::loopback;
    }

    if ((address.bytes[0] & 0xffu) == 0xfe &&
        (address.bytes[1] & 0xc0u) == 0x80u) {  // fe80::/10
        return AddressScope::link_local;
    }
    if ((address.bytes[0] & 0xffu) == 0xfe &&
        (address.bytes[1] & 0xc0u) == 0xc0u) {  // fec0::/10, site-local
        return AddressScope::private_network;
    }
    if ((address.bytes[0] & 0xfeu) == 0xfcu) {  // fc00::/7
        return AddressScope::private_network;
    }
    if (address.bytes[0] == 0xff) {  // ff00::/8
        return AddressScope::multicast;
    }

    // IPv4-mapped IPv6 (::ffff:0:0/96). Classify the embedded address so an
    // attacker cannot hide 10/8 or 127/8 behind a v6 literal.
    if (bytes_are_zero(address.bytes.data(), 10) &&
        address.bytes[10] == 0xff && address.bytes[11] == 0xff) {
        const std::uint32_t mapped =
            (static_cast<std::uint32_t>(address.bytes[12]) << 24) |
            (static_cast<std::uint32_t>(address.bytes[13]) << 16) |
            (static_cast<std::uint32_t>(address.bytes[14]) << 8) |
            static_cast<std::uint32_t>(address.bytes[15]);
        return classify_ipv4(mapped);
    }

    auto embedded_v4 = [](const std::uint8_t* bytes) {
        return (static_cast<std::uint32_t>(bytes[0]) << 24) |
               (static_cast<std::uint32_t>(bytes[1]) << 16) |
               (static_cast<std::uint32_t>(bytes[2]) << 8) |
               static_cast<std::uint32_t>(bytes[3]);
    };

    // Deprecated IPv4-compatible IPv6 (::/96), excluding :: and ::1 handled
    // above.
    if (bytes_are_zero(address.bytes.data(), 12)) {
        return classify_ipv4(embedded_v4(address.bytes.data() + 12));
    }

    // Well-known NAT64 prefix 64:ff9b::/96.
    if (address.bytes[0] == 0x00 && address.bytes[1] == 0x64 &&
        address.bytes[2] == 0xff && address.bytes[3] == 0x9b &&
        bytes_are_zero(address.bytes.data() + 4, 8)) {
        return classify_ipv4(embedded_v4(address.bytes.data() + 12));
    }

    // Local-use NAT64 64:ff9b:1::/48 may translate into private space.
    if (address.bytes[0] == 0x00 && address.bytes[1] == 0x64 &&
        address.bytes[2] == 0xff && address.bytes[3] == 0x9b &&
        address.bytes[4] == 0x00 && address.bytes[5] == 0x01) {
        return AddressScope::private_network;
    }

    // 6to4 (2002::/16) embeds an IPv4 address in bits 16..47. Reject 6to4
    // forms that wrap a private or otherwise non-global destination.
    if (address.bytes[0] == 0x20 && address.bytes[1] == 0x02) {
        const AddressScope inner =
            classify_ipv4(embedded_v4(address.bytes.data() + 2));
        if (inner != AddressScope::global) {
            return inner;
        }
    }

    return AddressScope::global;
}

bool is_forbidden_data_target(const IpAddress& address) noexcept {
    return classify_address(address) != AddressScope::global;
}

const char* to_string(AddressScope scope) noexcept {
    switch (scope) {
        case AddressScope::global:
            return "global";
        case AddressScope::unspecified:
            return "unspecified";
        case AddressScope::loopback:
            return "loopback";
        case AddressScope::link_local:
            return "link_local";
        case AddressScope::private_network:
            return "private_network";
        case AddressScope::shared_address:
            return "shared_address";
        case AddressScope::multicast:
            return "multicast";
        case AddressScope::reserved:
            return "reserved";
    }
    return "unknown";
}

// ---------------------------------------------------------------------------
// Decisions and policy
// ---------------------------------------------------------------------------

const char* to_string(DenyReason reason) noexcept {
    switch (reason) {
        case DenyReason::none:
            return "none";
        case DenyReason::invalid_input:
            return "invalid_input";
        case DenyReason::session_unknown:
            return "session_unknown";
        case DenyReason::session_closed:
            return "session_closed";
        case DenyReason::session_expired:
            return "session_expired";
        case DenyReason::session_conflict:
            return "session_conflict";
        case DenyReason::resource_limit:
            return "resource_limit";
        case DenyReason::proxy_untrusted:
            return "proxy_untrusted";
        case DenyReason::proxy_invalid:
            return "proxy_invalid";
        case DenyReason::address_required:
            return "address_required";
        case DenyReason::address_mismatch:
            return "address_mismatch";
        case DenyReason::address_outside_client_subnet:
            return "address_outside_client_subnet";
        case DenyReason::forbidden_address:
            return "forbidden_address";
        case DenyReason::port_not_allowed:
            return "port_not_allowed";
        case DenyReason::grant_unknown:
            return "grant_unknown";
        case DenyReason::grant_expired:
            return "grant_expired";
        case DenyReason::grant_consumed:
            return "grant_consumed";
        case DenyReason::peer_mismatch:
            return "peer_mismatch";
    }
    return "unknown";
}

Decision Decision::allow(std::string detail) {
    return Decision{true, DenyReason::none, std::move(detail)};
}

Decision Decision::deny(DenyReason reason, std::string detail) {
    return Decision{false, reason, std::move(detail)};
}

DataConnectionPolicy::DataConnectionPolicy(PolicyConfig config,
                                           NowFunction now)
    : config_(std::move(config)), now_(std::move(now)) {
    config_.ipv4_client_prefix =
        std::min<std::uint8_t>(config_.ipv4_client_prefix, 32);
    config_.ipv6_client_prefix =
        std::min<std::uint8_t>(config_.ipv6_client_prefix, 128);
}

bool DataConnectionPolicy::session_expired(const SessionRecord& record,
                                           TimePoint at) const {
    if (config_.session_idle_timeout.count() <= 0) {
        return true;
    }
    return at >= record.last_activity + config_.session_idle_timeout;
}

bool DataConnectionPolicy::grant_expired(const GrantRecord& record,
                                         TimePoint at) const {
    if (config_.grant_ttl.count() <= 0) {
        return true;
    }
    return at >= record.grant.expires_at;
}

void DataConnectionPolicy::erase_grant(std::uint64_t grant_id) {
    grants_.erase(grant_id);
}

void DataConnectionPolicy::erase_session(std::uint64_t session_id) {
    for (auto it = grants_.begin(); it != grants_.end();) {
        if (it->second.grant.session_id == session_id) {
            it = grants_.erase(it);
        } else {
            ++it;
        }
    }
    sessions_.erase(session_id);
}

std::size_t DataConnectionPolicy::pending_grants_for(
    std::uint64_t session_id, TimePoint at) {
    std::size_t count = 0;
    for (auto it = grants_.begin(); it != grants_.end();) {
        const bool expired = grant_expired(it->second, at);
        const auto session = sessions_.find(it->second.grant.session_id);
        const bool orphaned =
            session == sessions_.end() || session->second.closed;
        if (expired || orphaned) {
            it = grants_.erase(it);
            continue;
        }
        if (it->second.grant.session_id == session_id &&
            !it->second.consumed) {
            ++count;
        }
        ++it;
    }
    return count;
}

Subnet DataConnectionPolicy::source_subnet_for(const IpAddress& address) const {
    const std::uint8_t prefix = address.is_v4() ? config_.ipv4_client_prefix
                                                : config_.ipv6_client_prefix;
    Subnet subnet;
    subnet.network = masked_network(address, prefix);
    subnet.prefix_length = prefix;
    return subnet;
}

Decision DataConnectionPolicy::open_session(const CommandConnection& command) {
    std::lock_guard<std::mutex> lock(mutex_);
    const TimePoint current = now();

    if (command.session_id == 0) {
        return Decision::deny(DenyReason::invalid_input,
                              "session id 0 is reserved");
    }
    if (sessions_.find(command.session_id) != sessions_.end()) {
        return Decision::deny(DenyReason::session_conflict,
                              "session id is already registered");
    }

    Endpoint client = command.transport_peer;
    if (command.proxied_client) {
        const bool trusted =
            std::any_of(config_.trusted_proxies.begin(),
                        config_.trusted_proxies.end(),
                        [&](const Subnet& subnet) {
                            return subnet.contains(
                                command.transport_peer.address);
                        });
        if (!trusted) {
            return Decision::deny(
                DenyReason::proxy_untrusted,
                "PROXY source supplied by an untrusted transport peer");
        }
        if (classify_address(command.proxied_client->address) ==
            AddressScope::unspecified) {
            return Decision::deny(DenyReason::proxy_invalid,
                                  "PROXY source address is unspecified");
        }
        client = *command.proxied_client;
    }

    if (classify_address(client.address) == AddressScope::unspecified) {
        return Decision::deny(DenyReason::invalid_input,
                              "client source address is unspecified");
    }

    for (auto it = sessions_.begin(); it != sessions_.end();) {
        if (session_expired(it->second, current)) {
            const std::uint64_t expired_id = it->first;
            ++it;
            erase_session(expired_id);
        } else {
            ++it;
        }
    }

    if (sessions_.size() >= config_.max_sessions) {
        return Decision::deny(DenyReason::resource_limit,
                              "session table is full");
    }

    SessionRecord record;
    record.command = command;
    record.client = client;
    record.client_subnet = source_subnet_for(client.address);
    record.last_activity = current;
    record.closed = false;
    sessions_.emplace(command.session_id, std::move(record));
    return Decision::allow("session registered");
}

Decision DataConnectionPolicy::close_session(std::uint64_t session_id) {
    std::lock_guard<std::mutex> lock(mutex_);
    erase_session(session_id);
    return Decision::allow("session closed");
}

bool DataConnectionPolicy::has_session(std::uint64_t session_id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = sessions_.find(session_id);
    if (it == sessions_.end()) {
        return false;
    }
    return !it->second.closed && !session_expired(it->second, now());
}

AuthorizationResult DataConnectionPolicy::authorize(
    std::uint64_t session_id, const DataRequest& request) {
    std::lock_guard<std::mutex> lock(mutex_);
    const TimePoint current = now();

    const auto session_it = sessions_.find(session_id);
    if (session_it == sessions_.end()) {
        return {Decision::deny(DenyReason::session_unknown,
                               "data request has no command session"),
                std::nullopt};
    }

    SessionRecord& session = session_it->second;
    if (session.closed) {
        return {Decision::deny(DenyReason::session_closed,
                               "command session is closed"),
                std::nullopt};
    }
    if (session_expired(session, current)) {
        erase_session(session_id);
        return {Decision::deny(DenyReason::session_expired,
                               "command session timed out"),
                std::nullopt};
    }

    if (request.port == 0) {
        return {Decision::deny(DenyReason::port_not_allowed,
                               "data port 0 is not a valid target"),
                std::nullopt};
    }
    if (!config_.allowed_data_ports.contains(request.port)) {
        return {Decision::deny(DenyReason::port_not_allowed,
                               "data port is outside the allowed range"),
                std::nullopt};
    }

    bool requires_address = false;
    switch (request.mode) {
        case DataMode::pasv:
        case DataMode::active:
        case DataMode::proxy:
            requires_address = true;
            break;
        case DataMode::epsv:
            requires_address = false;
            break;
        default:
            return {Decision::deny(DenyReason::invalid_input,
                                   "unknown data mode"),
                    std::nullopt};
    }

    if (requires_address && !request.declared_address) {
        return {Decision::deny(
                    DenyReason::address_required,
                    "this data mode requires a declared client address"),
                std::nullopt};
    }

    const IpAddress candidate =
        request.declared_address.value_or(session.client.address);

    if (request.mode == DataMode::epsv && request.declared_address &&
        *request.declared_address != session.client.address) {
        return {Decision::deny(
                    DenyReason::address_mismatch,
                    "EPSV attempted to override the command peer address"),
                std::nullopt};
    }

    const AddressScope scope = classify_address(candidate);
    if (scope != AddressScope::global) {
        return {Decision::deny(
                    DenyReason::forbidden_address,
                    std::string("data target is ") + to_string(scope)),
                std::nullopt};
    }

    if (!session.client_subnet.contains(candidate)) {
        return {Decision::deny(
                    DenyReason::address_outside_client_subnet,
                    "declared address is outside the client source subnet"),
                std::nullopt};
    }

    if (config_.require_exact_client_address &&
        candidate != session.client.address) {
        return {Decision::deny(
                    DenyReason::address_mismatch,
                    "declared address is not the command connection peer"),
                std::nullopt};
    }

    if (pending_grants_for(session_id, current) >=
        config_.max_pending_grants_per_session) {
        return {Decision::deny(
                    DenyReason::resource_limit,
                    "too many outstanding data grants for this session"),
                std::nullopt};
    }

    while (next_grant_id_ == 0 ||
           grants_.find(next_grant_id_) != grants_.end()) {
        ++next_grant_id_;
    }

    DataConnectionGrant grant;
    grant.grant_id = next_grant_id_++;
    grant.session_id = session_id;
    grant.mode = request.mode;
    grant.target = Endpoint{candidate, request.port};
    grant.compare_peer_port =
        request.mode == DataMode::active || request.mode == DataMode::proxy;
    if (grant.compare_peer_port) {
        grant.expected_peer = grant.target;
    } else {
        grant.expected_peer =
            Endpoint{session.client.address, static_cast<std::uint16_t>(0)};
    }
    grant.expires_at = current + config_.grant_ttl;

    GrantRecord record{grant, false};
    grants_.emplace(grant.grant_id, std::move(record));
    session.last_activity = current;

    return {Decision::allow("data connection authorized"), grant};
}

Decision DataConnectionPolicy::confirm_peer(const DataConnectionGrant& grant,
                                            const Endpoint& peer) {
    return confirm_peer(grant.grant_id, peer);
}

Decision DataConnectionPolicy::confirm_peer(std::uint64_t grant_id,
                                            const Endpoint& peer) {
    std::lock_guard<std::mutex> lock(mutex_);
    const TimePoint current = now();

    const auto grant_it = grants_.find(grant_id);
    if (grant_it == grants_.end()) {
        return Decision::deny(DenyReason::grant_unknown,
                              "data grant is unknown or already closed");
    }

    GrantRecord& record = grant_it->second;
    if (record.consumed) {
        return Decision::deny(DenyReason::grant_consumed,
                              "data grant has already been used");
    }
    if (grant_expired(record, current)) {
        erase_grant(grant_id);
        return Decision::deny(DenyReason::grant_expired,
                              "data grant timed out");
    }

    const auto session_it = sessions_.find(record.grant.session_id);
    if (session_it == sessions_.end()) {
        erase_grant(grant_id);
        return Decision::deny(DenyReason::session_unknown,
                              "grant session no longer exists");
    }
    if (session_it->second.closed) {
        return Decision::deny(DenyReason::session_closed,
                              "grant session is closed");
    }
    if (session_expired(session_it->second, current)) {
        erase_session(record.grant.session_id);
        return Decision::deny(DenyReason::session_expired,
                              "grant session timed out");
    }

    if (peer.address != record.grant.expected_peer.address ||
        (record.grant.compare_peer_port &&
         peer.port != record.grant.expected_peer.port)) {
        return Decision::deny(
            DenyReason::peer_mismatch,
            "data peer does not match the command session client");
    }

    if (!session_it->second.client_subnet.contains(peer.address)) {
        return Decision::deny(
            DenyReason::address_outside_client_subnet,
            "data peer is outside the client source subnet");
    }

    record.consumed = true;
    session_it->second.last_activity = current;
    return Decision::allow("data peer matches the command session client");
}

void DataConnectionPolicy::purge_expired() {
    std::lock_guard<std::mutex> lock(mutex_);
    const TimePoint current = now();

    for (auto it = sessions_.begin(); it != sessions_.end();) {
        if (it->second.closed || session_expired(it->second, current)) {
            const std::uint64_t id = it->first;
            ++it;
            erase_session(id);
        } else {
            ++it;
        }
    }

    for (auto it = grants_.begin(); it != grants_.end();) {
        const auto session = sessions_.find(it->second.grant.session_id);
        if (grant_expired(it->second, current) || session == sessions_.end() ||
            session->second.closed ||
            session_expired(session->second, current)) {
            it = grants_.erase(it);
        } else {
            ++it;
        }
    }
}

// ---------------------------------------------------------------------------
// Protocol parsers
// ---------------------------------------------------------------------------

std::optional<Endpoint> parse_ftp_port_argument(std::string_view argument) {
    argument = trim(argument);
    const auto parts = split(argument, ',');
    if (parts.size() != 6) {
        return std::nullopt;
    }

    std::array<std::uint32_t, 6> values{};
    for (std::size_t i = 0; i < values.size(); ++i) {
        const auto value = parse_decimal(parts[i], 255);
        if (!value) {
            return std::nullopt;
        }
        values[i] = *value;
    }

    const std::uint16_t port =
        static_cast<std::uint16_t>((values[4] << 8) | values[5]);
    if (port == 0) {
        return std::nullopt;
    }

    return Endpoint{IpAddress::from_v4(static_cast<std::uint8_t>(values[0]),
                                       static_cast<std::uint8_t>(values[1]),
                                       static_cast<std::uint8_t>(values[2]),
                                       static_cast<std::uint8_t>(values[3])),
                    port};
}

std::optional<Endpoint> parse_eprt_argument(std::string_view argument) {
    argument = trim(argument);
    if (argument.size() < 5) {
        return std::nullopt;
    }

    const char delimiter = argument.front();
    if (is_digit(delimiter) || delimiter == '+' || delimiter == '-') {
        return std::nullopt;
    }
    if (argument.back() != delimiter) {
        return std::nullopt;
    }

    const auto parts = split(argument, delimiter);
    if (parts.size() != 5 || !parts.front().empty() || !parts.back().empty()) {
        return std::nullopt;
    }

    const std::string_view family = parts[1];
    const auto address = IpAddress::parse(parts[2]);
    const auto port = parse_port(parts[3]);
    if (!address || !port || *port == 0) {
        return std::nullopt;
    }

    if (family == "1" && !address->is_v4()) {
        return std::nullopt;
    }
    if (family == "2" && address->is_v4()) {
        return std::nullopt;
    }
    if (family != "1" && family != "2") {
        return std::nullopt;
    }

    return Endpoint{*address, *port};
}

std::optional<Endpoint> parse_pasv_response(std::string_view line) {
    const std::size_t open = line.find('(');
    if (open == std::string_view::npos) {
        return std::nullopt;
    }
    const std::size_t close = line.find(')', open + 1);
    if (close == std::string_view::npos) {
        return std::nullopt;
    }
    return parse_ftp_port_argument(line.substr(open + 1, close - open - 1));
}

std::optional<std::uint16_t> parse_epsv_response(std::string_view line) {
    const std::size_t open = line.find('(');
    if (open == std::string_view::npos) {
        return std::nullopt;
    }
    const std::size_t close = line.find(')', open + 1);
    if (close == std::string_view::npos) {
        return std::nullopt;
    }

    std::string_view inner = trim(line.substr(open + 1, close - open - 1));
    if (inner.empty()) {
        return std::nullopt;
    }
    if (is_digit(inner.front())) {
        const auto port = parse_port(inner);
        if (!port || *port == 0) {
            return std::nullopt;
        }
        return port;
    }

    const char delimiter = inner.front();
    if (inner.back() != delimiter) {
        return std::nullopt;
    }
    const auto parts = split(inner, delimiter);
    std::optional<std::uint16_t> found;
    for (const auto& part : parts) {
        if (part.empty()) {
            continue;
        }
        const auto port = parse_port(part);
        if (!port) {
            return std::nullopt;
        }
        if (found) {
            return std::nullopt;
        }
        found = port;
    }
    if (!found || *found == 0) {
        return std::nullopt;
    }
    return found;
}

std::optional<ProxyInfo> parse_proxy_v1(std::string_view header) {
    header = trim(header);
    if (header.empty()) {
        return std::nullopt;
    }

    std::vector<std::string_view> tokens;
    std::size_t begin = 0;
    while (begin < header.size()) {
        while (begin < header.size() && header[begin] == ' ') {
            ++begin;
        }
        if (begin == header.size()) {
            break;
        }
        std::size_t end = header.find(' ', begin);
        if (end == std::string_view::npos) {
            end = header.size();
        }
        tokens.push_back(header.substr(begin, end - begin));
        begin = end;
    }

    if (tokens.size() != 6 || tokens[0] != "PROXY") {
        return std::nullopt;
    }
    if (tokens[1] != "TCP4" && tokens[1] != "TCP6") {
        return std::nullopt;
    }

    const auto source = IpAddress::parse(tokens[2]);
    const auto destination = IpAddress::parse(tokens[3]);
    const auto source_port = parse_port(tokens[4]);
    const auto destination_port = parse_port(tokens[5]);
    if (!source || !destination || !source_port || !destination_port) {
        return std::nullopt;
    }
    if (tokens[1] == "TCP4" && (!source->is_v4() || !destination->is_v4())) {
        return std::nullopt;
    }
    if (tokens[1] == "TCP6" && (source->is_v4() || destination->is_v4())) {
        return std::nullopt;
    }

    return ProxyInfo{Endpoint{*source, *source_port},
                     Endpoint{*destination, *destination_port}};
}

std::optional<std::string> format_ftp_port_argument(const Endpoint& endpoint) {
    if (!endpoint.address.is_v4() || endpoint.port == 0) {
        return std::nullopt;
    }
    const auto bytes = v4_bytes(endpoint.address);
    return std::to_string(bytes[0]) + "," + std::to_string(bytes[1]) + "," +
           std::to_string(bytes[2]) + "," + std::to_string(bytes[3]) + "," +
           std::to_string(endpoint.port >> 8) + "," +
           std::to_string(endpoint.port & 0xff);
}

std::string format_epsv_argument(std::uint16_t port) {
    return "(|||" + std::to_string(port) + "|)";
}

}  // namespace netsec

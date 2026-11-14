#include "netsec/certificate.hpp"

#include <array>
#include <charconv>
#include <string_view>

#include "netsec/wire.hpp"

namespace netsec {

namespace {

constexpr std::string_view kEd25519CertType =
    "ssh-ed25519-cert-v01@openssh.com";
constexpr std::string_view kEd25519KeyType = "ssh-ed25519";

bool parseDecimal(std::string_view text, int& value) {
    if (text.empty()) {
        return false;
    }
    int parsed = 0;
    const char* begin = text.data();
    const char* end = text.data() + text.size();
    auto [ptr, ec] = std::from_chars(begin, end, parsed);
    if (ec != std::errc{} || ptr != end) {
        return false;
    }
    value = parsed;
    return true;
}

bool parseIpv4(std::string_view text,
               std::array<std::uint8_t, 4>& octets) {
    std::size_t start = 0;
    int count = 0;
    while (count < 4) {
        std::size_t dot = text.find('.', start);
        std::string_view part =
            (dot == std::string_view::npos) ? text.substr(start)
                                            : text.substr(start, dot - start);
        int value = 0;
        if (!parseDecimal(part, value) || value < 0 || value > 255) {
            return false;
        }
        octets[static_cast<std::size_t>(count)] =
            static_cast<std::uint8_t>(value);
        ++count;
        if (dot == std::string_view::npos) {
            break;
        }
        start = dot + 1;
    }
    return count == 4 && text.find('.', start) == std::string_view::npos;
}

int hexValue(char c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return c - 'a' + 10;
    }
    if (c >= 'A' && c <= 'F') {
        return c - 'A' + 10;
    }
    return -1;
}

bool parseIpv6(std::string_view text,
               std::array<std::uint16_t, 8>& groups) {
    std::size_t doubleColon = text.find("::");
    bool hasDoubleColon = doubleColon != std::string_view::npos;
    if (hasDoubleColon &&
        text.find("::", doubleColon + 1) != std::string_view::npos) {
        return false;
    }

    auto parseGroupList = [&](std::string_view part,
                              std::array<std::uint16_t, 8>& output,
                              int& count) {
        if (part.empty()) {
            return true;
        }
        std::size_t start = 0;
        while (true) {
            std::size_t colon = part.find(':', start);
            std::string_view group =
                (colon == std::string_view::npos)
                    ? part.substr(start)
                    : part.substr(start, colon - start);
            if (group.empty() || group.size() > 4 || count >= 8) {
                return false;
            }
            int value = 0;
            for (char c : group) {
                int digit = hexValue(c);
                if (digit < 0) {
                    return false;
                }
                value = (value << 4) | digit;
            }
            output[static_cast<std::size_t>(count++)] =
                static_cast<std::uint16_t>(value);
            if (colon == std::string_view::npos) {
                break;
            }
            start = colon + 1;
        }
        return true;
    };

    if (hasDoubleColon) {
        std::array<std::uint16_t, 8> head{};
        std::array<std::uint16_t, 8> tail{};
        int headCount = 0;
        int tailCount = 0;
        std::string_view left = text.substr(0, doubleColon);
        std::string_view right = text.substr(doubleColon + 2);
        if (!parseGroupList(left, head, headCount) ||
            !parseGroupList(right, tail, tailCount)) {
            return false;
        }
        if (headCount + tailCount >= 8) {
            return false;
        }
        for (int i = 0; i < headCount; ++i) {
            groups[static_cast<std::size_t>(i)] =
                head[static_cast<std::size_t>(i)];
        }
        for (int i = 0; i < tailCount; ++i) {
            groups[static_cast<std::size_t>(7 - tailCount + 1 + i)] =
                tail[static_cast<std::size_t>(i)];
        }
        return true;
    }

    int count = 0;
    if (!parseGroupList(text, groups, count) || count != 8) {
        return false;
    }
    return true;
}

std::optional<IpPrefix> parseCidr(std::string_view entry) {
    std::size_t slash = entry.find('/');
    std::string_view address =
        (slash == std::string_view::npos) ? entry : entry.substr(0, slash);
    std::string_view prefixText =
        (slash == std::string_view::npos) ? std::string_view{}
                                          : entry.substr(slash + 1);

    std::array<std::uint8_t, 4> ipv4{};
    if (parseIpv4(address, ipv4)) {
        int prefix = 32;
        if (!prefixText.empty() &&
            (!parseDecimal(prefixText, prefix) || prefix < 0 ||
             prefix > 32)) {
            return std::nullopt;
        }
        IpPrefix result;
        result.address.assign(ipv4.begin(), ipv4.end());
        result.prefixLength = prefix;
        return result;
    }

    std::array<std::uint16_t, 8> ipv6{};
    if (parseIpv6(address, ipv6)) {
        int prefix = 128;
        if (!prefixText.empty() &&
            (!parseDecimal(prefixText, prefix) || prefix < 0 ||
             prefix > 128)) {
            return std::nullopt;
        }
        IpPrefix result;
        result.address.resize(16);
        for (std::size_t i = 0; i < 8; ++i) {
            result.address[i * 2] =
                static_cast<std::uint8_t>(ipv6[i] >> 8);
            result.address[i * 2 + 1] =
                static_cast<std::uint8_t>(ipv6[i] & 0xFF);
        }
        result.prefixLength = prefix;
        return result;
    }
    return std::nullopt;
}

std::optional<AllowedEndpoint> parseEndpoint(std::string_view value) {
    std::size_t separator = value.rfind(':');
    if (separator == std::string_view::npos || separator == 0 ||
        separator + 1 >= value.size()) {
        return std::nullopt;
    }
    AllowedEndpoint endpoint;
    endpoint.host.assign(value.substr(0, separator));
    endpoint.port.assign(value.substr(separator + 1));

    int port = 0;
    if (endpoint.port != "*" &&
        (!parseDecimal(endpoint.port, port) || port <= 0 || port > 65535)) {
        return std::nullopt;
    }
    if (endpoint.host.size() >= 2 && endpoint.host.front() == '[' &&
        endpoint.host.back() == ']') {
        endpoint.host = endpoint.host.substr(1, endpoint.host.size() - 2);
    }
    if (endpoint.host.empty()) {
        return std::nullopt;
    }
    return endpoint;
}

struct OptionAccumulator {
    bool forceCommandSeen = false;
    bool sourceAddressSeen = false;
    bool ptySeen = false;
    bool userRcSeen = false;
    bool agentSeen = false;
    bool portFwdSeen = false;
    bool x11Seen = false;
};

bool applyCriticalOption(std::string_view name, std::string_view value,
                         CriticalOptions& options, OptionAccumulator& seen,
                         std::string& error) {
    auto requireEmpty = [&](bool already, const char* label) {
        if (already || !value.empty()) {
            error = std::string("invalid critical option ") + label;
            return false;
        }
        return true;
    };

    if (name == "force-command") {
        if (seen.forceCommandSeen) {
            error = "duplicate force-command";
            return false;
        }
        seen.forceCommandSeen = true;
        options.forceCommand.emplace(value);
        return true;
    }
    if (name == "source-address") {
        if (seen.sourceAddressSeen) {
            error = "duplicate source-address";
            return false;
        }
        seen.sourceAddressSeen = true;
        std::size_t start = 0;
        while (true) {
            std::size_t comma = value.find(',', start);
            std::string_view entry =
                (comma == std::string_view::npos)
                    ? value.substr(start)
                    : value.substr(start, comma - start);
            auto prefix = parseCidr(entry);
            if (!prefix) {
                error = "malformed source-address entry";
                return false;
            }
            options.sourceAddresses.push_back(*prefix);
            if (comma == std::string_view::npos) {
                break;
            }
            start = comma + 1;
        }
        return true;
    }
    if (name == "permit-open") {
        auto endpoint = parseEndpoint(value);
        if (!endpoint) {
            error = "malformed permit-open";
            return false;
        }
        options.permitOpen.push_back(*endpoint);
        return true;
    }
    if (name == "permit-listen") {
        auto endpoint = parseEndpoint(value);
        if (!endpoint) {
            error = "malformed permit-listen";
            return false;
        }
        options.permitListen.push_back(*endpoint);
        return true;
    }
    if (name == "permit-pty") {
        if (!requireEmpty(seen.ptySeen, "permit-pty")) return false;
        seen.ptySeen = true;
        options.permitPty = true;
        return true;
    }
    if (name == "permit-user-rc") {
        if (!requireEmpty(seen.userRcSeen, "permit-user-rc")) return false;
        seen.userRcSeen = true;
        options.permitUserRc = true;
        return true;
    }
    if (name == "permit-agent-forwarding") {
        if (!requireEmpty(seen.agentSeen, "permit-agent-forwarding"))
            return false;
        seen.agentSeen = true;
        options.permitAgentForwarding = true;
        return true;
    }
    if (name == "permit-port-forwarding") {
        if (!requireEmpty(seen.portFwdSeen, "permit-port-forwarding"))
            return false;
        seen.portFwdSeen = true;
        options.permitPortForwarding = true;
        return true;
    }
    if (name == "permit-X11-forwarding") {
        if (!requireEmpty(seen.x11Seen, "permit-X11-forwarding")) return false;
        seen.x11Seen = true;
        options.permitX11Forwarding = true;
        return true;
    }

    error = "unknown critical option: ";
    error.append(name);
    return false;
}

void applyExtension(std::string_view name, Extensions& extensions) {
    if (name == "no-touch-required") {
        extensions.noTouchRequired = true;
    } else if (name == "permit-X11-forwarding") {
        extensions.permitX11Forwarding = true;
    } else if (name == "permit-agent-forwarding") {
        extensions.permitAgentForwarding = true;
    } else if (name == "permit-port-forwarding") {
        extensions.permitPortForwarding = true;
    } else if (name == "permit-user-rc") {
        extensions.permitUserRc = true;
    } else if (name == "permit-pty") {
        extensions.permitPty = true;
    } else {
        extensions.unknown.emplace_back(name);
    }
}

}  // namespace

CertificateParseResult parseCertificate(std::string_view blob) {
    auto fail = [](std::string message) -> CertificateParseResult {
        return {std::nullopt, ParseError{std::move(message)}};
    };

    wire::Reader reader(blob);
    SshCertificate cert;
    cert.rawCertificate.assign(blob);

    std::string_view certType;
    if (!reader.readString(certType)) {
        return fail("truncated certificate type");
    }
    if (certType != kEd25519CertType) {
        return fail("unsupported certificate type (only Ed25519 v01)");
    }
    cert.keyType.assign(certType);

    if (!reader.readString(cert.nonce)) {
        return fail("truncated nonce");
    }

    std::string_view embeddedKeyType;
    if (!reader.readString(embeddedKeyType)) {
        return fail("truncated public key type");
    }
    if (embeddedKeyType != kEd25519KeyType) {
        return fail("embedded key is not Ed25519");
    }
    if (!reader.readString(cert.publicKey)) {
        return fail("truncated public key");
    }

    if (!reader.readUint64(cert.serial)) {
        return fail("truncated serial");
    }

    std::uint32_t rawType = 0;
    if (!reader.readUint32(rawType)) {
        return fail("truncated certificate kind");
    }
    if (rawType != 1 && rawType != 2) {
        return fail("invalid certificate kind");
    }
    cert.type = rawType == 1 ? CertificateType::User : CertificateType::Host;

    std::uint32_t principalCount = 0;
    if (!reader.readUint32(principalCount)) {
        return fail("truncated principal count");
    }
    for (std::uint32_t i = 0; i < principalCount; ++i) {
        std::string principal;
        if (!reader.readString(principal)) {
            return fail("truncated principal");
        }
        cert.principals.push_back(std::move(principal));
    }

    if (!reader.readUint64(cert.validAfter) ||
        !reader.readUint64(cert.validBefore)) {
        return fail("truncated validity window");
    }
    if (cert.validBefore < cert.validAfter) {
        return fail("validity window inverted");
    }

    if (!reader.readString(cert.keyId)) {
        return fail("truncated key id");
    }

    OptionAccumulator seen;
    std::uint32_t criticalCount = 0;
    if (!reader.readUint32(criticalCount)) {
        return fail("truncated critical option count");
    }
    for (std::uint32_t i = 0; i < criticalCount; ++i) {
        std::string name;
        std::string value;
        if (!reader.readString(name) || !reader.readString(value)) {
            return fail("truncated critical option");
        }
        std::string error;
        if (!applyCriticalOption(name, value, cert.critical, seen, error)) {
            return fail(error);
        }
    }

    std::uint32_t extensionCount = 0;
    if (!reader.readUint32(extensionCount)) {
        return fail("truncated extension count");
    }
    for (std::uint32_t i = 0; i < extensionCount; ++i) {
        std::string name;
        std::string value;
        if (!reader.readString(name) || !reader.readString(value)) {
            return fail("truncated extension");
        }
        applyExtension(name, cert.extensions);
    }

    if (!reader.readString(cert.reserved)) {
        return fail("truncated reserved field");
    }

    // Embedded CA key: type string followed by the CA key blob. Both are
    // retained so the trust store can match on exact wire identity.
    std::string_view caKeyType;
    if (!reader.readString(caKeyType)) {
        return fail("truncated CA key type");
    }
    if (caKeyType != kEd25519KeyType) {
        return fail("CA key is not Ed25519");
    }
    std::string_view caKeyBlob;
    if (!reader.readString(caKeyBlob)) {
        return fail("truncated CA key");
    }
    {
        wire::Writer caWriter;
        caWriter.writeString(std::string_view(kEd25519KeyType));
        caWriter.writeString(caKeyBlob);
        cert.signatureKey = caWriter.take();
    }

    if (!reader.readString(cert.signature)) {
        return fail("truncated signature");
    }
    if (!reader.empty()) {
        return fail("trailing bytes after certificate");
    }

    return {std::move(cert), std::nullopt};
}

bool samePublicKey(std::string_view lhs, std::string_view rhs) noexcept {
    return lhs == rhs;
}

}  // namespace netsec

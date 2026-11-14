#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace netsec {

enum class CertificateType {
    User = 1,
    Host = 2,
};

// One parsed `source-address` CIDR entry (IPv4 or IPv6).
struct IpPrefix {
    // Network address in raw byte order: 4 bytes for IPv4, 16 for IPv6.
    std::vector<std::uint8_t> address;
    int prefixLength = 0;
};

// One parsed `permit-open` / `permit-listen` host:port entry.
struct AllowedEndpoint {
    std::string host;  // literal IP or hostname, possibly "*"
    std::string port;  // decimal port or "*"
};

// Critical options recognized by this implementation. Any critical option not
// listed here is unknown and rejects the certificate at parse time.
struct CriticalOptions {
    std::optional<std::string> forceCommand;
    std::vector<IpPrefix> sourceAddresses;
    bool permitPortForwarding = false;
    bool permitAgentForwarding = false;
    bool permitX11Forwarding = false;
    bool permitUserRc = false;
    bool permitPty = false;
    std::vector<AllowedEndpoint> permitOpen;    // direct-tcpip allow list
    std::vector<AllowedEndpoint> permitListen;  // tcpip-forward allow list
};

// Extensions only grant capabilities; unknown extensions are advisory and
// therefore ignored rather than rejected (per PROTOCOL.certkeys).
struct Extensions {
    bool noTouchRequired = false;
    bool permitX11Forwarding = false;
    bool permitAgentForwarding = false;
    bool permitPortForwarding = false;
    bool permitUserRc = false;
    bool permitPty = false;
    std::vector<std::string> unknown;  // retained for auditing only
};

struct SshCertificate {
    std::string nonce;
    std::string keyType;  // e.g. "ssh-ed25519-cert-v01@openssh.com"
    std::string publicKey;
    std::uint64_t serial = 0;
    CertificateType type = CertificateType::User;
    std::string keyId;
    std::vector<std::string> principals;
    std::uint64_t validAfter = 0;   // Unix seconds, inclusive
    std::uint64_t validBefore = 0;  // Unix seconds, exclusive
    CriticalOptions critical;
    Extensions extensions;
    std::string reserved;
    std::string signatureKey;   // embedded CA public key blob
    std::string signature;      // full SSH signature blob
    std::string rawCertificate;  // original wire bytes
};

struct ParseError {
    std::string message;
};

struct CertificateParseResult {
    std::optional<SshCertificate> certificate;
    std::optional<ParseError> error;

    explicit operator bool() const noexcept { return certificate.has_value(); }
};

// Parse an SSH certificate blob (the certkey wire format). Fails closed on
// malformed input, unsupported key types, or unknown critical options.
CertificateParseResult parseCertificate(std::string_view blob);

// Compare two raw public-key blobs (or embedded CA keys) by wire identity.
bool samePublicKey(std::string_view lhs, std::string_view rhs) noexcept;

}  // namespace netsec

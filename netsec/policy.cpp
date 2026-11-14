#include "netsec/policy.hpp"

#include <algorithm>
#include <array>
#include <charconv>
#include <string_view>

#include "netsec/wire.hpp"

namespace netsec {

namespace {

bool parseClientAddress(std::string_view text,
                        std::vector<std::uint8_t>& bytes, int& family) {
    // Strip a possible TCP port. IPv6 literals may appear as "[::1]:512".
    if (!text.empty() && text.front() == '[') {
        std::size_t closeBracket = text.find(']');
        if (closeBracket == std::string_view::npos) {
            return false;
        }
        text = text.substr(1, closeBracket - 1);
    } else if (text.find('.') != std::string_view::npos) {
        // Bare IPv4 literal with an explicit TCP port ("10.0.0.7:51512").
        std::size_t colon = text.find(':');
        if (colon != std::string_view::npos &&
            text.find(':', colon + 1) == std::string_view::npos) {
            text = text.substr(0, colon);
        }
    }

    std::array<std::uint8_t, 4> ipv4{};
    int octets = 0;
    std::size_t start = 0;
    bool v4 = true;
    while (true) {
        std::size_t dot = text.find('.', start);
        std::string_view part =
            (dot == std::string_view::npos) ? text.substr(start)
                                            : text.substr(start, dot - start);
        int value = 0;
        const char* ptr = part.data();
        auto [end, ec] = std::from_chars(ptr, ptr + part.size(), value);
        if (ec != std::errc{} || end != ptr + part.size() || value < 0 ||
            value > 255 || octets >= 4) {
            v4 = false;
            break;
        }
        ipv4[static_cast<std::size_t>(octets++)] =
            static_cast<std::uint8_t>(value);
        if (dot == std::string_view::npos) {
            break;
        }
        start = dot + 1;
    }
    if (v4 && octets == 4) {
        bytes.assign(ipv4.begin(), ipv4.end());
        family = 4;
        return true;
    }

    // Minimal IPv6 parser with "::" compression (no embedded IPv4 notation).
    std::array<std::uint16_t, 8> groups{};
    std::size_t doubleColon = text.find("::");
    auto parseGroups = [&](std::string_view part,
                           std::array<std::uint16_t, 8>& output, int& count) {
        if (part.empty()) {
            return true;
        }
        std::size_t pos = 0;
        while (true) {
            std::size_t colon = part.find(':', pos);
            std::string_view group =
                (colon == std::string_view::npos)
                    ? part.substr(pos)
                    : part.substr(pos, colon - pos);
            if (group.empty() || group.size() > 4 || count >= 8) {
                return false;
            }
            int value = 0;
            for (char c : group) {
                int digit = 0;
                if (c >= '0' && c <= '9') {
                    digit = c - '0';
                } else if (c >= 'a' && c <= 'f') {
                    digit = c - 'a' + 10;
                } else if (c >= 'A' && c <= 'F') {
                    digit = c - 'A' + 10;
                } else {
                    return false;
                }
                value = (value << 4) | digit;
            }
            output[static_cast<std::size_t>(count++)] =
                static_cast<std::uint16_t>(value);
            if (colon == std::string_view::npos) {
                break;
            }
            pos = colon + 1;
        }
        return true;
    };

    if (doubleColon != std::string_view::npos &&
        text.find("::", doubleColon + 1) == std::string_view::npos) {
        std::array<std::uint16_t, 8> head{};
        std::array<std::uint16_t, 8> tail{};
        int headCount = 0;
        int tailCount = 0;
        if (!parseGroups(text.substr(0, doubleColon), head, headCount) ||
            !parseGroups(text.substr(doubleColon + 2), tail, tailCount) ||
            headCount + tailCount >= 8) {
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
    } else {
        int count = 0;
        if (!parseGroups(text, groups, count) || count != 8) {
            return false;
        }
    }

    bytes.resize(16);
    for (std::size_t i = 0; i < 8; ++i) {
        bytes[i * 2] = static_cast<std::uint8_t>(groups[i] >> 8);
        bytes[i * 2 + 1] = static_cast<std::uint8_t>(groups[i] & 0xFF);
    }
    family = 6;
    return true;
}

bool matchesPrefix(const std::vector<std::uint8_t>& address,
                   const IpPrefix& prefix) {
    if (address.size() != prefix.address.size()) {
        return false;
    }
    int fullBytes = prefix.prefixLength / 8;
    int remainder = prefix.prefixLength % 8;
    for (int i = 0; i < fullBytes; ++i) {
        if (address[static_cast<std::size_t>(i)] !=
            prefix.address[static_cast<std::size_t>(i)]) {
            return false;
        }
    }
    if (remainder > 0 &&
        fullBytes < static_cast<int>(address.size())) {
        std::uint8_t mask =
            static_cast<std::uint8_t>(0xFFu << (8 - remainder));
        if ((address[static_cast<std::size_t>(fullBytes)] & mask) !=
            (prefix.address[static_cast<std::size_t>(fullBytes)] & mask)) {
            return false;
        }
    }
    return true;
}

bool sourceAddressAllowed(const CriticalOptions& options,
                          const std::string& clientAddress) {
    if (options.sourceAddresses.empty()) {
        return true;  // no source-address constraint
    }
    std::vector<std::uint8_t> address;
    int family = 0;
    if (!parseClientAddress(clientAddress, address, family)) {
        return false;
    }
    for (const IpPrefix& prefix : options.sourceAddresses) {
        if (matchesPrefix(address, prefix)) {
            return true;
        }
    }
    return false;
}

bool hostMatch(std::string_view pattern, std::string_view host) {
    if (pattern == "*") {
        return true;
    }
    return pattern == host;
}

bool endpointAllowed(const std::vector<AllowedEndpoint>& allowList,
                     const ChannelRequest& request) {
    if (allowList.empty()) {
        return false;  // presence of the option denies everything by default
    }
    return std::any_of(
        allowList.begin(), allowList.end(), [&](const AllowedEndpoint& entry) {
            return hostMatch(entry.host, request.targetHost) &&
                   (entry.port == "*" || entry.port == request.targetPort);
        });
}

}  // namespace

bool StructuralSignatureVerifier::verify(
    const SshCertificate& certificate, const std::string& caKeyBlob) const {
    // Structural validation only: the signature blob must be non-empty and
    // must carry an algorithm identifier, and the CA key blob must be present.
    wire::Reader signatureReader(certificate.signature);
    std::string_view algorithm;
    std::string_view signatureBytes;
    if (!signatureReader.readString(algorithm) || algorithm.empty() ||
        !signatureReader.readString(signatureBytes) ||
        signatureBytes.empty() || !signatureReader.empty()) {
        return false;
    }
    if (caKeyBlob.empty()) {
        return false;
    }
    return true;
}

ConstrainedSession::ConstrainedSession(
    SshCertificate certificate, std::shared_ptr<TrustStore> trustStore,
    const ICertificateVerifier* verifier, std::string clientAddress)
    : certificate_(std::move(certificate)),
      trustStore_(std::move(trustStore)),
      verifier_(verifier),
      clientAddress_(std::move(clientAddress)) {}

AuthzDecision ConstrainedSession::evaluateBase(
    std::uint64_t nowUnixSeconds) const {
    AuthzDecision decision;
    decision.generation = trustStore_ ? trustStore_->generation() : 0;

    auto deny = [&](DenyReason reason, bool invalidate) {
        decision.allowed = false;
        decision.reason = reason;
        decision.invalidateSession = invalidate;
        if (invalidate) {
            markInvalid(reason);
        }
        return decision;
    };

    if (certificate_.type != CertificateType::User) {
        return deny(DenyReason::NotUserCertificate, true);
    }
    if (nowUnixSeconds < certificate_.validAfter) {
        return deny(DenyReason::NotYetValid, true);
    }
    if (nowUnixSeconds >= certificate_.validBefore) {
        return deny(DenyReason::Expired, true);
    }
    if (!sourceAddressAllowed(certificate_.critical, clientAddress_)) {
        return deny(DenyReason::SourceAddressDenied, true);
    }

    TrustStore::Snapshot snapshot = trustStore_->snapshot();
    lastGeneration_ = snapshot.generation;

    if (snapshot.revokedSerials.find(certificate_.serial) !=
        snapshot.revokedSerials.end()) {
        return deny(DenyReason::Revoked, true);
    }

    const TrustedCa* trustedCa = nullptr;
    for (const TrustedCa& ca : snapshot.cas) {
        if (samePublicKey(ca.keyBlob, certificate_.signatureKey)) {
            trustedCa = &ca;
            break;
        }
    }
    if (trustedCa == nullptr) {
        return deny(DenyReason::UntrustedCa, true);
    }
    if (!trustedCa->allowedPrincipals.empty()) {
        bool caPrincipalAllowed = false;
        for (const std::string& allowed : trustedCa->allowedPrincipals) {
            if (std::find(certificate_.principals.begin(),
                          certificate_.principals.end(),
                          allowed) != certificate_.principals.end()) {
                caPrincipalAllowed = true;
                break;
            }
        }
        if (!caPrincipalAllowed) {
            return deny(DenyReason::PrincipalMismatch, true);
        }
    }

    if (verifier_ != nullptr &&
        !verifier_->verify(certificate_, trustedCa->keyBlob)) {
        return deny(DenyReason::BadSignature, true);
    }

    decision.allowed = true;
    return decision;
}

AuthzDecision ConstrainedSession::reevaluate(
    std::uint64_t nowUnixSeconds) const {
    if (!valid_) {
        AuthzDecision decision;
        decision.reason = invalidReason_;
        decision.invalidateSession = true;
        decision.generation = lastGeneration_;
        return decision;
    }
    return evaluateBase(nowUnixSeconds);
}

AuthzDecision ConstrainedSession::evaluateSession(
    const ChannelRequest& request) const {
    AuthzDecision decision;
    decision.allowed = true;
    decision.originalCommand = request.requestedCommand;
    decision.generation = lastGeneration_;

    if (request.requestsPty && !certificate_.critical.permitPty &&
        !certificate_.extensions.permitPty) {
        decision.allowed = false;
        decision.reason = DenyReason::PtyNotPermitted;
        return decision;
    }

    // force-command is the security boundary for every session channel for
    // the whole transport lifetime: whatever the client requested, the forced
    // command is what executes. The client's string survives only as the
    // original command (e.g. SSH_ORIGINAL_COMMAND).
    if (certificate_.critical.forceCommand.has_value()) {
        decision.effectiveCommand = *certificate_.critical.forceCommand;
    } else {
        decision.effectiveCommand = request.requestedCommand;
    }
    return decision;
}

AuthzDecision ConstrainedSession::evaluateForwarding(
    const ChannelRequest& request) const {
    AuthzDecision decision;
    decision.generation = lastGeneration_;
    const CriticalOptions& critical = certificate_.critical;
    const Extensions& extensions = certificate_.extensions;

    auto deny = [&](DenyReason reason) {
        decision.allowed = false;
        decision.reason = reason;
        return decision;
    };

    switch (request.type) {
        case ChannelType::DirectTcpIp: {
            if (!critical.permitPortForwarding &&
                !extensions.permitPortForwarding) {
                return deny(DenyReason::ForwardingNotPermitted);
            }
            if (!endpointAllowed(critical.permitOpen, request)) {
                return deny(DenyReason::ForwardingTargetDenied);
            }
            break;
        }
        case ChannelType::TcpIpForward: {
            if (!critical.permitPortForwarding &&
                !extensions.permitPortForwarding) {
                return deny(DenyReason::ForwardingNotPermitted);
            }
            if (!endpointAllowed(critical.permitListen, request)) {
                return deny(DenyReason::ForwardingTargetDenied);
            }
            break;
        }
        case ChannelType::AuthAgent: {
            if (!critical.permitAgentForwarding &&
                !extensions.permitAgentForwarding) {
                return deny(DenyReason::AgentForwardingNotPermitted);
            }
            break;
        }
        case ChannelType::X11: {
            if (!critical.permitX11Forwarding &&
                !extensions.permitX11Forwarding) {
                return deny(DenyReason::X11NotPermitted);
            }
            break;
        }
        case ChannelType::Session:
            break;
    }

    decision.allowed = true;
    return decision;
}

AuthzDecision ConstrainedSession::openChannel(
    const ChannelRequest& request, std::uint64_t nowUnixSeconds) const {
    // Every multiplexed channel re-runs the full certificate evaluation
    // against the current generation before channel-specific policy applies.
    AuthzDecision base = reevaluate(nowUnixSeconds);
    if (!base.allowed) {
        return base;
    }

    if (request.type == ChannelType::Session) {
        return evaluateSession(request);
    }
    return evaluateForwarding(request);
}

AuthzDecision ConstrainedSession::onRekey(
    const std::string& newHostKeyBlob,
    std::uint64_t nowUnixSeconds) const {
    // Host-key rotation is intentionally irrelevant to user authorization.
    (void)newHostKeyBlob;
    return reevaluate(nowUnixSeconds);
}

bool ConstrainedSession::valid() const noexcept {
    return valid_;
}

DenyReason ConstrainedSession::invalidReason() const noexcept {
    return invalidReason_;
}

std::uint64_t ConstrainedSession::lastEvaluatedGeneration() const noexcept {
    return lastGeneration_;
}

const SshCertificate& ConstrainedSession::certificate() const noexcept {
    return certificate_;
}

const std::string& ConstrainedSession::clientAddress() const noexcept {
    return clientAddress_;
}

void ConstrainedSession::markInvalid(DenyReason reason) const {
    valid_ = false;
    invalidReason_ = reason;
}

ConstraintEngine::ConstraintEngine(
    std::shared_ptr<TrustStore> trustStore,
    const ICertificateVerifier* verifier)
    : trustStore_(std::move(trustStore)),
      verifier_(verifier != nullptr ? verifier : &defaultVerifier_) {}

AuthenticationResult ConstraintEngine::authenticate(
    std::string_view certificateBlob, std::string_view authenticatedPrincipal,
    std::string_view clientAddress, std::uint64_t nowUnixSeconds) const {
    AuthenticationResult result;

    CertificateParseResult parsed = parseCertificate(certificateBlob);
    if (!parsed) {
        result.decision.reason = DenyReason::MalformedCertificate;
        if (!parsed.error->message.empty() &&
            parsed.error->message.rfind("unknown critical option", 0) == 0) {
            result.decision.reason = DenyReason::UnknownCriticalOption;
        }
        result.decision.invalidateSession = true;
        return result;
    }

    SshCertificate certificate = std::move(*parsed.certificate);
    if (certificate.type != CertificateType::User) {
        result.decision.reason = DenyReason::NotUserCertificate;
        result.decision.invalidateSession = true;
        return result;
    }

    // Empty principal lists fail closed: a certificate that names no principal
    // cannot be bound to any authenticated identity.
    if (certificate.principals.empty() ||
        std::find(certificate.principals.begin(),
                  certificate.principals.end(),
                  authenticatedPrincipal) ==
            certificate.principals.end()) {
        result.decision.reason = DenyReason::PrincipalMismatch;
        result.decision.invalidateSession = true;
        return result;
    }

    auto session = std::make_shared<ConstrainedSession>(
        std::move(certificate), trustStore_, verifier_,
        std::string(clientAddress));
    result.decision = session->reevaluate(nowUnixSeconds);
    if (result.decision.allowed) {
        result.session = std::move(session);
    }
    return result;
}

const char* describe(DenyReason reason) noexcept {
    switch (reason) {
        case DenyReason::None:
            return "allowed";
        case DenyReason::MalformedCertificate:
            return "malformed certificate";
        case DenyReason::NotUserCertificate:
            return "not a user certificate";
        case DenyReason::PrincipalMismatch:
            return "principal mismatch";
        case DenyReason::UntrustedCa:
            return "untrusted CA";
        case DenyReason::BadSignature:
            return "signature verification failed";
        case DenyReason::NotYetValid:
            return "certificate not yet valid";
        case DenyReason::Expired:
            return "certificate expired";
        case DenyReason::Revoked:
            return "certificate revoked";
        case DenyReason::UnknownCriticalOption:
            return "unknown critical option";
        case DenyReason::SourceAddressDenied:
            return "source address denied";
        case DenyReason::ForcedCommandMismatch:
            return "forced command mismatch";
        case DenyReason::ForwardingNotPermitted:
            return "forwarding not permitted";
        case DenyReason::ForwardingTargetDenied:
            return "forwarding target denied";
        case DenyReason::PtyNotPermitted:
            return "pty not permitted";
        case DenyReason::X11NotPermitted:
            return "X11 forwarding not permitted";
        case DenyReason::AgentForwardingNotPermitted:
            return "agent forwarding not permitted";
        case DenyReason::UnsupportedChannel:
            return "unsupported channel";
    }
    return "unknown";
}

}  // namespace netsec

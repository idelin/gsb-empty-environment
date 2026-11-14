#pragma once

#include <cstdint>
#include <memory>
#include <string>

#include "netsec/certificate.hpp"
#include "netsec/trust_store.hpp"

namespace netsec {

// Cryptographic signature verification boundary.
//
// This component deliberately contains no cryptography: a production server
// injects an implementation backed by its SSH library (e.g. libcrypto /
// libsodium). StructuralSignatureVerifier only validates the signature blob
// shape and is suitable for tests and for transports that authenticate the
// certificate out-of-band.
class ICertificateVerifier {
public:
    virtual ~ICertificateVerifier() = default;

    // Verifies `certificate.signature` over `certificate.rawCertificate` using
    // `caKeyBlob` (the raw public-key blob from the trust store).
    virtual bool verify(const SshCertificate& certificate,
                        const std::string& caKeyBlob) const = 0;
};

// Default verifier: structural validation only. Must be replaced in a real
// deployment unless the transport already proved certificate authenticity.
class StructuralSignatureVerifier final : public ICertificateVerifier {
public:
    bool verify(const SshCertificate& certificate,
                const std::string& caKeyBlob) const override;
};

enum class ChannelType {
    Session,        // shell / exec / subsystem
    DirectTcpIp,    // local port forwarding
    TcpIpForward,   // remote port forwarding
    X11,
    AuthAgent,      // ssh-agent forwarding
};

struct ChannelRequest {
    ChannelType type = ChannelType::Session;

    // Session channel: the command the client asked to execute, if any.
    // Empty means an interactive shell. With force-command this value is
    // ignored when deciding what runs and preserved as the "original command".
    std::string requestedCommand;
    bool requestsPty = false;

    // DirectTcpIp / TcpIpForward target.
    std::string targetHost;
    std::string targetPort;  // decimal port number as text
};

enum class DenyReason {
    None,
    MalformedCertificate,
    NotUserCertificate,
    PrincipalMismatch,
    UntrustedCa,
    BadSignature,
    NotYetValid,
    Expired,
    Revoked,
    UnknownCriticalOption,
    SourceAddressDenied,
    ForcedCommandMismatch,
    ForwardingNotPermitted,
    ForwardingTargetDenied,
    PtyNotPermitted,
    X11NotPermitted,
    AgentForwardingNotPermitted,
    UnsupportedChannel,
};

struct AuthzDecision {
    bool allowed = false;
    DenyReason reason = DenyReason::None;
    // The command that must actually execute on a session channel. Set to the
    // force-command when present, otherwise the client's requested command.
    std::string effectiveCommand;
    // What the client originally asked for (empty for an interactive shell).
    std::string originalCommand;
    // True when this failure is a policy/session-integrity violation that must
    // tear the whole transport down rather than merely rejecting the channel.
    bool invalidateSession = false;
    // Generation of the trust store against which this decision was made.
    std::uint64_t generation = 0;
};

// One authenticated, certificate-bound SSH transport.
//
// The constraints captured here outlive the first authentication: every
// channel open and every rekey re-runs the full evaluation against the current
// trust store, so connection multiplexing and renegotiation cannot restore
// capabilities the certificate never granted.
class ConstrainedSession {
public:
    ConstrainedSession(SshCertificate certificate,
                       std::shared_ptr<TrustStore> trustStore,
                       const ICertificateVerifier* verifier,
                       std::string clientAddress);

    // Full re-evaluation of certificate identity, validity window, revocation
    // and source address against the current trust-store generation. Called
    // automatically on every channel open and rekey; also callable directly.
    AuthzDecision reevaluate(std::uint64_t nowUnixSeconds) const;

    // Evaluate a channel open request. Re-runs reevaluate() first, then applies
    // the certificate's channel-specific constraints.
    AuthzDecision openChannel(const ChannelRequest& request,
                              std::uint64_t nowUnixSeconds) const;

    // Rekey handler. Re-evaluates the whole certificate. `newHostKeyBlob` is
    // intentionally unused: host-key rotation changes transport identity, never
    // user authorization. Returns the refreshed decision.
    AuthzDecision onRekey(const std::string& newHostKeyBlob,
                          std::uint64_t nowUnixSeconds) const;

    bool valid() const noexcept;
    DenyReason invalidReason() const noexcept;
    std::uint64_t lastEvaluatedGeneration() const noexcept;

    const SshCertificate& certificate() const noexcept;
    const std::string& clientAddress() const noexcept;

private:
    AuthzDecision evaluateBase(std::uint64_t nowUnixSeconds) const;
    AuthzDecision evaluateSession(const ChannelRequest& request) const;
    AuthzDecision evaluateForwarding(const ChannelRequest& request) const;
    void markInvalid(DenyReason reason) const;

    SshCertificate certificate_;
    std::shared_ptr<TrustStore> trustStore_;
    const ICertificateVerifier* verifier_;
    std::string clientAddress_;
    mutable bool valid_ = true;
    mutable DenyReason invalidReason_ = DenyReason::None;
    mutable std::uint64_t lastGeneration_ = 0;
};

struct AuthenticationResult {
    AuthzDecision decision;
    std::shared_ptr<ConstrainedSession> session;  // null on failure
};

// Entry point used by the SSH server after transport-level authentication.
// Parsing failures and unknown critical options are rejected before any state
// is created.
class ConstraintEngine {
public:
    ConstraintEngine(std::shared_ptr<TrustStore> trustStore,
                     const ICertificateVerifier* verifier = nullptr);

    AuthenticationResult authenticate(std::string_view certificateBlob,
                                      std::string_view authenticatedPrincipal,
                                      std::string_view clientAddress,
                                      std::uint64_t nowUnixSeconds) const;

private:
    std::shared_ptr<TrustStore> trustStore_;
    StructuralSignatureVerifier defaultVerifier_;
    const ICertificateVerifier* verifier_;
};

const char* describe(DenyReason reason) noexcept;

}  // namespace netsec

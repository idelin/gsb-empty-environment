#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <mutex>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

using Byte = std::uint8_t;
using Bytes = std::vector<Byte>;
using ByteView = std::span<const Byte>;

inline constexpr std::size_t kSmb2HeaderSize = 64;
inline constexpr std::size_t kPreauthHashSize = 64;
inline constexpr std::size_t kProofSize = 32;

// ---------------------------------------------------------------------------
// Wire identifiers
// ---------------------------------------------------------------------------

enum class MessageKind : std::uint16_t {
    NegotiateRequest = 0x0000,
    NegotiateResponse = 0x0001,
    SessionSetupRequest = 0x0002,
    SessionSetupResponse = 0x0003,
};

enum class Dialect : std::uint16_t {
    Smb2002 = 0x0202,
    Smb210 = 0x0210,
    Smb300 = 0x0300,
    Smb302 = 0x0302,
    Smb311 = 0x0311,
};

enum class Cipher : std::uint16_t {
    None = 0x0000,
    Aes128Ccm = 0x0001,
    Aes128Gcm = 0x0002,
    Aes256Ccm = 0x0003,
    Aes256Gcm = 0x0004,
};

enum class SigningAlgorithm : std::uint16_t {
    HmacSha256 = 0x0000,
    AesCmac = 0x0001,
    AesGmac = 0x0002,
};

enum class PreauthHashAlgorithm : std::uint16_t {
    Sha512 = 0x0001,
};

enum class ErrorCode : std::uint16_t {
    None = 0,
    InvalidArgument,
    ChainResetNotAllowed,
    EmptyMessage,
    MessageTruncated,
    MessageNotSmb2,
    UnexpectedCommand,
    CompoundMessageUnsupported,
    UnexpectedMessage,
    NegotiationIncomplete,
    NegotiationAlreadyFinished,
    SessionSetupFailed,
    DuplicateNegotiateContext,
    DuplicateAlgorithm,
    UnknownPreauthAlgorithm,
    UnknownCipherAlgorithm,
    UnknownCompressionAlgorithm,
    UnknownMandatoryAlgorithm,
    MissingPreauthIntegrityContext,
    InvalidNegotiateContext,
    InvalidSecurityBuffer,
    DialectNotOffered,
    UnsupportedDialect,
    DialectDowngrade,
    DialectMismatch,
    ServerGuidMismatch,
    CipherMismatch,
    SigningMismatch,
    PreauthAlgorithmMismatch,
    PeerIdentityMismatch,
    PreauthHashMismatch,
    ProofMismatch,
    DuplicateChannel,
    StaleAttempt,
    AttemptFailed,
    SessionNotEstablished,
    AsyncFailure,
    InternalError,
};

class Error : public std::runtime_error {
  public:
    Error(ErrorCode code, std::string message);

    [[nodiscard]] ErrorCode code() const noexcept { return code_; }

  private:
    ErrorCode code_;
};

[[nodiscard]] std::string_view to_string(ErrorCode code) noexcept;

// ---------------------------------------------------------------------------
// Digests, GUIDs and cryptographic helpers
// ---------------------------------------------------------------------------

struct Digest512 {
    std::array<Byte, kPreauthHashSize> bytes{};

    [[nodiscard]] bool is_zero() const noexcept;
    [[nodiscard]] bool operator==(const Digest512&) const noexcept = default;
    [[nodiscard]] std::string to_hex() const;
};

struct Digest256 {
    std::array<Byte, 32> bytes{};

    [[nodiscard]] bool operator==(const Digest256&) const noexcept = default;
    [[nodiscard]] std::string to_hex() const;
};

struct Guid {
    std::array<Byte, 16> bytes{};

    [[nodiscard]] bool operator==(const Guid&) const noexcept = default;
    [[nodiscard]] std::string to_hex() const;
};

[[nodiscard]] Digest512 sha512(ByteView data) noexcept;
[[nodiscard]] Digest256 sha256(ByteView data) noexcept;
[[nodiscard]] Bytes hmac_sha256(ByteView key, ByteView data);
[[nodiscard]] bool constant_time_equal(ByteView lhs, ByteView rhs) noexcept;
[[nodiscard]] Bytes to_bytes(std::string_view value);

// ---------------------------------------------------------------------------
// Raw pre-authentication transcript
// ---------------------------------------------------------------------------

/**
 * One SMB 3.1.1 pre-authentication integrity chain, scoped to exactly one
 * TCP transport connection.
 *
 * The chain is initialised with 64 zero bytes and every appended message is
 * hashed exactly as supplied:
 *
 *     H(0) = 64 * 0x00
 *     H(n) = SHA-512(H(n-1) || raw_message_n)
 *
 * No SMB header/body re-encoding happens in this class.  The transcript
 * history is retained so a new connection can never inherit another
 * connection's progress by resetting the same object.  Call reset() only while
 * the chain is pristine; allocate a new chain for a new TCP connection.
 */
class PreauthIntegrityChain {
  public:
    PreauthIntegrityChain();

    /** Reset is accepted only for a pristine chain (no messages hashed yet). */
    void reset();

    /** Hash one complete raw SMB2 message exactly as it appeared on the wire. */
    void append(MessageKind kind, ByteView raw_message);
    void append(MessageKind kind, const Bytes& raw_message) {
        append(kind, ByteView(raw_message));
    }

    [[nodiscard]] const Digest512& digest() const noexcept { return digest_; }
    [[nodiscard]] Digest512 current_hash() const noexcept { return digest_; }
    [[nodiscard]] const std::vector<Bytes>& messages() const noexcept { return messages_; }
    [[nodiscard]] std::size_t message_count() const noexcept { return messages_.size(); }
    [[nodiscard]] bool started() const noexcept { return !messages_.empty(); }

    /** A brand-new per-connection state, always starting from all zeroes. */
    [[nodiscard]] static PreauthIntegrityChain new_connection() {
        return PreauthIntegrityChain{};
    }

  private:
    Digest512 digest_{};
    std::vector<Bytes> messages_;
};

// ---------------------------------------------------------------------------
// Negotiated security parameters and completed channel transcript
// ---------------------------------------------------------------------------

struct PeerIdentity {
    std::string host;
    Bytes certificate_sha256;

    [[nodiscard]] bool operator==(const PeerIdentity& other) const noexcept;
    [[nodiscard]] bool operator!=(const PeerIdentity& other) const noexcept {
        return !(*this == other);
    }
};

struct SecurityParameters {
    Dialect dialect = Dialect::Smb311;
    Cipher cipher = Cipher::None;
    SigningAlgorithm signing = SigningAlgorithm::AesCmac;
    PreauthHashAlgorithm preauth_hash = PreauthHashAlgorithm::Sha512;
    Guid server_guid{};

    [[nodiscard]] bool operator==(const SecurityParameters&) const noexcept = default;
};

/**
 * The immutable result of one TCP connection's NEGOTIATE and SESSION_SETUP
 * exchange.  It may only be produced by ChannelNegotiation::finish().
 */
struct ChannelTranscript {
    std::uint64_t channel_id = 0;
    SecurityParameters security{};
    PeerIdentity peer{};
    Digest512 preauth_hash{};
    bool dialect_offered = true;
    std::size_t message_count = 0;

    [[nodiscard]] bool operator==(const ChannelTranscript&) const noexcept = default;
};

struct ChannelOptions {
    std::uint64_t channel_id = 0;
    PeerIdentity peer{};

    // Optional local policy overrides.  When absent, the values are derived
    // from the NEGOTIATE response and the selected dialect.
    std::optional<Cipher> selected_cipher;
    std::optional<SigningAlgorithm> selected_signing;

    bool require_smb311_preauth_context = true;
    bool reject_unknown_algorithms = true;
    bool reject_unknown_contexts = false;
    bool require_successful_session_setup = true;
};

/**
 * Transactional parser for one transport connection.  append() validates the
 * complete raw message before touching the pre-authentication chain.  A
 * rejected append leaves the chain byte-for-byte unchanged, so malformed or
 * out-of-order traffic can never publish a partial negotiation.
 */
class ChannelNegotiation {
  public:
    explicit ChannelNegotiation(ChannelOptions options = {});

    void append(MessageKind kind, ByteView raw_message);
    void append(MessageKind kind, const Bytes& raw_message) {
        append(kind, ByteView(raw_message));
    }

    [[nodiscard]] bool complete() const noexcept;
    [[nodiscard]] bool failed() const noexcept;
    [[nodiscard]] bool finished() const noexcept;
    [[nodiscard]] const Digest512& current_hash() const noexcept;
    [[nodiscard]] const SecurityParameters& security() const noexcept;

    /** Finish only a fully successful transcript; no partial result escapes. */
    [[nodiscard]] ChannelTranscript finish();

    /** Mark an asynchronous transport attempt failed; it can never finish. */
    void fail(ErrorCode reason = ErrorCode::AsyncFailure);

  private:
    enum class Stage {
        NegotiateRequest,
        NegotiateResponse,
        SessionSetupRequest,
        SessionSetupResponse,
        Complete,
        Failed,
    };

    void validate_and_update(MessageKind kind, ByteView raw);
    void parse_negotiate_request(ByteView raw);
    void parse_negotiate_response(ByteView raw);
    void check_transport_message(MessageKind kind, ByteView raw) const;

    ChannelOptions options_;
    Stage stage_{Stage::NegotiateRequest};
    PreauthIntegrityChain chain_{};
    SecurityParameters security_{};
    std::vector<Dialect> offered_dialects_;
    std::vector<Cipher> offered_ciphers_;
    std::vector<PreauthHashAlgorithm> offered_preauth_algorithms_;
    bool saw_preauth_context_{false};
    bool dialect_offered_{true};
    bool completed_session_setup_{false};
    bool final_session_setup_success_{false};
    bool finished_{false};
    bool failed_{false};
    ErrorCode failure_reason_{ErrorCode::None};
};

// ---------------------------------------------------------------------------
// Session identity and authenticated channel proof
// ---------------------------------------------------------------------------

/**
 * Immutable security identity exported by a successfully established session.
 * A reconnect can consume this value but can never replace it with values
 * derived from a new, attacker-influenced connection.
 */
class SessionIdentity {
  public:
    SessionIdentity(const SessionIdentity&) = default;
    SessionIdentity(SessionIdentity&&) noexcept = default;
    SessionIdentity& operator=(const SessionIdentity&) = delete;
    SessionIdentity& operator=(SessionIdentity&&) noexcept = delete;
    ~SessionIdentity() = default;

    [[nodiscard]] Dialect dialect() const noexcept { return security_.dialect; }
    [[nodiscard]] Cipher cipher() const noexcept { return security_.cipher; }
    [[nodiscard]] SigningAlgorithm signing() const noexcept { return security_.signing; }
    [[nodiscard]] PreauthHashAlgorithm preauth_algorithm() const noexcept {
        return security_.preauth_hash;
    }
    [[nodiscard]] const Guid& server_guid() const noexcept { return security_.server_guid; }
    [[nodiscard]] const std::string& host() const noexcept { return peer_.host; }
    [[nodiscard]] const Bytes& certificate_sha256() const noexcept {
        return peer_.certificate_sha256;
    }
    [[nodiscard]] const Digest512& preauth_hash() const noexcept { return preauth_hash_; }
    [[nodiscard]] const SecurityParameters& security() const noexcept { return security_; }
    [[nodiscard]] const PeerIdentity& peer() const noexcept { return peer_; }
    [[nodiscard]] const Digest256& binding_digest() const noexcept { return binding_; }
    [[nodiscard]] const Digest256& authenticated_key_commitment() const noexcept {
        return key_commitment_;
    }

    [[nodiscard]] bool operator==(const SessionIdentity&) const noexcept = default;

  private:
    friend class SessionNegotiation;
    SessionIdentity(SecurityParameters security, PeerIdentity peer, Digest512 hash,
                    ByteView authenticated_session_key);

    SecurityParameters security_;
    PeerIdentity peer_;
    Digest512 preauth_hash_;
    Digest256 key_commitment_;
    Digest256 binding_;
};

[[nodiscard]] Bytes compute_preauth_proof(ByteView authenticated_session_key,
                                          const ChannelTranscript& channel);
[[nodiscard]] bool verify_preauth_proof(ByteView authenticated_session_key,
                                        const ChannelTranscript& channel,
                                        ByteView proof) noexcept;

struct BoundChannel {
    std::uint64_t channel_id = 0;
    SecurityParameters security{};
    PeerIdentity peer{};
    Digest512 preauth_hash{};
    Digest256 session_binding{};
};

/**
 * Established SMB session.  The identity is frozen at establishment time.
 * Joining an auxiliary channel verifies that channel's complete pre-auth chain
 * with the authenticated session key and requires every negotiated security
 * parameter, the server GUID and the peer identity to equal the frozen
 * identity before the channel is published.
 */
class SessionNegotiation {
  public:
    SessionNegotiation(const SessionNegotiation&) = default;
    SessionNegotiation(SessionNegotiation&&) noexcept = default;
    SessionNegotiation& operator=(const SessionNegotiation&) = delete;
    SessionNegotiation& operator=(SessionNegotiation&&) noexcept = delete;
    ~SessionNegotiation();

    [[nodiscard]] static SessionNegotiation establish(const ChannelTranscript& channel,
                                                      ByteView authenticated_session_key,
                                                      ByteView channel_proof);

    [[nodiscard]] static std::optional<SessionNegotiation> try_establish(
        const ChannelTranscript& channel, ByteView authenticated_session_key,
        ByteView channel_proof, ErrorCode* error = nullptr) noexcept;

    [[nodiscard]] const SessionIdentity& identity() const noexcept { return identity_; }
    [[nodiscard]] const std::vector<BoundChannel>& channels() const noexcept {
        return channels_;
    }
    [[nodiscard]] std::size_t channel_count() const noexcept { return channels_.size(); }

    /** Join a multichannel transport using the session key stored at setup. */
    [[nodiscard]] BoundChannel join(const ChannelTranscript& channel, ByteView channel_proof);

    /** Join using an explicitly supplied authenticated session key. */
    [[nodiscard]] BoundChannel join(const ChannelTranscript& channel,
                                    ByteView authenticated_session_key,
                                    ByteView channel_proof);

    [[nodiscard]] std::optional<ErrorCode> try_join(const ChannelTranscript& channel,
                                                    ByteView channel_proof) noexcept;
    [[nodiscard]] std::optional<ErrorCode> try_join(const ChannelTranscript& channel,
                                                    ByteView authenticated_session_key,
                                                    ByteView channel_proof) noexcept;

    /**
     * Reconnect using only the immutable identity exported by the original
     * session.  The result keeps that identity (including the original preauth
     * hash); lower dialects, different GUIDs and different security parameters
     * are rejected.
     */
    [[nodiscard]] static SessionNegotiation reconnect(
        const SessionIdentity& original_identity, const ChannelTranscript& new_channel,
        ByteView authenticated_session_key, ByteView channel_proof);

    [[nodiscard]] static std::optional<SessionNegotiation> try_reconnect(
        const SessionIdentity& original_identity, const ChannelTranscript& new_channel,
        ByteView authenticated_session_key, ByteView channel_proof,
        ErrorCode* error = nullptr) noexcept;

  private:
    SessionNegotiation(SessionIdentity identity, Bytes session_key,
                       std::vector<BoundChannel> channels);

    [[nodiscard]] ErrorCode validate_channel(const ChannelTranscript& channel,
                                             bool reconnect) const noexcept;

    SessionIdentity identity_;
    Bytes session_key_;
    std::vector<BoundChannel> channels_;
    std::unordered_set<std::uint64_t> channel_ids_;
};

using NegotiatedSession = SessionNegotiation;
using NegotiationState = ChannelNegotiation;
using PreauthHash = PreauthIntegrityChain;
using SessionBinding = SessionIdentity;

// ---------------------------------------------------------------------------
// Asynchronous connection transactions
// ---------------------------------------------------------------------------

/**
 * Small attempt coordinator for callers whose transports complete on a
 * different thread.  A channel is published at most once and only after both
 * its complete transcript and its proof have been verified.  Failing an
 * attempt, completing it out of order, using a stale handle, or racing a
 * reconnect never leaves a partial session behind.
 */
class AsyncSessionNegotiation {
  public:
    struct AttemptId {
        std::uint64_t epoch = 0;
        std::uint64_t id = 0;

        [[nodiscard]] bool operator==(const AttemptId&) const noexcept = default;
    };

    [[nodiscard]] static AsyncSessionNegotiation establish(
        const ChannelTranscript& channel, ByteView authenticated_session_key,
        ByteView channel_proof);

    [[nodiscard]] AttemptId begin_channel(ChannelOptions options);
    void append(AttemptId attempt, MessageKind kind, ByteView raw_message);
    void append(AttemptId attempt, MessageKind kind, const Bytes& raw_message) {
        append(attempt, kind, ByteView(raw_message));
    }
    void fail(AttemptId attempt, ErrorCode reason = ErrorCode::AsyncFailure);

    [[nodiscard]] bool complete(AttemptId attempt, ByteView channel_proof,
                                ErrorCode* error = nullptr);
    [[nodiscard]] bool complete(AttemptId attempt, ByteView authenticated_session_key,
                                ByteView channel_proof, ErrorCode* error = nullptr);

    [[nodiscard]] const SessionIdentity& identity() const noexcept { return session_.identity(); }
    [[nodiscard]] const std::vector<BoundChannel>& channels() const noexcept {
        return session_.channels();
    }
    [[nodiscard]] SessionNegotiation& session() noexcept { return session_; }
    [[nodiscard]] const SessionNegotiation& session() const noexcept { return session_; }

    /** Move the async wrapper's session out after all attempts are quiesced. */
    [[nodiscard]] SessionNegotiation take_session();

  private:
    struct AttemptState {
        ChannelNegotiation negotiation;
        bool failed = false;
        bool completed = false;
    };

    explicit AsyncSessionNegotiation(SessionNegotiation session) noexcept;

    AttemptState* find(AttemptId attempt, ErrorCode& error) noexcept;

    mutable std::mutex mutex_;
    SessionNegotiation session_;
    std::uint64_t next_attempt_id_{1};
    std::unordered_map<std::uint64_t, AttemptState> attempts_;
    std::unordered_set<std::uint64_t> retired_attempts_;
};

}  // namespace netsec

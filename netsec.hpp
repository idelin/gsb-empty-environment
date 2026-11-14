#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <limits>
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
using PublicKey = std::array<Byte, 32>;
using Signature = std::array<Byte, 64>;
using Digest = std::array<Byte, 32>;

[[nodiscard]] std::string to_hex(std::span<const Byte> bytes);
[[nodiscard]] Bytes from_hex(std::string_view text);
[[nodiscard]] Digest sha256(std::span<const Byte> data);
[[nodiscard]] Digest sha256(std::string_view text);

// Ed25519 signing key. Only the download service should hold one of these.
class PrivateKey {
public:
    PrivateKey();
    explicit PrivateKey(const std::array<Byte, 32>& seed);

    [[nodiscard]] static PrivateKey generate();
    [[nodiscard]] static PrivateKey from_hex(std::string_view seed_hex);

    [[nodiscard]] PublicKey public_key() const;
    [[nodiscard]] Signature sign(std::span<const Byte> message) const;
    [[nodiscard]] Signature sign(std::string_view message) const;
    [[nodiscard]] const std::array<Byte, 32>& seed() const noexcept;

private:
    std::array<Byte, 32> seed_{};
};

using SigningKey = PrivateKey;

struct Certificate {
    std::string subject;
    std::string issuer;
    PublicKey public_key{};
    std::uint64_t serial{};
    std::int64_t not_before{0};
    std::int64_t not_after{std::numeric_limits<std::int64_t>::max()};
    bool is_ca{false};
    Signature signature{};

    [[nodiscard]] Bytes signing_payload() const;
};

[[nodiscard]] Bytes certificate_signing_payload(const Certificate& certificate);

[[nodiscard]] Certificate make_root_certificate(
    std::string subject,
    const PrivateKey& key,
    std::uint64_t serial = 1,
    std::int64_t not_before = 0,
    std::int64_t not_after = std::numeric_limits<std::int64_t>::max());

[[nodiscard]] Certificate issue_certificate(
    std::string subject,
    const PublicKey& subject_key,
    std::string issuer,
    const PrivateKey& issuer_key,
    std::uint64_t serial,
    std::int64_t not_before = 0,
    std::int64_t not_after = std::numeric_limits<std::int64_t>::max(),
    bool is_ca = false);

[[nodiscard]] Certificate issue_certificate(
    std::string subject,
    const PublicKey& subject_key,
    const Certificate& issuer_certificate,
    const PrivateKey& issuer_key,
    std::uint64_t serial,
    std::int64_t not_before = 0,
    std::int64_t not_after = std::numeric_limits<std::int64_t>::max(),
    bool is_ca = false);

struct TrustAnchor {
    std::string subject;
    PublicKey public_key{};
};

struct Profile {
    std::string id;
    std::uint64_t version{};
    Bytes payload;
};

struct DownloadRequest {
    std::string transaction_id;
    std::string eid;
    std::string server_identity;
    std::string nonce;
    Profile profile;
    std::vector<Certificate> certificate_chain;
    Signature signature{};
    // 0 means "use the current system time" for certificate validity checks.
    std::int64_t validation_time{0};
};

[[nodiscard]] Bytes download_signing_payload(const DownloadRequest& request);
[[nodiscard]] Signature sign_download_request(
    const PrivateKey& server_key,
    const DownloadRequest& request);
[[nodiscard]] bool verify_download_signature(
    const PublicKey& server_key,
    const DownloadRequest& request);

enum class ErrorCode {
    None = 0,
    InvalidArgument,
    EidMismatch,
    TransactionAlreadyUsed,
    InvalidNonce,
    NonceReplayed,
    InvalidCertificateChain,
    UntrustedCertificate,
    CertificateNotYetValid,
    CertificateExpired,
    ServerIdentityMismatch,
    InvalidSignature,
    VersionRollback,
    UnknownSession,
    StageOutOfOrder,
    StageAlreadyConfirmed,
    TransactionAlreadyInstalled,
    StateMismatch,
};

[[nodiscard]] const char* error_code_name(ErrorCode code) noexcept;

class TransactionError : public std::runtime_error {
public:
    TransactionError(ErrorCode code, std::string message);

    [[nodiscard]] ErrorCode code() const noexcept;

private:
    ErrorCode code_;
};

template <typename T>
struct Result {
    std::optional<T> value;
    ErrorCode error{ErrorCode::None};
    std::string message;

    [[nodiscard]] explicit operator bool() const noexcept { return value.has_value(); }
    [[nodiscard]] bool ok() const noexcept { return value.has_value(); }
    [[nodiscard]] T& operator*() { return *value; }
    [[nodiscard]] const T& operator*() const { return *value; }
    [[nodiscard]] T* operator->() { return &*value; }
    [[nodiscard]] const T* operator->() const { return &*value; }
};

struct StatusResult {
    ErrorCode error{ErrorCode::None};
    std::string message;

    [[nodiscard]] explicit operator bool() const noexcept { return error == ErrorCode::None; }
    [[nodiscard]] bool ok() const noexcept { return error == ErrorCode::None; }
};

enum class DownloadStage {
    Downloaded = 0,
    DownloadConfirmed,
    Installed,
};

struct DownloadReceipt {
    std::string transaction_id;
    std::string session_id;
    std::string eid;
    std::string server_identity;
    std::string nonce;
    std::string profile_id;
    std::uint64_t profile_version{};
    Digest profile_digest{};
    Bytes profile_data;
};

struct InstallReceipt {
    std::string transaction_id;
    std::string session_id;
    std::string eid;
    std::string profile_id;
    std::uint64_t profile_version{};
    Digest profile_digest{};
};

struct ManagerState {
    std::string eid;
    std::unordered_map<std::string, std::uint64_t> installed_versions;
    std::unordered_map<std::string, Bytes> installed_profiles;
    std::unordered_set<std::string> used_transactions;
    std::unordered_set<std::string> used_nonces;
};

class DownloadTransactionManager {
public:
    explicit DownloadTransactionManager(std::string device_eid);
    DownloadTransactionManager(std::string device_eid,
                               const Certificate& root_certificate);
    DownloadTransactionManager(std::string device_eid, TrustAnchor anchor);

    [[nodiscard]] const std::string& eid() const noexcept;

    void add_trust_anchor(const Certificate& root_certificate);
    void add_trust_anchor(TrustAnchor anchor);
    void add_trust_anchor(std::string subject, const PublicKey& public_key);
    [[nodiscard]] std::size_t trust_anchor_count() const noexcept;

    // A service should obtain this nonce from the target device and put it in
    // the signed request. Once nonce issuance is used, only issued, unused
    // nonces are accepted.
    [[nodiscard]] std::string issue_nonce();
    void require_issued_nonce(bool required = true) noexcept;
    [[nodiscard]] bool requires_issued_nonce() const noexcept;

    // Stage 1: validate and reserve the transaction. Throws TransactionError.
    [[nodiscard]] DownloadReceipt download(const DownloadRequest& request);
    [[nodiscard]] DownloadReceipt begin_download(const DownloadRequest& request);
    [[nodiscard]] Result<DownloadReceipt> try_download(const DownloadRequest& request) noexcept;
    [[nodiscard]] Result<DownloadReceipt> try_begin_download(
        const DownloadRequest& request) noexcept;

    // Stage 2: the caller confirms that the profile was transferred intact.
    void confirm_download(std::string_view session_id);
    void confirm_download(const DownloadReceipt& receipt);
    [[nodiscard]] StatusResult try_confirm_download(std::string_view session_id) noexcept;
    [[nodiscard]] StatusResult try_confirm_download(
        const DownloadReceipt& receipt) noexcept;

    // Stage 3: atomically commit the stored profile and advance its version
    // floor. The receipt's profile bytes are never trusted during this step.
    [[nodiscard]] InstallReceipt install(std::string_view session_id);
    [[nodiscard]] InstallReceipt install(const DownloadReceipt& receipt);
    [[nodiscard]] InstallReceipt confirm_install(std::string_view session_id);
    [[nodiscard]] InstallReceipt confirm_install(const DownloadReceipt& receipt);
    [[nodiscard]] Result<InstallReceipt> try_install(std::string_view session_id) noexcept;
    [[nodiscard]] Result<InstallReceipt> try_install(const DownloadReceipt& receipt) noexcept;
    [[nodiscard]] Result<InstallReceipt> try_confirm_install(
        std::string_view session_id) noexcept;
    [[nodiscard]] Result<InstallReceipt> try_confirm_install(
        const DownloadReceipt& receipt) noexcept;

    [[nodiscard]] DownloadStage stage(std::string_view session_id) const;
    [[nodiscard]] bool transaction_seen(std::string_view transaction_id) const noexcept;
    [[nodiscard]] std::optional<Bytes> downloaded_profile(
        std::string_view session_id) const;
    [[nodiscard]] std::uint64_t installed_version(std::string_view profile_id) const noexcept;
    [[nodiscard]] std::optional<Bytes> installed_profile(
        std::string_view profile_id) const;

    [[nodiscard]] ManagerState snapshot() const;
    void restore_state(const ManagerState& state);

private:
    struct PendingTransaction {
        std::string transaction_id;
        std::string session_id;
        std::string server_identity;
        std::string nonce;
        std::string profile_id;
        std::uint64_t profile_version{};
        Digest profile_digest{};
        Bytes profile_data;
        DownloadStage stage{DownloadStage::Downloaded};
    };

    [[nodiscard]] DownloadReceipt download_impl(const DownloadRequest& request);
    [[nodiscard]] InstallReceipt install_impl(std::string_view session_id);
    void confirm_download_impl(std::string_view session_id);
    [[nodiscard]] ErrorCode validate_certificate_chain(
        const std::vector<Certificate>& chain,
        std::string_view expected_server_identity,
        std::int64_t validation_time) const;

    std::string device_eid_;
    std::unordered_map<std::string, std::vector<PublicKey>> trust_anchors_;
    std::unordered_set<std::string> outstanding_nonces_;
    bool require_issued_nonce_{false};

    std::unordered_set<std::string> used_transactions_;
    std::unordered_set<std::string> used_nonces_;
    std::unordered_map<std::string, PendingTransaction> sessions_;
    std::unordered_map<std::string, std::uint64_t> installed_versions_;
    std::unordered_map<std::string, Bytes> installed_profiles_;
};

using DownloadTransaction = DownloadTransactionManager;
using TransactionManager = DownloadTransactionManager;

}  // namespace netsec

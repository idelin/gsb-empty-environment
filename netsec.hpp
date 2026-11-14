#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec::biometric {

enum class Algorithm : std::uint16_t {
    unknown = 0,
    fingerprint_v1 = 1,
    fingerprint_v2 = 2,
    fingerprint_v3 = 3,
};

[[nodiscard]] constexpr std::uint16_t algorithm_version(Algorithm algorithm) noexcept
{
    return static_cast<std::uint16_t>(algorithm);
}

[[nodiscard]] constexpr bool is_known_algorithm(Algorithm algorithm) noexcept
{
    return algorithm == Algorithm::fingerprint_v1
        || algorithm == Algorithm::fingerprint_v2
        || algorithm == Algorithm::fingerprint_v3;
}

enum class Status : std::uint8_t {
    ok = 0,
    invalid_argument,
    malformed_template,
    integrity_failure,
    key_mismatch,
    user_mismatch,
    device_mismatch,
    enrollment_session_mismatch,
    stale_generation,
    template_expired,
    template_not_yet_valid,
    algorithm_downgrade,
    algorithm_mismatch,
    unsupported_algorithm,
    secure_memory_failure,
};

[[nodiscard]] std::string_view status_name(Status status) noexcept;

// The registration identity is authenticated as part of the template envelope.
// Replacing a victim's record with another valid envelope therefore cannot make
// the identity fields match the victim's lookup key.
struct TemplateRegistration {
    std::string user_id;
    std::string device_id;
    std::string enrollment_session_id;
    Algorithm algorithm{Algorithm::fingerprint_v2};
    std::uint64_t generation{0};
    std::uint64_t issued_at_unix{0};
};

// Values supplied by the trusted authentication path, not by the stored record.
struct TemplateExpectation {
    std::string user_id;
    std::string device_id;
    std::string enrollment_session_id;

    // Optional server-side high-water mark. A valid but older envelope is
    // rejected when this does not match.
    std::optional<std::uint64_t> expected_generation;
};

struct TemplateMetadata {
    std::string user_id;
    std::string device_id;
    std::string enrollment_session_id;
    Algorithm algorithm{Algorithm::unknown};
    std::uint64_t generation{0};
    std::uint64_t issued_at_unix{0};
    std::string key_id;
};

// Policy values must come from the trusted service configuration. In
// particular, minimum_generation is the server-side enrollment high-water mark
// used to reject a correctly signed but obsolete template.
struct ValidationPolicy {
    // Set to the current Unix time to enable age/future-time checks.
    std::uint64_t now_unix{0};
    std::uint64_t maximum_age_seconds{0};
    std::uint64_t minimum_generation{0};
    Algorithm minimum_algorithm{Algorithm::fingerprint_v2};
    std::optional<Algorithm> expected_algorithm;
    std::uint64_t allowed_clock_skew_seconds{0};
};

namespace detail {
struct KeyAccess;
struct ProtectedBytesAccess;
} // namespace detail

class EncryptionKey final {
public:
    // key_material must contain exactly 32 bytes. Invalid input throws
    // std::invalid_argument.
    EncryptionKey(std::string key_id, std::span<const std::byte> key_material);
    ~EncryptionKey() noexcept;

    EncryptionKey(const EncryptionKey&) = delete;
    EncryptionKey& operator=(const EncryptionKey&) = delete;
    EncryptionKey(EncryptionKey&& other) noexcept;
    EncryptionKey& operator=(EncryptionKey&& other) noexcept;

    [[nodiscard]] const std::string& id() const noexcept;

    // Convenience for provisioning paths. hex_key must contain 64 hex digits.
    [[nodiscard]] static EncryptionKey from_hex(std::string key_id, std::string_view hex_key);

private:
    void wipe() noexcept;

    std::string key_id_;
    std::array<std::byte, 32> material_{};

    friend struct detail::KeyAccess;
};

// Move-only plaintext storage backed by page-aligned, mlock'ed memory bounded
// by inaccessible guard pages. Construction fails closed if the page cannot be
// locked. The destructor overwrites and releases the buffer.
class ProtectedBytes final {
public:
    ProtectedBytes() noexcept = default;
    explicit ProtectedBytes(std::size_t size);
    ~ProtectedBytes() noexcept;

    ProtectedBytes(const ProtectedBytes&) = delete;
    ProtectedBytes& operator=(const ProtectedBytes&) = delete;
    ProtectedBytes(ProtectedBytes&& other) noexcept;
    ProtectedBytes& operator=(ProtectedBytes&& other) noexcept;

    [[nodiscard]] std::span<const std::byte> bytes() const noexcept;
    [[nodiscard]] std::size_t size() const noexcept;
    [[nodiscard]] bool empty() const noexcept;
    [[nodiscard]] bool is_locked() const noexcept;

private:
    void release() noexcept;

    std::byte* data_{nullptr};
    void* region_{nullptr};
    std::size_t region_size_{0};
    std::size_t size_{0};
    bool locked_{false};

    friend struct detail::ProtectedBytesAccess;
};

struct SealResult;
struct ImportResult;

[[nodiscard]] SealResult seal_template(
    std::span<const std::byte> plaintext,
    const TemplateRegistration& registration,
    const EncryptionKey& key);

[[nodiscard]] ImportResult import_template(std::span<const std::byte> encoded);

// A serialized, authenticated envelope. Import performs structural validation
// only; authentication and decryption happen in open_template.
class TemplateEnvelope final {
public:
    TemplateEnvelope() noexcept = default;

    TemplateEnvelope(const TemplateEnvelope&) = default;
    TemplateEnvelope& operator=(const TemplateEnvelope&) = default;
    TemplateEnvelope(TemplateEnvelope&&) noexcept = default;
    TemplateEnvelope& operator=(TemplateEnvelope&&) noexcept = default;
    ~TemplateEnvelope() = default;

    [[nodiscard]] std::span<const std::byte> encoded() const noexcept;
    [[nodiscard]] std::size_t size() const noexcept;
    [[nodiscard]] explicit operator bool() const noexcept;

private:
    explicit TemplateEnvelope(std::vector<std::byte> encoded) noexcept;

    std::vector<std::byte> encoded_;

    friend SealResult seal_template(
        std::span<const std::byte>,
        const TemplateRegistration&,
        const EncryptionKey&);
    friend ImportResult import_template(std::span<const std::byte>);
};

struct SealResult {
    Status status{Status::invalid_argument};
    TemplateEnvelope envelope;

    [[nodiscard]] explicit operator bool() const noexcept
    {
        return status == Status::ok;
    }
};

struct ImportResult {
    Status status{Status::malformed_template};
    TemplateEnvelope envelope;

    [[nodiscard]] explicit operator bool() const noexcept
    {
        return status == Status::ok;
    }
};

struct OpenResult {
    Status status{Status::malformed_template};
    ProtectedBytes plaintext;
    TemplateMetadata metadata;

    [[nodiscard]] explicit operator bool() const noexcept
    {
        return status == Status::ok;
    }
};

// Encrypts and authenticates a template. The envelope binds the user, device,
// enrollment session, algorithm/version, generation, issuance time, key id,
// and nonce. Metadata is authenticated before plaintext is decrypted.
[[nodiscard]] SealResult seal_template(
    std::span<const std::byte> plaintext,
    const TemplateRegistration& registration,
    const EncryptionKey& key);

// Fails closed on malformed envelopes, MAC/key failures, identity or policy
// mismatches, and unsupported/downgraded algorithms. Only after all checks
// pass is the ciphertext decrypted into ProtectedBytes.
[[nodiscard]] OpenResult open_template(
    const TemplateEnvelope& envelope,
    const TemplateExpectation& expectation,
    const ValidationPolicy& policy,
    const EncryptionKey& key);

struct LivenessResult {
    bool passed{false};
    double score{0.0};
};

struct MatchPolicy {
    double minimum_match_score{0.90};
    double minimum_liveness_score{0.80};
};

enum class MatchDecisionReason : std::uint8_t {
    accepted = 0,
    invalid_evidence,
    match_score_below_threshold,
    liveness_failed,
    liveness_score_below_threshold,
};

[[nodiscard]] std::string_view match_decision_reason_name(
    MatchDecisionReason reason) noexcept;

struct MatchDecision {
    bool accepted{false};
    MatchDecisionReason reason{MatchDecisionReason::invalid_evidence};

    [[nodiscard]] explicit operator bool() const noexcept
    {
        return accepted;
    }
};

// Accepts only when both the biometric score and the liveness result pass.
// A high similarity score can never override a failed/non-present liveness
// signal. Non-finite or out-of-range evidence fails closed.
[[nodiscard]] MatchDecision decide_match(
    double match_score,
    const LivenessResult& liveness,
    const MatchPolicy& policy) noexcept;

[[nodiscard]] MatchDecision decide_match(
    double match_score,
    bool liveness_passed,
    const MatchPolicy& policy) noexcept;

} // namespace netsec::biometric

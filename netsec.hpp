#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;

enum class HashAlgorithm : std::uint8_t {
    Sha1 = 1,
    Sha256 = 2,
    Sha384 = 3,
    Sha512 = 4,
};

enum class ValidationError {
    None,
    InvalidPolicy,
    InvalidArgument,
    EmptyLog,
    UnknownEventType,
    MissingEvent,
    ExtraEvent,
    EventOrderMismatch,
    PcrIndexMismatch,
    MissingDigest,
    UnexpectedDigest,
    DuplicateDigest,
    UnsupportedAlgorithm,
    DigestAlgorithmMismatch,
    DigestLengthMismatch,
    DigestMismatch,
    PcrSelectionMismatch,
    PcrValueMismatch,
    QuoteNonceMismatch,
    QuotePolicyMismatch,
    UntrustedAk,
    InvalidAk,
    QuoteSignatureInvalid,
};

inline constexpr std::size_t kNoEvent = static_cast<std::size_t>(-1);

std::size_t digest_size(HashAlgorithm algorithm);
std::string_view algorithm_name(HashAlgorithm algorithm);

Bytes sha1(std::span<const std::uint8_t> data);
Bytes sha256(std::span<const std::uint8_t> data);
Bytes sha384(std::span<const std::uint8_t> data);
Bytes sha512(std::span<const std::uint8_t> data);
Bytes compute_digest(HashAlgorithm algorithm, std::span<const std::uint8_t> data);

struct EventDigest {
    HashAlgorithm algorithm = HashAlgorithm::Sha256;
    Bytes value;

    bool operator==(const EventDigest&) const = default;
};

struct MeasurementEvent {
    std::string type;
    std::uint32_t pcr_index = 0;
    std::vector<EventDigest> digests;

    bool operator==(const MeasurementEvent&) const = default;
};

struct MeasurementLog {
    std::vector<MeasurementEvent> events;

    bool operator==(const MeasurementLog&) const = default;
};

struct MeasurementPolicy {
    std::string id;
    std::vector<MeasurementEvent> expected_events;
    std::vector<HashAlgorithm> pcr_banks;
};

struct PcrValue {
    HashAlgorithm algorithm = HashAlgorithm::Sha256;
    std::uint32_t pcr_index = 0;
    Bytes value;

    bool operator==(const PcrValue&) const = default;
};

struct PcrSelection {
    HashAlgorithm algorithm = HashAlgorithm::Sha256;
    std::uint32_t pcr_index = 0;

    bool operator==(const PcrSelection&) const = default;
};

struct RsaPublicKey {
    Bytes modulus;
    std::uint32_t exponent = 65537;
};

struct Quote {
    Bytes nonce;
    std::string ak_id;
    std::string policy_id;
    HashAlgorithm signature_hash = HashAlgorithm::Sha256;
    std::vector<PcrValue> pcrs;
    Bytes signature;
};

struct QuotePolicy {
    Bytes expected_nonce;
    std::string policy_id;
    std::string expected_ak_id;
    std::map<std::string, RsaPublicKey, std::less<>> trusted_aks;
    std::vector<PcrSelection> pcr_selection;
};

struct VerificationResult {
    ValidationError error = ValidationError::None;
    std::size_t event_index = kNoEvent;
    std::string detail;
    std::vector<PcrValue> computed_pcrs;

    [[nodiscard]] bool ok() const noexcept {
        return error == ValidationError::None;
    }

    explicit operator bool() const noexcept {
        return ok();
    }
};

Bytes quote_signing_payload(const Quote& quote);

std::vector<PcrValue> replay_measurement_log(
    const MeasurementLog& log,
    const MeasurementPolicy& policy);

VerificationResult verify_measurement_log(
    const MeasurementLog& log,
    const MeasurementPolicy& policy,
    std::span<const PcrValue> claimed_pcrs = {});

VerificationResult verify_measurement_chain(
    const MeasurementLog& log,
    const MeasurementPolicy& policy,
    std::span<const PcrValue> claimed_pcrs = {});

VerificationResult verify_quote(
    const Quote& quote,
    const QuotePolicy& policy,
    std::span<const PcrValue> expected_pcrs = {});

VerificationResult verify_attestation(
    const MeasurementLog& log,
    const MeasurementPolicy& measurement_policy,
    const Quote& quote,
    const QuotePolicy& quote_policy);

class MeasurementLogVerifier {
public:
    explicit MeasurementLogVerifier(MeasurementPolicy policy);

    [[nodiscard]] const MeasurementPolicy& policy() const noexcept;
    VerificationResult verify(
        const MeasurementLog& log,
        std::span<const PcrValue> claimed_pcrs = {}) const;

private:
    MeasurementPolicy policy_;
};

class AttestationVerifier {
public:
    AttestationVerifier(MeasurementPolicy measurement_policy, QuotePolicy quote_policy);

    VerificationResult verify(const MeasurementLog& log, const Quote& quote) const;

private:
    MeasurementPolicy measurement_policy_;
    QuotePolicy quote_policy_;
};

}  // namespace netsec

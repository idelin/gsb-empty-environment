#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <deque>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <set>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

enum class ValidationError {
    none = 0,
    malformed_type,
    unsupported_type,
    malformed_source,
    malformed_destination,
    address_mismatch,
    unknown_source,
    source_type_not_allowed,
    malformed_timestamp,
    stale_timestamp,
    future_timestamp,
    invalid_sequence,
    sequence_rollback,
    duplicate_message,
    invalid_charset,
    message_too_long,
    payload_too_long,
    instruction_injection,
    missing_signature,
    invalid_signature,
    confirmation_required,
    invalid_confirmation,
};

[[nodiscard]] std::string_view to_string(ValidationError error) noexcept;

struct InboundMessage {
    std::string type;
    std::string source;
    std::string destination;
    std::string timestamp;
    std::uint64_t sequence{0};
    std::string payload;

    // Critical messages carry a separate confirmation reference and a
    // confirmation signature made by the ground system.
    std::string confirmation;
    std::string confirmation_signature;

    // Hex-encoded HMAC-SHA-256 over canonical_signing_input(message).
    std::string signature;
};

struct ConfirmationProof {
    std::string reference;
    std::string signature;
};

struct TrustedMessage {
    std::string type;
    std::string source;
    std::string destination;
    std::string timestamp;
    std::uint64_t sequence{0};
    std::string payload;
    std::chrono::system_clock::time_point issued_at;
    std::chrono::system_clock::time_point received_at;
    bool critical{false};
};

struct ValidationResult {
    bool accepted{false};
    ValidationError error{ValidationError::none};
    std::string detail;
    std::optional<TrustedMessage> message;

    [[nodiscard]] explicit operator bool() const noexcept { return accepted; }
};

struct TrustedSource {
    std::string address;
    std::vector<std::uint8_t> key;

    // Empty means that the source may send every type allowed by the policy.
    std::set<std::string> allowed_types;

    // The last sequence already consumed before this process started.
    std::uint64_t initial_sequence{0};

    TrustedSource() = default;

    TrustedSource(std::string source_address, std::string_view secret)
        : address(std::move(source_address)),
          key(secret.begin(), secret.end()) {}
};

struct SourcePolicy {
    std::string local_address;

    std::set<std::string> allowed_types{
        "ACARS", "ADSC", "AIRMET", "ATC", "CLEARANCE", "CPDLC",
        "INSTRUCTION", "METAR", "PIREP", "POSITION", "ROUTE",
        "SIGMET", "TAF", "WEATHER", "WX",
    };

    std::set<std::string> critical_types{
        "ATC", "CLEARANCE", "INSTRUCTION", "ROUTE",
    };

    std::set<std::string> weather_types{
        "AIRMET", "METAR", "PIREP", "SIGMET", "TAF", "WEATHER", "WX",
    };

    std::size_t max_type_length{16};
    std::size_t max_address_length{16};
    std::size_t max_confirmation_length{64};
    std::size_t max_payload_length{1024};
    std::size_t max_message_length{2048};

    // Number of accepted content digests retained per source.
    std::size_t replay_window{256};

    std::chrono::seconds max_age{300};
    std::chrono::seconds max_future_skew{30};

    bool require_signature{true};
    bool require_critical_confirmation{true};
    bool require_critical_confirmation_signature{true};
    bool reject_instruction_in_noncritical{true};
    bool allow_multiline_payload{false};
};

// Canonical, length-prefixed input used by the message signature. It covers
// all message fields except the two signature fields. The confirmation
// signature separately binds the digest of the same core fields.
// Type/source/destination are upper-cased; payload is preserved.
[[nodiscard]] std::string canonical_signing_input(const InboundMessage& message);

// SHA-256 digest of the message fields before either signature is attached.
[[nodiscard]] std::string message_digest(const InboundMessage& message);

[[nodiscard]] std::string sha256_hex(std::string_view data);
[[nodiscard]] std::string sha256_hex(std::span<const std::uint8_t> data);

[[nodiscard]] std::string hmac_sha256_hex(
    std::span<const std::uint8_t> key,
    std::string_view data);

[[nodiscard]] std::string hmac_sha256_hex(
    std::string_view key,
    std::string_view data);

// Signatures are lower-case hexadecimal HMAC-SHA-256 values.
[[nodiscard]] std::string sign_message(
    const InboundMessage& message,
    std::span<const std::uint8_t> key);

[[nodiscard]] std::string sign_message(
    const InboundMessage& message,
    std::string_view key);

[[nodiscard]] std::string make_confirmation_signature(
    const InboundMessage& message,
    std::span<const std::uint8_t> key);

[[nodiscard]] std::string make_confirmation_signature(
    const InboundMessage& message,
    std::string_view key);

[[nodiscard]] bool verify_message_signature(
    const InboundMessage& message,
    std::string_view key);

// Compares two byte strings without an early exit on the first mismatch.
[[nodiscard]] bool constant_time_equal(
    std::string_view left,
    std::string_view right) noexcept;

class MessageSource {
public:
    using Clock = std::function<std::chrono::system_clock::time_point()>;

    explicit MessageSource(SourcePolicy policy = {}, Clock clock = {});

    MessageSource(const MessageSource&) = delete;
    MessageSource& operator=(const MessageSource&) = delete;
    MessageSource(MessageSource&& other);
    MessageSource& operator=(MessageSource&& other);

    // Registration is immutable until add_source/upsert_source is called.
    [[nodiscard]] bool add_source(TrustedSource source);
    [[nodiscard]] bool add_source(
        std::string_view address,
        std::string_view key);
    [[nodiscard]] bool register_source(TrustedSource source);
    [[nodiscard]] bool register_source(
        std::string_view address,
        std::string_view key);

    // Replaces an existing registration atomically. It does not reset the
    // source's consumed sequence or replay window.
    [[nodiscard]] bool upsert_source(TrustedSource source);
    [[nodiscard]] bool remove_source(std::string_view address);
    [[nodiscard]] bool has_source(std::string_view address) const;
    [[nodiscard]] std::size_t source_count() const;

    [[nodiscard]] ValidationResult accept(const InboundMessage& message);
    [[nodiscard]] ValidationResult accept(
        const InboundMessage& message,
        const ConfirmationProof& proof);

    [[nodiscard]] ValidationResult ingest(const InboundMessage& message) {
        return accept(message);
    }

    [[nodiscard]] ValidationResult ingest(
        const InboundMessage& message,
        const ConfirmationProof& proof) {
        return accept(message, proof);
    }

private:
    struct SourceState {
        std::vector<std::uint8_t> key;
        std::set<std::string> allowed_types;
        std::uint64_t last_sequence{0};
        std::deque<std::string> recent_digests;
    };

    [[nodiscard]] ValidationResult accept_impl(
        InboundMessage message,
        const ConfirmationProof* proof);

    SourcePolicy policy_;
    Clock clock_;
    mutable std::mutex mutex_;
    std::map<std::string, SourceState> sources_;
};

using AviationMessageSource = MessageSource;
using TrustedMessageSource = MessageSource;

}  // namespace netsec

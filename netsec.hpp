#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <mutex>
#include <string>
#include <string_view>

namespace netsec {

inline constexpr std::uint64_t audit_format_version = 1;
inline constexpr std::size_t audit_hash_hex_size = 64;
inline constexpr std::size_t audit_signature_hex_size = 64;
inline constexpr std::string_view audit_genesis_hash =
    "0000000000000000000000000000000000000000000000000000000000000000";

// Only request-controlled data belongs here. Identity and time deliberately
// have no place in this type.
struct AuditEvent {
    std::string action;
    std::string object;
    std::string outcome;
};

// Must be produced by the trusted server-side session/clock, never by parsing
// an untrusted request body.
struct ServerContext {
    std::string principal;
    std::int64_t unix_time_ms{};
};

using ServerContextProvider = std::function<ServerContext()>;

struct AuditRecord {
    std::uint64_t version{audit_format_version};
    std::uint64_t sequence{};
    std::int64_t timestamp_ms{};
    std::string principal;
    std::string action;
    std::string object;
    std::string outcome;
    std::string previous_hash;
    std::string hash;
    std::string signature;
};

// Canonical bytes covered by both the record hash and the HMAC signature.
// Every field through previous_hash is included, in this fixed order.
std::string canonical_payload(const AuditRecord& record);

// Canonical JSON without a trailing newline.
std::string encode_record(const AuditRecord& record);

// Parses one physical log line (without its terminating newline) using the
// same canonical representation emitted by encode_record(). Non-canonical
// JSON is rejected.
AuditRecord parse_record(std::string_view line);

// Appends an authenticated, hash-linked record to an in-memory chain.
//
// append() returns exactly one newline-terminated physical log line. The
// principal and timestamp are obtained from server_context for every record;
// the caller cannot supply them through AuditEvent.
class AuditLogger {
public:
    AuditLogger(std::string signing_key, ServerContextProvider server_context,
                std::uint64_t first_sequence = 1,
                std::string previous_hash =
                    std::string(audit_genesis_hash));

    std::string append(const AuditEvent& event);

    std::uint64_t next_sequence() const;
    std::string last_hash() const;

private:
    std::string signing_key_;
    ServerContextProvider server_context_;
    std::uint64_t next_sequence_;
    std::string last_hash_;
    mutable std::mutex mutex_;
};

enum class VerificationFailure {
    none,
    invalid_argument,
    empty_log,
    malformed_record,
    sequence_mismatch,
    previous_hash_mismatch,
    record_hash_mismatch,
    signature_mismatch,
};

struct VerificationResult {
    bool ok{false};
    VerificationFailure failure{VerificationFailure::none};
    std::size_t failed_record{};
    std::string message;
    std::size_t records_verified{};
    std::uint64_t next_sequence{};
    std::string last_hash;
};

// Verifies a complete log or a chain segment. first_previous_hash must be the
// hash immediately before first_sequence (the genesis hash for a fresh chain).
VerificationResult verify_log(
    std::string_view log, std::string_view signing_key,
    std::uint64_t first_sequence = 1,
    std::string_view first_previous_hash = audit_genesis_hash);

}  // namespace netsec

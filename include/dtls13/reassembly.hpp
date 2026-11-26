// SPDX-License-Identifier: MIT
// Secure out-of-order reassembly of fragmented DTLS 1.3 handshake
// messages (RFC 9147 section 4.4).

#ifndef DTLS13_REASSEMBLY_HPP
#define DTLS13_REASSEMBLY_HPP

#include <cstdint>
#include <unordered_map>
#include <vector>

#include "dtls13/types.hpp"

namespace dtls13 {

// DTLS handshake message types that matter to the reassembly layer.
// Values come from the TLS 1.3 / DTLS 1.3 IANA registry.
enum class HandshakeType : std::uint8_t {
    ClientHello = 1,
    ServerHello = 2,
    HelloRetryRequest = 6,
    EncryptedExtensions = 8,
    Certificate = 11,
    CertificateVerify = 15,
    Finished = 20,
    Ack = 26,
};

// Size in bytes of the DTLS handshake header (RFC 9147 section 4.4):
//   msg_type(1) | length(3) | message_seq(2) | fragment_offset(3) |
//   fragment_length(3)
constexpr std::size_t kHandshakeHeaderSize = 12;

// A received handshake fragment. `body` covers only the fragment payload
// (fragment_length bytes), i.e. it excludes the 12-byte handshake header.
struct HandshakeFragment {
    std::uint8_t msg_type = 0;
    std::uint32_t total_length = 0;
    std::uint16_t message_seq = 0;
    std::uint32_t fragment_offset = 0;
    std::uint32_t fragment_length = 0;
    ByteView body;
};

// Resource limits that bound reassembly memory. All limits are enforced
// before any new storage is committed, so hostile peers cannot grow the
// tables without bound.
struct ReassemblyLimits {
    // Maximum declared/assembled size of one handshake message.
    std::uint32_t max_message_bytes = 1u << 18;       // 256 KiB
    // Maximum number of concurrently open (incomplete) message slots.
    std::size_t max_open_messages = 16;
    // Maximum payload bytes cached across all open slots.
    std::size_t max_total_buffered_bytes = 1u << 20;  // 1 MiB
};

// A fully reassembled handshake message.
struct ReassembledMessage {
    std::uint8_t msg_type = 0;
    std::uint16_t message_seq = 0;
    std::uint32_t total_length = 0;
    Bytes body;
};

// Parses a serialized handshake fragment (header + body) from a record
// payload. The returned HandshakeFragment aliases `record_payload`.
Result<HandshakeFragment> parseHandshakeFragment(ByteView record_payload);

// Reassembly engine. One instance serves one DTLS transport and keeps a
// merge table keyed by message_seq.
//
// Security rules implemented here:
//   * fragment_offset + fragment_length > total_length is rejected.
//   * For the same message_seq, changes of msg_type or total_length are
//     rejected as injection attempts.
//   * Every overlapping byte is compared; identical overlap is treated as
//     harmless retransmission, any mismatch is a fatal conflict.
//   * A slot that ever failed validation is poisoned and rejects all later
//     fragments, preventing an attacker from "repairing" an attack.
//   * Message count and buffered-byte budgets prevent memory exhaustion.
class HandshakeReassembler {
public:
    explicit HandshakeReassembler(ReassemblyLimits limits = {})
        : limits_(limits) {}

    struct AddResult {
        ErrorCode code = ErrorCode::Ok;
        bool completed = false;  // Valid only when code == Ok.
        ReassembledMessage message;  // Populated when completed.
    };

    AddResult addFragment(const HandshakeFragment& fragment);

    // Convenience wrapper that first parses a serialized fragment.
    AddResult addRecordPayload(ByteView record_payload);

    // Drop all state associated with `message_seq`.
    void discard(std::uint16_t message_seq);
    void reset();

    std::size_t openMessageCount() const noexcept { return slots_.size(); }
    std::size_t bufferedBytes() const noexcept { return buffered_bytes_; }
    const ReassemblyLimits& limits() const noexcept { return limits_; }

private:
    struct Slot {
        std::uint8_t msg_type = 0;
        std::uint32_t total_length = 0;
        Bytes data;                    // total_length bytes when allocated.
        std::vector<std::uint8_t> present;  // 1 bit per byte.
        std::uint32_t received_bytes = 0;
        bool poisoned = false;

        bool isBitSet(std::uint32_t index) const noexcept;
        void setBit(std::uint32_t index) noexcept;
    };

    AddResult reject(Slot& slot, ErrorCode code);

    std::unordered_map<std::uint16_t, Slot> slots_;
    ReassemblyLimits limits_;
    std::size_t buffered_bytes_ = 0;
};

}  // namespace dtls13

#endif  // DTLS13_REASSEMBLY_HPP

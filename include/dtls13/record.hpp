// SPDX-License-Identifier: MIT
// DTLS 1.3 (RFC 9147) Unified Header decoding, 48-bit implicit sequence
// number reconstruction and per-epoch sliding replay windows.

#ifndef DTLS13_RECORD_HPP
#define DTLS13_RECORD_HPP

#include <array>
#include <cstdint>
#include <unordered_map>
#include <vector>

#include "dtls13/types.hpp"

namespace dtls13 {

// RFC 9147 section 4.5: sequence numbers use the low 48 bits of the
// (D)TLS 64-bit record sequence number.
constexpr std::uint64_t kSeqSpaceMask = 0x0000FFFFFFFFFFFFULL;
constexpr std::uint64_t kSeqSpaceSize = 1ULL << 48;
constexpr unsigned kReplayWindowBits = 64;

// First octet of the DTLS 1.3 Unified Header.
//   0 0 1 B E S L R
// The high three bits are the fixed 0b001 pattern. B/E/S/L are described
// in RFC 9147 section 4.5; R is reserved and MUST be zero on the wire.
constexpr std::uint8_t kUhFixedPatternMask = 0xE0;
constexpr std::uint8_t kUhFixedPattern = 0x20;
constexpr std::uint8_t kUhCidFlag = 0x10;        // B
constexpr std::uint8_t kUhEpochFlag = 0x08;      // E
constexpr std::uint8_t kUhSeq16Flag = 0x04;      // S
constexpr std::uint8_t kUhLengthFlag = 0x02;     // L
constexpr std::uint8_t kUhReservedFlag = 0x01;   // R

// Decoded Unified Header. All multi-byte integers are stored in host order.
struct UnifiedHeader {
    bool cid_present = false;
    bool epoch_present = false;
    bool seq_16_bits = false;
    bool length_present = false;

    // Low two octets of the connection epoch (only valid when E is set).
    std::uint16_t epoch = 0;
    // Low 8 or 16 bits of the record sequence number.
    std::uint64_t truncated_seq = 0;
    unsigned seq_bits = 8;
    // When L is clear the record consumes the remainder of the datagram.
    std::uint16_t length = 0;
    bool has_length = false;
    // Connection ID (empty when B is clear).
    ByteView cid;

    // Number of serialized header octets (excludes the leading content
    // type octet and the encrypted record body).
    std::size_t header_size = 0;
};

// A single DTLS record as parsed off a UDP datagram. `body` points into
// the caller-owned datagram buffer; its lifetime must exceed this struct.
struct ParsedRecord {
    ContentType outer_type = ContentType::Invalid;
    UnifiedHeader header;
    ByteView body;
    // Raw serialized record header bytes: content type octet + unified
    // header. This is the AAD input for AEAD protection (RFC 9147 4.5.2).
    ByteView raw_header;
};

// Reconstruct the full 48-bit record sequence number from a truncated
// 8- or 16-bit value, given the high-water mark of already accepted
// sequence numbers in this epoch (`window_end`).
//
// Implements the algorithm of RFC 9147 section 4.5.1, expressed with
// modular arithmetic so that 48-bit wrap-around never underflows. The
// candidate congruent to the truncated value that lies closest to
// window_end is selected; an exact tie resolves toward the higher value,
// matching the RFC pseudocode (candidate <= window_end - 2^(bits-1)).
std::uint64_t reconstructSequence(std::uint64_t truncated,
                                  unsigned bits,
                                  std::uint64_t window_end) noexcept;

// Per-epoch 64-entry sliding replay window. Sequence numbers live in the
// 48-bit space and arithmetic wraps modulo 2^48.
//
// Lifecycle contract:
//   1. inspect() decides whether a record may be processed. It never
//      mutates window state, so an AEAD failure cannot poison the window.
//   2. The caller performs AEAD authentication.
//   3. commit() is called only after authentication succeeds; this is the
//      sole point allowed to advance the window / set the seen bitmap.
class ReplayWindow {
public:
    enum class Verdict {
        Fresh,
        Replayed,
        TooOld,
    };

    ReplayWindow() = default;

    Verdict inspect(std::uint64_t seq48) const noexcept;
    void commit(std::uint64_t seq48) noexcept;

    std::uint64_t windowEnd() const noexcept { return window_end_; }
    std::uint64_t bitmap() const noexcept { return bitmap_; }
    bool hasSeenAny() const noexcept { return initialized_; }

private:
    std::uint64_t window_end_ = 0;
    std::uint64_t bitmap_ = 0;
    bool initialized_ = false;
};

// Parse one record starting at `datagram[offset]`. On success the result
// holds offsets into `datagram` and `offset` is advanced past the record.
// `current_epoch` supplies the epoch when the E bit is elided, per the
// RFC 9147 rule that an absent epoch field means "the current epoch".
ErrorCode parseOneRecord(ByteView datagram,
                         std::size_t& offset,
                         std::uint16_t current_epoch,
                         ParsedRecord& out);

// Parse every record contained in a datagram (multiple records may share
// one UDP payload). The returned views alias `datagram`.
Result<std::vector<ParsedRecord>> parseDatagram(ByteView datagram,
                                                std::uint16_t initial_epoch);

// Stateful record scanner: tracks the most recent epoch and supports
// datagrams that rely on epoch elision.
class RecordScanner {
public:
    explicit RecordScanner(std::uint16_t initial_epoch = 0) noexcept
        : current_epoch_(initial_epoch) {}

    ErrorCode next(ByteView datagram,
                   std::size_t& offset,
                   ParsedRecord& out);

    std::uint16_t currentEpoch() const noexcept { return current_epoch_; }
    void reset(std::uint16_t epoch) noexcept { current_epoch_ = epoch; }

private:
    std::uint16_t current_epoch_;
};

// Map of independent replay windows keyed by epoch. Sequence spaces must
// never be shared across epochs, so every epoch gets its own window and
// its own high-water mark.
class EpochReplayTable {
public:
    explicit EpochReplayTable(std::size_t max_epochs = 8)
        : max_epochs_(max_epochs) {}

    // Returns the window for `epoch`, creating it on first use. Fails with
    // EpochLimitReached once the configured budget is exhausted.
    ErrorCode getOrCreate(std::uint16_t epoch, ReplayWindow*& out);

    ReplayWindow* find(std::uint16_t epoch);
    const ReplayWindow* find(std::uint16_t epoch) const;
    std::size_t size() const noexcept { return windows_.size(); }
    void clear() noexcept { windows_.clear(); }

private:
    std::unordered_map<std::uint16_t, ReplayWindow> windows_;
    std::size_t max_epochs_;
};

}  // namespace dtls13

#endif  // DTLS13_RECORD_HPP

#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <span>
#include <string_view>
#include <vector>

namespace netsec {

// A 256-bit SRTP master key. Per-epoch authentication keys are derived from it
// and are bound to the session id, SSRC and key epoch.
inline constexpr std::size_t kMasterKeySize = 32;
inline constexpr std::size_t kAuthTagSize = 16;
inline constexpr std::size_t kMaxReplayWindow = 4096;
inline constexpr std::size_t kDefaultReplayWindow = 128;
inline constexpr std::size_t kMaxPayloadSize = 16U * 1024U * 1024U;
inline constexpr std::uint32_t kMaxForwardGap = 32767U;
inline constexpr std::uint16_t kMaxRetainedEpochs = 16;
inline constexpr std::uint16_t kMaxEpochAdvance = 16;
inline constexpr std::uint64_t kMaxPacketsPerEpoch = 16'777'216ULL;

// Wire layout (all integers are big endian):
//   magic[4] version[1] flags[1] key_epoch[2] session_id[4] ssrc[4]
//   sequence[2] timestamp[4] payload_length[4] payload[...] auth_tag[16]
inline constexpr std::size_t kWireHeaderSize = 26;
inline constexpr std::size_t kWireOverhead = kWireHeaderSize + kAuthTagSize;

using Key = std::array<std::uint8_t, kMasterKeySize>;
using Bytes = std::vector<std::uint8_t>;
using ByteSpan = std::span<const std::uint8_t>;

struct MediaPacket {
    std::uint32_t session_id{};
    std::uint32_t ssrc{};
    std::uint16_t sequence{};
    std::uint32_t timestamp{};
    std::uint16_t key_epoch{};
    Bytes payload;
};

enum class VerifyStatus : std::uint8_t {
    accepted,
    accepted_reordered,
    rejected_malformed,
    rejected_session,
    rejected_ssrc,
    rejected_unknown_epoch,
    rejected_expired_epoch,
    rejected_authentication,
    rejected_duplicate,
    rejected_rollback,
    rejected_forward_gap,
    rejected_key_exhausted,
};

[[nodiscard]] const char* to_string(VerifyStatus status) noexcept;

struct VerifyResult {
    VerifyStatus status = VerifyStatus::rejected_malformed;

    [[nodiscard]] bool accepted() const noexcept {
        return status == VerifyStatus::accepted ||
               status == VerifyStatus::accepted_reordered;
    }

    [[nodiscard]] bool rejected() const noexcept { return !accepted(); }
    explicit operator bool() const noexcept { return accepted(); }
    [[nodiscard]] const char* message() const noexcept { return to_string(status); }
};

enum class ReorderPolicy : std::uint8_t {
    // Reordered packets inside the replay window are delivered.
    accept,
    // Any packet below the highest accepted sequence is dropped.
    reject,
};

enum class GapPolicy : std::uint8_t {
    // An authenticated forward gap larger than max_forward_gap is dropped.
    reject,
    // An authenticated forward gap is accepted and the sequence state resyncs.
    resync,
};

struct ReplayPolicy {
    // Number of sequence values retained. Must be in [1, kMaxReplayWindow].
    std::size_t window = kDefaultReplayWindow;
    ReorderPolicy reorder = ReorderPolicy::accept;
    GapPolicy gap = GapPolicy::reject;
    // Maximum accepted forward step in 16-bit sequence space. Must be in
    // [1, kMaxForwardGap]. Gaps larger than this are handled by GapPolicy.
    std::uint32_t max_forward_gap = 1024;
};

struct SenderConfig {
    std::uint32_t session_id{};
    std::uint32_t ssrc{};
    Key master_key{};
    std::uint16_t initial_epoch = 0;
    // Bounds the amount of media protected by one derived epoch key.
    std::uint64_t max_packets_per_epoch = 1'000'000;
};

struct ReceiverConfig {
    std::uint32_t session_id{};
    std::uint32_t ssrc{};
    Key master_key{};
    std::uint16_t initial_epoch = 0;
    // Number of older epochs that remain acceptable after a local rotation.
    std::uint16_t retained_epochs = 2;
    // Largest authenticated forward jump in key epoch, in addition to the
    // epochs explicitly installed with rotate_to().
    std::uint16_t max_epoch_advance = 1;
    // Bounds how many packets one derived epoch key may protect. Packets that
    // would exceed this bound are rejected until a key rotation.
    std::uint64_t max_packets_per_epoch = 1'000'000;
    ReplayPolicy replay{};
};

struct SenderStats {
    std::uint64_t sealed = 0;
    std::uint64_t rotations = 0;
};

struct ReceiverStats {
    std::uint64_t received = 0;
    std::uint64_t accepted = 0;
    std::uint64_t reordered = 0;
    std::uint64_t duplicates = 0;
    std::uint64_t authentication_failures = 0;
    std::uint64_t policy_rejections = 0;
};

// Protects media packets with an authenticated, session-bound envelope.
// Sender is not thread safe; use one instance per sending session.
class Sender {
public:
    explicit Sender(SenderConfig config);
    ~Sender();
    Sender(const Sender&) = delete;
    Sender& operator=(const Sender&) = delete;
    Sender(Sender&&) noexcept;
    Sender& operator=(Sender&&) noexcept;

    [[nodiscard]] std::uint32_t session_id() const noexcept;
    [[nodiscard]] std::uint32_t ssrc() const noexcept;
    [[nodiscard]] std::uint16_t key_epoch() const noexcept;
    [[nodiscard]] std::uint64_t packets_in_epoch() const noexcept;
    [[nodiscard]] const SenderStats& stats() const noexcept;

    // Returns false (without writing out) when the current epoch key has
    // reached max_packets_per_epoch. Never allocates on the failure path.
    [[nodiscard]] bool try_seal(const MediaPacket& packet, Bytes& out) noexcept;
    [[nodiscard]] bool try_seal(std::uint16_t sequence, std::uint32_t timestamp,
                                ByteSpan payload, Bytes& out) noexcept;

    // Convenience wrappers. Throw std::invalid_argument for invalid input and
    // std::runtime_error when the epoch key must be rotated first.
    [[nodiscard]] Bytes seal(const MediaPacket& packet);
    [[nodiscard]] Bytes seal(std::uint16_t sequence, std::uint32_t timestamp,
                             ByteSpan payload);

    // Advances to the next key epoch. Returns false at epoch 65535.
    bool rotate_key() noexcept;

    // Installs a new master key at an epoch. Intended for externally signalled
    // SRTP rekeying. Only increasing epochs are accepted.
    bool set_master_key(Key key, std::uint16_t epoch) noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

// Verifies and orders authenticated media packets. A packet is never allowed to
// affect replay or epoch state before its authentication tag has been checked.
// Receiver is not thread safe; use one instance per receiving session.
class Receiver {
public:
    explicit Receiver(ReceiverConfig config);
    ~Receiver();
    Receiver(const Receiver&) = delete;
    Receiver& operator=(const Receiver&) = delete;
    Receiver(Receiver&&) noexcept;
    Receiver& operator=(Receiver&&) noexcept;

    [[nodiscard]] std::uint32_t session_id() const noexcept;
    [[nodiscard]] std::uint32_t ssrc() const noexcept;
    [[nodiscard]] std::uint16_t active_epoch() const noexcept;
    [[nodiscard]] const ReceiverStats& stats() const noexcept;

    // On rejection, out is cleared. Replay and epoch state are only advanced by
    // an accepted packet.
    [[nodiscard]] VerifyResult receive(ByteSpan wire, MediaPacket& out);
    [[nodiscard]] VerifyResult receive(ByteSpan wire);

    // Moves the accepted epoch range forward without requiring a packet from
    // the new epoch first. Returns false for a non-increasing epoch.
    bool rotate_to(std::uint16_t epoch) noexcept;

    // Installs an externally signalled SRTP master key for a new epoch. Older
    // epochs continue to verify with their prior master key until they fall
    // outside retained_epochs. Returns false for a non-increasing epoch.
    bool set_master_key(Key key, std::uint16_t epoch);

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using StreamSender = Sender;
using StreamReceiver = Receiver;
using StreamAuthSender = Sender;
using StreamAuthReceiver = Receiver;

}  // namespace netsec

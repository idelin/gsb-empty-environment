#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <initializer_list>
#include <map>
#include <optional>
#include <set>
#include <stdexcept>
#include <string_view>
#include <utility>
#include <variant>
#include <vector>

namespace netsec {

inline constexpr std::size_t kMaxConnectionIdLength = 20;
inline constexpr std::size_t kStatelessResetTokenLength = 16;

enum class ConnectionIdError {
  none = 0,
  kNone = 0,
  None = 0,
  frame_encoding_error = 1,
  kFrameEncodingError = 1,
  FrameEncodingError = 1,
  FRAME_ENCODING_ERROR = 1,
  protocol_violation = 2,
  kProtocolViolation = 2,
  ProtocolViolation = 2,
  PROTOCOL_VIOLATION = 2,
  connection_id_limit_error = 3,
  kConnectionIdLimitError = 3,
  ConnectionIdLimitError = 3,
  CONNECTION_ID_LIMIT_ERROR = 3,
};

using FrameError = ConnectionIdError;
using ErrorCode = ConnectionIdError;

inline constexpr ConnectionIdError FRAME_ENCODING_ERROR =
    ConnectionIdError::frame_encoding_error;
inline constexpr ConnectionIdError PROTOCOL_VIOLATION =
    ConnectionIdError::protocol_violation;
inline constexpr ConnectionIdError CONNECTION_ID_LIMIT_ERROR =
    ConnectionIdError::connection_id_limit_error;

const char* to_string(ConnectionIdError error) noexcept;

// A connection ID keeps its wire bytes verbatim. Length validation belongs to
// frame processing so malformed frames can be reported without losing data.
class ConnectionId {
 public:
  ConnectionId() noexcept = default;
  ConnectionId(std::initializer_list<std::uint8_t> bytes) : bytes_(bytes) {}
  explicit ConnectionId(std::vector<std::uint8_t> bytes)
      : bytes_(std::move(bytes)) {}
  ConnectionId(const std::uint8_t* bytes, std::size_t length) {
    if (bytes != nullptr && length != 0) {
      bytes_.assign(bytes, bytes + length);
    }
  }
  explicit ConnectionId(std::string_view bytes) {
    bytes_.assign(bytes.begin(), bytes.end());
  }
  template <std::size_t N>
  explicit ConnectionId(const std::array<std::uint8_t, N>& bytes)
      : bytes_(bytes.begin(), bytes.end()) {}

  std::size_t size() const noexcept { return bytes_.size(); }
  std::size_t length() const noexcept { return bytes_.size(); }
  bool empty() const noexcept { return bytes_.empty(); }
  const std::uint8_t* data() const noexcept {
    return bytes_.empty() ? nullptr : bytes_.data();
  }
  const std::vector<std::uint8_t>& bytes() const noexcept { return bytes_; }
  std::uint8_t operator[](std::size_t index) const noexcept {
    return bytes_[index];
  }

  bool operator==(const ConnectionId& other) const noexcept {
    return bytes_ == other.bytes_;
  }
  bool operator!=(const ConnectionId& other) const noexcept {
    return !(*this == other);
  }

 private:
  std::vector<std::uint8_t> bytes_;
};

class StatelessResetToken {
 public:
  StatelessResetToken() noexcept { bytes_.fill(0); }
  StatelessResetToken(std::initializer_list<std::uint8_t> bytes) {
    assign(bytes.begin(), bytes.size());
  }
  explicit StatelessResetToken(const std::vector<std::uint8_t>& bytes) {
    assign(bytes.data(), bytes.size());
  }
  StatelessResetToken(const std::uint8_t* bytes, std::size_t length) {
    assign(bytes, length);
  }
  template <std::size_t N>
  explicit StatelessResetToken(const std::array<std::uint8_t, N>& bytes) {
    assign(bytes.data(), bytes.size());
  }

  const std::uint8_t* data() const noexcept { return bytes_.data(); }
  const std::array<std::uint8_t, kStatelessResetTokenLength>& bytes()
      const noexcept {
    return bytes_;
  }
  std::uint8_t operator[](std::size_t index) const noexcept {
    return bytes_[index];
  }

  bool operator==(const StatelessResetToken& other) const noexcept {
    return bytes_ == other.bytes_;
  }
  bool operator!=(const StatelessResetToken& other) const noexcept {
    return !(*this == other);
  }

 private:
  void assign(const std::uint8_t* bytes, std::size_t length) {
    if (bytes == nullptr || length != kStatelessResetTokenLength) {
      throw std::invalid_argument(
          "a stateless reset token must contain exactly 16 bytes");
    }
    std::copy(bytes, bytes + length, bytes_.begin());
  }

  std::array<std::uint8_t, kStatelessResetTokenLength> bytes_{};
};

struct NewConnectionIdFrame {
  std::uint64_t sequence_number = 0;
  std::uint64_t retire_prior_to = 0;
  ConnectionId connection_id;
  StatelessResetToken stateless_reset_token;

  NewConnectionIdFrame() = default;
  NewConnectionIdFrame(std::uint64_t sequence, std::uint64_t retire_before,
                       ConnectionId id, StatelessResetToken token = {})
      : sequence_number(sequence),
        retire_prior_to(retire_before),
        connection_id(std::move(id)),
        stateless_reset_token(token) {}
};

struct RetireConnectionIdFrame {
  std::uint64_t sequence_number = 0;

  RetireConnectionIdFrame() = default;
  explicit RetireConnectionIdFrame(std::uint64_t sequence)
      : sequence_number(sequence) {}
};

using NewConnectionId = NewConnectionIdFrame;
using RetireConnectionId = RetireConnectionIdFrame;
using ConnectionIdFrame =
    std::variant<NewConnectionIdFrame, RetireConnectionIdFrame>;

struct IssuedConnectionId {
  std::uint64_t sequence_number = 0;
  ConnectionId connection_id;
  StatelessResetToken stateless_reset_token;
};

// The stack supplies this callback so production code can use its secure CID
// generator. The callback only supplies bytes; the manager allocates sequence
// numbers and enforces retirement/replacement accounting.
using ConnectionIdGenerator = std::function<
    std::pair<ConnectionId, StatelessResetToken>(std::uint64_t)>;

struct ConnectionIdManagerOptions {
  std::uint64_t active_connection_id_limit = 2;
  bool peer_connection_uses_zero_length_cid = false;
  bool local_connection_uses_zero_length_cid = false;
  ConnectionIdGenerator connection_id_generator;
};

struct ProcessResult {
  ConnectionIdError error = ConnectionIdError::none;
  bool duplicate = false;
  std::size_t retired_peer_connection_ids = 0;
  std::size_t retired_local_connection_ids = 0;
  std::optional<IssuedConnectionId> replacement;

  bool ok() const noexcept { return error == ConnectionIdError::none; }
  explicit operator bool() const noexcept { return ok(); }
};

using ConnectionIdFrameResult = ProcessResult;
using PacketResult = ProcessResult;

class ConnectionIdManager {
 private:
  struct FrameOutcome {
    ConnectionIdError error = ConnectionIdError::none;
    bool duplicate = false;
    std::size_t retired_peer_connection_ids = 0;
    std::size_t retired_local_connection_ids = 0;
    bool replacement_eligible = false;
  };

 public:
  class PacketProcessor {
   public:
    PacketProcessor(PacketProcessor&&) noexcept = default;
    PacketProcessor& operator=(PacketProcessor&&) noexcept = default;
    PacketProcessor(const PacketProcessor&) = delete;
    PacketProcessor& operator=(const PacketProcessor&) = delete;

    // Adds one frame to the packet. The returned error is sticky: after the
    // first error, subsequent frames are not applied.
    ConnectionIdError process(const ConnectionIdFrame& frame);

    // Completes the packet and performs the single allowed replacement.
    // Calling finish more than once returns the same result.
    ProcessResult finish();

   private:
    friend class ConnectionIdManager;
    explicit PacketProcessor(ConnectionIdManager& manager)
        : manager_(&manager) {}

    ConnectionIdManager* manager_;
    FrameOutcome outcome_;
    std::optional<ProcessResult> final_result_;
  };

  ConnectionIdManager();
  explicit ConnectionIdManager(std::uint64_t active_connection_id_limit);
  ConnectionIdManager(std::uint64_t active_connection_id_limit,
                      bool peer_connection_uses_zero_length_cid);
  ConnectionIdManager(std::uint64_t active_connection_id_limit,
                      const ConnectionId& initial_peer_connection_id);
  explicit ConnectionIdManager(ConnectionIdManagerOptions options);

  // Process all frames from one received packet. At most one replacement CID
  // is emitted, even if the packet retires many local CIDs.
  ProcessResult process_packet(const std::vector<ConnectionIdFrame>& frames);
  ProcessResult process_packet(std::initializer_list<ConnectionIdFrame> frames);
  ProcessResult process_frame(const ConnectionIdFrame& frame);
  PacketProcessor begin_packet();

  // Convenience entry points treat each call as one received packet.
  ProcessResult process_new_connection_id(const NewConnectionIdFrame& frame);
  ProcessResult process_retire_connection_id(
      const RetireConnectionIdFrame& frame);
  ProcessResult process_new_connection_id(
      std::uint64_t sequence_number, std::uint64_t retire_prior_to,
      const ConnectionId& connection_id,
      const StatelessResetToken& stateless_reset_token = {});
  ProcessResult process_retire_connection_id(std::uint64_t sequence_number);

  ProcessResult handle_new_connection_id(const NewConnectionIdFrame& frame) {
    return process_new_connection_id(frame);
  }
  ProcessResult handle_new_connection_id(
      std::uint64_t sequence_number, std::uint64_t retire_prior_to,
      const ConnectionId& connection_id,
      const StatelessResetToken& stateless_reset_token = {}) {
    return process_new_connection_id(sequence_number, retire_prior_to,
                                     connection_id, stateless_reset_token);
  }
  ProcessResult handle_retire_connection_id(
      const RetireConnectionIdFrame& frame) {
    return process_retire_connection_id(frame);
  }
  ProcessResult handle_retire_connection_id(std::uint64_t sequence_number) {
    return process_retire_connection_id(sequence_number);
  }
  ProcessResult on_new_connection_id(const NewConnectionIdFrame& frame) {
    return process_new_connection_id(frame);
  }
  ProcessResult on_retire_connection_id(
      const RetireConnectionIdFrame& frame) {
    return process_retire_connection_id(frame);
  }

  // The initial local CID has sequence number zero. This allocates the next
  // sequence and returns the generated bytes for a NEW_CONNECTION_ID frame.
  IssuedConnectionId issue_connection_id();

  std::uint64_t active_connection_id_limit() const noexcept {
    return active_connection_id_limit_;
  }
  std::size_t active_peer_connection_id_count() const noexcept {
    return peer_connection_ids_.size();
  }
  std::size_t peer_connection_id_count() const noexcept {
    return active_peer_connection_id_count();
  }
  std::uint64_t peer_retire_prior_to() const noexcept {
    return peer_retire_prior_to_;
  }
  std::uint64_t next_local_connection_id_sequence() const noexcept {
    return next_local_sequence_;
  }
  std::size_t retired_local_connection_id_count() const noexcept {
    return retired_local_connection_ids_.size();
  }
  std::size_t replacement_connection_id_count() const noexcept {
    return replacement_connection_id_count_;
  }

 private:
  struct PeerConnectionId {
    ConnectionId connection_id;
    StatelessResetToken stateless_reset_token;
  };

  FrameOutcome apply_new_connection_id(const NewConnectionIdFrame& frame);
  FrameOutcome apply_retire_connection_id(
      const RetireConnectionIdFrame& frame);
  FrameOutcome apply_frame(const ConnectionIdFrame& frame);
  ProcessResult finish_packet(const FrameOutcome& outcome);
  void schedule_replacement(ProcessResult& result);
  std::pair<ConnectionId, StatelessResetToken> generate_connection_id(
      std::uint64_t sequence_number);

  std::uint64_t active_connection_id_limit_ = 2;
  bool peer_uses_zero_length_cid_ = false;
  bool local_uses_zero_length_cid_ = false;
  ConnectionIdGenerator connection_id_generator_;

  std::map<std::uint64_t, PeerConnectionId> peer_connection_ids_;
  std::uint64_t peer_retire_prior_to_ = 0;

  std::uint64_t next_local_sequence_ = 1;
  std::set<std::uint64_t> retired_local_connection_ids_;
  std::size_t replacement_connection_id_count_ = 0;
};

}  // namespace netsec

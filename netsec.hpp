#pragma once

#include <cstddef>
#include <cstdint>
#include <initializer_list>
#include <map>
#include <optional>
#include <vector>

namespace netsec {

inline constexpr std::uint32_t TP_CM_PGN = 0x00EC00u;
inline constexpr std::uint32_t TP_DT_PGN = 0x00EB00u;
inline constexpr std::uint32_t ADDRESS_CLAIM_PGN = 0x00EE00u;
inline constexpr std::uint8_t TP_CM_BAM = 32u;
inline constexpr std::uint8_t J1939_NULL_ADDRESS = 254u;
inline constexpr std::uint8_t J1939_GLOBAL_ADDRESS = 255u;
inline constexpr std::uint64_t TP_TIMEOUT_MS = 750u;
inline constexpr std::uint16_t TP_MIN_BYTES = 9u;
inline constexpr std::uint16_t TP_MAX_BYTES = 1785u;

std::uint32_t pgn_from_can_id(std::uint32_t can_id) noexcept;
std::uint8_t source_address_from_can_id(std::uint32_t can_id) noexcept;
std::uint8_t destination_address_from_can_id(std::uint32_t can_id) noexcept;
std::uint32_t
make_can_id(std::uint8_t source_address, std::uint32_t pgn,
            std::uint8_t priority = 6u,
            std::uint8_t destination = J1939_GLOBAL_ADDRESS) noexcept;

struct CanFrame {
  std::uint32_t id = 0u;
  std::uint8_t data[8] = {};
  std::uint8_t dlc = 0u;
  std::uint64_t timestamp_ms = 0u;

  CanFrame() = default;
  CanFrame(std::uint32_t identifier, const std::uint8_t *payload,
           std::uint8_t payload_length, std::uint64_t timestamp = 0u) noexcept;
  CanFrame(std::uint32_t identifier,
           std::initializer_list<std::uint8_t> payload,
           std::uint64_t timestamp = 0u) noexcept;
  CanFrame(std::uint32_t identifier,
           std::initializer_list<std::uint8_t> payload,
           std::uint8_t payload_length, std::uint64_t timestamp = 0u) noexcept;
  CanFrame(std::uint32_t identifier, std::uint8_t payload_length,
           std::initializer_list<std::uint8_t> payload,
           std::uint64_t timestamp = 0u) noexcept;
};

enum class TpStatus : std::uint8_t {
  Ignored = 0,
  Rejected = 1,
  Started = 2,
  InProgress = 3,
  Complete = 4,
  Completed = 4,
  Aborted = 5,
  SessionStarted = 2,
  SessionInProgress = 3,
  SessionAborted = 5,
  AddressClaimAccepted = 6,
  AddressClaimRejected = 7
};

enum class TpAbortReason : std::uint8_t {
  None = 0,
  InvalidControl,
  InvalidFrameLength,
  InvalidTotalBytes,
  PacketCountMismatch,
  InvalidSequence,
  OutOfSequence,
  DuplicateSequence,
  Timeout,
  ReplacedByNewBam,
  AddressClaimConflict,
  MalformedFrame
};

struct TpMessage {
  std::uint8_t source_address = 0u;
  std::uint32_t pgn = 0u;
  std::uint32_t total_bytes = 0u;
  std::uint8_t total_packets = 0u;
  std::vector<std::uint8_t> payload;
};

struct TpResult {
  TpStatus status = TpStatus::Ignored;
  TpAbortReason reason = TpAbortReason::None;
  bool completed = false;
  bool complete = false;
  bool session_active = false;
  bool replaced = false;
  std::uint8_t source_address = 0u;
  std::uint32_t pgn = 0u;
  std::uint32_t total_bytes = 0u;
  std::uint8_t total_packets = 0u;
  std::uint64_t name = 0u;
  std::vector<std::uint8_t> payload;
  std::vector<std::uint8_t> data;
  std::optional<TpMessage> message;

  bool active() const noexcept {
    return status == TpStatus::Started || status == TpStatus::InProgress;
  }
  bool accepted() const noexcept {
    return status != TpStatus::Rejected &&
           status != TpStatus::AddressClaimRejected;
  }
  explicit operator bool() const noexcept { return completed || complete; }
};

class TpReassembler {
public:
  TpResult on_frame(const CanFrame &frame);

  TpResult on_frame(std::uint32_t can_id, const std::uint8_t *data,
                    std::uint8_t dlc, std::uint64_t timestamp_ms);

  TpResult on_frame(std::uint32_t can_id,
                    std::initializer_list<std::uint8_t> data,
                    std::uint64_t timestamp_ms);

  TpResult on_address_claim(std::uint8_t source_address, std::uint64_t name,
                            std::uint64_t timestamp_ms = 0u);

  TpResult on_address_claim(const CanFrame &frame);

  bool has_session(std::uint8_t source_address) const noexcept;
  std::size_t session_count() const noexcept;
  std::optional<std::uint64_t>
  name_for_address(std::uint8_t source_address) const;

  void clear_session(std::uint8_t source_address) noexcept;
  void clear_all_sessions() noexcept;
  void reset() noexcept;

  std::size_t expire_sessions(std::uint64_t now_ms) noexcept;

  TpResult onFrame(const CanFrame &frame) { return on_frame(frame); }
  TpResult receive(const CanFrame &frame) { return on_frame(frame); }
  TpResult receive_frame(const CanFrame &frame) { return on_frame(frame); }
  TpResult process_frame(const CanFrame &frame) { return on_frame(frame); }
  TpResult feed(const CanFrame &frame) { return on_frame(frame); }
  TpResult handle_frame(const CanFrame &frame) { return on_frame(frame); }
  TpResult on_can_frame(const CanFrame &frame) { return on_frame(frame); }
  TpResult on_can_frame(std::uint32_t can_id, const std::uint8_t *data,
                        std::uint8_t dlc, std::uint64_t timestamp_ms) {
    return on_frame(can_id, data, dlc, timestamp_ms);
  }
  TpResult on_can_message(std::uint32_t can_id, const std::uint8_t *data,
                          std::uint8_t dlc, std::uint64_t timestamp_ms) {
    return on_frame(can_id, data, dlc, timestamp_ms);
  }

  TpResult claim_address(std::uint8_t source_address, std::uint64_t name) {
    return on_address_claim(source_address, name);
  }
  TpResult handle_address_claim(std::uint8_t source_address,
                                std::uint64_t name) {
    return on_address_claim(source_address, name);
  }

private:
  struct Session {
    std::uint32_t pgn = 0u;
    std::uint32_t total_bytes = 0u;
    std::uint8_t total_packets = 0u;
    std::uint8_t expected_sequence = 1u;
    std::uint64_t last_timestamp_ms = 0u;
    std::vector<std::uint8_t> payload;
  };

  TpResult handle_tp_cm(const CanFrame &frame, std::uint8_t source_address);
  TpResult handle_tp_dt(const CanFrame &frame, std::uint8_t source_address);

  std::map<std::uint8_t, Session> sessions_;
  std::map<std::uint8_t, std::uint64_t> address_names_;
};

using J1939TpReassembler = TpReassembler;
using TransportProtocol = TpReassembler;
using TpReceiver = TpReassembler;
using Reassembler = TpReassembler;
using J1939TP = TpReassembler;
using J1939TransportProtocol = TpReassembler;

inline TpResult receive_frame(TpReassembler &reassembler,
                              const CanFrame &frame) {
  return reassembler.on_frame(frame);
}

inline TpResult process_frame(TpReassembler &reassembler,
                              const CanFrame &frame) {
  return reassembler.on_frame(frame);
}

} // namespace netsec

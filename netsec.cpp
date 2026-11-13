#include "netsec.hpp"

#include <algorithm>
#include <utility>

namespace netsec {
namespace {

CanFrame make_frame(std::uint32_t can_id, const std::uint8_t *data,
                    std::uint8_t dlc, std::uint64_t timestamp_ms) {
  CanFrame frame;
  frame.id = can_id;
  frame.dlc = std::min<std::uint8_t>(dlc, 8u);
  if (data != nullptr) {
    for (std::uint8_t i = 0u; i < frame.dlc; ++i) {
      frame.data[i] = data[i];
    }
  }
  frame.timestamp_ms = timestamp_ms;
  return frame;
}

TpResult ignored_result(std::uint8_t source_address, std::uint32_t pgn) {
  TpResult result;
  result.status = TpStatus::Ignored;
  result.source_address = source_address;
  result.pgn = pgn;
  return result;
}

TpResult rejected_result(std::uint8_t source_address, std::uint32_t pgn,
                         TpAbortReason reason) {
  TpResult result;
  result.status = TpStatus::Rejected;
  result.reason = reason;
  result.source_address = source_address;
  result.pgn = pgn;
  return result;
}

void copy_payload(TpResult &result, std::vector<std::uint8_t> payload) {
  result.payload = std::move(payload);
  result.data = result.payload;
}

} // namespace

std::uint32_t pgn_from_can_id(std::uint32_t can_id) noexcept {
  std::uint32_t pgn = (can_id >> 8u) & 0x3FFFFu;
  if (((pgn >> 8u) & 0xFFu) < 240u) {
    pgn &= 0x3FF00u;
  }
  return pgn;
}

std::uint8_t source_address_from_can_id(std::uint32_t can_id) noexcept {
  return static_cast<std::uint8_t>(can_id & 0xFFu);
}

std::uint8_t destination_address_from_can_id(std::uint32_t can_id) noexcept {
  const std::uint8_t pf = static_cast<std::uint8_t>((can_id >> 16u) & 0xFFu);
  if (pf < 240u) {
    return static_cast<std::uint8_t>((can_id >> 8u) & 0xFFu);
  }
  return J1939_GLOBAL_ADDRESS;
}

std::uint32_t make_can_id(std::uint8_t source_address, std::uint32_t pgn,
                          std::uint8_t priority,
                          std::uint8_t destination) noexcept {
  std::uint32_t can_id = (static_cast<std::uint32_t>(priority) & 0x7u) << 26u;
  const std::uint8_t pf = static_cast<std::uint8_t>((pgn >> 8u) & 0xFFu);
  if (pf < 240u) {
    can_id |= (pgn & 0x3FF00u) << 8u;
    can_id |= static_cast<std::uint32_t>(destination) << 8u;
  } else {
    can_id |= (pgn & 0x3FFFFu) << 8u;
  }
  can_id |= static_cast<std::uint32_t>(source_address & 0xFFu);
  return can_id;
}

CanFrame::CanFrame(std::uint32_t identifier, const std::uint8_t *payload,
                   std::uint8_t payload_length,
                   std::uint64_t timestamp) noexcept {
  id = identifier;
  dlc = std::min<std::uint8_t>(payload_length, 8u);
  if (payload != nullptr) {
    for (std::uint8_t i = 0u; i < dlc; ++i) {
      data[i] = payload[i];
    }
  }
  timestamp_ms = timestamp;
}

CanFrame::CanFrame(std::uint32_t identifier,
                   std::initializer_list<std::uint8_t> payload,
                   std::uint64_t timestamp) noexcept {
  id = identifier;
  dlc = static_cast<std::uint8_t>(std::min<std::size_t>(payload.size(), 8u));
  std::size_t index = 0u;
  for (std::uint8_t value : payload) {
    if (index >= 8u) {
      break;
    }
    data[index++] = value;
  }
  timestamp_ms = timestamp;
}

CanFrame::CanFrame(std::uint32_t identifier,
                   std::initializer_list<std::uint8_t> payload,
                   std::uint8_t payload_length,
                   std::uint64_t timestamp) noexcept {
  id = identifier;
  dlc = static_cast<std::uint8_t>(std::min<std::size_t>(
      std::min<std::size_t>(payload.size(),
                            static_cast<std::size_t>(payload_length)),
      8u));
  std::size_t index = 0u;
  for (std::uint8_t value : payload) {
    if (index >= dlc) {
      break;
    }
    data[index++] = value;
  }
  timestamp_ms = timestamp;
}

CanFrame::CanFrame(std::uint32_t identifier, std::uint8_t payload_length,
                   std::initializer_list<std::uint8_t> payload,
                   std::uint64_t timestamp) noexcept
    : CanFrame(identifier, payload, payload_length, timestamp) {}

TpResult TpReassembler::on_frame(const CanFrame &frame) {
  const std::uint8_t source_address = source_address_from_can_id(frame.id);
  const std::uint32_t pgn = pgn_from_can_id(frame.id);

  if (pgn == TP_CM_PGN) {
    if (source_address == J1939_NULL_ADDRESS ||
        source_address == J1939_GLOBAL_ADDRESS) {
      return ignored_result(source_address, pgn);
    }
    return handle_tp_cm(frame, source_address);
  }

  if (pgn == TP_DT_PGN) {
    if (source_address == J1939_NULL_ADDRESS ||
        source_address == J1939_GLOBAL_ADDRESS) {
      return ignored_result(source_address, pgn);
    }
    return handle_tp_dt(frame, source_address);
  }

  if (pgn == ADDRESS_CLAIM_PGN) {
    if (source_address == J1939_NULL_ADDRESS ||
        source_address == J1939_GLOBAL_ADDRESS) {
      return ignored_result(source_address, pgn);
    }
    return on_address_claim(frame);
  }

  return ignored_result(source_address, pgn);
}

TpResult TpReassembler::on_frame(std::uint32_t can_id, const std::uint8_t *data,
                                 std::uint8_t dlc, std::uint64_t timestamp_ms) {
  return on_frame(make_frame(can_id, data, dlc, timestamp_ms));
}

TpResult TpReassembler::on_frame(std::uint32_t can_id,
                                 std::initializer_list<std::uint8_t> data,
                                 std::uint64_t timestamp_ms) {
  return on_frame(CanFrame(can_id, data, timestamp_ms));
}

TpResult TpReassembler::handle_tp_cm(const CanFrame &frame,
                                     std::uint8_t source_address) {
  if (frame.dlc < 1u) {
    return rejected_result(source_address, TP_CM_PGN,
                           TpAbortReason::MalformedFrame);
  }
  if (frame.data[0] != TP_CM_BAM) {
    return ignored_result(source_address, TP_CM_PGN);
  }
  if (frame.dlc < 8u) {
    return rejected_result(source_address, TP_CM_PGN,
                           TpAbortReason::InvalidFrameLength);
  }

  const std::uint32_t total_bytes =
      static_cast<std::uint32_t>(frame.data[1]) |
      (static_cast<std::uint32_t>(frame.data[2]) << 8u);
  const std::uint8_t total_packets = frame.data[3];
  const std::uint32_t message_pgn =
      static_cast<std::uint32_t>(frame.data[4]) |
      (static_cast<std::uint32_t>(frame.data[5]) << 8u) |
      (static_cast<std::uint32_t>(frame.data[6]) << 16u);

  if (total_bytes < TP_MIN_BYTES || total_bytes > TP_MAX_BYTES) {
    return rejected_result(source_address, TP_CM_PGN,
                           TpAbortReason::InvalidTotalBytes);
  }

  const std::uint32_t required_packets = (total_bytes + 6u) / 7u;
  if (total_packets == 0u || total_packets != required_packets) {
    return rejected_result(source_address, TP_CM_PGN,
                           TpAbortReason::PacketCountMismatch);
  }

  const bool replaced = sessions_.find(source_address) != sessions_.end();
  sessions_.erase(source_address);

  Session session;
  session.pgn = message_pgn;
  session.total_bytes = total_bytes;
  session.total_packets = total_packets;
  session.last_timestamp_ms = frame.timestamp_ms;
  session.payload.reserve(static_cast<std::size_t>(total_bytes));
  sessions_.emplace(source_address, std::move(session));

  TpResult result;
  result.status = TpStatus::Started;
  result.source_address = source_address;
  result.pgn = message_pgn;
  result.total_bytes = total_bytes;
  result.total_packets = total_packets;
  result.session_active = true;
  result.replaced = replaced;
  return result;
}

TpResult TpReassembler::handle_tp_dt(const CanFrame &frame,
                                     std::uint8_t source_address) {
  auto session_it = sessions_.find(source_address);
  if (session_it == sessions_.end()) {
    return ignored_result(source_address, TP_DT_PGN);
  }

  Session &session = session_it->second;
  auto abort_session = [&](TpAbortReason reason) {
    TpResult result;
    result.status = TpStatus::Aborted;
    result.reason = reason;
    result.source_address = source_address;
    result.pgn = session.pgn;
    result.total_bytes = session.total_bytes;
    result.total_packets = session.total_packets;
    sessions_.erase(session_it);
    return result;
  };

  if (frame.timestamp_ms >= session.last_timestamp_ms &&
      frame.timestamp_ms - session.last_timestamp_ms > TP_TIMEOUT_MS) {
    return abort_session(TpAbortReason::Timeout);
  }

  session.last_timestamp_ms = frame.timestamp_ms;

  if (frame.dlc < 1u) {
    return abort_session(TpAbortReason::MalformedFrame);
  }
  if (frame.dlc < 8u) {
    return abort_session(TpAbortReason::InvalidFrameLength);
  }

  const std::uint8_t sequence = frame.data[0];
  if (sequence == 0u || sequence > session.total_packets) {
    return abort_session(TpAbortReason::InvalidSequence);
  }

  if (sequence != session.expected_sequence) {
    const TpAbortReason reason = sequence < session.expected_sequence
                                     ? TpAbortReason::DuplicateSequence
                                     : TpAbortReason::OutOfSequence;
    return abort_session(reason);
  }

  const std::uint32_t remaining =
      session.total_bytes - static_cast<std::uint32_t>(session.payload.size());
  const std::uint32_t bytes_to_copy = std::min<std::uint32_t>(7u, remaining);
  for (std::uint32_t i = 0u; i < bytes_to_copy; ++i) {
    session.payload.push_back(frame.data[1u + i]);
  }

  ++session.expected_sequence;

  if (sequence == session.total_packets) {
    TpMessage message;
    message.source_address = source_address;
    message.pgn = session.pgn;
    message.total_bytes = session.total_bytes;
    message.total_packets = session.total_packets;
    message.payload = std::move(session.payload);
    sessions_.erase(session_it);

    TpResult result;
    result.status = TpStatus::Complete;
    result.completed = true;
    result.complete = true;
    result.source_address = source_address;
    result.pgn = message.pgn;
    result.total_bytes = message.total_bytes;
    result.total_packets = message.total_packets;
    copy_payload(result, message.payload);
    result.message = std::move(message);
    return result;
  }

  TpResult result;
  result.status = TpStatus::InProgress;
  result.source_address = source_address;
  result.pgn = session.pgn;
  result.total_bytes = session.total_bytes;
  result.total_packets = session.total_packets;
  result.session_active = true;
  return result;
}

TpResult TpReassembler::on_address_claim(std::uint8_t source_address,
                                         std::uint64_t name,
                                         std::uint64_t timestamp_ms) {
  (void)timestamp_ms;

  if (source_address == J1939_NULL_ADDRESS ||
      source_address == J1939_GLOBAL_ADDRESS) {
    TpResult result = ignored_result(source_address, ADDRESS_CLAIM_PGN);
    result.name = name;
    return result;
  }

  auto name_it = address_names_.find(source_address);
  if (name_it == address_names_.end()) {
    address_names_.emplace(source_address, name);
    TpResult result;
    result.status = TpStatus::AddressClaimAccepted;
    result.source_address = source_address;
    result.pgn = ADDRESS_CLAIM_PGN;
    result.name = name;
    return result;
  }

  if (name == name_it->second) {
    TpResult result;
    result.status = TpStatus::AddressClaimAccepted;
    result.source_address = source_address;
    result.pgn = ADDRESS_CLAIM_PGN;
    result.name = name;
    return result;
  }

  if (name < name_it->second) {
    const bool had_session = sessions_.find(source_address) != sessions_.end();
    name_it->second = name;
    sessions_.erase(source_address);

    TpResult result;
    result.status = TpStatus::AddressClaimAccepted;
    result.source_address = source_address;
    result.pgn = ADDRESS_CLAIM_PGN;
    result.name = name;
    result.replaced = had_session;
    return result;
  }

  sessions_.erase(source_address);
  TpResult result;
  result.status = TpStatus::AddressClaimRejected;
  result.reason = TpAbortReason::AddressClaimConflict;
  result.source_address = source_address;
  result.pgn = ADDRESS_CLAIM_PGN;
  result.name = name;
  return result;
}

TpResult TpReassembler::on_address_claim(const CanFrame &frame) {
  const std::uint8_t source_address = source_address_from_can_id(frame.id);
  if (frame.dlc < 8u) {
    return rejected_result(source_address, ADDRESS_CLAIM_PGN,
                           TpAbortReason::MalformedFrame);
  }

  std::uint64_t name = 0u;
  for (std::size_t i = 0u; i < 8u; ++i) {
    name |= static_cast<std::uint64_t>(frame.data[i]) << (8u * i);
  }
  return on_address_claim(source_address, name, frame.timestamp_ms);
}

bool TpReassembler::has_session(std::uint8_t source_address) const noexcept {
  return sessions_.find(source_address) != sessions_.end();
}

std::size_t TpReassembler::session_count() const noexcept {
  return sessions_.size();
}

std::optional<std::uint64_t>
TpReassembler::name_for_address(std::uint8_t source_address) const {
  const auto it = address_names_.find(source_address);
  if (it == address_names_.end()) {
    return std::nullopt;
  }
  return it->second;
}

void TpReassembler::clear_session(std::uint8_t source_address) noexcept {
  sessions_.erase(source_address);
}

void TpReassembler::clear_all_sessions() noexcept { sessions_.clear(); }

void TpReassembler::reset() noexcept {
  sessions_.clear();
  address_names_.clear();
}

std::size_t TpReassembler::expire_sessions(std::uint64_t now_ms) noexcept {
  std::size_t expired = 0u;
  auto it = sessions_.begin();
  while (it != sessions_.end()) {
    if (now_ms >= it->second.last_timestamp_ms &&
        now_ms - it->second.last_timestamp_ms > TP_TIMEOUT_MS) {
      it = sessions_.erase(it);
      ++expired;
    } else {
      ++it;
    }
  }
  return expired;
}

} // namespace netsec

#include "netsec.hpp"

#include <algorithm>
#include <iterator>

namespace netsec {

const char* to_string(ConnectionIdError error) noexcept {
  switch (error) {
    case ConnectionIdError::none:
      return "NONE";
    case ConnectionIdError::frame_encoding_error:
      return "FRAME_ENCODING_ERROR";
    case ConnectionIdError::protocol_violation:
      return "PROTOCOL_VIOLATION";
    case ConnectionIdError::connection_id_limit_error:
      return "CONNECTION_ID_LIMIT_ERROR";
  }
  return "UNKNOWN";
}

ConnectionIdManager::ConnectionIdManager() = default;

ConnectionIdManager::ConnectionIdManager(
    std::uint64_t active_connection_id_limit)
    : ConnectionIdManager(ConnectionIdManagerOptions{
          active_connection_id_limit, false, false, {}}) {}

ConnectionIdManager::ConnectionIdManager(
    std::uint64_t active_connection_id_limit,
    bool peer_connection_uses_zero_length_cid)
    : ConnectionIdManager(ConnectionIdManagerOptions{
          active_connection_id_limit,
          peer_connection_uses_zero_length_cid,
          false,
          {}}) {}

ConnectionIdManager::ConnectionIdManager(
    std::uint64_t active_connection_id_limit,
    const ConnectionId& initial_peer_connection_id)
    : ConnectionIdManager(ConnectionIdManagerOptions{
          active_connection_id_limit,
          initial_peer_connection_id.empty(),
          false,
          {}}) {}

ConnectionIdManager::ConnectionIdManager(ConnectionIdManagerOptions options)
    : active_connection_id_limit_(options.active_connection_id_limit),
      peer_uses_zero_length_cid_(
          options.peer_connection_uses_zero_length_cid),
      local_uses_zero_length_cid_(
          options.local_connection_uses_zero_length_cid),
      connection_id_generator_(std::move(options.connection_id_generator)) {}

ConnectionIdManager::FrameOutcome
ConnectionIdManager::apply_new_connection_id(
    const NewConnectionIdFrame& frame) {
  FrameOutcome outcome;

  // A connection that uses a zero-length CID cannot migrate using a new CID.
  // RFC 9000 requires every NEW_CONNECTION_ID frame to be rejected in this
  // state, before considering the frame's contents.
  if (peer_uses_zero_length_cid_) {
    outcome.error = ConnectionIdError::protocol_violation;
    return outcome;
  }

  if (frame.connection_id.size() < 1 ||
      frame.connection_id.size() > kMaxConnectionIdLength) {
    outcome.error = ConnectionIdError::frame_encoding_error;
    return outcome;
  }

  if (frame.retire_prior_to > frame.sequence_number) {
    outcome.error = ConnectionIdError::frame_encoding_error;
    return outcome;
  }

  auto existing = peer_connection_ids_.find(frame.sequence_number);
  if (existing != peer_connection_ids_.end()) {
    if (existing->second.connection_id != frame.connection_id ||
        existing->second.stateless_reset_token !=
            frame.stateless_reset_token) {
      outcome.error = ConnectionIdError::protocol_violation;
      return outcome;
    }

    outcome.duplicate = true;
    // A repeated active sequence with the same CID and reset token is ignored.
    return outcome;
  }

  const std::uint64_t effective_retire_prior_to =
      std::max(peer_retire_prior_to_, frame.retire_prior_to);
  const auto first_active =
      peer_connection_ids_.lower_bound(effective_retire_prior_to);
  std::size_t retired = static_cast<std::size_t>(
      std::distance(peer_connection_ids_.begin(), first_active));
  std::size_t active_after =
      static_cast<std::size_t>(
          std::distance(first_active, peer_connection_ids_.end()));

  // A sequence number below the effective retire_prior_to value is already
  // retired on arrival and therefore does not consume a slot.
  if (frame.sequence_number >= effective_retire_prior_to) {
    ++active_after;
  }

  if (static_cast<std::uint64_t>(active_after) >
      active_connection_id_limit_) {
    outcome.error = ConnectionIdError::connection_id_limit_error;
    return outcome;
  }

  peer_connection_ids_.erase(peer_connection_ids_.begin(), first_active);
  if (frame.sequence_number >= effective_retire_prior_to) {
    peer_connection_ids_.emplace(
        frame.sequence_number,
        PeerConnectionId{frame.connection_id, frame.stateless_reset_token});
  }
  peer_retire_prior_to_ = effective_retire_prior_to;
  outcome.retired_peer_connection_ids = retired;
  return outcome;
}

ConnectionIdManager::FrameOutcome
ConnectionIdManager::apply_retire_connection_id(
    const RetireConnectionIdFrame& frame) {
  FrameOutcome outcome;

  if (frame.sequence_number >= next_local_sequence_) {
    outcome.error = ConnectionIdError::protocol_violation;
    return outcome;
  }

  if (retired_local_connection_ids_.find(frame.sequence_number) !=
      retired_local_connection_ids_.end()) {
    outcome.duplicate = true;
    return outcome;
  }

  retired_local_connection_ids_.insert(frame.sequence_number);
  outcome.retired_local_connection_ids = 1;
  outcome.replacement_eligible = !local_uses_zero_length_cid_;
  return outcome;
}

ConnectionIdManager::FrameOutcome ConnectionIdManager::apply_frame(
    const ConnectionIdFrame& frame) {
  if (const auto* new_cid = std::get_if<NewConnectionIdFrame>(&frame)) {
    return apply_new_connection_id(*new_cid);
  }
  if (const auto* retire_cid =
          std::get_if<RetireConnectionIdFrame>(&frame)) {
    return apply_retire_connection_id(*retire_cid);
  }

  FrameOutcome outcome;
  outcome.error = ConnectionIdError::protocol_violation;
  return outcome;
}

ProcessResult ConnectionIdManager::finish_packet(
    const FrameOutcome& outcome) {
  ProcessResult result;
  result.error = outcome.error;
  result.duplicate = outcome.duplicate;
  result.retired_peer_connection_ids =
      outcome.retired_peer_connection_ids;
  result.retired_local_connection_ids =
      outcome.retired_local_connection_ids;

  if (result.ok() && outcome.replacement_eligible) {
    schedule_replacement(result);
  }
  return result;
}

void ConnectionIdManager::schedule_replacement(ProcessResult& result) {
  if (local_uses_zero_length_cid_) {
    return;
  }

  // One packet can retire many IDs, but it can cause only one outgoing CID.
  // Cumulatively, replacements can never outnumber accepted retirements.
  if (replacement_connection_id_count_ >=
      retired_local_connection_ids_.size()) {
    return;
  }

  result.replacement = issue_connection_id();
  ++replacement_connection_id_count_;
}

ConnectionIdError ConnectionIdManager::PacketProcessor::process(
    const ConnectionIdFrame& frame) {
  if (final_result_) {
    return ConnectionIdError::protocol_violation;
  }
  if (outcome_.error != ConnectionIdError::none) {
    return outcome_.error;
  }

  const auto current = manager_->apply_frame(frame);
  if (current.error != ConnectionIdError::none) {
    outcome_.error = current.error;
    outcome_.replacement_eligible = false;
    return outcome_.error;
  }

  outcome_.duplicate = outcome_.duplicate || current.duplicate;
  outcome_.retired_peer_connection_ids +=
      current.retired_peer_connection_ids;
  outcome_.retired_local_connection_ids +=
      current.retired_local_connection_ids;
  outcome_.replacement_eligible =
      outcome_.replacement_eligible || current.replacement_eligible;
  return ConnectionIdError::none;
}

ProcessResult ConnectionIdManager::PacketProcessor::finish() {
  if (!final_result_) {
    final_result_ = manager_->finish_packet(outcome_);
  }
  return *final_result_;
}

ConnectionIdManager::PacketProcessor ConnectionIdManager::begin_packet() {
  return PacketProcessor(*this);
}

ProcessResult ConnectionIdManager::process_packet(
    const std::vector<ConnectionIdFrame>& frames) {
  auto packet = begin_packet();
  for (const auto& frame : frames) {
    if (packet.process(frame) != ConnectionIdError::none) {
      break;
    }
  }
  return packet.finish();
}

ProcessResult ConnectionIdManager::process_packet(
    std::initializer_list<ConnectionIdFrame> frames) {
  return process_packet(std::vector<ConnectionIdFrame>(frames));
}

ProcessResult ConnectionIdManager::process_frame(
    const ConnectionIdFrame& frame) {
  return process_packet(std::vector<ConnectionIdFrame>{frame});
}

ProcessResult ConnectionIdManager::process_new_connection_id(
    const NewConnectionIdFrame& frame) {
  return finish_packet(apply_new_connection_id(frame));
}

ProcessResult ConnectionIdManager::process_retire_connection_id(
    const RetireConnectionIdFrame& frame) {
  return finish_packet(apply_retire_connection_id(frame));
}

ProcessResult ConnectionIdManager::process_new_connection_id(
    std::uint64_t sequence_number, std::uint64_t retire_prior_to,
    const ConnectionId& connection_id,
    const StatelessResetToken& stateless_reset_token) {
  return process_new_connection_id(NewConnectionIdFrame{
      sequence_number, retire_prior_to, connection_id,
      stateless_reset_token});
}

ProcessResult ConnectionIdManager::process_retire_connection_id(
    std::uint64_t sequence_number) {
  return process_retire_connection_id(
      RetireConnectionIdFrame{sequence_number});
}

IssuedConnectionId ConnectionIdManager::issue_connection_id() {
  const std::uint64_t sequence_number = next_local_sequence_++;
  auto generated = generate_connection_id(sequence_number);
  return IssuedConnectionId{sequence_number, std::move(generated.first),
                            generated.second};
}

std::pair<ConnectionId, StatelessResetToken>
ConnectionIdManager::generate_connection_id(
    std::uint64_t sequence_number) {
  if (connection_id_generator_) {
    return connection_id_generator_(sequence_number);
  }

  // Deterministic fallback for tests and examples. Production deployments
  // should install ConnectionIdGenerator backed by a CSPRNG.
  std::vector<std::uint8_t> cid_bytes(8);
  for (std::size_t index = 0; index < cid_bytes.size(); ++index) {
    cid_bytes[index] = static_cast<std::uint8_t>(
        (sequence_number >> (index * 8U)) & 0xffU);
  }

  std::array<std::uint8_t, kStatelessResetTokenLength> token_bytes{};
  for (std::size_t index = 0; index < 8; ++index) {
    token_bytes[index] = static_cast<std::uint8_t>(
        (sequence_number >> (index * 8U)) & 0xffU);
    token_bytes[index + 8] = static_cast<std::uint8_t>(
        ((sequence_number >> (index * 8U)) ^ 0xa5U) & 0xffU);
  }

  return {ConnectionId{std::move(cid_bytes)},
          StatelessResetToken{token_bytes}};
}

}  // namespace netsec

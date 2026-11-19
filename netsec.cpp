#include "netsec.hpp"

namespace netsec {

ConnectionIdManager::ConnectionIdManager(
    std::uint64_t active_connection_id_limit,
    bool local_uses_zero_length_cid)
    : active_connection_id_limit_(active_connection_id_limit),
      local_uses_zero_length_cid_(local_uses_zero_length_cid) {}

void ConnectionIdManager::set_cid_generator(CidGenerator generator) {
  cid_generator_ = std::move(generator);
}

ConnectionIdManager::ConnectionId ConnectionIdManager::GenerateCid(
    std::uint64_t sequence_number) {
  if (cid_generator_) {
    return cid_generator_(sequence_number);
  }
  // 默认实现:由序号派生的 8 字节确定性 CID,仅供测试;
  // 生产环境应通过 set_cid_generator 注入随机源。
  ConnectionId cid(8);
  for (std::size_t i = 0; i < cid.size(); ++i) {
    cid[i] = static_cast<std::uint8_t>(sequence_number >> (i * 8));
  }
  return cid;
}

QuicError ConnectionIdManager::OnNewConnectionId(
    std::uint64_t sequence_number, std::uint64_t retire_prior_to,
    const ConnectionId& connection_id) {
  // 使用零长度连接 ID 的连接收到该帧为 PROTOCOL_VIOLATION。
  if (local_uses_zero_length_cid_) {
    return QuicError::kProtocolViolation;
  }
  // CID 长度须在 1 到 20 字节之间。
  if (connection_id.empty() ||
      connection_id.size() > kMaxConnectionIdLength) {
    return QuicError::kFrameEncodingError;
  }
  // retire_prior_to 不得大于帧内序号。
  if (retire_prior_to > sequence_number) {
    return QuicError::kFrameEncodingError;
  }
  // 序号小于已见过的 retire_prior_to,视为已退役,直接忽略。
  if (sequence_number < max_retire_prior_to_) {
    return QuicError::kNoError;
  }
  // 重复序号:内容不同为 PROTOCOL_VIOLATION,内容相同为无操作。
  const auto existing = peer_cids_.find(sequence_number);
  if (existing != peer_cids_.end()) {
    if (existing->second != connection_id) {
      return QuicError::kProtocolViolation;
    }
    return QuicError::kNoError;
  }

  peer_cids_.emplace(sequence_number, connection_id);

  // 应用 retire_prior_to:退役所有序号小于它的已保存 ID。
  if (retire_prior_to > max_retire_prior_to_) {
    max_retire_prior_to_ = retire_prior_to;
  }
  for (auto it = peer_cids_.begin();
       it != peer_cids_.end() && it->first < retire_prior_to;) {
    it = peer_cids_.erase(it);
  }

  // 处理退役要求后仍保存的有效 ID 数不得超过限制。
  if (peer_cids_.size() > active_connection_id_limit_) {
    return QuicError::kConnectionIdLimitError;
  }
  return QuicError::kNoError;
}

QuicError ConnectionIdManager::OnRetireConnectionId(
    std::uint64_t sequence_number) {
  // 序号不得大于等于本端已发放的最大序号加一(即不得退役未发放的 ID)。
  if (sequence_number >= next_issued_seq_) {
    return QuicError::kProtocolViolation;
  }
  // 重复退役同一序号为无操作。
  if (!retired_seqs_.insert(sequence_number).second) {
    return QuicError::kNoError;
  }
  issued_cids_.erase(sequence_number);
  MaybeReplenish();
  return QuicError::kNoError;
}

void ConnectionIdManager::BeginPacket() {
  replenished_this_packet_ = false;
}

std::uint64_t ConnectionIdManager::IssueConnectionId() {
  const std::uint64_t seq = next_issued_seq_++;
  issued_cids_.emplace(seq, GenerateCid(seq));
  return seq;
}

void ConnectionIdManager::MaybeReplenish() {
  // 补发约束:累计补发数不得超过已退役数,
  // 且每个到达的数据包最多触发一次补发。
  if (replenished_this_packet_) {
    return;
  }
  if (replenished_count_ >= retired_seqs_.size()) {
    return;
  }
  const std::uint64_t seq = next_issued_seq_++;
  ConnectionId cid = GenerateCid(seq);
  issued_cids_.emplace(seq, cid);
  pending_new_ids_.emplace_back(seq, std::move(cid));
  ++replenished_count_;
  replenished_this_packet_ = true;
}

std::optional<std::pair<std::uint64_t, ConnectionIdManager::ConnectionId>>
ConnectionIdManager::PopPendingNewId() {
  if (pending_new_ids_.empty()) {
    return std::nullopt;
  }
  auto front = std::move(pending_new_ids_.front());
  pending_new_ids_.erase(pending_new_ids_.begin());
  return front;
}

bool ConnectionIdManager::HasPeerCid(std::uint64_t sequence_number) const {
  return peer_cids_.count(sequence_number) != 0;
}

bool ConnectionIdManager::IsIssued(std::uint64_t sequence_number) const {
  return issued_cids_.count(sequence_number) != 0;
}

}  // namespace netsec

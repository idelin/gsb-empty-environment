#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <optional>
#include <set>
#include <utility>
#include <vector>

namespace netsec {

// QUIC 连接错误类型(对应 RFC 9000 相关错误码)。
enum class QuicError {
  kNoError,
  kFrameEncodingError,
  kProtocolViolation,
  kConnectionIdLimitError,
};

// 供 QUIC 帧处理循环调用的连接 ID 管理器。
//
// 负责两类状态:
//  1. 对端通过 NEW_CONNECTION_ID 提供的、本端保存的对端连接 ID 集合,
//     受本端传输参数 active_connection_id_limit 约束。
//  2. 本端发放给对端的连接 ID,处理对端的 RETIRE_CONNECTION_ID,
//     并按需补发新 ID(防止退役洪泛造成的流量放大)。
class ConnectionIdManager {
 public:
  using ConnectionId = std::vector<std::uint8_t>;
  // 为新序号生成本端连接 ID 的回调,生产环境应注入随机源。
  using CidGenerator = std::function<ConnectionId(std::uint64_t sequence)>;

  static constexpr std::size_t kMaxConnectionIdLength = 20;

  // active_connection_id_limit 来自本端传输参数;
  // local_uses_zero_length_cid 表示本端连接使用零长度连接 ID。
  explicit ConnectionIdManager(std::uint64_t active_connection_id_limit,
                               bool local_uses_zero_length_cid = false);

  void set_cid_generator(CidGenerator generator);

  // ---- 帧处理接口(由帧处理循环调用) ----

  // 处理对端发来的 NEW_CONNECTION_ID 帧。
  QuicError OnNewConnectionId(std::uint64_t sequence_number,
                              std::uint64_t retire_prior_to,
                              const ConnectionId& connection_id);

  // 处理对端发来的 RETIRE_CONNECTION_ID 帧。
  QuicError OnRetireConnectionId(std::uint64_t sequence_number);

  // 每收到一个数据包、处理其帧之前调用一次,
  // 用于实现"每个数据包最多触发一次补发"。
  void BeginPacket();

  // ---- 本端发放侧 ----

  // 发放一个新的本端连接 ID,返回其序号。
  std::uint64_t IssueConnectionId();

  // 取出因退役而待补发的 (序号, CID),没有则返回 nullopt。
  std::optional<std::pair<std::uint64_t, ConnectionId>> PopPendingNewId();

  // ---- 状态查询(主要供测试与栈内诊断) ----
  std::size_t stored_peer_cid_count() const { return peer_cids_.size(); }
  bool HasPeerCid(std::uint64_t sequence_number) const;
  bool IsIssued(std::uint64_t sequence_number) const;
  std::uint64_t next_issued_sequence() const { return next_issued_seq_; }
  std::uint64_t retired_count() const { return retired_seqs_.size(); }
  std::uint64_t replenished_count() const { return replenished_count_; }

 private:
  ConnectionId GenerateCid(std::uint64_t sequence_number);
  void MaybeReplenish();

  std::uint64_t active_connection_id_limit_;
  bool local_uses_zero_length_cid_;

  // 对端提供的连接 ID:序号 -> CID 内容。
  std::map<std::uint64_t, ConnectionId> peer_cids_;
  // 迄今为止见过的最大 retire_prior_to。
  std::uint64_t max_retire_prior_to_ = 0;

  // 本端发放的连接 ID:序号 -> CID 内容。
  std::map<std::uint64_t, ConnectionId> issued_cids_;
  std::uint64_t next_issued_seq_ = 0;
  std::set<std::uint64_t> retired_seqs_;
  std::uint64_t replenished_count_ = 0;
  bool replenished_this_packet_ = false;
  std::vector<std::pair<std::uint64_t, ConnectionId>> pending_new_ids_;

  CidGenerator cid_generator_;
};

}  // namespace netsec

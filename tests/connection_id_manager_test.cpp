#include <cstdlib>
#include <iostream>

#include "netsec.hpp"

namespace {

int g_failures = 0;

#define CHECK(cond)                                                     \
  do {                                                                  \
    if (!(cond)) {                                                      \
      ++g_failures;                                                     \
      std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << " " #cond \
                << std::endl;                                           \
    }                                                                   \
  } while (0)

using netsec::ConnectionIdManager;
using netsec::QuicError;

ConnectionIdManager::ConnectionId Cid(std::uint8_t fill,
                                      std::size_t len = 4) {
  return ConnectionIdManager::ConnectionId(len, fill);
}

// CID 长度越界(0 或 >20)为 FRAME_ENCODING_ERROR。
void TestInvalidCidLength() {
  ConnectionIdManager mgr(4);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0xAA, 0)) ==
        QuicError::kFrameEncodingError);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0xAA, 21)) ==
        QuicError::kFrameEncodingError);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0xAA, 1)) == QuicError::kNoError);
  CHECK(mgr.OnNewConnectionId(2, 0, Cid(0xBB, 20)) == QuicError::kNoError);
}

// 保存的对端 ID 数超过 active_connection_id_limit
// 为 CONNECTION_ID_LIMIT_ERROR。
void TestLimitExceeded() {
  ConnectionIdManager mgr(2);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x01)) == QuicError::kNoError);
  CHECK(mgr.OnNewConnectionId(2, 0, Cid(0x02)) == QuicError::kNoError);
  CHECK(mgr.OnNewConnectionId(3, 0, Cid(0x03)) ==
        QuicError::kConnectionIdLimitError);
}

// 序号相同内容不同为 PROTOCOL_VIOLATION;内容相同的重复帧无操作。
void TestDuplicateSequence() {
  ConnectionIdManager mgr(4);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x0A)) == QuicError::kNoError);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x0A)) == QuicError::kNoError);
  CHECK(mgr.stored_peer_cid_count() == 1);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x0B)) ==
        QuicError::kProtocolViolation);
}

// retire_prior_to 大于序号为 FRAME_ENCODING_ERROR;
// 合法的 retire_prior_to 先退役旧 ID,再计入上限。
void TestRetirePriorTo() {
  ConnectionIdManager bad(4);
  CHECK(bad.OnNewConnectionId(1, 2, Cid(0x01)) ==
        QuicError::kFrameEncodingError);

  ConnectionIdManager mgr(2);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x01)) == QuicError::kNoError);
  CHECK(mgr.OnNewConnectionId(2, 0, Cid(0x02)) == QuicError::kNoError);
  // retire_prior_to=2 退役序号 1,腾出席位后不超上限。
  CHECK(mgr.OnNewConnectionId(3, 2, Cid(0x03)) == QuicError::kNoError);
  CHECK(!mgr.HasPeerCid(1));
  CHECK(mgr.stored_peer_cid_count() == 2);
  // 序号小于已见过的 retire_prior_to,视为已退役,直接忽略。
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x09)) == QuicError::kNoError);
  CHECK(mgr.stored_peer_cid_count() == 2);
}

// 退役洪泛:未发放序号为 PROTOCOL_VIOLATION,重复退役无操作;
// 补发累计不超过已退役数,且每个数据包最多补发一次。
void TestRetireFloodNoAmplification() {
  ConnectionIdManager mgr(8);
  for (int i = 0; i < 4; ++i) {
    mgr.IssueConnectionId();
  }
  mgr.BeginPacket();

  // 退役未发放的序号 -> PROTOCOL_VIOLATION。
  CHECK(mgr.OnRetireConnectionId(4) == QuicError::kProtocolViolation);
  CHECK(mgr.OnRetireConnectionId(100) == QuicError::kProtocolViolation);

  // 同一数据包内洪泛退役 0、1、2:只触发一次补发。
  CHECK(mgr.OnRetireConnectionId(0) == QuicError::kNoError);
  CHECK(mgr.OnRetireConnectionId(1) == QuicError::kNoError);
  CHECK(mgr.OnRetireConnectionId(2) == QuicError::kNoError);
  // 重复退役同一序号无操作。
  CHECK(mgr.OnRetireConnectionId(2) == QuicError::kNoError);
  CHECK(mgr.retired_count() == 3);
  CHECK(mgr.replenished_count() == 1);
  CHECK(mgr.PopPendingNewId().has_value());
  CHECK(!mgr.PopPendingNewId().has_value());

  // 新数据包到来后才允许下一次补发。
  mgr.BeginPacket();
  CHECK(mgr.OnRetireConnectionId(3) == QuicError::kNoError);
  CHECK(mgr.retired_count() == 4);
  CHECK(mgr.replenished_count() == 2);
  CHECK(mgr.PopPendingNewId().has_value());
  CHECK(!mgr.PopPendingNewId().has_value());

  // 持续退役(已无未退役 ID 可退,重复帧不再触发补发):
  // 累计补发数始终不超过已退役数。
  for (int i = 0; i < 10; ++i) {
    mgr.BeginPacket();
    CHECK(mgr.OnRetireConnectionId(0) == QuicError::kNoError);
    CHECK(mgr.replenished_count() <= mgr.retired_count());
  }
  CHECK(mgr.replenished_count() == 2);
  // 补发的新 ID 序号递增且已登记为已发放。
  CHECK(mgr.IsIssued(4));
  CHECK(mgr.IsIssued(5));
  CHECK(mgr.next_issued_sequence() == 6);
}

// 零长度连接 ID 的连接收到 NEW_CONNECTION_ID 为 PROTOCOL_VIOLATION。
void TestZeroLengthConnection() {
  ConnectionIdManager mgr(4, /*local_uses_zero_length_cid=*/true);
  CHECK(mgr.OnNewConnectionId(1, 0, Cid(0x01)) ==
        QuicError::kProtocolViolation);
  CHECK(mgr.stored_peer_cid_count() == 0);
}

}  // namespace

int main() {
  TestInvalidCidLength();
  TestLimitExceeded();
  TestDuplicateSequence();
  TestRetirePriorTo();
  TestRetireFloodNoAmplification();
  TestZeroLengthConnection();
  if (g_failures != 0) {
    std::cerr << g_failures << " check(s) failed" << std::endl;
    return EXIT_FAILURE;
  }
  std::cout << "all tests passed" << std::endl;
  return EXIT_SUCCESS;
}

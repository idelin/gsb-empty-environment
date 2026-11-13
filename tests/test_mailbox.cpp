#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

#include "netsec.hpp"

namespace {

int g_failures = 0;

#define CHECK(cond)                                                  \
  do {                                                               \
    if (!(cond)) {                                                   \
      ++g_failures;                                                  \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,   \
                   #cond);                                           \
    }                                                                \
  } while (0)

// 构造邮箱帧:长度默认取负载长度,可用 declared_len 伪造。
std::vector<std::uint8_t> makeFrame(std::uint8_t counter, std::uint8_t type,
                                    const std::vector<std::uint8_t>& payload,
                                    std::uint16_t declared_len = 0xFFFF) {
  const std::uint16_t length =
      declared_len == 0xFFFF ? static_cast<std::uint16_t>(payload.size())
                             : declared_len;
  std::vector<std::uint8_t> frame;
  frame.push_back(static_cast<std::uint8_t>(length & 0xFF));
  frame.push_back(static_cast<std::uint8_t>(length >> 8));
  frame.push_back(0x34);  // address 0x1234
  frame.push_back(0x12);
  frame.push_back(0x00);  // channel 0, priority 0
  frame.push_back(static_cast<std::uint8_t>((counter << 4) | type));
  frame.insert(frame.end(), payload.begin(), payload.end());
  return frame;
}

std::vector<std::uint8_t> coePayload(std::uint8_t service, std::uint8_t cmd) {
  const std::uint16_t coe_header =
      static_cast<std::uint16_t>((service << 12) | 0x000);
  return {static_cast<std::uint8_t>(coe_header & 0xFF),
          static_cast<std::uint8_t>(coe_header >> 8), cmd, 0x00, 0x00, 0x00};
}

std::vector<std::uint8_t> foeWriteRequest(const std::string& name) {
  std::vector<std::uint8_t> payload = {0x02, 0x00,              // opcode WRQ
                                       0x00, 0x00, 0x00, 0x00};  // password
  payload.insert(payload.end(), name.begin(), name.end());
  return payload;
}

netsec::MailboxValidator makeCoeFoeValidator(std::uint16_t sm_size = 512) {
  netsec::MailboxValidator v(sm_size);
  v.allowType(netsec::kMailboxTypeCoE);
  v.allowType(netsec::kMailboxTypeFoE);
  return v;
}

void testLengthExceedsSm() {
  auto v = makeCoeFoeValidator(64);
  // 声明长度 100,SM 只有 64:长度字段 > SM - 6。
  auto frame = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x42), 100);
  CHECK(frame.size() < 100 + netsec::kMailboxHeaderSize);
  auto r = v.validate(frame.data(), frame.size());
  CHECK(r.verdict == netsec::Verdict::kReject);
  CHECK(r.reason == netsec::RejectReason::kLengthExceedsSm);
}

void testLengthExceedsReceived() {
  auto v = makeCoeFoeValidator(512);
  // 声明长度 50,实际负载只有 6 字节:长度字段 > 实际收到 - 6。
  auto frame = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x42), 50);
  auto r = v.validate(frame.data(), frame.size());
  CHECK(r.verdict == netsec::Verdict::kReject);
  CHECK(r.reason == netsec::RejectReason::kLengthExceedsReceived);
}

void testCounterZeroReserved() {
  auto v = makeCoeFoeValidator();
  auto frame = makeFrame(0, netsec::kMailboxTypeCoE, coePayload(3, 0x42));
  auto r = v.validate(frame.data(), frame.size());
  CHECK(r.verdict == netsec::Verdict::kReject);
  CHECK(r.reason == netsec::RejectReason::kCounterReserved);
}

void testRetransmitDropped() {
  auto v = makeCoeFoeValidator();
  auto frame = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x42));
  CHECK(v.validate(frame.data(), frame.size()).verdict ==
        netsec::Verdict::kAccept);
  // 相同计数器视为重传:丢弃但不计为错误。
  auto r = v.validate(frame.data(), frame.size());
  CHECK(r.verdict == netsec::Verdict::kDropDuplicate);
  CHECK(r.reason == netsec::RejectReason::kNone);
  CHECK(v.duplicateCount() == 1);
  CHECK(v.rejectCount() == 0);
}

void testSequenceGapLoggedOnly() {
  auto v = makeCoeFoeValidator();
  auto f1 = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x42));
  auto f3 = makeFrame(3, netsec::kMailboxTypeCoE, coePayload(3, 0x42));
  CHECK(v.validate(f1.data(), f1.size()).verdict == netsec::Verdict::kAccept);
  // 跳号(期望 2 收到 3):只记录不拒绝。
  CHECK(v.validate(f3.data(), f3.size()).verdict == netsec::Verdict::kAccept);
  CHECK(v.sequenceGapCount() == 1);
  // 计数器 7 之后回绕到 1 不算跳号。
  auto w = makeCoeFoeValidator();
  for (std::uint8_t c : {6, 7, 1, 2}) {
    auto f = makeFrame(c, netsec::kMailboxTypeCoE, coePayload(3, 0x42));
    CHECK(w.validate(f.data(), f.size()).verdict == netsec::Verdict::kAccept);
  }
  CHECK(w.sequenceGapCount() == 0);
}

void testTypeNotRegistered() {
  netsec::MailboxValidator v(512);
  v.allowType(netsec::kMailboxTypeCoE);  // 只登记 CoE
  auto frame = makeFrame(1, netsec::kMailboxTypeFoE, foeWriteRequest("a.bin"));
  auto r = v.validate(frame.data(), frame.size());
  CHECK(r.verdict == netsec::Verdict::kReject);
  CHECK(r.reason == netsec::RejectReason::kTypeNotAllowed);
}

void testFoePathTraversal() {
  const char* bad_names[] = {"../etc/passwd", "a/b", "a\\b", "..", ""};
  for (const char* name : bad_names) {
    auto v = makeCoeFoeValidator();
    auto frame = makeFrame(1, netsec::kMailboxTypeFoE, foeWriteRequest(name));
    auto r = v.validate(frame.data(), frame.size());
    CHECK(r.verdict == netsec::Verdict::kReject);
    CHECK(r.reason == netsec::RejectReason::kFoeNameTraversal ||
          r.reason == netsec::RejectReason::kFoeNameEmpty);
  }
  // 文件名内嵌 NUL 字节:拒绝。
  {
    auto v = makeCoeFoeValidator();
    auto payload = foeWriteRequest("a");
    payload.push_back(0x00);
    payload.push_back('b');
    auto frame = makeFrame(1, netsec::kMailboxTypeFoE, payload);
    auto r = v.validate(frame.data(), frame.size());
    CHECK(r.verdict == netsec::Verdict::kReject);
    CHECK(r.reason == netsec::RejectReason::kFoeNameTraversal);
  }
  // 合法文件名放行。
  auto v = makeCoeFoeValidator();
  auto ok = makeFrame(1, netsec::kMailboxTypeFoE, foeWriteRequest("firmware.bin"));
  CHECK(v.validate(ok.data(), ok.size()).verdict == netsec::Verdict::kAccept);
  // 读请求(opcode 1)不校验文件名。
  auto rrq = makeFrame(2, netsec::kMailboxTypeFoE,
                       {0x01, 0x00, 0x2E, 0x2E, 0x00, 0x00});
  CHECK(v.validate(rrq.data(), rrq.size()).verdict == netsec::Verdict::kAccept);
}

void testCoeToggleMustAlternate() {
  // 分段下载响应序列:初始化响应(scs=3)-> 段响应(scs=1,toggle 交替)。
  {
    auto v = makeCoeFoeValidator();
    auto init = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x60));
    CHECK(v.validate(init.data(), init.size()).verdict ==
          netsec::Verdict::kAccept);
    auto seg0 = makeFrame(2, netsec::kMailboxTypeCoE, coePayload(3, 0x20));
    CHECK(v.validate(seg0.data(), seg0.size()).verdict ==
          netsec::Verdict::kAccept);
    // toggle 未翻转(仍为 0):拒绝。
    auto seg0again = makeFrame(3, netsec::kMailboxTypeCoE, coePayload(3, 0x20));
    auto r = v.validate(seg0again.data(), seg0again.size());
    CHECK(r.verdict == netsec::Verdict::kReject);
    CHECK(r.reason == netsec::RejectReason::kCoeToggleMismatch);
  }
  // 正确交替:toggle 0 -> 1 -> 0 全部放行。
  {
    auto v = makeCoeFoeValidator();
    const std::uint8_t cmds[] = {0x60, 0x20, 0x30, 0x20};
    for (std::uint8_t i = 0; i < 4; ++i) {
      auto f = makeFrame(static_cast<std::uint8_t>(i + 1),
                         netsec::kMailboxTypeCoE, coePayload(3, cmds[i]));
      CHECK(v.validate(f.data(), f.size()).verdict == netsec::Verdict::kAccept);
    }
  }
  // 未初始化就收到后续段:拒绝。
  {
    auto v = makeCoeFoeValidator();
    auto seg = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x20));
    auto r = v.validate(seg.data(), seg.size());
    CHECK(r.verdict == netsec::Verdict::kReject);
    CHECK(r.reason == netsec::RejectReason::kCoeUnexpectedSegment);
  }
  // 加速传输(bit2=1)不进入段跟踪。
  {
    auto v = makeCoeFoeValidator();
    auto exp = makeFrame(1, netsec::kMailboxTypeCoE, coePayload(3, 0x42));
    CHECK(v.validate(exp.data(), exp.size()).verdict ==
          netsec::Verdict::kAccept);
  }
}

void testFrameTooShort() {
  auto v = makeCoeFoeValidator();
  const std::uint8_t raw[] = {0x01, 0x00, 0x00};
  auto r = v.validate(raw, sizeof(raw));
  CHECK(r.verdict == netsec::Verdict::kReject);
  CHECK(r.reason == netsec::RejectReason::kFrameTooShort);
}

}  // namespace

int main() {
  testLengthExceedsSm();
  testLengthExceedsReceived();
  testCounterZeroReserved();
  testRetransmitDropped();
  testSequenceGapLoggedOnly();
  testTypeNotRegistered();
  testFoePathTraversal();
  testCoeToggleMustAlternate();
  testFrameTooShort();
  if (g_failures != 0) {
    std::fprintf(stderr, "%d check(s) failed\n", g_failures);
    return 1;
  }
  std::printf("all mailbox validator tests passed\n");
  return 0;
}

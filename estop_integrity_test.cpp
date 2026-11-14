#include "estop_integrity.hpp"

#include <cstdio>
#include <cstdlib>

namespace {

using netsec::estop::BusCommand;
using netsec::estop::BusFrame;
using netsec::estop::EstopIntegrity;
using netsec::estop::ResetRequest;
using netsec::estop::ResetResult;
using netsec::estop::SecretKey;
using netsec::estop::StopReason;
using netsec::estop::TimePoint;
using namespace std::chrono_literals;

int failures = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,        \
                   __LINE__, #cond);                             \
      ++failures;                                                \
    }                                                            \
  } while (0)

SecretKey test_key() {
  SecretKey key{};
  for (std::size_t i = 0; i < key.size(); ++i) {
    key[i] = static_cast<std::uint8_t>(0xA5u ^ i);
  }
  return key;
}

// 被控设备:仅当安全门控允许时电机运转。
struct Device {
  bool motor_running = false;
  void update(const EstopIntegrity& estop) {
    motor_running = estop.safe_to_run();
  }
};

class BusMaster {
 public:
  explicit BusMaster(SecretKey key) : key_(key) {}
  BusFrame next(BusCommand command) {
    BusFrame frame;
    frame.sequence = ++sequence_;
    frame.command = command;
    frame.tag = netsec::estop::sign_frame(key_, frame.sequence, command);
    return frame;
  }
  std::uint64_t sequence() const { return sequence_; }

 private:
  SecretKey key_;
  std::uint64_t sequence_ = 0;
};

// 场景 1:急停伪造 —— 攻击者伪造"通道释放/屏蔽急停"报文,设备必须停机。
void test_estop_forgery() {
  const SecretKey key = test_key();
  EstopIntegrity estop(key);
  BusMaster master(key);
  Device device;
  const TimePoint t0 = TimePoint{1s};

  // 合法双通道按下 -> 停机。
  CHECK(estop.process_frame(master.next(BusCommand::kChannelAAsserted), t0));
  CHECK(estop.process_frame(master.next(BusCommand::kChannelBAsserted),
                            t0 + 5ms));
  device.update(estop);
  CHECK(!device.motor_running);
  CHECK(estop.stop_reason() == StopReason::kEstopLoop);

  // 攻击者伪造"通道A释放"(无密钥,标签错误)-> 拒绝并保持停机。
  BusFrame forged;
  forged.sequence = master.sequence() + 1;
  forged.command = BusCommand::kChannelAReleased;
  forged.tag = {};  // 伪造签名
  CHECK(!estop.process_frame(forged, t0 + 10ms));
  device.update(estop);
  CHECK(!device.motor_running);
  CHECK(estop.stop_reason() == StopReason::kSecurityViolation);

  // 攻击者伪造"屏蔽急停"报文 -> 拒绝,仍停机。
  BusFrame mask;
  mask.sequence = master.sequence() + 2;
  mask.command = BusCommand::kMaskEstop;
  mask.tag = netsec::estop::sign_frame(test_key(), mask.sequence,
                                       BusCommand::kMaskEstop);
  CHECK(!estop.process_frame(mask, t0 + 20ms));
  device.update(estop);
  CHECK(!device.motor_running);

  // 攻击者重放旧报文 -> 拒绝,仍停机。
  BusFrame replay;
  replay.sequence = 1;
  replay.command = BusCommand::kChannelAReleased;
  replay.tag =
      netsec::estop::sign_frame(key, replay.sequence, replay.command);
  CHECK(!estop.process_frame(replay, t0 + 30ms));
  device.update(estop);
  CHECK(!device.motor_running);
}

// 场景 2:复位伪造 —— 无现场确认或无有效签名的复位必须被拒绝。
void test_reset_forgery() {
  const SecretKey key = test_key();
  EstopIntegrity estop(key);
  BusMaster master(key);
  Device device;
  const TimePoint t0 = TimePoint{1s};

  CHECK(estop.process_frame(master.next(BusCommand::kChannelAAsserted), t0));
  CHECK(estop.process_frame(master.next(BusCommand::kChannelBAsserted),
                            t0 + 5ms));
  // 合法释放双通道,回路恢复,但仍处于闭锁停止。
  CHECK(estop.process_frame(master.next(BusCommand::kChannelAReleased),
                            t0 + 10ms));
  CHECK(estop.process_frame(master.next(BusCommand::kChannelBReleased),
                            t0 + 15ms));
  device.update(estop);
  CHECK(!device.motor_running);

  // 伪造复位:无现场确认、无签名。
  ResetRequest forged;
  forged.physical_confirmed = false;
  forged.nonce = estop.reset_nonce();
  forged.signature = {};
  CHECK(estop.request_reset(forged) ==
        ResetResult::kRejectedNoPhysicalConfirm);
  device.update(estop);
  CHECK(!device.motor_running);

  // 伪造复位:有现场确认但签名错误(攻击者无密钥)。
  ResetRequest bad_sig;
  bad_sig.physical_confirmed = true;
  bad_sig.nonce = estop.reset_nonce();
  bad_sig.signature = netsec::estop::sign_reset(test_key(), 9999);
  CHECK(estop.request_reset(bad_sig) ==
        ResetResult::kRejectedBadSignature);
  device.update(estop);
  CHECK(!device.motor_running);

  // 伪造复位:随机数不匹配。
  ResetRequest bad_nonce;
  bad_nonce.physical_confirmed = true;
  bad_nonce.nonce = estop.reset_nonce() + 1;
  bad_nonce.signature = netsec::estop::sign_reset(key, bad_nonce.nonce);
  CHECK(estop.request_reset(bad_nonce) == ResetResult::kRejectedBadNonce);
  device.update(estop);
  CHECK(!device.motor_running);
}

// 场景 3:单通道失效 —— 仅一个通道动作,差异超时后必须故障停机。
void test_single_channel_failure() {
  const SecretKey key = test_key();
  EstopIntegrity estop(key, 50ms);
  BusMaster master(key);
  Device device;
  const TimePoint t0 = TimePoint{1s};

  device.update(estop);
  CHECK(device.motor_running);  // 初始可运行

  // 仅通道 A 动作,通道 B 失效(不动作)。
  CHECK(estop.process_frame(master.next(BusCommand::kChannelAAsserted), t0));
  estop.tick(t0 + 60ms);  // 超过差异时间窗
  device.update(estop);
  CHECK(!device.motor_running);
  CHECK(estop.stop_reason() == StopReason::kDiscrepancy);

  // 差异故障期间复位必须被拒绝(回路不健康)。
  ResetRequest request;
  request.physical_confirmed = true;
  request.nonce = estop.reset_nonce();
  request.signature = netsec::estop::sign_reset(key, request.nonce);
  CHECK(estop.request_reset(request) == ResetResult::kRejectedLoopActive);
  device.update(estop);
  CHECK(!device.motor_running);
}

// 场景 4:合法复位 —— 双通道急停后,现场确认 + 安全签名复位成功。
void test_legitimate_reset() {
  const SecretKey key = test_key();
  EstopIntegrity estop(key);
  BusMaster master(key);
  Device device;
  const TimePoint t0 = TimePoint{1s};

  CHECK(estop.process_frame(master.next(BusCommand::kChannelAAsserted), t0));
  CHECK(estop.process_frame(master.next(BusCommand::kChannelBAsserted),
                            t0 + 5ms));
  device.update(estop);
  CHECK(!device.motor_running);

  // 急停回路未恢复时复位被拒绝。
  ResetRequest early;
  early.physical_confirmed = true;
  early.nonce = estop.reset_nonce();
  early.signature = netsec::estop::sign_reset(key, early.nonce);
  CHECK(estop.request_reset(early) == ResetResult::kRejectedLoopActive);

  // 双通道释放,回路恢复。
  CHECK(estop.process_frame(master.next(BusCommand::kChannelAReleased),
                            t0 + 10ms));
  CHECK(estop.process_frame(master.next(BusCommand::kChannelBReleased),
                            t0 + 15ms));
  device.update(estop);
  CHECK(!device.motor_running);  // 停止闭锁保持,等待复位

  // 合法复位:现场确认 + 有效安全签名。
  ResetRequest request;
  request.physical_confirmed = true;
  request.nonce = estop.reset_nonce();
  request.signature = netsec::estop::sign_reset(key, request.nonce);
  CHECK(estop.request_reset(request) == ResetResult::kAccepted);
  device.update(estop);
  CHECK(device.motor_running);
}

// 场景 5:硬接线优先 —— 硬接线急停不可被总线报文或复位覆盖。
void test_hardwired_priority() {
  const SecretKey key = test_key();
  EstopIntegrity estop(key);
  BusMaster master(key);
  Device device;
  const TimePoint t0 = TimePoint{1s};

  device.update(estop);
  CHECK(device.motor_running);

  // 硬接线急停动作 -> 立即停机。
  estop.set_hardwired_estop(true);
  device.update(estop);
  CHECK(!device.motor_running);
  CHECK(estop.stop_reason() == StopReason::kHardwired);

  // 总线"一切正常"报文不能覆盖硬接线停机。
  CHECK(estop.process_frame(master.next(BusCommand::kChannelAReleased), t0));
  CHECK(estop.process_frame(master.next(BusCommand::kChannelBReleased),
                            t0 + 5ms));
  device.update(estop);
  CHECK(!device.motor_running);
  CHECK(estop.stop_reason() == StopReason::kHardwired);

  // 硬接线动作期间,即使合法签名复位也被拒绝。
  ResetRequest request;
  request.physical_confirmed = true;
  request.nonce = estop.reset_nonce();
  request.signature = netsec::estop::sign_reset(key, request.nonce);
  CHECK(estop.request_reset(request) ==
        ResetResult::kRejectedHardwiredActive);
  device.update(estop);
  CHECK(!device.motor_running);

  // 硬接线释放后仍需授权复位才能恢复运行。
  estop.set_hardwired_estop(false);
  device.update(estop);
  CHECK(!device.motor_running);  // 闭锁保持
  request.nonce = estop.reset_nonce();
  request.signature = netsec::estop::sign_reset(key, request.nonce);
  CHECK(estop.request_reset(request) == ResetResult::kAccepted);
  device.update(estop);
  CHECK(device.motor_running);
}

}  // namespace

int main() {
  test_estop_forgery();
  test_reset_forgery();
  test_single_channel_failure();
  test_legitimate_reset();
  test_hardwired_priority();
  if (failures != 0) {
    std::fprintf(stderr, "%d check(s) failed\n", failures);
    return EXIT_FAILURE;
  }
  std::puts("all estop integrity scenarios passed");
  return EXIT_SUCCESS;
}

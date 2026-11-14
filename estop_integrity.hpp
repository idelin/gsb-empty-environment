#pragma once

#include <array>
#include <chrono>
#include <cstdint>

// 急停完整性组件:供 C++20 安全总线组件调用。
//
// 安全策略:
//  - 急停回路为双通道(A/B),两通道须在差异时间窗内一致,否则判定差异故障。
//  - 单通道失效(仅一侧动作、卡滞)按故障处理,设备停机并闭锁。
//  - 安全总线报文必须携带单调序号与 HMAC-SHA256 签名;伪造、重放、
//    屏蔽急停、总线清除急停一律拒绝并闭锁为安全停。
//  - 复位必须同时满足:现场确认(本地复位按钮)与有效安全签名,
//    且硬接线未动作、双通道均已释放。
//  - 硬接线急停优先级最高,任何总线报文与复位都无法覆盖。

namespace netsec::estop {

using Clock = std::chrono::steady_clock;
using TimePoint = Clock::time_point;
using SecretKey = std::array<std::uint8_t, 32>;
using HmacTag = std::array<std::uint8_t, 32>;

// 报文/复位签名原语(HMAC-SHA256)。
HmacTag hmac_sha256(const SecretKey& key, const void* data, std::size_t length);

enum class BusCommand : std::uint8_t {
  kChannelAAsserted = 1,
  kChannelAReleased = 2,
  kChannelBAsserted = 3,
  kChannelBReleased = 4,
  kMaskEstop = 5,   // 屏蔽急停:策略上永远拒绝
  kClearEstop = 6,  // 总线清除急停:不允许,仅授权复位可清除
};

struct BusFrame {
  std::uint64_t sequence = 0;
  BusCommand command = BusCommand::kChannelAReleased;
  HmacTag tag{};
};

enum class StopReason : std::uint8_t {
  kNone = 0,
  kEstopLoop,          // 双通道一致按下
  kDiscrepancy,        // 双通道差异超限
  kHardwired,          // 硬接线急停
  kSecurityViolation,  // 伪造 / 重放 / 屏蔽等安全违规
};

enum class ResetResult : std::uint8_t {
  kAccepted = 0,
  kRejectedNotStopped,        // 未处于停止状态
  kRejectedHardwiredActive,   // 硬接线仍动作
  kRejectedLoopActive,        // 急停回路仍动作或存在差异
  kRejectedNoPhysicalConfirm, // 缺少现场确认
  kRejectedBadNonce,          // 复位随机数不匹配
  kRejectedBadSignature,      // 安全签名无效(复位伪造)
};

struct ResetRequest {
  bool physical_confirmed = false;  // 现场确认:本地复位按钮硬信号
  std::uint64_t nonce = 0;          // 必须匹配组件当前颁发的随机数
  HmacTag signature{};              // HMAC(key, "RESET" || nonce)
};

// 为授权总线主站生成合法帧签名(测试与对端实现共用)。
HmacTag sign_frame(const SecretKey& key, std::uint64_t sequence, BusCommand command);
// 为授权复位生成安全签名。
HmacTag sign_reset(const SecretKey& key, std::uint64_t nonce);

class EstopIntegrity {
 public:
  explicit EstopIntegrity(SecretKey key,
                          std::chrono::milliseconds discrepancy_window =
                              std::chrono::milliseconds{50});

  // 硬接线急停输入,优先级最高。
  void set_hardwired_estop(bool active);

  // 处理安全总线报文;返回是否被接受。伪造/重放/屏蔽一律拒绝并闭锁停机。
  bool process_frame(const BusFrame& frame, TimePoint now);

  // 推进内部时钟,用于差异超时判定(无新报文时也要调用)。
  void tick(TimePoint now);

  // 当前复位随机数;每次成功复位或启动时更新。
  std::uint64_t reset_nonce() const { return reset_nonce_; }

  // 请求复位:需现场确认 + 安全签名 + 回路恢复正常。
  ResetResult request_reset(const ResetRequest& request);

  // 设备安全门控:仅当无任何停止条件时才允许运动。
  bool safe_to_run() const { return stop_reason_ == StopReason::kNone; }
  StopReason stop_reason() const { return stop_reason_; }

 private:
  void latch_stop(StopReason reason);
  void evaluate_discrepancy(TimePoint now);
  bool loop_healthy() const;

  SecretKey key_;
  std::chrono::milliseconds discrepancy_window_;

  bool channel_a_ = false;  // true = 急停按下
  bool channel_b_ = false;
  bool discrepancy_pending_ = false;
  TimePoint discrepancy_since_{};

  bool hardwired_ = false;
  StopReason stop_reason_ = StopReason::kNone;
  std::uint64_t last_sequence_ = 0;
  std::uint64_t reset_nonce_ = 1;
};

}  // namespace netsec::estop

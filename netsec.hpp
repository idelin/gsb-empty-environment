#pragma once

#include <array>
#include <cstdint>
#include <optional>
#include <set>
#include <span>
#include <string>
#include <vector>

namespace netsec {

// 48 位 MAC 地址（BSSID / 站点地址）。
struct MacAddress {
  std::array<std::uint8_t, 6> bytes{};

  friend bool operator==(const MacAddress&, const MacAddress&) = default;
  friend bool operator<(const MacAddress& lhs, const MacAddress& rhs) {
    return lhs.bytes < rhs.bytes;
  }
  std::string to_string() const;
  static MacAddress from_string(const std::string& text);
};

// 需要保护的关键管理帧子类型。
enum class FrameType : std::uint8_t {
  Deauthentication,
  Disassociation,
  SaQueryRequest,
  SaQueryResponse,
  Action,
};

// 802.11 原因码（子集）。
enum class ReasonCode : std::uint16_t {
  Unspecified = 1,
  PreviousAuthNoLongerValid = 2,
  DeauthenticatedLeaving = 3,
  DisassociatedInactivity = 4,
  DisassociatedApBusy = 5,
  Class2FromNonAuth = 6,
  Class3FromNonAssoc = 7,
  DisassociatedLeaving = 8,
  NotAssociated = 9,
};

// 管理帧。受保护帧携带单调序列号与截断 HMAC-SHA-256 认证标签。
struct ManagementFrame {
  FrameType type = FrameType::Action;
  MacAddress source;       // 发送方（应为本关联的 AP 或对端 STA）
  MacAddress destination;
  ReasonCode reason = ReasonCode::Unspecified;
  std::uint64_t sequence = 0;  // 单调递增序列号（防回退/重放）
  bool protected_frame = false;
  std::array<std::uint8_t, 16> mic{};  // 截断的消息认证码
};

// 入站帧裁决结果。
enum class FrameVerdict {
  Accepted,                  // 通过全部校验，允许生效
  DroppedUnprotected,        // 未携带保护标记，直接丢弃
  DroppedSourceMismatch,     // 来源不属于当前关联（伪造接入点）
  DroppedSequenceRollback,   // 序列号回退/重放
  DroppedAuthenticationFailed,  // 认证标签校验失败
  DroppedReasonNotPermitted, // 断开原因码不在允许集合内
};

const char* to_string(FrameVerdict verdict);

// 受保护关联：对关键管理帧提供双向认证与防重放。
// 出站帧由 protect_outbound() 附加序列号与认证标签；
// 入站帧由 process_inbound() 校验来源、序列号与认证标签。
class ProtectedAssociation {
public:
  // allowed_disconnect_reasons：允许触发本地断开的认证帧原因码集合。
  ProtectedAssociation(MacAddress self, MacAddress ap,
                       std::span<const std::uint8_t> shared_key,
                       std::set<ReasonCode> allowed_disconnect_reasons);

  // 生成受保护的出站管理帧（双向认证的“出”方向）。
  ManagementFrame protect_outbound(FrameType type, ReasonCode reason);

  // 校验入站管理帧；通过时更新接收序列号。
  FrameVerdict process_inbound(const ManagementFrame& frame);

  // 认证帧是否允许触发本地断开（原因码受限）。
  bool disconnect_permitted(ReasonCode reason) const;

  const MacAddress& associated_ap() const { return ap_; }
  std::uint64_t tx_sequence() const { return tx_sequence_; }
  std::uint64_t rx_sequence() const { return rx_sequence_; }

  // 计算帧认证标签（供对端/测试构造合法帧）。
  std::array<std::uint8_t, 16> compute_mic(const ManagementFrame& frame) const;

private:
  MacAddress self_;
  MacAddress ap_;
  std::vector<std::uint8_t> key_;
  std::uint64_t tx_sequence_ = 0;
  std::uint64_t rx_sequence_ = 0;
  bool rx_initialized_ = false;
  std::set<ReasonCode> allowed_disconnect_reasons_;
};

// 重连策略：限制重连目标与节奏，防止被踢到伪造接入点。
struct ReconnectPolicy {
  std::set<MacAddress> allowed_bssids;   // 仅允许重连到已知 BSSID
  unsigned max_attempts = 3;             // 单次掉线允许的最大重连尝试
  unsigned backoff_ms = 100;             // 每次尝试的退避基数（指数增长）
};

enum class ReconnectDecision {
  Permitted,            // 允许向该 BSSID 发起重连
  DeniedUnknownBssid,   // 目标不在允许列表（疑似伪造接入点）
  DeniedAttemptsExhausted,  // 重连次数耗尽
};

const char* to_string(ReconnectDecision decision);

// 重连控制器：按策略裁决重连请求并施加退避。
class ReconnectController {
public:
  explicit ReconnectController(ReconnectPolicy policy);

  // 请求重连到指定 BSSID；返回裁决与建议等待毫秒数。
  std::pair<ReconnectDecision, unsigned> request(const MacAddress& bssid);

  // 成功重连后复位尝试计数。
  void reset();

  unsigned attempts() const { return attempts_; }

private:
  ReconnectPolicy policy_;
  unsigned attempts_ = 0;
};

}  // namespace netsec

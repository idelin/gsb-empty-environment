#include "netsec.hpp"

#include <cstdlib>
#include <iostream>
#include <string_view>

namespace {

using netsec::FrameType;
using netsec::FrameVerdict;
using netsec::MacAddress;
using netsec::ManagementFrame;
using netsec::ProtectedAssociation;
using netsec::ReasonCode;
using netsec::ReconnectController;
using netsec::ReconnectDecision;
using netsec::ReconnectPolicy;

int failures = 0;

void check(bool condition, std::string_view name) {
  if (condition) {
    std::cout << "PASS  " << name << '\n';
  } else {
    std::cout << "FAIL  " << name << '\n';
    ++failures;
  }
}

const MacAddress kClientMac = MacAddress::from_string("02:00:00:00:00:01");
const MacAddress kApMac = MacAddress::from_string("02:00:00:00:00:aa");
const MacAddress kRogueApMac = MacAddress::from_string("66:65:76:69:6c:01");
const std::string_view kKey = "pairwise-master-key-demo";

std::span<const std::uint8_t> key_bytes() {
  return {reinterpret_cast<const std::uint8_t*>(kKey.data()), kKey.size()};
}

ProtectedAssociation make_client() {
  return ProtectedAssociation(
      kClientMac, kApMac, key_bytes(),
      {ReasonCode::DeauthenticatedLeaving, ReasonCode::DisassociatedInactivity});
}

ProtectedAssociation make_ap() {
  return ProtectedAssociation(
      kApMac, kClientMac, key_bytes(),
      {ReasonCode::DeauthenticatedLeaving, ReasonCode::DisassociatedInactivity,
       ReasonCode::DisassociatedLeaving});
}

// 模拟客户端：仅当帧被接受且为断开类帧时才掉线。
struct Client {
  ProtectedAssociation assoc;
  bool connected = true;

  void on_frame(const ManagementFrame& frame) {
    if (assoc.process_inbound(frame) != FrameVerdict::Accepted) return;
    if (frame.type == FrameType::Deauthentication ||
        frame.type == FrameType::Disassociation)
      connected = false;
  }
};

// 1) 去认证伪造：未受保护的 deauth（即使源 MAC 仿冒正确）必须被丢弃。
void test_deauth_forgery() {
  Client client{make_client(), true};
  ManagementFrame forged;
  forged.type = FrameType::Deauthentication;
  forged.source = kApMac;  // 攻击者仿冒 AP 地址
  forged.destination = kClientMac;
  forged.reason = ReasonCode::DeauthenticatedLeaving;
  forged.protected_frame = false;
  check(client.assoc.process_inbound(forged) == FrameVerdict::DroppedUnprotected,
        "forged unprotected deauth is dropped");

  // 攻击者加上保护标记，但没有密钥，认证标签无效。
  forged.protected_frame = true;
  forged.sequence = 1;
  check(client.assoc.process_inbound(forged) ==
            FrameVerdict::DroppedAuthenticationFailed,
        "forged protected deauth without key is dropped");
  check(client.connected, "client survives deauth forgery");
}

// 2) 序列回退：重放旧的合法帧必须被丢弃。
void test_sequence_rollback() {
  Client client{make_client(), true};
  auto ap = make_ap();
  const auto first = ap.protect_outbound(FrameType::Action, ReasonCode::Unspecified);
  const auto second = ap.protect_outbound(FrameType::Action, ReasonCode::Unspecified);
  check(client.assoc.process_inbound(second) == FrameVerdict::Accepted,
        "fresh frame accepted");
  check(client.assoc.process_inbound(first) ==
            FrameVerdict::DroppedSequenceRollback,
        "replayed older frame dropped");
  check(client.assoc.process_inbound(second) ==
            FrameVerdict::DroppedSequenceRollback,
        "replayed same frame dropped");

  // 攻击者重放合法的去认证帧也无法踢掉客户端。
  auto ap2 = make_ap();
  const auto deauth =
      ap2.protect_outbound(FrameType::Deauthentication, ReasonCode::DeauthenticatedLeaving);
  Client client2{make_client(), true};
  check(client2.assoc.process_inbound(deauth) == FrameVerdict::Accepted,
        "first legit deauth accepted");
  client2.connected = true;  // 假设上层重新拉起
  Client client3{make_client(), true};
  client3.assoc.process_inbound(ap2.protect_outbound(FrameType::Action,
                                                     ReasonCode::Unspecified));
  check(client3.assoc.process_inbound(deauth) ==
            FrameVerdict::DroppedSequenceRollback,
        "replayed deauth after newer frame dropped");
  check(client3.connected, "client survives replayed deauth");
}

// 3) 伪造接入点：来源不属于当前关联的帧必须被丢弃。
void test_rogue_ap_source() {
  Client client{make_client(), true};
  // 伪造 AP 甚至用相同密钥结构自签，但源地址不是关联的 AP。
  ProtectedAssociation rogue(kRogueApMac, kClientMac, key_bytes(),
                             {ReasonCode::DeauthenticatedLeaving});
  const auto frame =
      rogue.protect_outbound(FrameType::Deauthentication, ReasonCode::DeauthenticatedLeaving);
  check(client.assoc.process_inbound(frame) == FrameVerdict::DroppedSourceMismatch,
        "frame from rogue AP dropped");
  check(client.connected, "client survives rogue AP deauth");
}

// 4) 合法管理帧：正确来源、序列与认证标签的帧被接受；原因码受限。
void test_legitimate_management() {
  Client client{make_client(), true};
  auto ap = make_ap();
  const auto deauth =
      ap.protect_outbound(FrameType::Deauthentication, ReasonCode::DeauthenticatedLeaving);
  check(client.assoc.process_inbound(deauth) == FrameVerdict::Accepted,
        "legitimate protected deauth accepted");

  // 双向认证：客户端出站帧同样被 AP 侧接受。
  auto outbound =
      client.assoc.protect_outbound(FrameType::Disassociation, ReasonCode::DisassociatedLeaving);
  auto ap_verify = make_ap();
  check(ap_verify.process_inbound(outbound) == FrameVerdict::Accepted,
        "client-originated frame authenticated by AP");

  // 原因码不在允许集合内的断开帧被丢弃。
  Client client2{make_client(), true};
  auto ap2 = make_ap();
  const auto odd =
      ap2.protect_outbound(FrameType::Deauthentication, ReasonCode::Class2FromNonAuth);
  check(client2.assoc.process_inbound(odd) ==
            FrameVerdict::DroppedReasonNotPermitted,
        "deauth with restricted reason dropped");
  check(client2.connected, "client survives restricted-reason deauth");
}

// 5) 重连策略：仅允许已知 BSSID，限制尝试次数并指数退避。
void test_reconnect_policy() {
  ReconnectController controller(
      ReconnectPolicy{{kApMac}, /*max_attempts=*/3, /*backoff_ms=*/100});

  const auto [rogue_decision, rogue_wait] = controller.request(kRogueApMac);
  check(rogue_decision == ReconnectDecision::DeniedUnknownBssid,
        "reconnect to rogue AP denied");
  check(controller.attempts() == 0, "denied attempt not counted");

  const auto [d1, w1] = controller.request(kApMac);
  const auto [d2, w2] = controller.request(kApMac);
  const auto [d3, w3] = controller.request(kApMac);
  check(d1 == ReconnectDecision::Permitted && w1 == 100, "first reconnect permitted");
  check(d2 == ReconnectDecision::Permitted && w2 == 200, "backoff grows");
  check(d3 == ReconnectDecision::Permitted && w3 == 400, "backoff doubles again");
  const auto [d4, w4] = controller.request(kApMac);
  check(d4 == ReconnectDecision::DeniedAttemptsExhausted,
        "attempts exhausted after limit");

  controller.reset();
  const auto [d5, w5] = controller.request(kApMac);
  check(d5 == ReconnectDecision::Permitted && w5 == 100,
        "reset restores reconnect budget");
}

}  // namespace

int main() {
  test_deauth_forgery();
  test_sequence_rollback();
  test_rogue_ap_source();
  test_legitimate_management();
  test_reconnect_policy();
  if (failures != 0) {
    std::cerr << failures << " check(s) failed\n";
    return EXIT_FAILURE;
  }
  std::cout << "all checks passed\n";
  return EXIT_SUCCESS;
}

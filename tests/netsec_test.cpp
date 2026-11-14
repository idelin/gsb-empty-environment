#include "netsec.hpp"

#include <cstdio>
#include <string>

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& name) {
  ++g_checks;
  if (!condition) {
    ++g_failures;
    std::fprintf(stderr, "FAIL: %s\n", name.c_str());
  }
}

using netsec::AreaId;
using netsec::Ipv4Addr;
using netsec::RouterId;

constexpr RouterId kSelf = 0x0a000001;     // 10.0.0.1
constexpr RouterId kPeer = 0x0a000002;     // 10.0.0.2 (configured vlink peer)
constexpr RouterId kAttacker = 0x0a000063; // 10.0.0.99
constexpr RouterId kHostA = 0x0a00000a;
constexpr RouterId kHostB = 0x0a00000b;
constexpr Ipv4Addr kPeerSource = 0xc0000202;   // 192.0.2.2
constexpr Ipv4Addr kForgedSource = 0xc0000263; // 192.0.2.99
constexpr AreaId kTransitArea = 1;
constexpr AreaId kLocalArea = 0;

netsec::Lsa make_router_lsa(RouterId advertiser, std::uint32_t sequence,
                            std::vector<netsec::RouterLink> links,
                            std::uint16_t age = 30) {
  netsec::Lsa lsa;
  lsa.header.age = age;
  lsa.header.type = netsec::LsaType::kRouter;
  lsa.header.link_state_id = advertiser;
  lsa.header.advertising_router = advertiser;
  lsa.header.sequence = sequence;
  lsa.links = std::move(links);
  netsec::finalize_lsa(lsa);
  return lsa;
}

netsec::VirtualLinkConfig make_config() {
  netsec::VirtualLinkConfig config;
  config.local_router_id = kSelf;
  config.peer_router_id = kPeer;
  config.transit_area = kTransitArea;
  config.peer_source = kPeerSource;
  return config;
}

// 1. 伪端点:伪造 router-id / 区域 / 源地址的 Hello 必须被拒绝。
void test_fake_endpoint() {
  const netsec::VirtualLinkConfig config = make_config();

  const netsec::HelloPacket forged_id{kAttacker, kTransitArea, kPeerSource};
  check(netsec::validate_hello(config, forged_id) ==
            netsec::HelloVerdict::kBadEndpoint,
        "fake endpoint: forged router-id rejected");

  const netsec::HelloPacket wrong_area{kPeer, 2, kPeerSource};
  check(netsec::validate_hello(config, wrong_area) ==
            netsec::HelloVerdict::kBadArea,
        "fake endpoint: wrong transit area rejected");

  // 攻击者盗用合法 peer 的 router-id,但源地址对不上。
  const netsec::HelloPacket forged_source{kPeer, kTransitArea, kForgedSource};
  check(netsec::validate_hello(config, forged_source) ==
            netsec::HelloVerdict::kBadSource,
        "fake endpoint: spoofed router-id with wrong source rejected");
}

// 2. 跨区域 LSA:非本区域 LSA 与未信任的 AS 外部 LSA 不得进入 SPF。
void test_cross_area_lsa() {
  netsec::AreaPolicy policy;
  policy.local_area = kLocalArea;
  policy.trusted_external_advertisers = {kPeer};

  netsec::Lsa cross_area =
      make_router_lsa(kAttacker, netsec::kInitialSequence,
                      {{kSelf, 1}, {kHostA, 1}});
  check(netsec::spf_filter(policy, /*arrival_area=*/1, cross_area) ==
            netsec::SpfVerdict::kWrongArea,
        "cross-area: LSA from foreign area rejected");

  netsec::Lsa external = make_router_lsa(kAttacker, netsec::kInitialSequence,
                                         {{kHostA, 1}});
  external.header.type = netsec::LsaType::kAsExternal;
  netsec::finalize_lsa(external);
  check(netsec::spf_filter(policy, kLocalArea, external) ==
            netsec::SpfVerdict::kUntrustedExternal,
        "cross-area: untrusted AS-external rejected");

  netsec::Lsa trusted_external = external;
  trusted_external.header.advertising_router = kPeer;
  trusted_external.header.link_state_id = kPeer;
  netsec::finalize_lsa(trusted_external);
  check(netsec::spf_filter(policy, kLocalArea, trusted_external) ==
            netsec::SpfVerdict::kAccept,
        "cross-area: trusted AS-external accepted");

  // 攻击者的跨区域 LSA 即使进了 LSDB,也不能影响路由表。
  netsec::LinkStateDatabase db;
  check(db.install(make_router_lsa(kSelf, netsec::kInitialSequence, {})) ==
            netsec::InstallResult::kInstalled,
        "cross-area: self LSA installed");
  check(db.install(cross_area) == netsec::InstallResult::kInstalled,
        "cross-area: attacker LSA stored in LSDB");
  const auto routes =
      netsec::run_spf(policy, /*arrival_area=*/1, db, kSelf);
  check(routes.count(kHostA) == 0 && routes.count(kAttacker) == 0,
        "cross-area: attacker route not injected into routing table");
}

// 3. 序列回退:重复与回退 LSA 丢弃,只有更新的序列号能替换。
void test_sequence_rollback() {
  netsec::LinkStateDatabase db;
  const std::uint32_t base = netsec::kInitialSequence + 4;

  check(db.install(make_router_lsa(kPeer, base, {{kHostA, 1}})) ==
            netsec::InstallResult::kInstalled,
        "rollback: first copy installed");
  check(db.install(make_router_lsa(kPeer, base, {{kHostA, 1}})) ==
            netsec::InstallResult::kDuplicate,
        "rollback: identical sequence dropped as duplicate");
  check(db.install(make_router_lsa(kPeer, base - 1, {{kHostB, 1}})) ==
            netsec::InstallResult::kRollback,
        "rollback: older sequence dropped");
  check(db.install(make_router_lsa(kPeer, base + 1, {{kHostB, 1}})) ==
            netsec::InstallResult::kUpdated,
        "rollback: newer sequence replaces stored copy");

  const netsec::Lsa* stored = db.lookup(netsec::LsaKey{
      netsec::LsaType::kRouter, kPeer, kPeer});
  check(stored != nullptr && stored->header.sequence == base + 1 &&
            stored->links.size() == 1 &&
            stored->links.front().neighbor == kHostB,
        "rollback: LSDB holds only the newest LSA");

  // 保留序列号 0x80000000 非法。
  netsec::Lsa reserved = make_router_lsa(kAttacker, 0x80000000u, {});
  netsec::LsaVerdict verdict = netsec::LsaVerdict::kAccept;
  check(db.install(reserved, &verdict) == netsec::InstallResult::kRejected &&
            verdict == netsec::LsaVerdict::kBadSequence,
        "rollback: reserved sequence number rejected");
}

// 4. 合法虚链路:配置匹配的 Hello 与 LSA 被接受并产生路由。
void test_legitimate_virtual_link() {
  const netsec::VirtualLinkConfig config = make_config();
  const netsec::HelloPacket hello{kPeer, kTransitArea, kPeerSource};
  check(netsec::validate_hello(config, hello) ==
            netsec::HelloVerdict::kAccept,
        "vlink: configured peer hello accepted");

  netsec::AreaPolicy policy;
  policy.local_area = kLocalArea;
  policy.trusted_external_advertisers = {kPeer};

  netsec::LinkStateDatabase db;
  check(db.install(make_router_lsa(
            kSelf, netsec::kInitialSequence, {{kPeer, 10}})) ==
            netsec::InstallResult::kInstalled,
        "vlink: self LSA installed");
  check(db.install(make_router_lsa(
            kPeer, netsec::kInitialSequence,
            {{kSelf, 10}, {kHostA, 5}})) == netsec::InstallResult::kInstalled,
        "vlink: peer LSA installed");

  const auto routes = netsec::run_spf(policy, kLocalArea, db, kSelf);
  check(routes.count(kPeer) == 1 && routes.at(kPeer) == 10,
        "vlink: peer reachable with correct metric");
  check(routes.count(kHostA) == 1 && routes.at(kHostA) == 15,
        "vlink: two-hop route installed via virtual link");
}

// 5. 拓扑变化:更高序列号更新路由;篡改校验和与非法老化被拒绝。
void test_topology_change() {
  netsec::AreaPolicy policy;
  policy.local_area = kLocalArea;

  netsec::LinkStateDatabase db;
  db.install(make_router_lsa(kSelf, netsec::kInitialSequence,
                             {{kPeer, 10}}));
  db.install(make_router_lsa(kPeer, netsec::kInitialSequence,
                             {{kSelf, 10}, {kHostA, 5}}));

  auto routes = netsec::run_spf(policy, kLocalArea, db, kSelf);
  check(routes.count(kHostA) == 1 && routes.count(kHostB) == 0,
        "topology: initial route to A only");

  // 拓扑变化:peer 撤销到 A 的链路,改为通告 B。
  check(db.install(make_router_lsa(kPeer, netsec::kInitialSequence + 1,
                                   {{kSelf, 10}, {kHostB, 7}})) ==
            netsec::InstallResult::kUpdated,
        "topology: newer LSA replaces old");
  routes = netsec::run_spf(policy, kLocalArea, db, kSelf);
  check(routes.count(kHostA) == 0, "topology: withdrawn route removed");
  check(routes.count(kHostB) == 1 && routes.at(kHostB) == 17,
        "topology: new route to B installed");

  // 篡改一个字节后校验和失效。
  netsec::Lsa tampered = make_router_lsa(kPeer, netsec::kInitialSequence + 2,
                                         {{kSelf, 10}, {kAttacker, 1}});
  tampered.links.front().metric = 1; // 改内容不重算校验和
  netsec::LsaVerdict verdict = netsec::LsaVerdict::kAccept;
  check(db.install(tampered, &verdict) == netsec::InstallResult::kRejected &&
            verdict == netsec::LsaVerdict::kBadChecksum,
        "topology: tampered LSA fails checksum");

  // 非法老化值。
  netsec::Lsa aged = make_router_lsa(kPeer, netsec::kInitialSequence + 2,
                                     {{kSelf, 10}});
  aged.header.age = netsec::kMaxAge + 1;
  netsec::finalize_lsa(aged);
  check(db.install(aged, &verdict) == netsec::InstallResult::kRejected &&
            verdict == netsec::LsaVerdict::kBadAge,
        "topology: age beyond MaxAge rejected");

  routes = netsec::run_spf(policy, kLocalArea, db, kSelf);
  check(routes.count(kAttacker) == 0,
        "topology: rejected LSAs never reach the routing table");
}

} // namespace

int main() {
  test_fake_endpoint();
  test_cross_area_lsa();
  test_sequence_rollback();
  test_legitimate_virtual_link();
  test_topology_change();

  std::printf("%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}

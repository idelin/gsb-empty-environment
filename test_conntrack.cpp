#include <cstdio>
#include <cstdlib>

#include "netsec.hpp"

namespace {

int g_failures = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,         \
                   __LINE__, #cond);                             \
      ++g_failures;                                              \
    }                                                            \
  } while (0)

using netsec::ConnState;
using netsec::ConntrackTable;
using netsec::FiveTuple;
using netsec::FlowKey;
using netsec::NatMapping;
using netsec::PacketView;
using netsec::Protocol;

FiveTuple tuple(std::uint32_t src_ip, std::uint32_t dst_ip,
                std::uint16_t src_port, std::uint16_t dst_port,
                Protocol proto = Protocol::TCP) {
  return FiveTuple{src_ip, dst_ip, src_port, dst_port, proto};
}

// 场景 1:五元组碰撞。bucket_count=1 强制所有条目落在同一桶,
// 验证按完整键比较后不同租户/不同五元组的映射互不串扰。
void test_tuple_collision() {
  ConntrackTable table(1);
  const std::uint32_t tenant_a = 1001;
  const std::uint32_t tenant_b = 2002;

  FlowKey a1{1, tenant_a, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  FlowKey a2{1, tenant_a, tuple(0x0a000001, 0x0a000002, 1001, 80)};
  FlowKey b1{1, tenant_b, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  NatMapping ma{0xcb007101, 40001};
  NatMapping ma2{0xcb007101, 40002};
  NatMapping mb{0xcb007102, 50001};

  auto* ea1 = table.insert(a1, ma, 0);
  auto* ea2 = table.insert(a2, ma2, 0);
  auto* eb1 = table.insert(b1, mb, 0);
  CHECK(ea1 != ea2 && ea1 != eb1 && ea2 != eb1);

  CHECK(table.find(a1)->mapping() == ma);
  CHECK(table.find(a2)->mapping() == ma2);
  CHECK(table.find(b1)->mapping() == mb);
  // 同五元组不同租户:哈希相同,完整键不同,不得命中他租户条目。
  CHECK(table.find(b1) != table.find(a1));
  CHECK(table.find(b1)->mapping().xlated_ip == mb.xlated_ip);
  // 不存在的组合不得误命中。
  FlowKey ghost{1, tenant_b, tuple(0x0a000001, 0x0a000002, 9999, 80)};
  CHECK(table.find(ghost) == nullptr);
}

// 场景 2:zone 混用。同租户同五元组、不同 zone 必须是独立条目。
void test_zone_isolation() {
  ConntrackTable table(1);
  const std::uint32_t tenant = 1001;
  FlowKey z1{1, tenant, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  FlowKey z2{2, tenant, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  NatMapping m1{0xcb007101, 40001};
  NatMapping m2{0xcb007102, 40002};

  auto* e1 = table.insert(z1, m1, 0);
  auto* e2 = table.insert(z2, m2, 0);
  CHECK(e1 != e2);
  CHECK(table.find(z1)->mapping() == m1);
  CHECK(table.find(z2)->mapping() == m2);
  // zone 3 无此流,不得串到 zone 1/2 的映射。
  FlowKey z3{3, tenant, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  CHECK(table.find(z3) == nullptr);
}

// 场景 3:超时复用。过期条目经状态机进入 DYING,引用计数为 0 才可
// 复用/回收;引用未清零时不得复用也不得回收。
void test_timeout_reuse() {
  ConntrackTable table(1);
  FlowKey flow{1, 1001, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  NatMapping m1{0xcb007101, 40001};
  NatMapping m2{0xcb007109, 40009};

  auto* e1 = table.insert(flow, m1, 0);
  CHECK(e1->state() == ConnState::NEW);

  // 未超时前同流 insert 返回活跃条目本身,不新建。
  CHECK(table.insert(flow, m2, 10) == e1);
  CHECK(table.size() == 1);

  // NEW 超时(30s)后迁移到 DYING。
  table.tick(ConntrackTable::kNewTimeout);
  CHECK(e1->state() == ConnState::DYING);

  // 引用计数为 0:同流新连接复用该槽位,映射更新,状态回到 NEW。
  auto* e2 = table.insert(flow, m2, 40);
  CHECK(e2 == e1);
  CHECK(e2->state() == ConnState::NEW);
  CHECK(e2->mapping() == m2);
  CHECK(table.size() == 1);

  // 再次过期,但这次持有引用:不得回收、不得复用。
  table.acquire(*e2);
  table.tick(40 + ConntrackTable::kNewTimeout);
  CHECK(e2->state() == ConnState::DYING);
  CHECK(table.reclaim() == 0);
  CHECK(table.size() == 1);
  NatMapping m3{0xcb007103, 40003};
  auto* e3 = table.insert(flow, m3, 80);
  CHECK(e3 != e2);  // 引用未清零,另建条目,两条目按键中状态区分。
  CHECK(e3->state() == ConnState::NEW);
  CHECK(table.size() == 2);

  // 释放引用后可回收 DYING 旧条目;活跃条目不受影响。
  table.release(*e2);
  CHECK(table.reclaim() == 1);
  CHECK(table.size() == 1);
  CHECK(table.find(flow) == e3);
  CHECK(table.find(flow)->mapping() == m3);
}

// 场景 4:分片关联。首片建立关联,非首片(无端口)按 ip_id 命中同一条目;
// 其他租户相同 ip_id 不得关联。
void test_fragment_association() {
  ConntrackTable table(1);
  FlowKey flow{1, 1001, tuple(0x0a000001, 0x0a000002, 1000, 80, Protocol::UDP)};
  NatMapping m{0xcb007101, 40001};
  auto* e = table.insert(flow, m, 0);

  PacketView first{0x0a000001, 0x0a000002, 1000, 80, Protocol::UDP,
                   7777,       true,        true,  false};
  CHECK(table.process_packet(flow, first, 1) == e);

  // 非首片:无端口,同 (src,dst,proto,ip_id),命中同一条目。
  FlowKey no_ports{1, 1001, tuple(0x0a000001, 0x0a000002, 0, 0, Protocol::UDP)};
  PacketView tail{0x0a000001, 0x0a000002, 0, 0, Protocol::UDP,
                  7777,       false,       true, false};
  auto* hit = table.process_packet(no_ports, tail, 2);
  CHECK(hit == e);
  CHECK(hit->mapping() == m);

  // 另一租户相同 ip_id 的分片不得关联到该条目。
  FlowKey other_tenant{1, 2002, tuple(0x0a000001, 0x0a000002, 0, 0,
                                      Protocol::UDP)};
  CHECK(table.process_packet(other_tenant, tail, 2) == nullptr);
  // 另一 zone 同样不得关联。
  FlowKey other_zone{2, 1001, tuple(0x0a000001, 0x0a000002, 0, 0,
                                    Protocol::UDP)};
  CHECK(table.process_packet(other_zone, tail, 2) == nullptr);

  // 分片关联超时后失效。
  table.tick(1 + ConntrackTable::kFragTimeout);
  CHECK(table.process_packet(no_ports, tail, 40) == nullptr);
}

// 场景 5:合法连接全生命周期 + ICMP 差错关联,验证映射不串租户。
void test_legit_connection() {
  ConntrackTable table(1);
  const std::uint32_t tenant_a = 1001;
  const std::uint32_t tenant_b = 2002;
  FlowKey flow{1, tenant_a, tuple(0x0a000001, 0x0a000002, 1000, 80)};
  NatMapping m{0xcb007101, 40001};
  auto* e = table.insert(flow, m, 0);

  PacketView syn{0x0a000001, 0x0a000002, 1000, 80, Protocol::TCP,
                 100,         true,        false, false};
  CHECK(table.process_packet(flow, syn, 1) == e);
  CHECK(e->state() == ConnState::NEW);

  // 反向报文确认,NEW -> ESTABLISHED,映射保持不变。
  FlowKey rev{1, tenant_a, tuple(0x0a000002, 0x0a000001, 80, 1000)};
  PacketView synack{0x0a000002, 0x0a000001, 80, 1000, Protocol::TCP,
                    200,        true,        false, false};
  CHECK(table.process_packet(rev, synack, 2) == e);
  CHECK(e->state() == ConnState::ESTABLISHED);
  CHECK(e->mapping() == m);

  // ICMP 差错:内嵌原始五元组关联回本租户条目。
  CHECK(table.process_icmp_error(1, tenant_a,
                                 tuple(0x0a000001, 0x0a000002, 1000, 80),
                                 3) == e);
  // 反向内嵌(差错来自对端方向)同样关联。
  CHECK(table.process_icmp_error(1, tenant_a,
                                 tuple(0x0a000002, 0x0a000001, 80, 1000),
                                 3) == e);
  // 其他租户/其他 zone 的相同内嵌五元组不得关联。
  CHECK(table.process_icmp_error(1, tenant_b,
                                 tuple(0x0a000001, 0x0a000002, 1000, 80),
                                 3) == nullptr);
  CHECK(table.process_icmp_error(2, tenant_a,
                                 tuple(0x0a000001, 0x0a000002, 1000, 80),
                                 3) == nullptr);

  // FIN 序列:ESTABLISHED -> FIN_WAIT -> TIME_WAIT。
  PacketView fin{0x0a000001, 0x0a000002, 1000, 80, Protocol::TCP,
                 300,         true,        false, true};
  CHECK(table.process_packet(flow, fin, 4) == e);
  CHECK(e->state() == ConnState::FIN_WAIT);
  PacketView fin2{0x0a000002, 0x0a000001, 80, 1000, Protocol::TCP,
                  301,        true,        false, true};
  CHECK(table.process_packet(rev, fin2, 5) == e);
  CHECK(e->state() == ConnState::TIME_WAIT);

  // TIME_WAIT 超时 -> DYING -> 回收,此后查找不命中。
  table.tick(5 + ConntrackTable::kTimeWaitTimeout);
  CHECK(e->state() == ConnState::DYING);
  CHECK(table.reclaim() == 1);
  CHECK(table.find(flow) == nullptr);
  CHECK(table.size() == 0);
}

}  // namespace

int main() {
  test_tuple_collision();
  test_zone_isolation();
  test_timeout_reuse();
  test_fragment_association();
  test_legit_connection();
  if (g_failures != 0) {
    std::fprintf(stderr, "%d check(s) failed\n", g_failures);
    return EXIT_FAILURE;
  }
  std::puts("all conntrack tests passed");
  return EXIT_SUCCESS;
}

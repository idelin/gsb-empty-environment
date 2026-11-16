#include "netsec.hpp"

#include <cstdio>
#include <vector>

using netsec::SackBlock;
using netsec::SackScoreboard;

static int g_failures = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    if (!(cond)) {                                                         \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);          \
      ++g_failures;                                                        \
    }                                                                      \
  } while (0)

static void test_mss_negotiation() {
  // 默认下限 536：协商 48 时按 536 使用。
  SackScoreboard sb;
  CHECK(sb.negotiate_mss(48));
  CHECK(sb.mss() == 536);
  // 协商值高于下限则保持。
  SackScoreboard sb2;
  CHECK(sb2.negotiate_mss(1440));
  CHECK(sb2.mss() == 1440);
  // 对端通告低于 48：拒绝连接。
  SackScoreboard sb3;
  CHECK(!sb3.negotiate_mss(47));
  CHECK(!sb3.negotiate_mss(0));
  // 调用方下限设为 48 时，MSS 48 可用。
  SackScoreboard sb4(48);
  CHECK(sb4.negotiate_mss(48));
  CHECK(sb4.mss() == 48);
  SackScoreboard sb5(48);
  CHECK(!sb5.negotiate_mss(30));
}

static void test_reversed_and_out_of_window() {
  SackScoreboard sb(48);
  CHECK(sb.negotiate_mss(48));
  const std::uint32_t una = 1000, nxt = 2000;
  std::vector<SackBlock> blocks = {
      {1500, 1400},  // 反向
      {1600, 1600},  // 空区间
      {900, 1100},   // left < snd_una
      {1000, 1100},  // left == snd_una（窗口为 (snd_una, snd_nxt]）
      {1900, 2100},  // right > snd_nxt
      {1200, 1300},  // 唯一有效块
  };
  CHECK(sb.process_ack(una, nxt, blocks) == 100);
  CHECK(sb.range_count() == 1);
  CHECK(sb.sacked_bytes() == 100);
}

static void test_max_blocks_per_ack() {
  SackScoreboard sb(48);
  CHECK(sb.negotiate_mss(48));
  std::vector<SackBlock> blocks;
  for (int i = 0; i < 6; ++i) {  // 6 个互不相邻的有效块
    std::uint32_t l = 100 + i * 96;
    blocks.push_back({l, l + 48});
  }
  CHECK(sb.process_ack(0, 1000, blocks) == 4 * 48);  // 只处理前 4 个
  CHECK(sb.range_count() == 4);
}

static void test_duplicate_ack_not_counted() {
  SackScoreboard sb(48);
  CHECK(sb.negotiate_mss(48));
  std::vector<SackBlock> blocks = {{1100, 1200}, {1300, 1400}};
  CHECK(sb.process_ack(1000, 2000, blocks) == 200);
  // 重复 ACK 携带同样的块：不重复计数。
  CHECK(sb.process_ack(1000, 2000, blocks) == 0);
  CHECK(sb.process_ack(1000, 2000, blocks) == 0);
  CHECK(sb.sacked_bytes() == 200);
  CHECK(sb.range_count() == 2);
}

static void test_merge_and_range_count() {
  SackScoreboard sb(48);
  CHECK(sb.negotiate_mss(48));
  // 相邻块合并为一个区间。
  std::vector<SackBlock> adj = {{1100, 1200}, {1200, 1300}, {1300, 1400}};
  CHECK(sb.process_ack(1000, 2000, adj) == 300);
  CHECK(sb.range_count() == 1);
  // 新块桥接两个已有区间。
  SackScoreboard sb2(48);
  CHECK(sb2.negotiate_mss(48));
  std::vector<SackBlock> two = {{1100, 1200}, {1300, 1400}};
  CHECK(sb2.process_ack(1000, 2000, two) == 200);
  CHECK(sb2.range_count() == 2);
  std::vector<SackBlock> bridge = {{1200, 1300}};
  CHECK(sb2.process_ack(1000, 2000, bridge) == 100);
  CHECK(sb2.range_count() == 1);
  CHECK(sb2.sacked_bytes() == 300);
}

static void test_interleaved_sack_flood() {
  SackScoreboard sb(48);
  CHECK(sb.negotiate_mss(48));
  const std::uint32_t mss = 48;
  const int slots = 600;  // 600 个被 SACK 的 48 字节槽，间隔 48 字节空洞
  const std::uint32_t nxt = mss * 2 * slots;
  // 交错顺序：步长 149 与 600 互素，相邻送达的块彼此远离。
  std::vector<SackBlock> ack;
  int delivered = 0;
  for (int k = 0; k < slots; ++k) {
    int i = (k * 149) % slots;
    std::uint32_t l = (2u * i + 1u) * mss;
    ack.push_back({l, l + mss});
    if (ack.size() == 4 || k == slots - 1) {
      sb.process_ack(0, nxt, ack);
      delivered += static_cast<int>(ack.size());
      // 单 ACK 处理步数受 (区间数 + 块数) 常数倍约束。
      CHECK(sb.last_ack_steps() <= 16 * (sb.range_count() + ack.size()) + 16);
      CHECK(sb.range_count() <= SackScoreboard::kMaxRanges);
      ack.clear();
    }
  }
  CHECK(delivered == slots);
  // 区间数硬上限 128，不得无限增长。
  CHECK(sb.range_count() <= 128);
  CHECK(sb.degraded());
  // 回退态：只重传 snd_una 起的一个段。
  std::vector<SackBlock> rtx = sb.retransmit_segments(0, nxt);
  CHECK(rtx.size() == 1);
  CHECK(rtx[0].left == 0 && rtx[0].right == mss);
  // 累积 ACK 推进 snd_una 覆盖全部区间后退出回退态。
  sb.process_ack(nxt, nxt, {});
  CHECK(!sb.degraded());
  CHECK(sb.range_count() == 0);
}

static void test_retransmit_segmentation() {
  SackScoreboard sb(48);
  CHECK(sb.negotiate_mss(48));
  const std::uint32_t nxt = 1000;
  std::vector<SackBlock> blocks = {{48, 96}, {240, 480}, {500, 560}};
  CHECK(sb.process_ack(0, nxt, blocks) == 48 + 240 + 60);
  std::vector<SackBlock> rtx = sb.retransmit_segments(0, nxt);
  std::vector<SackBlock> expect = {
      {0, 48},                          // 整个空洞恰好一个 MSS
      {96, 144}, {144, 192}, {192, 240},  // 144 字节空洞整段切分
      {480, 500},                       // 空洞整体不足 MSS，按边界整段发
      {560, 608}, {608, 656}, {656, 704}, {704, 752}, {752, 800},
      {800, 848}, {848, 896}, {896, 944}, {944, 992},
      {992, 1000},                      // 队列尾部允许不足 MSS
  };
  CHECK(rtx.size() == expect.size());
  for (std::size_t i = 0; i < rtx.size() && i < expect.size(); ++i) {
    CHECK(rtx[i].left == expect[i].left);
    CHECK(rtx[i].right == expect[i].right);
  }
  // 除队列尾部外，没有 MSS 以下的碎片（整体不足 MSS 的边界空洞除外）。
  for (const SackBlock& s : rtx) {
    bool ok = (s.right - s.left >= 48) || (s.right == nxt) ||
              (s.left == 480 && s.right == 500);
    CHECK(ok);
  }

  // 空洞余量不足 MSS 且不在尾部：并入前一段，不制造碎片。
  SackScoreboard sb2(48);
  CHECK(sb2.negotiate_mss(48));
  std::vector<SackBlock> b2 = {{48, 140}, {200, 400}};
  CHECK(sb2.process_ack(0, 400, b2) == 92 + 200);
  std::vector<SackBlock> rtx2 = sb2.retransmit_segments(0, 400);
  // 空洞 [140,200)：48 整段 + 余量 12 并入前一段 => {140,200}
  CHECK(rtx2.size() == 2);
  CHECK(rtx2[0].left == 0 && rtx2[0].right == 48);
  CHECK(rtx2[1].left == 140 && rtx2[1].right == 200);
}

int main() {
  test_mss_negotiation();
  test_reversed_and_out_of_window();
  test_max_blocks_per_ack();
  test_duplicate_ack_not_counted();
  test_merge_and_range_count();
  test_interleaved_sack_flood();
  test_retransmit_segmentation();
  if (g_failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::printf("%d check(s) failed\n", g_failures);
  return 1;
}

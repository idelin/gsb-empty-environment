#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace netsec {

// 半开序号区间 [left, right)
struct SackBlock {
  std::uint32_t left;
  std::uint32_t right;
};

// TCP 发送端 SACK 记分板，供 ACK 处理路径调用。
// 防御目标：极小 MSS + 交错 SACK 洪泛造成的重传队列碎片化与 CPU 耗尽。
class SackScoreboard {
public:
  static constexpr std::uint32_t kHardMinMss = 48;    // 对端通告低于此值拒绝连接
  static constexpr std::size_t kMaxBlocksPerAck = 4;  // 单个 ACK 最多处理的 SACK 块数
  static constexpr std::size_t kMaxRanges = 128;      // 记分板区间数量上限

  // min_mss：调用方允许的 MSS 下限，协商值低于它时按下限使用。
  explicit SackScoreboard(std::uint32_t min_mss = 536);

  // 用对端通告的 MSS 完成协商；返回 false 表示必须拒绝连接（peer_mss < 48）。
  bool negotiate_mss(std::uint32_t peer_mss);
  std::uint32_t mss() const { return mss_; }

  // 处理一个 ACK 携带的 SACK 块列表，返回本次新覆盖的字节数。
  // 反向、越出 (snd_una, snd_nxt] 的块被忽略；最多处理 4 个有效块；
  // 重复 ACK 携带同样的块时返回 0，不重复计数。
  // 处理步数不超过 (区间数 + 块数) 的常数倍，见 last_ack_steps()。
  std::uint64_t process_ack(std::uint32_t snd_una, std::uint32_t snd_nxt,
                            const std::vector<SackBlock>& blocks);

  // 生成重传段：按区间边界切分、每段不超过 MSS；
  // 不产生 MSS 以下的碎片，队列尾部（snd_nxt 处）除外。
  // 记分板处于回退态（degraded）时只返回 snd_una 起的一个段。
  std::vector<SackBlock> retransmit_segments(std::uint32_t snd_una,
                                             std::uint32_t snd_nxt) const;

  std::size_t range_count() const { return ranges_.size(); }
  std::uint64_t sacked_bytes() const { return sacked_bytes_; }
  std::uint64_t last_ack_steps() const { return last_steps_; }
  // true 表示曾因区间超限丢弃过最旧区间，重传回退为 snd_una 起单段。
  bool degraded() const { return degraded_; }

  void clear();

private:
  struct Range {
    std::uint32_t left;
    std::uint32_t right;
    std::uint64_t age;  // 单调递增序号，用于淘汰最旧区间
  };

  // 插入 [left, right) 并与重叠/相邻区间合并，返回新覆盖的字节数。
  std::uint64_t insert(std::uint32_t left, std::uint32_t right);
  void evict_oldest();
  void emit_hole(std::uint32_t hl, std::uint32_t hr, std::uint32_t snd_nxt,
                 std::vector<SackBlock>& out) const;

  std::uint32_t min_mss_;
  std::uint32_t mss_ = 0;
  std::vector<Range> ranges_;  // 升序、互不重叠、互不相邻
  std::uint64_t sacked_bytes_ = 0;
  std::uint64_t next_age_ = 0;
  std::uint64_t last_steps_ = 0;
  bool degraded_ = false;
};

} // namespace netsec

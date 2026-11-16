#include "netsec.hpp"

#include <algorithm>

namespace netsec {

SackScoreboard::SackScoreboard(std::uint32_t min_mss) : min_mss_(min_mss) {}

bool SackScoreboard::negotiate_mss(std::uint32_t peer_mss) {
  if (peer_mss < kHardMinMss) {
    return false;  // 对端通告过小，拒绝连接
  }
  mss_ = std::max(peer_mss, min_mss_);
  return true;
}

std::uint64_t SackScoreboard::process_ack(std::uint32_t snd_una,
                                          std::uint32_t snd_nxt,
                                          const std::vector<SackBlock>& blocks) {
  last_steps_ = 0;

  // 丢弃已被累积 ACK 覆盖的区间（snd_una 已前移）。
  std::size_t keep = 0;
  while (keep < ranges_.size() && ranges_[keep].right <= snd_una) {
    ++keep;
    ++last_steps_;
  }
  if (keep > 0) {
    ranges_.erase(ranges_.begin(), ranges_.begin() + keep);
    if (ranges_.empty()) {
      degraded_ = false;  // 记分板已清空，退出回退态
    }
  }

  std::uint64_t newly = 0;
  std::size_t processed = 0;
  for (const SackBlock& b : blocks) {
    ++last_steps_;
    if (processed == kMaxBlocksPerAck) {
      break;
    }
    // 区间反向（含空区间）或越出 (snd_una, snd_nxt] 的块忽略。
    if (b.right <= b.left || b.left <= snd_una || b.right > snd_nxt) {
      continue;
    }
    ++processed;
    newly += insert(b.left, b.right);
  }
  sacked_bytes_ += newly;
  return newly;
}

std::uint64_t SackScoreboard::insert(std::uint32_t left, std::uint32_t right) {
  // 第一个 right >= left 的区间（相邻也参与合并）。
  std::size_t i = 0;
  while (i < ranges_.size() && ranges_[i].right < left) {
    ++i;
    ++last_steps_;
  }

  std::uint64_t covered = 0;
  std::uint32_t merged_left = left;
  std::uint32_t merged_right = right;
  std::uint64_t age = next_age_++;
  std::size_t j = i;
  while (j < ranges_.size() && ranges_[j].left <= merged_right) {
    const Range& rg = ranges_[j];
    std::uint32_t ovl = std::max(left, rg.left);
    std::uint32_t ovr = std::min(right, rg.right);
    if (ovr > ovl) {
      covered += ovr - ovl;  // 已被旧区间覆盖的部分不计入新增
    }
    merged_left = std::min(merged_left, rg.left);
    merged_right = std::max(merged_right, rg.right);
    age = std::min(age, rg.age);  // 合并后保留最旧的年龄
    ++j;
    ++last_steps_;
  }

  Range merged{merged_left, merged_right, age};
  if (j == i) {
    ranges_.insert(ranges_.begin() + i, merged);
  } else {
    ranges_[i] = merged;
    ranges_.erase(ranges_.begin() + i + 1, ranges_.begin() + j);
  }

  // 区间数量硬上限：超出时放弃最旧的区间信息，回退到粗粒度重传。
  while (ranges_.size() > kMaxRanges) {
    evict_oldest();
    degraded_ = true;
  }
  return (right - left) - covered;
}

void SackScoreboard::evict_oldest() {
  std::size_t oldest = 0;
  for (std::size_t k = 1; k < ranges_.size(); ++k) {
    ++last_steps_;
    if (ranges_[k].age < ranges_[oldest].age) {
      oldest = k;
    }
  }
  ranges_.erase(ranges_.begin() + oldest);
}

std::vector<SackBlock>
SackScoreboard::retransmit_segments(std::uint32_t snd_una,
                                    std::uint32_t snd_nxt) const {
  std::vector<SackBlock> out;
  if (snd_nxt <= snd_una || mss_ == 0) {
    return out;
  }
  if (degraded_) {
    // 回退态：只重传 snd_una 起的一个段。
    out.push_back({snd_una, std::min(snd_nxt, snd_una + mss_)});
    return out;
  }
  std::uint32_t cursor = snd_una;
  for (const Range& rg : ranges_) {
    if (rg.right <= snd_una || rg.left >= snd_nxt) {
      continue;
    }
    emit_hole(cursor, std::min(rg.left, snd_nxt), snd_nxt, out);
    cursor = rg.right;
    if (cursor >= snd_nxt) {
      return out;
    }
  }
  emit_hole(cursor, snd_nxt, snd_nxt, out);
  return out;
}

void SackScoreboard::emit_hole(std::uint32_t hl, std::uint32_t hr,
                               std::uint32_t snd_nxt,
                               std::vector<SackBlock>& out) const {
  std::uint32_t pos = hl;
  while (hr - pos >= mss_) {
    out.push_back({pos, pos + mss_});
    pos += mss_;
  }
  if (pos < hr) {
    if (hr == snd_nxt || pos == hl) {
      // 队列尾部允许不足 MSS；整个空洞不足 MSS 时按边界整段发送。
      out.push_back({pos, hr});
    } else {
      // 余量并入本空洞的前一段，避免制造 MSS 以下的碎片。
      out.back().right = hr;
    }
  }
}

void SackScoreboard::clear() {
  ranges_.clear();
  sacked_bytes_ = 0;
  last_steps_ = 0;
  degraded_ = false;
}

} // namespace netsec

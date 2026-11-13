#include "netsec.hpp"

#include <algorithm>
#include <limits>
#include <stdexcept>
#include <utility>

namespace {

netsec::Sequence saturating_add(netsec::Sequence value, std::uint64_t amount) noexcept {
    const auto limit = std::numeric_limits<netsec::Sequence>::max();
    if (amount > limit - value) {
        return limit;
    }
    return value + amount;
}

std::vector<netsec::SeqRange> normalize_queue(std::vector<netsec::SeqRange> queue) {
    for (const auto& range : queue) {
        if (range.empty()) {
            throw std::invalid_argument("send queue contains an empty or reversed range");
        }
    }

    std::sort(queue.begin(), queue.end(), [](const auto& lhs, const auto& rhs) {
        return lhs.begin < rhs.begin || (lhs.begin == rhs.begin && lhs.end < rhs.end);
    });

    std::vector<netsec::SeqRange> normalized;
    normalized.reserve(queue.size());
    for (const auto& range : queue) {
        if (normalized.empty() || range.begin > normalized.back().end) {
            normalized.push_back(range);
        } else if (range.end > normalized.back().end) {
            normalized.back().end = range.end;
        }
    }
    return normalized;
}

}

namespace netsec {

std::optional<std::uint32_t> negotiate_mss(
    std::uint32_t advertised_mss,
    std::uint32_t mss_floor) noexcept {
    if (!acceptable_mss(advertised_mss)) {
        return std::nullopt;
    }
    return std::max(advertised_mss, mss_floor);
}

SackScoreboard::SackScoreboard(
    std::uint32_t advertised_mss,
    std::uint32_t mss_floor)
    : advertised_mss_(advertised_mss),
      mss_(negotiate_mss(advertised_mss, mss_floor).value_or(0)) {
    if (mss_ == 0) {
        throw std::invalid_argument("advertised MSS is below the absolute minimum of 48");
    }
}

std::optional<SackScoreboard> SackScoreboard::create(
    std::uint32_t advertised_mss,
    std::uint32_t mss_floor) {
    if (!acceptable_mss(advertised_mss)) {
        return std::nullopt;
    }
    return SackScoreboard(advertised_mss, mss_floor);
}

void SackScoreboard::reset(std::vector<SeqRange> send_queue) {
    send_queue_ = normalize_queue(std::move(send_queue));
    if (send_queue_.empty()) {
        snd_una_ = 0;
        snd_nxt_ = 0;
    } else {
        snd_una_ = send_queue_.front().begin;
        snd_nxt_ = send_queue_.back().end;
    }
    sacked_.clear();
    fallback_ = false;
    last_processing_steps_ = 0;
}

void SackScoreboard::reset(
    std::vector<SeqRange> send_queue,
    Sequence snd_una,
    Sequence snd_nxt) {
    if (snd_una > snd_nxt) {
        throw std::invalid_argument("snd_una must not be greater than snd_nxt");
    }
    send_queue_ = normalize_queue(std::move(send_queue));
    snd_una_ = snd_una;
    snd_nxt_ = snd_nxt;
    sacked_.clear();
    fallback_ = false;
    last_processing_steps_ = 0;
}

AckResult SackScoreboard::process_ack(
    Sequence cumulative_ack,
    const std::vector<SackBlock>& sack_blocks) {
    AckResult result;
    std::size_t steps = 0;

    result.intervals_before = sacked_.size();
    result.blocks_offered = sack_blocks.size();

    bool snd_una_advanced = false;
    Sequence new_una = cumulative_ack;
    if (new_una > snd_nxt_) {
        new_una = snd_nxt_;
        result.ack_clamped = true;
    }
    if (new_una > snd_una_) {
        snd_una_ = new_una;
        snd_una_advanced = true;
        ++steps;
    }
    if (snd_una_advanced) {
        fallback_ = false;
    }

    std::size_t remove_count = 0;
    while (remove_count < sacked_.size() && sacked_[remove_count].end <= snd_una_) {
        ++remove_count;
        ++steps;
    }
    if (remove_count != 0) {
        sacked_.erase(sacked_.begin(), sacked_.begin() + static_cast<std::ptrdiff_t>(remove_count));
    }
    if (!sacked_.empty() && sacked_.front().begin < snd_una_) {
        sacked_.front().begin = snd_una_;
        ++steps;
    }

    const std::size_t processing_limit =
        std::min(sack_blocks.size(), kMaxSackBlocksPerAck);

    for (std::size_t index = 0; index < processing_limit; ++index) {
        ++steps;
        ++result.blocks_considered;

        const SackBlock block = sack_blocks[index];
        if (block.empty() || block.begin <= snd_una_ || block.end > snd_nxt_) {
            ++result.blocks_ignored;
            continue;
        }

        std::size_t first = 0;
        while (first < sacked_.size() && sacked_[first].end < block.begin) {
            ++first;
            ++steps;
        }
        if (first < sacked_.size() &&
            sacked_[first].begin <= block.begin &&
            sacked_[first].end >= block.end) {
            ++result.blocks_duplicate;
            continue;
        }

        Sequence merged_begin = block.begin;
        Sequence merged_end = block.end;
        std::size_t last = first;
        Sequence replaced_bytes = 0;
        while (last < sacked_.size() && sacked_[last].begin <= merged_end) {
            ++steps;
            merged_begin = std::min(merged_begin, sacked_[last].begin);
            merged_end = std::max(merged_end, sacked_[last].end);
            replaced_bytes += sacked_[last].length();
            ++last;
        }

        const SeqRange merged_range{merged_begin, merged_end};
        if (first == last) {
            sacked_.insert(
                sacked_.begin() + static_cast<std::ptrdiff_t>(first), merged_range);
        } else {
            sacked_[first] = merged_range;
            sacked_.erase(
                sacked_.begin() + static_cast<std::ptrdiff_t>(first + 1),
                sacked_.begin() + static_cast<std::ptrdiff_t>(last));
        }

        ++result.blocks_applied;
        result.newly_sacked_bytes += merged_range.length() - replaced_bytes;

        if (sacked_.size() > kMaxScoreboardIntervals) {
            result.limit_exceeded = true;
            while (sacked_.size() > kMaxScoreboardIntervals) {
                sacked_.erase(sacked_.begin());
                ++result.intervals_evicted;
                ++steps;
            }
        }
    }

    if (sack_blocks.size() > processing_limit) {
        result.blocks_ignored += sack_blocks.size() - processing_limit;
    }

    if (result.limit_exceeded) {
        fallback_ = true;
    }

    result.snd_una = snd_una_;
    result.snd_nxt = snd_nxt_;
    result.intervals_after = sacked_.size();
    result.fallback = fallback_;
    result.processing_steps = steps;
    last_processing_steps_ = steps;
    result.retransmit = build_retransmission_plan(nullptr);
    return result;
}

SeqRange SackScoreboard::fallback_range() const noexcept {
    if (snd_una_ >= snd_nxt_) {
        return {};
    }
    Sequence end = saturating_add(snd_una_, mss_);
    if (end > snd_nxt_) {
        end = snd_nxt_;
    }
    return SeqRange{snd_una_, end};
}

std::vector<SeqRange> SackScoreboard::retransmission_plan() const {
    std::size_t steps = 0;
    return build_retransmission_plan(&steps);
}

std::vector<SeqRange> SackScoreboard::build_retransmission_plan(
    std::size_t* steps) const {
    auto tick = [steps]() {
        if (steps != nullptr) {
            ++*steps;
        }
    };

    if (snd_una_ >= snd_nxt_ || send_queue_.empty()) {
        tick();
        return {};
    }
    if (fallback_) {
        const SeqRange range = fallback_range();
        tick();
        if (range.empty()) {
            return {};
        }
        return {range};
    }

    std::vector<SeqRange> unsacked_spans;
    unsacked_spans.reserve(sacked_.size() + 1);

    std::size_t sacked_index = 0;
    for (const auto& raw_segment : send_queue_) {
        tick();
        const Sequence segment_begin = std::max(raw_segment.begin, snd_una_);
        const Sequence segment_end = std::min(raw_segment.end, snd_nxt_);
        if (segment_begin >= segment_end) {
            continue;
        }

        while (sacked_index < sacked_.size() &&
               sacked_[sacked_index].end <= segment_begin) {
            ++sacked_index;
            tick();
        }

        Sequence cursor = segment_begin;
        std::size_t index = sacked_index;
        while (index < sacked_.size() && sacked_[index].begin < segment_end) {
            tick();
            if (sacked_[index].begin > cursor) {
                const Sequence span_end = std::min(sacked_[index].begin, segment_end);
                if (cursor < span_end) {
                    unsacked_spans.push_back(SeqRange{cursor, span_end});
                }
                cursor = span_end;
            }
            if (sacked_[index].end > cursor) {
                cursor = sacked_[index].end;
            }
            if (cursor >= segment_end) {
                break;
            }
            ++index;
        }
        if (cursor < segment_end) {
            unsacked_spans.push_back(SeqRange{cursor, segment_end});
        }
        sacked_index = index;
    }

    if (unsacked_spans.empty()) {
        return {};
    }

    std::vector<SeqRange> plan;
    plan.reserve(unsacked_spans.size());

    std::size_t index = 0;
    while (index < unsacked_spans.size()) {
        tick();
        SeqRange current = unsacked_spans[index];

        if (current.length() < mss_ && current.end != snd_nxt_) {
            const Sequence minimum_end = std::min(
                snd_nxt_, saturating_add(current.begin, mss_));
            current.end = std::max(current.end, minimum_end);
        }

        while (index + 1 < unsacked_spans.size() &&
               unsacked_spans[index + 1].begin <= current.end) {
            ++index;
            tick();
            current.end = std::max(current.end, unsacked_spans[index].end);
        }

        if (!plan.empty() && current.begin <= plan.back().end) {
            plan.back().end = std::max(plan.back().end, current.end);
        } else {
            plan.push_back(current);
        }
        ++index;
    }

    return plan;
}

}

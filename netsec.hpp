#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <utility>
#include <vector>

namespace netsec {

using Sequence = std::uint64_t;

inline constexpr std::uint32_t kMinimumAdvertisedMss = 48;
inline constexpr std::uint32_t kDefaultMssFloor = 536;
inline constexpr std::size_t kMaxSackBlocksPerAck = 4;
inline constexpr std::size_t kMaxScoreboardIntervals = 128;

struct SeqRange {
    Sequence begin{0};
    Sequence end{0};

    [[nodiscard]] constexpr Sequence length() const noexcept {
        return end > begin ? end - begin : 0;
    }

    [[nodiscard]] constexpr bool empty() const noexcept {
        return begin >= end;
    }

    friend constexpr bool operator==(const SeqRange& lhs, const SeqRange& rhs) noexcept {
        return lhs.begin == rhs.begin && lhs.end == rhs.end;
    }

    friend constexpr bool operator!=(const SeqRange& lhs, const SeqRange& rhs) noexcept {
        return !(lhs == rhs);
    }
};

using Segment = SeqRange;
using SackBlock = SeqRange;

[[nodiscard]] constexpr bool acceptable_mss(std::uint32_t advertised_mss) noexcept {
    return advertised_mss >= kMinimumAdvertisedMss;
}

[[nodiscard]] std::optional<std::uint32_t> negotiate_mss(
    std::uint32_t advertised_mss,
    std::uint32_t mss_floor = kDefaultMssFloor) noexcept;

struct Ack {
    Sequence cumulative_ack{0};
    std::vector<SackBlock> sack_blocks;
};

struct AckResult {
    Sequence snd_una{0};
    Sequence snd_nxt{0};
    std::size_t blocks_offered{0};
    std::size_t blocks_considered{0};
    std::size_t blocks_applied{0};
    std::size_t blocks_ignored{0};
    std::size_t blocks_duplicate{0};
    Sequence newly_sacked_bytes{0};
    std::size_t intervals_before{0};
    std::size_t intervals_after{0};
    std::size_t intervals_evicted{0};
    bool limit_exceeded{false};
    bool fallback{false};
    bool ack_clamped{false};
    std::size_t processing_steps{0};
    std::vector<SeqRange> retransmit;
};

class SackScoreboard {
public:
    explicit SackScoreboard(
        std::uint32_t advertised_mss,
        std::uint32_t mss_floor = kDefaultMssFloor);

    [[nodiscard]] static std::optional<SackScoreboard> create(
        std::uint32_t advertised_mss,
        std::uint32_t mss_floor = kDefaultMssFloor);

    [[nodiscard]] std::uint32_t advertised_mss() const noexcept { return advertised_mss_; }
    [[nodiscard]] std::uint32_t mss() const noexcept { return mss_; }
    [[nodiscard]] std::uint32_t effective_mss() const noexcept { return mss_; }

    void reset(std::vector<SeqRange> send_queue);
    void reset(std::vector<SeqRange> send_queue, Sequence snd_una, Sequence snd_nxt);
    void set_send_queue(std::vector<SeqRange> send_queue) {
        reset(std::move(send_queue));
    }
    void set_send_queue(std::vector<SeqRange> send_queue, Sequence snd_una, Sequence snd_nxt) {
        reset(std::move(send_queue), snd_una, snd_nxt);
    }
    void update_send_queue(std::vector<SeqRange> send_queue) {
        reset(std::move(send_queue));
    }

    [[nodiscard]] AckResult process_ack(
        Sequence cumulative_ack,
        const std::vector<SackBlock>& sack_blocks);
    [[nodiscard]] AckResult process_ack(const Ack& ack) {
        return process_ack(ack.cumulative_ack, ack.sack_blocks);
    }
    [[nodiscard]] AckResult on_ack(
        Sequence cumulative_ack,
        const std::vector<SackBlock>& sack_blocks) {
        return process_ack(cumulative_ack, sack_blocks);
    }
    [[nodiscard]] AckResult handle_ack(
        Sequence cumulative_ack,
        const std::vector<SackBlock>& sack_blocks) {
        return process_ack(cumulative_ack, sack_blocks);
    }

    [[nodiscard]] Sequence snd_una() const noexcept { return snd_una_; }
    [[nodiscard]] Sequence snd_nxt() const noexcept { return snd_nxt_; }
    [[nodiscard]] const std::vector<SeqRange>& sacked_ranges() const noexcept {
        return sacked_;
    }
    [[nodiscard]] std::size_t interval_count() const noexcept { return sacked_.size(); }
    [[nodiscard]] bool fallback_active() const noexcept { return fallback_; }
    [[nodiscard]] std::size_t last_processing_steps() const noexcept {
        return last_processing_steps_;
    }
    [[nodiscard]] std::size_t last_ack_steps() const noexcept {
        return last_processing_steps_;
    }

    [[nodiscard]] std::vector<SeqRange> retransmission_plan() const;
    [[nodiscard]] std::vector<SeqRange> retransmit_ranges() const {
        return retransmission_plan();
    }
    [[nodiscard]] std::vector<SeqRange> retransmit() const {
        return retransmission_plan();
    }

private:
    [[nodiscard]] std::vector<SeqRange> build_retransmission_plan(std::size_t* steps) const;
    [[nodiscard]] SeqRange fallback_range() const noexcept;

    std::uint32_t advertised_mss_{0};
    std::uint32_t mss_{kDefaultMssFloor};
    Sequence snd_una_{0};
    Sequence snd_nxt_{0};
    std::vector<SeqRange> send_queue_;
    std::vector<SeqRange> sacked_;
    bool fallback_{false};
    std::size_t last_processing_steps_{0};
};

}

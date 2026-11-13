#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <exception>
#include <iostream>
#include <optional>
#include <random>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const std::string& message, int line) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL line " << line << ": " << message << '\n';
    }
}

#define CHECK(condition) check((condition), #condition, __LINE__)

bool ranges_equal(
    const std::vector<netsec::SeqRange>& actual,
    const std::vector<netsec::SeqRange>& expected) {
    if (actual.size() != expected.size()) {
        return false;
    }
    for (std::size_t index = 0; index < actual.size(); ++index) {
        if (actual[index] != expected[index]) {
            return false;
        }
    }
    return true;
}

void test_mss_negotiation() {
    CHECK(netsec::acceptable_mss(48));
    CHECK(!netsec::acceptable_mss(47));
    CHECK(netsec::negotiate_mss(48) == std::optional<std::uint32_t>{536});
    CHECK(netsec::negotiate_mss(48, 256) == std::optional<std::uint32_t>{256});
    CHECK(netsec::negotiate_mss(1000) == std::optional<std::uint32_t>{1000});
    CHECK(!netsec::negotiate_mss(47));
    CHECK(!netsec::SackScoreboard::create(47));

    netsec::SackScoreboard scoreboard(48);
    CHECK(scoreboard.advertised_mss() == 48);
    CHECK(scoreboard.mss() == 536);
    CHECK(scoreboard.effective_mss() == 536);

    bool rejected = false;
    try {
        netsec::SackScoreboard invalid(47);
    } catch (const std::invalid_argument&) {
        rejected = true;
    }
    CHECK(rejected);
}

void test_merge_and_duplicate_ack() {
    netsec::SackScoreboard scoreboard(536);
    scoreboard.reset({netsec::SeqRange{0, 2000}});

    auto result = scoreboard.process_ack(
        0,
        {
            netsec::SeqRange{100, 200},
            netsec::SeqRange{200, 300},
            netsec::SeqRange{300, 400},
            netsec::SeqRange{400, 500},
        });
    CHECK(result.blocks_considered == 4);
    CHECK(result.blocks_applied == 4);
    CHECK(result.intervals_before == 0);
    CHECK(result.intervals_after == 1);
    CHECK(scoreboard.interval_count() == 1);
    CHECK(ranges_equal(
        scoreboard.sacked_ranges(),
        {netsec::SeqRange{100, 500}}));

    auto duplicate = scoreboard.process_ack(
        0,
        {
            netsec::SeqRange{100, 200},
            netsec::SeqRange{200, 300},
            netsec::SeqRange{300, 400},
            netsec::SeqRange{400, 500},
        });
    CHECK(duplicate.blocks_duplicate == 4);
    CHECK(duplicate.blocks_applied == 0);
    CHECK(duplicate.newly_sacked_bytes == 0);
    CHECK(duplicate.intervals_before == 1);
    CHECK(duplicate.intervals_after == 1);
    CHECK(scoreboard.interval_count() == 1);

    auto extension = scoreboard.process_ack(0, {netsec::SeqRange{500, 700}});
    CHECK(extension.blocks_applied == 1);
    CHECK(scoreboard.interval_count() == 1);
    CHECK(ranges_equal(
        scoreboard.sacked_ranges(),
        {netsec::SeqRange{100, 700}}));
}

void test_reversed_and_out_of_range_blocks() {
    netsec::SackScoreboard scoreboard(536);
    scoreboard.reset({netsec::SeqRange{0, 1000}});

    auto result = scoreboard.process_ack(
        0,
        {
            netsec::SeqRange{500, 400},
            netsec::SeqRange{0, 100},
            netsec::SeqRange{900, 1000},
            netsec::SeqRange{100, 200},
            netsec::SeqRange{1000, 1200},
        });

    CHECK(result.blocks_considered == 4);
    CHECK(result.blocks_applied == 2);
    CHECK(result.blocks_ignored == 3);
    CHECK(scoreboard.interval_count() == 2);
    CHECK(ranges_equal(
        scoreboard.sacked_ranges(),
        {netsec::SeqRange{100, 200}, netsec::SeqRange{900, 1000}}));

    auto cumulative = scoreboard.process_ack(950, {netsec::SeqRange{960, 990}});
    CHECK(cumulative.snd_una == 950);
    CHECK(cumulative.intervals_after == 1);
    CHECK(ranges_equal(
        scoreboard.sacked_ranges(),
        {netsec::SeqRange{950, 1000}}));
}

void test_four_block_limit() {
    netsec::SackScoreboard scoreboard(536);
    scoreboard.reset({netsec::SeqRange{0, 3000}});

    auto result = scoreboard.process_ack(
        0,
        {
            netsec::SeqRange{100, 200},
            netsec::SeqRange{300, 400},
            netsec::SeqRange{500, 600},
            netsec::SeqRange{700, 800},
            netsec::SeqRange{900, 1000},
            netsec::SeqRange{1100, 1200},
        });
    CHECK(result.blocks_offered == 6);
    CHECK(result.blocks_considered == 4);
    CHECK(result.blocks_applied == 4);
    CHECK(result.blocks_ignored == 2);
    CHECK(scoreboard.interval_count() == 4);
}

void test_interleaved_sack_flood() {
    netsec::SackScoreboard scoreboard(48);
    std::vector<netsec::SeqRange> send_queue;
    for (std::uint64_t sequence = 0; sequence < 400000; sequence += 48) {
        send_queue.push_back(netsec::SeqRange{
            sequence, std::min<std::uint64_t>(sequence + 48, 400000)});
    }
    scoreboard.reset(send_queue);
    CHECK(scoreboard.mss() == 536);

    bool saw_overflow = false;
    for (std::uint64_t round = 0; round < 200; ++round) {
        std::vector<netsec::SeqRange> blocks;
        const std::uint64_t base = 1000 + round * 40;
        for (std::uint64_t offset = 0; offset < 4; ++offset) {
            const std::uint64_t begin = base + offset * 10;
            blocks.push_back(netsec::SeqRange{begin, begin + 1});
        }

        const std::size_t intervals_before = scoreboard.interval_count();
        auto result = scoreboard.process_ack(0, blocks);

        CHECK(result.blocks_considered <= netsec::kMaxSackBlocksPerAck);
        CHECK(result.processing_steps <=
              16 * (intervals_before + result.blocks_offered + 1));
        CHECK(scoreboard.interval_count() <= netsec::kMaxScoreboardIntervals);

        if (result.limit_exceeded) {
            saw_overflow = true;
            CHECK(result.fallback);
            CHECK(result.intervals_evicted >= 1);
            CHECK(result.retransmit.size() == 1);
            CHECK(result.retransmit.front().begin == 0);
            CHECK(result.retransmit.front().length() <= scoreboard.mss());
            CHECK(scoreboard.sacked_ranges().back().end >= blocks.back().end);
        }
    }

    CHECK(saw_overflow);
    CHECK(scoreboard.interval_count() == netsec::kMaxScoreboardIntervals);
    CHECK(scoreboard.fallback_active());

    const auto fallback_plan = scoreboard.retransmission_plan();
    CHECK(fallback_plan.size() == 1);
    CHECK(fallback_plan.front().begin == scoreboard.snd_una());
    CHECK(fallback_plan.front().length() == scoreboard.mss());
}

void test_retransmission_uses_interval_boundaries() {
    netsec::SackScoreboard scoreboard(536);
    scoreboard.reset({netsec::SeqRange{0, 10000}});
    (void)scoreboard.process_ack(0, {netsec::SeqRange{1000, 2000}});

    const auto plan = scoreboard.retransmission_plan();
    CHECK(ranges_equal(
        plan,
        {netsec::SeqRange{0, 1000}, netsec::SeqRange{2000, 10000}}));
    CHECK(plan.front().length() >= scoreboard.mss());
    CHECK(plan.back().length() >= scoreboard.mss());
}

void test_no_sub_mss_fragments_except_tail() {
    netsec::SackScoreboard scoreboard(48);
    scoreboard.reset({netsec::SeqRange{0, 2400}});

    for (std::uint64_t offset = 48; offset < 2400; offset += 96) {
        (void)scoreboard.process_ack(
            0,
            {netsec::SeqRange{offset, std::min<std::uint64_t>(offset + 48, 2400)}});
    }

    const auto plan = scoreboard.retransmission_plan();
    CHECK(!plan.empty());
    for (const auto& range : plan) {
        CHECK(range.length() >= scoreboard.mss() || range.end == scoreboard.snd_nxt());
    }
    CHECK(plan.back().end == scoreboard.snd_nxt());
}

void test_short_tail_may_be_smaller_than_mss() {
    netsec::SackScoreboard scoreboard(536);
    scoreboard.reset({netsec::SeqRange{0, 1000}});
    (void)scoreboard.process_ack(0, {netsec::SeqRange{100, 900}});

    const auto plan = scoreboard.retransmission_plan();
    CHECK(plan.size() == 2);
    CHECK(plan.front().length() >= scoreboard.mss());
    CHECK(plan.back().end == scoreboard.snd_nxt());
    CHECK(plan.back().length() < scoreboard.mss());
}

void test_ack_beyond_snd_nxt_is_clamped() {
    netsec::SackScoreboard scoreboard(536);
    scoreboard.reset({netsec::SeqRange{0, 1000}});

    auto result = scoreboard.process_ack(2000, {});
    CHECK(result.ack_clamped);
    CHECK(result.snd_una == 1000);
    CHECK(result.snd_nxt == 1000);
    CHECK(result.retransmit.empty());
}

void test_randomized_invariants() {
    std::mt19937 rng(0x5A17u);
    constexpr std::uint64_t kQueueEnd = 20000;

    for (int iteration = 0; iteration < 300; ++iteration) {
        static const std::uint32_t mss_choices[] = {48, 536, 1000, 1500};
        const std::uint32_t mss = mss_choices[iteration % 4];
        netsec::SackScoreboard scoreboard(mss);
        scoreboard.reset({netsec::SeqRange{0, kQueueEnd}});

        for (int ack_index = 0; ack_index < 40; ++ack_index) {
            std::vector<netsec::SeqRange> blocks;
            const std::size_t block_count = rng() % 7;
            for (std::size_t block_index = 0; block_index < block_count; ++block_index) {
                const std::uint64_t begin = rng() % (kQueueEnd - 1);
                const std::uint64_t length = 1 + (rng() % 300);
                const std::uint64_t end = std::min<std::uint64_t>(begin + length, kQueueEnd);
                if ((rng() & 1u) == 0) {
                    blocks.push_back(netsec::SeqRange{begin, end});
                } else {
                    blocks.push_back(netsec::SeqRange{end, begin});
                }
            }

            const std::size_t intervals_before = scoreboard.interval_count();
            const auto result = scoreboard.process_ack(0, blocks);
            CHECK(scoreboard.interval_count() <= netsec::kMaxScoreboardIntervals);
            CHECK(result.intervals_after == scoreboard.interval_count());
            CHECK(result.processing_steps <=
                  16 * (intervals_before + result.blocks_offered + 1));

            const auto& sacked = scoreboard.sacked_ranges();
            for (std::size_t index = 0; index < sacked.size(); ++index) {
                CHECK(!sacked[index].empty());
                CHECK(sacked[index].begin >= scoreboard.snd_una());
                CHECK(sacked[index].end <= scoreboard.snd_nxt());
                if (index != 0) {
                    CHECK(sacked[index - 1].end < sacked[index].begin);
                }
            }

            const auto plan = scoreboard.retransmission_plan();
            CHECK(plan.size() <= scoreboard.interval_count() + 1);
            for (std::size_t index = 0; index < plan.size(); ++index) {
                CHECK(!plan[index].empty());
                CHECK(plan[index].begin >= scoreboard.snd_una());
                CHECK(plan[index].end <= scoreboard.snd_nxt());
                CHECK(plan[index].length() >= scoreboard.mss() ||
                      plan[index].end == scoreboard.snd_nxt());
                if (index != 0) {
                    CHECK(plan[index - 1].end < plan[index].begin);
                }
            }

            std::vector<netsec::SeqRange> unsacked;
            std::uint64_t cursor = scoreboard.snd_una();
            for (const auto& range : sacked) {
                if (range.begin > cursor) {
                    unsacked.push_back(netsec::SeqRange{cursor, range.begin});
                }
                cursor = std::max(cursor, range.end);
            }
            if (cursor < scoreboard.snd_nxt()) {
                unsacked.push_back(netsec::SeqRange{cursor, scoreboard.snd_nxt()});
            }

            for (const auto& span : unsacked) {
                const auto covers = [&plan](std::uint64_t position) {
                    return std::any_of(
                        plan.begin(), plan.end(), [position](const auto& range) {
                            return range.begin <= position && position < range.end;
                        });
                };
                CHECK(covers(span.begin));
                CHECK(covers(span.end - 1));
            }
        }
    }
}

}

int main() {
    test_mss_negotiation();
    test_merge_and_duplicate_ack();
    test_reversed_and_out_of_range_blocks();
    test_four_block_limit();
    test_interleaved_sack_flood();
    test_retransmission_uses_interval_boundaries();
    test_no_sub_mss_fragments_except_tail();
    test_short_tail_may_be_smaller_than_mss();
    test_ack_beyond_snd_nxt_is_clamped();
    test_randomized_invariants();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}

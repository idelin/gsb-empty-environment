#include "netsec.hpp"

#include <cstdio>
#include <cstdlib>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                          \
    do {                                                                     \
        if (!(cond)) {                                                       \
            std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond);      \
            ++g_failures;                                                    \
        }                                                                    \
    } while (0)

constexpr NodeId A = 1;
constexpr NodeId B = 2;
constexpr NodeId C = 3;

ReceivedFrame make_frame(int frame_id, NodeId sender, int cycle_count,
                         bool sync = false, int payload_length = 8) {
    ReceivedFrame f;
    f.header.frame_id = frame_id;
    f.header.payload_length = payload_length;
    f.header.cycle_count = cycle_count;
    f.header.sync_frame = sync;
    f.sender = sender;
    return f;
}

// Slot 10: owner A, every cycle. Slot 20: owner B, even cycles.
// Slot 30: owner C, cycles 2,6,... (repetition 4, base 2).
SlotOwnershipMonitor make_monitor() {
    return SlotOwnershipMonitor(
        {
            SlotEntry{10, A, 1, 0},
            SlotEntry{20, B, 2, 0},
            SlotEntry{30, C, 4, 2},
        },
        {A, B});  // A and B are registered sync nodes
}

void test_owner_accepted_and_borrowed_slot() {
    auto m = make_monitor();
    m.begin_cycle(0);

    auto borrowed = m.inspect(make_frame(10, B, 0));  // B is not owner of slot 10
    auto legit = m.inspect(make_frame(20, B, 0));     // owner B on its own slot 20 (even cycle)
    m.end_cycle();

    const Verdict vb = m.verdict(borrowed);
    const Verdict va = m.verdict(legit);
    CHECK(vb.decision == Decision::Dropped);
    CHECK(vb.reason == DropReason::NotOwner);
    CHECK(va.decision == Decision::Accepted);
    CHECK(m.node_counters(B).violations == 1);
    CHECK(m.node_counters(A).violations == 0);
}

void test_repetition_wrong_phase() {
    auto m = make_monitor();

    // Odd cycle: slot 20 (rep 2, base 0) is not live.
    m.begin_cycle(1);
    auto odd = m.inspect(make_frame(20, B, 1));
    m.end_cycle();
    CHECK(m.verdict(odd).decision == Decision::Dropped);
    CHECK(m.verdict(odd).reason == DropReason::WrongPhase);
    CHECK(m.node_counters(B).violations == 0);  // phase error is not a strike

    // Even cycle: slot 20 live, owner frame passes.
    m.begin_cycle(2);
    auto even = m.inspect(make_frame(20, B, 2));
    m.end_cycle();
    CHECK(m.verdict(even).decision == Decision::Accepted);

    // Slot 30 (rep 4, base 2): cycle 0 wrong phase, cycle 2 in phase.
    m.begin_cycle(0);
    auto early = m.inspect(make_frame(30, C, 0));
    m.end_cycle();
    CHECK(m.verdict(early).reason == DropReason::WrongPhase);

    m.begin_cycle(2);
    auto in_phase = m.inspect(make_frame(30, C, 2));
    m.end_cycle();
    CHECK(m.verdict(in_phase).decision == Decision::Accepted);
}

void test_same_slot_double_frame_conflict() {
    auto m = make_monitor();

    // Two owner frames in the same slot/cycle: both discarded, charged to owner.
    m.begin_cycle(0);
    auto first = m.inspect(make_frame(10, A, 0));
    auto second = m.inspect(make_frame(10, A, 0));
    m.end_cycle();
    CHECK(m.verdict(first).decision == Decision::Dropped);
    CHECK(m.verdict(first).reason == DropReason::Conflict);
    CHECK(m.verdict(second).decision == Decision::Dropped);
    CHECK(m.verdict(second).reason == DropReason::Conflict);
    CHECK(m.slot_counters(10).conflict_events == 1);
    CHECK(m.node_counters(A).conflicts_charged == 1);
    CHECK(m.node_counters(A).frames_dropped == 2);

    // Mixed conflict: owner frame + non-owner frame; both discarded, the
    // non-owner still pays its violation, the conflict is charged to the owner.
    m.begin_cycle(1);
    auto owner_frame = m.inspect(make_frame(10, A, 1));
    auto intruder = m.inspect(make_frame(10, B, 1));
    m.end_cycle();
    CHECK(m.verdict(owner_frame).decision == Decision::Dropped);
    CHECK(m.verdict(owner_frame).reason == DropReason::Conflict);
    CHECK(m.verdict(intruder).decision == Decision::Dropped);
    CHECK(m.verdict(intruder).reason == DropReason::NotOwner);
    CHECK(m.node_counters(B).violations == 1);
    CHECK(m.slot_counters(10).conflict_events == 2);
    CHECK(m.node_counters(A).conflicts_charged == 2);
}

void test_sync_bit_policy() {
    auto m = make_monitor();

    // Non-registered node C asserts the sync bit: rejected, sync offense only.
    m.begin_cycle(2);  // cycle 2 keeps slot 30 live, but sync check fires first
    auto rogue_sync = m.inspect(make_frame(30, C, 2, true));
    m.end_cycle();
    CHECK(m.verdict(rogue_sync).decision == Decision::Dropped);
    CHECK(m.verdict(rogue_sync).reason == DropReason::SyncNotRegistered);
    CHECK(m.node_counters(C).sync_offenses == 1);
    CHECK(m.node_counters(C).violations == 0);
    CHECK(!m.is_isolated(C));

    // Registered sync node A with a stamped cycle count != global cycle.
    m.begin_cycle(3);
    auto stale_sync = m.inspect(make_frame(10, A, 2, true));
    m.end_cycle();
    CHECK(m.verdict(stale_sync).decision == Decision::Dropped);
    CHECK(m.verdict(stale_sync).reason == DropReason::SyncCycleMismatch);
    CHECK(m.node_counters(A).sync_offenses == 1);

    // Registered sync node with matching cycle count: accepted.
    m.begin_cycle(4);
    auto good_sync = m.inspect(make_frame(10, A, 4, true));
    m.end_cycle();
    CHECK(m.verdict(good_sync).decision == Decision::Accepted);
}

void test_isolation_threshold() {
    auto m = make_monitor();

    // B borrows A's slot 10 (live every cycle) three times.
    for (int cycle = 0; cycle < 3; ++cycle) {
        m.begin_cycle(cycle);
        auto id = m.inspect(make_frame(10, B, cycle));
        m.end_cycle();
        CHECK(m.verdict(id).reason == DropReason::NotOwner);
    }
    CHECK(m.node_counters(B).violations == 3);
    CHECK(m.is_isolated(B));

    // After the third strike, every later frame from B is isolated, including
    // a legitimate frame on B's own slot 20 at an in-phase (even) cycle.
    m.begin_cycle(4);
    auto quarantined = m.inspect(make_frame(20, B, 4));
    m.end_cycle();
    CHECK(m.verdict(quarantined).decision == Decision::Dropped);
    CHECK(m.verdict(quarantined).reason == DropReason::Isolated);
    CHECK(m.totals().isolated_frames == 1);
    CHECK(m.node_counters(B).violations == 3);  // no further strikes accrue
}

}  // namespace

int main() {
    test_owner_accepted_and_borrowed_slot();
    test_repetition_wrong_phase();
    test_same_slot_double_frame_conflict();
    test_sync_bit_policy();
    test_isolation_threshold();

    if (g_failures == 0) {
        std::printf("all slot-ownership tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", g_failures);
    return EXIT_FAILURE;
}

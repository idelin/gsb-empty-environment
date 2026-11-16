#include "slot_ownership.hpp"

#include <cstdio>
#include <cstdlib>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                            \
    do {                                                                       \
        if (!(cond)) {                                                         \
            std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond);       \
            ++g_failures;                                                      \
        }                                                                      \
    } while (0)

FrameHeader makeHeader(uint16_t frameId,
                       uint8_t cycle,
                       bool sync = false,
                       uint8_t payloadLength = 8) {
    FrameHeader header;
    header.frameId = frameId;
    header.payloadLength = payloadLength;
    header.cycleCount = cycle;
    header.syncIndicator = sync;
    return header;
}

SlotOwnershipGuard makeGuard() {
    // slot 10: ECU_A every cycle
    // slot 20: ECU_B on even cycles (rep 2, base 0)
    // slot 30: ECU_C on cycles 1, 5, 9, ... (rep 4, base 1)
    std::vector<SlotConfig> slots = {
        {10, "ECU_A", 1, 0},
        {20, "ECU_B", 2, 0},
        {30, "ECU_C", 4, 1},
    };
    std::vector<std::pair<PortId, NodeId>> ports = {
        {1, "ECU_A"}, {2, "ECU_B"}, {3, "ECU_C"},
    };
    return SlotOwnershipGuard(std::move(slots), std::move(ports), {"ECU_A"});
}

void testBorrowedSlotIsViolation() {
    SlotOwnershipGuard guard = makeGuard();

    // ECU_B transmits inside ECU_A's slot 10: ownership violation.
    InspectionResult r = guard.inspect(makeHeader(10, 0), 2 /* ECU_B */, 0);
    CHECK(r.action == Action::Drop);
    CHECK(r.reason == DropReason::NonOwnerSender);
    CHECK(guard.violationCount("ECU_B") == 1);
    CHECK(guard.acceptedCount() == 0);

    // The real owner gets through in the next cycle (same cycle would be a
    // collision with the rogue frame).
    InspectionResult ok = guard.inspect(makeHeader(10, 1), 1 /* ECU_A */, 1);
    CHECK(ok.action == Action::Accept);
    CHECK(guard.violationCount("ECU_A") == 0);
}

void testRepetitionPhase() {
    SlotOwnershipGuard guard = makeGuard();

    // slot 20, rep 2 base 0: odd cycle is the wrong phase.
    InspectionResult wrong = guard.inspect(makeHeader(20, 1), 2, 1);
    CHECK(wrong.action == Action::Drop);
    CHECK(wrong.reason == DropReason::WrongCyclePhase);
    CHECK(guard.violationCount("ECU_B") == 0);  // phase error is not a violation

    InspectionResult right = guard.inspect(makeHeader(20, 2), 2, 2);
    CHECK(right.action == Action::Accept);

    // slot 30, rep 4 base 1: active on cycles 1, 5.
    CHECK(guard.inspect(makeHeader(30, 2), 3, 2).reason ==
          DropReason::WrongCyclePhase);
    CHECK(guard.inspect(makeHeader(30, 5), 3, 5).action == Action::Accept);
}

void testSameSlotDoubleFrameConflict() {
    SlotOwnershipGuard guard = makeGuard();

    InspectionResult first = guard.inspect(makeHeader(10, 7), 1, 7);
    CHECK(first.action == Action::Accept);

    // Second frame in the same slot/cycle: collision, both frames dropped.
    InspectionResult second = guard.inspect(makeHeader(10, 7), 1, 7);
    CHECK(second.action == Action::Drop);
    CHECK(second.reason == DropReason::Conflict);

    // The conflict is charged to the node owning the slot (ECU_A).
    CHECK(guard.conflictCount("ECU_A") == 1);
    CHECK(guard.violationCount("ECU_A") == 0);  // conflict != violation

    // The earlier accept is revoked so the bus monitor can purge it.
    std::vector<uint64_t> revoked = guard.endCycle();
    CHECK(revoked.size() == 1);
    CHECK(revoked[0] == first.sequence);

    // Per-cycle state resets: slot 10 works again in the next cycle.
    InspectionResult next = guard.inspect(makeHeader(10, 8), 1, 8);
    CHECK(next.action == Action::Accept);
    CHECK(guard.drainRevokedSequences().empty());

    // A collision is defined by slot+cycle only: a non-owner second frame
    // conflicts with the owner's accepted frame and no extra violation is
    // counted for the second frame itself.
    InspectionResult ownerFirst = guard.inspect(makeHeader(10, 9), 1, 9);
    CHECK(ownerFirst.action == Action::Accept);
    InspectionResult rogueSecond = guard.inspect(makeHeader(10, 9), 2, 9);
    CHECK(rogueSecond.action == Action::Drop);
    CHECK(rogueSecond.reason == DropReason::Conflict);
    CHECK(guard.conflictCount("ECU_A") == 2);
    CHECK(guard.violationCount("ECU_B") == 0);
    std::vector<uint64_t> revokedMixed = guard.endCycle();
    CHECK(revokedMixed.size() == 1);
    CHECK(revokedMixed[0] == ownerFirst.sequence);
}

void testSyncRules() {
    SlotOwnershipGuard guard = makeGuard();

    // Non-sync node ECU_B sets the sync indicator.
    InspectionResult rogue = guard.inspect(makeHeader(20, 2, true), 2, 2);
    CHECK(rogue.action == Action::Drop);
    CHECK(rogue.reason == DropReason::SyncNotRegistered);
    CHECK(guard.violationCount("ECU_B") == 1);

    // Registered sync node but cycleCount disagrees with the global cycle.
    InspectionResult stale = guard.inspect(makeHeader(10, 2, true), 1, 3);
    CHECK(stale.action == Action::Drop);
    CHECK(stale.reason == DropReason::SyncCycleMismatch);
    CHECK(guard.violationCount("ECU_A") == 1);

    // Well-formed sync frame from the registered sync node passes.
    InspectionResult good = guard.inspect(makeHeader(10, 3, true), 1, 3);
    CHECK(good.action == Action::Accept);
}

void testQuarantineThreshold() {
    SlotOwnershipGuard guard = makeGuard();

    // Three ownership violations across distinct cycles (a second frame in
    // the same slot+cycle would be a conflict instead).
    for (uint8_t cycle = 0; cycle < kViolationQuarantineThreshold; ++cycle) {
        InspectionResult r =
            guard.inspect(makeHeader(10, cycle), 2, cycle);
        CHECK(r.reason == DropReason::NonOwnerSender);
    }
    CHECK(guard.violationCount("ECU_B") == 3);
    CHECK(guard.isQuarantined("ECU_B"));

    // Every later frame from ECU_B is quarantined, even its own valid slot.
    InspectionResult locked = guard.inspect(makeHeader(20, 3), 2, 3);
    CHECK(locked.action == Action::Drop);
    CHECK(locked.reason == DropReason::Quarantined);
    CHECK(guard.violationCount("ECU_B") == 3);  // no further counting

    // Other nodes are unaffected.
    CHECK(guard.inspect(makeHeader(10, 3), 1, 3).action == Action::Accept);
    CHECK(!guard.isQuarantined("ECU_A"));
}

void testMalformedAndUnknownInputs() {
    SlotOwnershipGuard guard = makeGuard();

    CHECK(guard.inspect(makeHeader(0, 0), 1, 0).reason ==
          DropReason::MalformedHeader);
    FrameHeader badId = makeHeader(1, 0);
    badId.frameId = kMaxFrameId + 1;
    CHECK(guard.inspect(badId, 1, 0).reason == DropReason::MalformedHeader);
    FrameHeader badCycle = makeHeader(10, 0);
    badCycle.cycleCount = kMaxCycleCount + 1;
    CHECK(guard.inspect(badCycle, 1, 0).reason == DropReason::MalformedHeader);
    FrameHeader badLen = makeHeader(10, 0);
    badLen.payloadLength = 0;
    CHECK(guard.inspect(badLen, 1, 0).reason == DropReason::MalformedHeader);

    CHECK(guard.inspect(makeHeader(10, 0), 99, 0).reason ==
          DropReason::UnknownPort);
    CHECK(guard.inspect(makeHeader(99, 0), 1, 0).reason ==
          DropReason::UnknownSlot);
}

void testCycleCounterWrapsAt64() {
    SlotOwnershipGuard guard = makeGuard();

    // slot 20 rep 2 base 0: even cycles active across the 63->0 wrap.
    CHECK(guard.inspect(makeHeader(20, 62), 2, 62).action == Action::Accept);
    CHECK(guard.inspect(makeHeader(20, 63), 2, 63).reason ==
          DropReason::WrongCyclePhase);
    InspectionResult wrapped = guard.inspect(makeHeader(20, 0), 2, 0);
    CHECK(wrapped.action == Action::Accept);

    // Sync cycle count is matched against the wrapped global cycle as well.
    CHECK(guard.inspect(makeHeader(10, 0, true), 1, 0).action == Action::Accept);
}

}  // namespace

int main() {
    testBorrowedSlotIsViolation();
    testRepetitionPhase();
    testSameSlotDoubleFrameConflict();
    testSyncRules();
    testQuarantineThreshold();
    testMalformedAndUnknownInputs();
    testCycleCounterWrapsAt64();

    if (g_failures == 0) {
        std::printf("slot_ownership: all checks passed\n");
        return EXIT_SUCCESS;
    }
    std::printf("slot_ownership: %d check(s) failed\n", g_failures);
    return EXIT_FAILURE;
}

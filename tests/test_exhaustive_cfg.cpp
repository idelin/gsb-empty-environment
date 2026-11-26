#include "pacbti/analyzer.hpp"

#include <set>

#include "test_util.hpp"

using namespace pacbti;

namespace {

constexpr std::uint16_t kType = 50u;
constexpr std::uint16_t kSlot = 21u;

ControlFlowGraph diamondCfg(bool taintedArm, bool cleanArm) {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock head;
    head.id = 0;
    head.hasNext = false;
    head.instructions = {
            build::loadRaw(0u, kType, "base_pointer"),
            build::condBranch(1u, 2u, "condition"),
    };

    BasicBlock tainted;
    tainted.id = 1;
    tainted.hasNext = true;
    tainted.next = 3;
    if (taintedArm) {
        tainted.instructions = {build::markTainted(0u, "attacker_arm")};
    } else {
        tainted.instructions = {build::fallthrough("clean-arm")};
    }

    BasicBlock clean;
    clean.id = 2;
    clean.hasNext = true;
    clean.next = 3;
    if (cleanArm) {
        clean.instructions = {build::fallthrough("clean-arm")};
    } else {
        clean.instructions = {build::markTainted(0u, "other-attacker-arm")};
    }

    BasicBlock sink;
    sink.id = 3;
    sink.hasNext = false;
    sink.instructions = {
            build::pacSign(1u, 0u, 2u, KeyDomain::IA, kType, "join-sign"),
            build::extWrite(1u, "observable_sink"),
    };

    cfg.blocks = {head, tainted, clean, sink};
    return cfg;
}

struct PathState {
    bool tainted = false;
    bool signedVisible = false;

    bool operator<(const PathState& other) const {
        if (tainted != other.tainted) {
            return tainted < other.tainted;
        }
        return signedVisible < other.signedVisible;
    }
};

std::set<PathState> enumerateDiamondPaths(bool taintedArm, bool cleanArm) {
    std::set<PathState> results;
    for (bool takeTaintedArm : {false, true}) {
        bool tainted = takeTaintedArm ? taintedArm : !cleanArm;
        PathState state;
        state.tainted = tainted;
        state.signedVisible = true;
        results.insert(state);
    }
    return results;
}

ControlFlowGraph loopCfg() {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock entry;
    entry.id = 0;
    entry.hasNext = true;
    entry.next = 1;
    entry.instructions = {
            build::loadRaw(0u, kType, "loop_carried_pointer"),
            build::stackStore(0u, kSlot, "frame_slot"),
    };

    BasicBlock loopHead;
    loopHead.id = 1;
    loopHead.hasNext = false;
    loopHead.instructions = {
            build::stackLoad(0u, kSlot, "reload"),
            build::markTainted(0u, "incremental_taint"),
            build::stackStore(0u, kSlot, "writeback"),
            build::condBranch(1u, 2u, "loop_back_edge"),
    };

    BasicBlock exitBlock;
    exitBlock.id = 2;
    exitBlock.hasNext = false;
    exitBlock.instructions = {
            build::stackLoad(3u, kSlot, "exit_reload"),
            build::pacSign(4u, 3u, 5u, KeyDomain::DB, kType, "pacdb-exit"),
            build::extWrite(4u, "loop_observable_sink"),
    };

    cfg.blocks = {entry, loopHead, exitBlock};
    return cfg;
}

}  // namespace

TEST_CASE("exhaustive.diamond_may_taint_matches_path_enumeration") {
    const bool combinations[4][2] = {
            {true, true}, {true, false}, {false, true}, {false, false}};
    for (const auto& combo : combinations) {
        bool taintedArm = combo[0];
        bool cleanArm = combo[1];
        ControlFlowGraph cfg = diamondCfg(taintedArm, cleanArm);
        Analyzer analyzer(cfg);
        AnalysisReport report = analyzer.run();

        std::set<PathState> reference =
                enumerateDiamondPaths(taintedArm, cleanArm);
        bool referenceTainted = false;
        for (const PathState& state : reference) {
            referenceTainted = referenceTainted || state.tainted;
        }

        if (referenceTainted) {
            CHECK(report.hasKind(FindingKind::SigningOracle));
        } else {
            CHECK(!report.hasKind(FindingKind::SigningOracle));
        }

        const Label& joined = analyzer.blockEntry(3u).getReg(0u);
        CHECK(joined.isRaw());
        CHECK_EQ(joined.attackerTainted, referenceTainted);
    }
}

TEST_CASE("exhaustive.loop_reaches_fixpoint_and_keeps_taint_at_exit") {
    ControlFlowGraph cfg = loopCfg();
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();

    CHECK(report.fixpointIterations >= 3u);
    CHECK(report.hasKind(FindingKind::SigningOracle));
    const Label& exitLabel = analyzer.blockExit(2u).getReg(4u);
    CHECK(exitLabel.isSigned());
    CHECK_EQ(exitLabel.key, KeyDomain::DB);
    CHECK(exitLabel.attackerTainted);
}

TEST_CASE("exhaustive.cross_block_stack_slot_preserves_signed_label") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock producer;
    producer.id = 0;
    producer.hasNext = true;
    producer.next = 1;
    producer.instructions = {
            build::loadRaw(0u, kType, "raw"),
            build::pacSign(0u, 0u, 1u, KeyDomain::IA, kType, "pacia"),
            build::stackStore(0u, kSlot, "spill"),
    };

    BasicBlock consumer;
    consumer.id = 1;
    consumer.hasNext = false;
    consumer.instructions = {
            build::stackLoad(2u, kSlot, "reload"),
            build::indirectBranch(2u, KeyDomain::IA, false, kInvalidType,
                                  "braa"),
    };

    cfg.blocks = {producer, consumer};
    cfg.types = {FunctionPointerType{kType, "callback", {0x7000u}}};
    cfg.codePoints = {CodePoint{0x7000u, BtiKind::BtiC, true}};

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::AuthBypassXpac));
    CHECK(!report.hasKind(FindingKind::PoisonedBranch));
    CHECK(!report.hasKind(FindingKind::MissingBtiLanding));
    CHECK(analyzer.blockEntry(1u).getStack(kSlot).isSigned());
}

TEST_CASE("exhaustive.reachable_block_states_are_available_for_inspection") {
    ControlFlowGraph cfg = loopCfg();
    Analyzer analyzer(cfg);
    analyzer.run();
    for (std::uint32_t blockId = 0;
         blockId < static_cast<std::uint32_t>(cfg.blocks.size());
         ++blockId) {
        const AbstractState& entry = analyzer.blockEntry(blockId);
        const AbstractState& exit = analyzer.blockExit(blockId);
        CHECK(!entry.regs.empty());
        CHECK(!exit.regs.empty());
    }
}

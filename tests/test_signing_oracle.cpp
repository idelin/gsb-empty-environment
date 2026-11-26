#include "pacbti/analyzer.hpp"

#include "test_util.hpp"

using namespace pacbti;

namespace {

constexpr std::uint8_t kAttacker = 0u;
constexpr std::uint8_t kModifier = 1u;
constexpr std::uint8_t kSigned = 2u;
constexpr std::uint16_t kSlot = 16u;
constexpr std::uint16_t kOpaqueType = 20u;

ControlFlowGraph oracleCfg(bool taintedPointer, bool taintedModifier,
                           bool expose) {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock block;
    block.id = 0;
    block.hasNext = false;

    if (taintedPointer) {
        block.instructions.push_back(build::input(kAttacker, "attacker_ptr"));
    } else {
        block.instructions.push_back(
                build::loadRaw(kAttacker, kOpaqueType, "trusted_ptr"));
    }

    if (taintedModifier) {
        block.instructions.push_back(build::input(kModifier, "attacker_mod"));
    }

    block.instructions.push_back(
            build::pacSign(kSigned, kAttacker, kModifier, KeyDomain::IA,
                           kOpaqueType, "pacia-oracle"));

    if (expose) {
        block.instructions.push_back(
                build::extWrite(kSigned, "shared_ring_buffer"));
        block.instructions.push_back(
                build::stackStore(kSigned, kSlot, "spill-signed"));
    }

    cfg.blocks.push_back(block);
    return cfg;
}

}  // namespace

TEST_CASE("oracle.tainted_pointer_signed_and_exposed_is_flagged") {
    ControlFlowGraph cfg = oracleCfg(true, false, true);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::SigningOracle), 2u);
    const Finding& finding =
            *report.ofKind(FindingKind::SigningOracle).front();
    CHECK_EQ(finding.trace[0].instructionName, std::string("pacia-oracle"));
    CHECK_EQ(finding.key, KeyDomain::IA);
}

TEST_CASE("oracle.tainted_modifier_alone_is_still_an_oracle") {
    ControlFlowGraph cfg = oracleCfg(false, true, true);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(report.hasKind(FindingKind::SigningOracle));
}

TEST_CASE("oracle.signed_value_never_leaving_the_frame_is_not_flagged") {
    ControlFlowGraph cfg = oracleCfg(true, false, false);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::SigningOracle));
}

TEST_CASE("oracle.untainted_signing_path_is_clean") {
    ControlFlowGraph cfg = oracleCfg(false, false, true);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::SigningOracle));
}

TEST_CASE("oracle.taint_flows_through_stack_and_move_before_signing") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::input(0u, "attacker_input"),
            build::stackStore(0u, kSlot, "save_input"),
            build::stackLoad(3u, kSlot, "reload_input"),
            build::mov(4u, 3u, "copy_input"),
            build::pacSign(5u, 4u, kModifier, KeyDomain::DA, kOpaqueType,
                           "pacda-oracle"),
            build::extWrite(5u, "device_mmio"),
    };
    cfg.blocks.push_back(block);

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(report.hasKind(FindingKind::SigningOracle));
    CHECK_EQ(report.ofKind(FindingKind::SigningOracle).front()->key,
              KeyDomain::DA);
}

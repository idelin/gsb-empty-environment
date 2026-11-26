#include "pacbti/analyzer.hpp"

#include "test_util.hpp"

using namespace pacbti;

namespace {

constexpr std::uint16_t kVtableType = 30u;
constexpr std::uint16_t kReturnType = 31u;
constexpr std::uint16_t kSlot = 12u;

ControlFlowGraph conflictCfg(bool sharedModifier, bool sameType) {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::loadRaw(1u, kVtableType, "vtable_slot"),
            build::loadRaw(2u, sameType ? kVtableType : kReturnType,
                           "return_slot"),
            build::loadRaw(3u, kInvalidType, "context_modifier"),
    };

    if (sharedModifier) {
        block.instructions.push_back(
                build::pacSign(4u, 1u, 3u, KeyDomain::IA, kVtableType,
                               "sign-vtable"));
        block.instructions.push_back(
                build::pacSign(5u, 2u, 3u, KeyDomain::IA,
                               sameType ? kVtableType : kReturnType,
                               "sign-return"));
    } else {
        block.instructions.push_back(
                build::loadRaw(6u, kInvalidType, "sp_modifier"));
        block.instructions.push_back(
                build::pacSign(4u, 1u, 3u, KeyDomain::IA, kVtableType,
                               "sign-vtable"));
        block.instructions.push_back(
                build::pacSign(5u, 2u, 6u, KeyDomain::IA,
                               sameType ? kVtableType : kReturnType,
                               "sign-return-distinct-mod"));
    }

    block.instructions.push_back(
            build::stackStore(4u, kSlot, "spill-vtable"));
    cfg.blocks.push_back(block);
    return cfg;
}

}  // namespace

TEST_CASE("conflict.distinct_types_sharing_modifier_are_flagged") {
    ControlFlowGraph cfg = conflictCfg(true, false);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::ModifierTypeConflict), 1u);

    const Finding& finding =
            *report.ofKind(FindingKind::ModifierTypeConflict).front();
    CHECK_EQ(finding.key, KeyDomain::IA);
    CHECK_EQ(finding.trace.size(), 2u);
    CHECK_EQ(finding.trace[0].instructionName, std::string("sign-vtable"));
    CHECK_EQ(finding.trace[1].instructionName, std::string("sign-return"));
}

TEST_CASE("conflict.same_type_reusing_modifier_is_allowed") {
    ControlFlowGraph cfg = conflictCfg(true, true);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::ModifierTypeConflict));
}

TEST_CASE("conflict.distinct_modifiers_even_for_distinct_types_are_allowed") {
    ControlFlowGraph cfg = conflictCfg(false, false);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::ModifierTypeConflict));
}

TEST_CASE("conflict.different_keys_with_same_modifier_are_separate_contexts") {
    ControlFlowGraph cfg = conflictCfg(true, false);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(report.hasKind(FindingKind::ModifierTypeConflict));

    ControlFlowGraph keySplit = conflictCfg(true, false);
    keySplit.blocks[0].instructions[3].key = KeyDomain::IB;
    keySplit.blocks[0].instructions[4].key = KeyDomain::IA;
    Analyzer splitAnalyzer(keySplit);
    AnalysisReport splitReport = splitAnalyzer.run();
    CHECK(!splitReport.hasKind(FindingKind::ModifierTypeConflict));
}

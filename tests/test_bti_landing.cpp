#include "pacbti/analyzer.hpp"

#include "test_util.hpp"

using namespace pacbti;

namespace {

constexpr std::uint16_t kJumpType = 40u;
constexpr std::uint16_t kCallType = 41u;
constexpr std::uint16_t kMixedType = 42u;
constexpr std::uint16_t kUnguardedType = 43u;

struct LandingCase {
    std::uint16_t typeId;
    std::uint64_t address;
    BtiKind landing;
    bool guarded;
};

ControlFlowGraph btiCfg(const std::vector<LandingCase>& cases,
                        std::uint16_t branchType,
                        bool call,
                        KeyDomain authKey) {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::loadRaw(0u, branchType, "load_function_pointer"),
            build::indirectBranch(0u, authKey, call, kInvalidType,
                                  call ? "blr" : "br"),
    };
    cfg.blocks.push_back(block);

    for (const LandingCase& testCase : cases) {
        FunctionPointerType type;
        type.id = testCase.typeId;
        type.name = "type-" + std::to_string(testCase.typeId);
        type.targets = {testCase.address};
        cfg.types.push_back(type);
        cfg.codePoints.push_back(CodePoint{testCase.address, testCase.landing,
                                           testCase.guarded});
    }
    return cfg;
}

}  // namespace

TEST_CASE("bti.guarded_jump_requires_c_or_jc_landing") {
    ControlFlowGraph good = btiCfg(
            {LandingCase{kJumpType, 0x2000u, BtiKind::BtiC, true}},
            kJumpType, false, KeyDomain::None);
    Analyzer goodAnalyzer(good);
    CHECK(!goodAnalyzer.run().hasKind(FindingKind::MissingBtiLanding));

    ControlFlowGraph bad = btiCfg(
            {LandingCase{kJumpType, 0x2000u, BtiKind::BtiJ, true}},
            kJumpType, false, KeyDomain::None);
    Analyzer badAnalyzer(bad);
    AnalysisReport badReport = badAnalyzer.run();
    CHECK_EQ(badReport.count(FindingKind::MissingBtiLanding), 1u);
    CHECK_EQ(badReport.ofKind(FindingKind::MissingBtiLanding)
                     .front()
                     ->targetAddress,
              0x2000u);
}

TEST_CASE("bti.guarded_call_requires_j_or_jc_landing") {
    ControlFlowGraph good = btiCfg(
            {LandingCase{kCallType, 0x3000u, BtiKind::BtiJc, true}},
            kCallType, true, KeyDomain::None);
    Analyzer goodAnalyzer(good);
    CHECK(!goodAnalyzer.run().hasKind(FindingKind::MissingBtiLanding));

    ControlFlowGraph bad = btiCfg(
            {LandingCase{kCallType, 0x3000u, BtiKind::BtiC, true}},
            kCallType, true, KeyDomain::None);
    Analyzer badAnalyzer(bad);
    CHECK(badAnalyzer.run().hasKind(FindingKind::MissingBtiLanding));
}

TEST_CASE("bti.unguarded_page_is_exempt_from_landing_check") {
    ControlFlowGraph cfg = btiCfg(
            {LandingCase{kUnguardedType, 0x4000u, BtiKind::None, false}},
            kUnguardedType, false, KeyDomain::None);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::MissingBtiLanding));
}

TEST_CASE("bti.authenticated_branch_enforces_landing_after_auth") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::loadRaw(0u, kCallType, "raw"),
            build::pacSign(0u, 0u, 1u, KeyDomain::IB, kCallType, "pacib"),
            build::indirectBranch(0u, KeyDomain::IB, true, kInvalidType,
                                  "blraa"),
    };
    cfg.blocks.push_back(block);
    cfg.types = {FunctionPointerType{kCallType, "call", {0x5000u}}};
    cfg.codePoints = {CodePoint{0x5000u, BtiKind::BtiC, true}};

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::MissingBtiLanding), 1u);
}

TEST_CASE("bti.all_reachable_targets_are_enumerated_independently") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::loadRaw(0u, kMixedType, "raw"),
            build::indirectBranch(0u, KeyDomain::None, false, kInvalidType,
                                  "br"),
    };
    cfg.blocks.push_back(block);
    cfg.types = {FunctionPointerType{
            kMixedType, "mixed", {0x6000u, 0x6010u, 0x6020u}}};
    cfg.codePoints = {
            CodePoint{0x6000u, BtiKind::BtiC, true},
            CodePoint{0x6010u, BtiKind::BtiJ, true},
            CodePoint{0x6020u, BtiKind::BtiJc, true},
    };

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::MissingBtiLanding), 1u);
    CHECK_EQ(report.ofKind(FindingKind::MissingBtiLanding)
                     .front()
                     ->targetAddress,
              0x6010u);
}

TEST_CASE("bti.untyped_branch_is_reported_as_unbounded") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::input(0u, "attacker_controlled_register"),
            build::indirectBranch(0u, KeyDomain::None, false, kInvalidType,
                                  "br"),
    };
    cfg.blocks.push_back(block);

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::UntypedIndirectBranch), 1u);
    CHECK(!report.hasKind(FindingKind::MissingBtiLanding));
}

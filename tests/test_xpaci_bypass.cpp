#include "pacbti/analyzer.hpp"

#include "test_util.hpp"

using namespace pacbti;

namespace {

constexpr std::uint8_t kPtr = 0u;
constexpr std::uint8_t kMod = 1u;
constexpr std::uint16_t kCallbackType = 10u;

ControlFlowGraph bypassCfg(bool useXpac, bool wrongKey) {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock block0;
    block0.id = 0;
    block0.hasNext = true;
    block0.next = 1;
    block0.instructions = {
            build::loadRaw(kPtr, kCallbackType, "raw_callback"),
            build::pacSign(kPtr, kPtr, kMod, KeyDomain::IA, kCallbackType,
                           "paciasp-sign"),
    };

    BasicBlock block1;
    block1.id = 1;
    block1.hasNext = true;
    block1.next = 2;
    KeyDomain authKey = wrongKey ? KeyDomain::IB : KeyDomain::IA;
    block1.instructions = {
            build::pacAuth(2u, kPtr, kMod, authKey, kCallbackType, "autia"),
    };

    BasicBlock block2;
    block2.id = 2;
    block2.hasNext = true;
    block2.next = 3;
    if (useXpac) {
        block2.instructions = {
                build::xpac(3u, 2u, KeyDomain::IA, "xpaci-strip"),
        };
    } else {
        block2.instructions = {
                build::mov(3u, 2u, "carry-poison"),
        };
    }

    BasicBlock block3;
    block3.id = 3;
    block3.hasNext = false;
    block3.instructions = {
            build::indirectBranch(3u, KeyDomain::None, false, kInvalidType,
                                  "br-unauthenticated"),
    };

    cfg.blocks = {block0, block1, block2, block3};
    cfg.types = {FunctionPointerType{kCallbackType, "callback", {0x1000u}}};
    cfg.codePoints = {CodePoint{0x1000u, BtiKind::BtiC, true}};
    return cfg;
}

}  // namespace

TEST_CASE("xpaci.authentication_poison_stripped_then_branched_is_flagged") {
    ControlFlowGraph cfg = bypassCfg(true, true);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::AuthBypassXpac), 1u);
    CHECK(!report.hasKind(FindingKind::PoisonedBranch));

    const Finding& finding =
            *report.ofKind(FindingKind::AuthBypassXpac).front();
    CHECK_EQ(finding.trace.size(), 2u);
    CHECK_EQ(finding.trace[0].instructionName, std::string("autia"));
    CHECK_EQ(finding.trace[1].instructionName, std::string("xpaci-strip"));
}

TEST_CASE("xpaci.poison_without_strip_is_reported_as_poisoned_branch") {
    ControlFlowGraph cfg = bypassCfg(false, true);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::PoisonedBranch), 1u);
    CHECK(!report.hasKind(FindingKind::AuthBypassXpac));
}

TEST_CASE("xpaci.successful_auth_then_xpaci_branch_is_clean") {
    ControlFlowGraph cfg = bypassCfg(true, false);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::AuthBypassXpac));
    CHECK(!report.hasKind(FindingKind::PoisonedBranch));
    CHECK_EQ(analyzer.blockExit(2u).getReg(3u).kind, LabelKind::Raw);
}

TEST_CASE("retaa.authentication_failure_faults_without_bypass_finding") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;

    BasicBlock signBlock;
    signBlock.id = 0;
    signBlock.hasNext = true;
    signBlock.next = 1;
    signBlock.instructions = {
            build::loadRaw(0u, kCallbackType, "raw_return_address"),
            build::pacSign(0u, 0u, 1u, KeyDomain::IA, kCallbackType,
                           "pacia-return"),
    };

    BasicBlock failBlock;
    failBlock.id = 1;
    failBlock.hasNext = true;
    failBlock.next = 2;
    failBlock.instructions = {
            build::pacAuth(2u, 0u, 1u, KeyDomain::IB, kCallbackType,
                           "wrong-key-autib"),
    };

    BasicBlock returnBlock;
    returnBlock.id = 2;
    returnBlock.hasNext = false;
    returnBlock.instructions = {
            build::authenticatedReturn(2u, KeyDomain::IA, "retaa"),
    };

    cfg.blocks = {signBlock, failBlock, returnBlock};
    cfg.types = {FunctionPointerType{kCallbackType, "return", {0x9000u}}};
    cfg.codePoints = {CodePoint{0x9000u, BtiKind::BtiC, true}};

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::AuthBypassXpac));
    CHECK(!report.hasKind(FindingKind::PoisonedBranch));
    CHECK(!report.hasKind(FindingKind::MissingBtiLanding));
    CHECK(analyzer.blockExit(1u).getReg(2u).isPoisoned());
}

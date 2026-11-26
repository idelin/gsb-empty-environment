#include "pacbti/analyzer.hpp"

#include "test_util.hpp"

using namespace pacbti;

namespace {

ControlFlowGraph keyDomainCfg(KeyDomain signKey, KeyDomain authKey) {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::loadRaw(0u, 60u, "data_pointer"),
            build::pacSign(1u, 0u, 2u, signKey, 60u, "pac-data"),
            build::pacAuth(3u, 1u, 2u, authKey, 60u, "aut-data"),
            build::indirectBranch(3u, KeyDomain::None, false, kInvalidType,
                                  "br"),
    };
    cfg.blocks.push_back(block);
    cfg.types = {FunctionPointerType{60u, "data", {0xA000u}}};
    cfg.codePoints = {CodePoint{0xA000u, BtiKind::BtiC, true}};
    return cfg;
}

}  // namespace

TEST_CASE("keys.matching_da_signature_authenticates_cleanly") {
    ControlFlowGraph cfg = keyDomainCfg(KeyDomain::DA, KeyDomain::DA);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::PoisonedBranch));
    CHECK(!report.hasKind(FindingKind::AuthBypassXpac));
    CHECK(analyzer.blockExit(0u).getReg(3u).isRaw());
}

TEST_CASE("keys.db_signed_value_failing_da_auth_poisons_branch") {
    ControlFlowGraph cfg = keyDomainCfg(KeyDomain::DB, KeyDomain::DA);
    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK_EQ(report.count(FindingKind::PoisonedBranch), 1u);
    CHECK(!report.hasKind(FindingKind::AuthBypassXpac));
}

TEST_CASE("keys.conflicts_are_scoped_per_key_domain") {
    ControlFlowGraph cfg;
    cfg.entryBlock = 0;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::loadRaw(0u, 61u, "ptr_a"),
            build::loadRaw(1u, 62u, "ptr_b"),
            build::loadRaw(2u, kInvalidType, "shared_modifier"),
            build::pacSign(3u, 0u, 2u, KeyDomain::DA, 61u, "pacda-a"),
            build::pacSign(4u, 1u, 2u, KeyDomain::DB, 62u, "pacdb-b"),
    };
    cfg.blocks.push_back(block);

    Analyzer analyzer(cfg);
    AnalysisReport report = analyzer.run();
    CHECK(!report.hasKind(FindingKind::ModifierTypeConflict));
}

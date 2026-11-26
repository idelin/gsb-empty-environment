#include "pacbti/pacbti.hpp"

#include <cstdio>

int main() {
    using namespace pacbti;
    ControlFlowGraph cfg;
    BasicBlock block;
    block.id = 0;
    block.hasNext = false;
    block.instructions = {
            build::input(0, "attacker"),
            build::pacSign(1, 0, 2, KeyDomain::IA, 10, "pacia"),
            build::extWrite(1, "shared_buffer"),
    };
    cfg.blocks.push_back(block);

    AnalysisReport report = Analyzer(cfg).run();
    std::printf("findings=%zu iterations=%u\n", report.findings.size(),
                report.fixpointIterations);
    return report.hasKind(FindingKind::SigningOracle) ? 0 : 2;
}

#include "ept_nested.hpp"

#include <cstdio>

using namespace eptn;

int main() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());

    const Permissions p01{true, true, true, false};
    const Permissions p12{true, true, false, true};
    if (ept01.map(0x1000, 0x2000, p01, PageKind::Small) != MapError::Ok ||
        ept12.map(0x3000, 0x1000, p12, PageKind::Small) != MapError::Ok) {
        std::fprintf(stderr, "demo mapping failed\n");
        return 2;
    }

    ComposeResult composed = composeExact(ept01, ept12);
    if (!composed.ok) {
        std::fprintf(stderr, "composition failed: %s\n",
                     composed.message.c_str());
        return 2;
    }

    WalkResult walk = composed.ept02.walk(0x3000);
    const WitnessStep& pte =
        walk.witness.steps[walk.witness.count - 1U];
    composed.ept02.rawEntry(pte.node_id, pte.index) |=
        composed.ept02.geometry().bit_x_usr;

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02, VerifyOptions::securityOnly());

    if (report.securityOk() || report.lowest_gpa != 0x3000) {
        std::fprintf(stderr, "demo failed to detect injected escalation\n");
        return 1;
    }

    std::printf("Detected EPT02 security violation: %s at %s\n",
                violationKindName(report.violations.front().kind),
                hex(report.lowest_gpa).c_str());
    std::printf("%s\n", formatWitnessBundle(report.lowest_witness).c_str());

    ExhaustiveReport exhaustive = run16BitExhaustiveCrossCheck(32);
    std::printf("Mini16 exhaustive: cases=%u pages=%llu bytes=%llu ok=%d\n",
                exhaustive.cases,
                static_cast<unsigned long long>(exhaustive.pages_checked),
                static_cast<unsigned long long>(exhaustive.byte_checks),
                exhaustive.ok ? 1 : 0);
    return exhaustive.ok ? 0 : 1;
}

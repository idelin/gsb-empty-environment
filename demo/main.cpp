// End-to-end demonstration of the bounded adversarial simulator.  Run it to
// watch the same two attacks compromise the double-fetch handler while the
// single-snapshot marshal engine keeps the enclave private memory untouched
// across every deterministic interleaving.
#include <cstdio>

#include "ccm/simulator.hpp"

using namespace ccm;

namespace {

void print_report(const char* title, const HandlerReport& report) {
    std::printf("  %-34s accepted=%-3s compromised=%-3s status=%s\n",
                title, report.accepted ? "yes" : "no",
                report.monitor_compromised ? "yes" : "no",
                status_name(report.library_status));
}

int run_scenario(AttackKind attack, const char* name) {
    std::printf("\n[scenario] %s\n", name);

    SimWorld vulnerable_world;
    vulnerable_world.build_benign_input(2, 8);
    install_attack(vulnerable_world, attack, 0);
    HandlerReport vulnerable =
        run_vulnerable_handler(vulnerable_world);
    print_report("double-fetch handler", vulnerable);

    SimWorld safe_world;
    safe_world.build_benign_input(2, 8);
    install_attack(safe_world, attack, 0);
    HandlerReport safe = run_safe_handler(safe_world);
    print_report("single-snapshot marshal", safe);

    bool expected =
        (attack == AttackKind::None)
            ? (vulnerable.accepted && safe.accepted &&
               !safe.monitor_compromised)
            : (!vulnerable.accepted && vulnerable.monitor_compromised &&
               !safe.monitor_compromised);
    std::printf("  expectation met: %s\n", expected ? "yes" : "NO");
    return expected ? 0 : 1;
}

}  // namespace

int main() {
    int failures = 0;
    failures += run_scenario(AttackKind::None, "benign input");
    failures += run_scenario(AttackKind::LengthDoubleFetch,
                             "length double-fetch TOCTOU");
    failures += run_scenario(AttackKind::PointerSwap,
                             "pointer swap into enclave memory");

    std::printf("\n[randomized] 128 bounded interleavings on the safe path\n");
    int compromises = 0;
    int rejections = 0;
    int acceptances = 0;
    for (std::uint64_t seed = 1; seed <= 128; ++seed) {
        SimWorld world;
        world.build_benign_input(2, 8);
        install_attack(world, AttackKind::RandomTamper, seed);
        HandlerReport report = run_safe_handler(world);
        if (report.monitor_compromised) {
            ++compromises;
        }
        if (report.accepted) {
            ++acceptances;
        } else {
            ++rejections;
        }
    }
    std::printf("  acceptances=%d clean rejections=%d compromises=%d\n",
                acceptances, rejections, compromises);
    if (compromises != 0) {
        ++failures;
    }

    std::printf("\nresult: %s\n",
                failures == 0 ? "ALL SAFETY PROPERTIES HOLD"
                              : "SAFETY PROPERTY VIOLATED");
    return failures == 0 ? 0 : 1;
}

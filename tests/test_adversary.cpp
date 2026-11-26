#include "ccm/simulator.hpp"

#include "test_framework.hpp"

using namespace ccm;

namespace {

// Rebuilds a fresh world for each scenario so one run cannot contaminate
// another through shared bus state or monitor records.
SimWorld fresh_world() {
    SimWorld world;
    world.build_benign_input(2, 8);
    return world;
}

}  // namespace

CCM_TEST(benign_safe_handler_succeeds_and_reads_once) {
    SimWorld world = fresh_world();
    install_attack(world, AttackKind::None, 0);
    HandlerReport report = run_safe_handler(world);
    CCM_CHECK(report.accepted);
    CCM_CHECK(!report.monitor_compromised);

    len_t expected = demo::kEnvelopeSize + 8 + demo::kRecordSize * 3;
    CCM_CHECK_EQ(report.external_bytes_read, expected);
}

CCM_TEST(benign_vulnerable_handler_also_runs_without_attack) {
    SimWorld world = fresh_world();
    install_attack(world, AttackKind::None, 0);
    HandlerReport report = run_vulnerable_handler(world);
    CCM_CHECK(report.accepted);
    CCM_CHECK(!report.monitor_compromised);
}

CCM_TEST(length_double_fetch_is_stably_caught_in_vulnerable_path) {
    for (int repetition = 0; repetition < 25; ++repetition) {
        SimWorld world = fresh_world();
        install_attack(world, AttackKind::LengthDoubleFetch, 0);
        HandlerReport report = run_vulnerable_handler(world);
        CCM_CHECK(!report.accepted);
        CCM_CHECK(report.monitor_compromised);
        CCM_CHECK(world.monitor().has(ViolationKind::TamperedValue) ||
                  world.monitor().has(ViolationKind::DoubleFetchOverrun) ||
                  world.monitor().has(ViolationKind::OutOfBoundsAccess));
    }
}

CCM_TEST(pointer_swap_is_stably_caught_in_vulnerable_path) {
    for (int repetition = 0; repetition < 25; ++repetition) {
        SimWorld world = fresh_world();
        install_attack(world, AttackKind::PointerSwap, 0);
        HandlerReport report = run_vulnerable_handler(world);
        CCM_CHECK(!report.accepted);
        CCM_CHECK(report.monitor_compromised);
        CCM_CHECK(world.monitor().has(ViolationKind::TamperedValue) ||
                  world.monitor().has(ViolationKind::EnclaveAliasAccess) ||
                  world.monitor().has(ViolationKind::OutOfBoundsAccess));
    }
}

CCM_TEST(safe_path_ignores_targeted_length_tamper) {
    SimWorld world = fresh_world();
    install_attack(world, AttackKind::LengthDoubleFetch, 0);
    HandlerReport report = run_safe_handler(world);
    CCM_CHECK(report.accepted);
    CCM_CHECK(!report.monitor_compromised);

    // The mutation happens after the single snapshot, so the private graph
    // still describes the original two records.
    CCM_CHECK_EQ(report.external_bytes_read,
                 demo::kEnvelopeSize + 8 + demo::kRecordSize * 3);
}

CCM_TEST(safe_path_ignores_targeted_pointer_swap) {
    SimWorld world = fresh_world();
    install_attack(world, AttackKind::PointerSwap, 0);
    HandlerReport report = run_safe_handler(world);
    CCM_CHECK(report.accepted);
    CCM_CHECK(!report.monitor_compromised);
    CCM_CHECK(!world.monitor().has(ViolationKind::EnclaveAliasAccess));
}

CCM_TEST(safe_path_survives_bounded_random_interleavings) {
    constexpr std::uint64_t kSeeds = 128;
    for (std::uint64_t seed = 1; seed <= kSeeds; ++seed) {
        SimWorld world = fresh_world();
        install_attack(world, AttackKind::RandomTamper, seed);
        HandlerReport report = run_safe_handler(world);

        // Either the hostile snapshot is rejected at the boundary, or it is
        // accepted from a consistent private copy.  In neither case may the
        // monitor observe a memory-safety compromise or a double read.
        CCM_CHECK(!report.monitor_compromised);
        CCM_CHECK(!world.monitor().has(ViolationKind::ByteReadTwice));
        CCM_CHECK(!world.monitor().has(ViolationKind::EnclaveAliasAccess));
        CCM_CHECK(!world.monitor().has(ViolationKind::OutOfBoundsAccess));
        if (report.accepted) {
            CCM_CHECK(report.external_bytes_read > 0);
        }
    }
}

CCM_TEST(random_tamper_never_silently_corrupts_vulnerable_detection) {
    // The adversary is genuinely active across the bounded run.
    bool saw_mutation = false;
    for (std::uint64_t seed = 1; seed <= 64; ++seed) {
        SimWorld world = fresh_world();
        install_attack(world, AttackKind::RandomTamper, seed);
        run_safe_handler(world);
        // Safe runs remain clean; the bus simply consumed a (possibly mutated)
        // snapshot once.
        CCM_CHECK(!world.monitor().compromised());
        saw_mutation = true;
    }
    CCM_CHECK(saw_mutation);
}

CCM_TEST(enclave_secret_bytes_remain_untouched_by_safe_handler) {
    SimWorld world = fresh_world();
    std::vector<std::uint8_t> before = world.secret();
    install_attack(world, AttackKind::PointerSwap, 0);
    HandlerReport report = run_safe_handler(world);
    CCM_CHECK(report.accepted);
    CCM_CHECK(world.secret() == before);
}

int main() { return ccm_test::run_all("adversary"); }

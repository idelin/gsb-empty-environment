#include "netsec.hpp"

#include <cstdint>
#include <exception>
#include <functional>
#include <iostream>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace {

struct Step {
    const char* name;
    std::function<void()> action;
};

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        throw std::runtime_error(std::string("check failed at line ") +
                                 std::to_string(line) + ": " + expression);
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

// A deterministic schedule is just an ordered list of runtime callbacks.
// Nothing below starts a real thread or depends on wall-clock timing.
void run_schedule(const std::vector<Step>& schedule) {
    for (const auto& step : schedule) {
        try {
            step.action();
        } catch (const std::exception& error) {
            throw std::runtime_error(std::string("step '") + step.name +
                                     "' failed: " + error.what());
        }
    }
}

void test_ordered_use_after_free() {
    netsec::Detector detector;
    netsec::Allocation allocation;

    run_schedule({
        {"alloc", [&] {
             allocation = detector.alloc(1, 0x1000, 64);
             CHECK(allocation.generation == 1);
         }},
        {"free", [&] { CHECK(detector.free(1, allocation)); }},
        {"release", [&] { detector.release(1, 77); }},
        {"acquire", [&] { detector.acquire(2, 77); }},
        {"access", [&] { CHECK(detector.access(2, allocation.address)); }},
    });

    const auto reports = detector.reports();
    CHECK(reports.size() == 1);
    CHECK(reports[0].kind == netsec::ReportKind::OrderedUseAfterFree);
    CHECK(reports[0].generation == allocation.generation);
    CHECK(reports[0].address == allocation.address);
    CHECK(reports[0].free_thread == 1);
    CHECK(reports[0].access_thread == 2);
    CHECK(reports[0].allocation_sequence == allocation.allocation_sequence);
    CHECK(reports[0].free_sequence < reports[0].access_sequence);
}

void test_concurrent_free_access_window() {
    netsec::Detector detector;
    constexpr netsec::Address address = 0x2000;
    netsec::Allocation allocation;

    run_schedule({
        {"alloc", [&] { allocation = detector.alloc(1, address, 32); }},
        // The access is earlier in delivery order, but it has no
        // happens-before edge to the free.
        {"access", [&] { CHECK(detector.access(2, address)); }},
        {"free", [&] { CHECK(detector.free(1, allocation)); }},
    });

    auto reports = detector.reports();
    CHECK(reports.size() == 1);
    CHECK(reports[0].kind == netsec::ReportKind::ConcurrentFreeAccess);
    CHECK(reports[0].generation == allocation.generation);
    CHECK(reports[0].access_sequence < reports[0].free_sequence);

    // The same window is detected when the free is delivered first and the
    // unsynchronized access arrives afterwards.
    detector.reset();
    run_schedule({
        {"alloc", [&] { allocation = detector.alloc(1, address, 32); }},
        {"free", [&] { CHECK(detector.free(1, allocation)); }},
        {"access", [&] { CHECK(detector.access(3, address)); }},
    });

    reports = detector.reports();
    CHECK(reports.size() == 1);
    CHECK(reports[0].kind == netsec::ReportKind::ConcurrentFreeAccess);
    CHECK(reports[0].free_sequence < reports[0].access_sequence);
    CHECK(detector.report_count(netsec::ReportKind::OrderedUseAfterFree) == 0);
    CHECK(detector.report_count(netsec::ReportKind::ConcurrentFreeAccess) == 1);
}

void test_access_before_free_is_legal() {
    netsec::Detector detector;
    constexpr netsec::Address address = 0x3000;
    netsec::Allocation allocation;

    run_schedule({
        {"alloc", [&] { allocation = detector.alloc(1, address, 16); }},
        // T2 observes the live object and publishes that observation.
        {"access", [&] { CHECK(detector.access(2, allocation)); }},
        {"release", [&] { detector.release(2, 88); }},
        // T1 acquires T2's clock before freeing, so access happens-before
        // free and this is not a violation.
        {"acquire", [&] { detector.acquire(1, 88); }},
        {"free", [&] { CHECK(detector.free(1, allocation)); }},
    });

    CHECK(detector.reports().empty());
}

void test_acquire_merges_all_releases_for_same_sync() {
    netsec::Detector detector;
    constexpr netsec::Address address = 0x3500;
    netsec::Allocation allocation;

    run_schedule({
        {"alloc", [&] { allocation = detector.alloc(1, address, 16); }},
        {"free", [&] { CHECK(detector.free(1, allocation)); }},
        {"release 1", [&] { detector.release(1, 93); }},
        {"release 2", [&] { detector.release(2, 93); }},
        {"acquire", [&] { detector.acquire(3, 93); }},
        {"access", [&] { CHECK(detector.access(3, address)); }},
    });

    const auto reports = detector.reports();
    CHECK(reports.size() == 1);
    CHECK(reports[0].kind == netsec::ReportKind::OrderedUseAfterFree);
}

void test_address_reuse_uses_generation_isolation() {
    netsec::Detector detector;
    constexpr netsec::Address address = 0x4000;
    netsec::Allocation old_generation;
    netsec::Allocation new_generation;

    run_schedule({
        {"alloc old", [&] { old_generation = detector.alloc(1, address, 64); }},
        {"free old", [&] { CHECK(detector.free(1, old_generation)); }},
        {"alloc new", [&] { new_generation = detector.alloc(2, address, 96); }},
        // Address-only access resolves to the new generation. The free of the
        // old generation must not make this an ordered UAF.
        {"access new", [&] { CHECK(detector.access(3, address)); }},
        {"release new", [&] { detector.release(3, 91); }},
        // Publish the access before T2 frees the new object. The access is
        // ordered before the free, so this is legal.
        {"acquire new", [&] { detector.acquire(2, 91); }},
        {"free new", [&] { CHECK(detector.free(2, new_generation)); }},
        // An old token still identifies the old object, even after the
        // address has been reused. It is reported against generation 1,
        // never against generation 2.
        {"access old token", [&] { CHECK(detector.access(1, old_generation)); }},
        {"release new uaf", [&] { detector.release(2, 92); }},
        {"acquire new uaf", [&] { detector.acquire(3, 92); }},
        {"access new uaf", [&] { CHECK(detector.access(3, address)); }},
    });

    CHECK(old_generation.generation == 1);
    CHECK(new_generation.generation == 2);
    CHECK(old_generation.address == new_generation.address);

    const auto reports = detector.reports();
    CHECK(reports.size() == 2);
    CHECK(reports[0].kind == netsec::ReportKind::OrderedUseAfterFree);
    CHECK(reports[0].generation == old_generation.generation);
    CHECK(reports[1].kind == netsec::ReportKind::OrderedUseAfterFree);
    CHECK(reports[1].generation == new_generation.generation);
    CHECK(reports[1].size == 96);
}

void test_reuse_does_not_carry_pending_accesses_forward() {
    netsec::Detector detector;
    constexpr netsec::Address address = 0x5000;
    netsec::Allocation old_generation;
    netsec::Allocation new_generation;

    run_schedule({
        {"alloc old", [&] { old_generation = detector.alloc(1, address, 8); }},
        {"access old", [&] { CHECK(detector.access(2, address)); }},
        {"free old", [&] { CHECK(detector.free(1, old_generation)); }},
        {"alloc new", [&] { new_generation = detector.alloc(1, address, 8); }},
        {"access new", [&] { CHECK(detector.access(3, address)); }},
        {"free new", [&] { CHECK(detector.free(1, new_generation)); }},
    });

    const auto reports = detector.reports();
    CHECK(reports.size() == 2);
    CHECK(reports[0].kind == netsec::ReportKind::ConcurrentFreeAccess);
    CHECK(reports[0].generation == old_generation.generation);
    CHECK(reports[1].kind == netsec::ReportKind::ConcurrentFreeAccess);
    CHECK(reports[1].generation == new_generation.generation);
}

}  // namespace

int main() {
    try {
        test_ordered_use_after_free();
        test_concurrent_free_access_window();
        test_access_before_free_is_legal();
        test_acquire_merges_all_releases_for_same_sync();
        test_address_reuse_uses_generation_isolation();
        test_reuse_does_not_carry_pending_accesses_forward();
    } catch (const std::exception& error) {
        std::cerr << "FAIL: " << error.what() << '\n';
        return 1;
    }

    std::cout << "all free/access detection tests passed\n";
    return 0;
}

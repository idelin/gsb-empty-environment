#include "netsec.hpp"

#include <climits>
#include <cstddef>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& message) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL: " << message << '\n';
    }
}

// Hook that always reports the bounds check as bypassed.
bool always_skip(std::size_t, void*) noexcept {
    return true;
}

// Hook that records every raw index it is asked about.
bool recording_skip(std::size_t index, void* user_data) noexcept {
    auto* seen = static_cast<std::vector<std::size_t>*>(user_data);
    if (seen != nullptr) {
        seen->push_back(index);
    }
    return true;
}

void test_padding_geometry() {
    netsec::PaddedTable<int> table(5);  // non-power-of-two logical size
    check(table.size() == 5, "logical size preserved");
    check(table.capacity() == 8, "5 pads up to capacity 8");
    check(table.capacity_mask() == 7, "capacity 8 uses mask 7");
    check(table.is_padded(), "5-element table reports padding");

    netsec::PaddedTable<int> exact(8);
    check(exact.capacity() == 8 && !exact.is_padded(),
          "power-of-two size needs no extra padding");

    netsec::PaddedTable<int> empty(0);
    check(empty.size() == 0 && empty.capacity() == 1,
          "empty table still owns one slot");

    check(netsec::next_power_of_two(1) == 1, "next pow2 of 1 is 1");
    check(netsec::next_power_of_two(7) == 8, "next pow2 of 7 is 8");
    check(netsec::next_power_of_two(8) == 8, "next pow2 of 8 is 8");
    check(netsec::next_power_of_two(9) == 16, "next pow2 of 9 is 16");
}

void test_mask_always_in_range() {
    constexpr std::size_t kMask = 7;  // capacity 8
    for (std::size_t index : {std::size_t(0), std::size_t(4), std::size_t(5),
                              std::size_t(7), std::size_t(8),
                              std::size_t(65535), SIZE_MAX}) {
        const std::size_t folded = netsec::mask_index(index, kMask);
        check(folded <= kMask, "masked index never exceeds mask");
        check(folded < (kMask + 1), "masked index stays below capacity");
    }
    check(netsec::mask_index(SIZE_MAX, kMask) == kMask,
          "SIZE_MAX folds to the last slot");
    check(netsec::mask_index(static_cast<std::size_t>(-1), kMask) == kMask,
          "negative conversion (-1) folds to last slot");
}

void test_normal_path_throws() {
    netsec::PaddedTable<int> table(5);
    for (std::size_t i = 0; i < table.size(); ++i) {
        table[i] = static_cast<int>(i) + 1;
    }

    check(table.at(0) == 1, "in-range at(0) works");
    check(table.at(4) == 5, "in-range at(len-1) works");

    bool threw = false;
    try {
        (void)table.at(5);  // first out-of-range index
    } catch (const std::out_of_range&) {
        threw = true;
    }
    check(threw, "at(len) throws out_of_range with no hook");

    threw = false;
    try {
        (void)table.at(SIZE_MAX);
    } catch (const std::out_of_range&) {
        threw = true;
    }
    check(threw, "at(SIZE_MAX) throws out_of_range with no hook");

    threw = false;
    try {
        (void)table.at_signed(-1);
    } catch (const std::out_of_range&) {
        threw = true;
    }
    check(threw, "negative signed index throws with no hook");
}

void test_hook_keeps_every_access_in_allocation() {
    constexpr std::size_t kLen = 5;
    netsec::PaddedTable<int> table(kLen);
    for (std::size_t i = 0; i < kLen; ++i) {
        table[i] = static_cast<int>(i) + 100;
    }

    std::vector<std::size_t> seen;
    table.set_skip_check_hook(recording_skip, &seen);

    const long long negative = -1;
    const std::size_t negative_converted = static_cast<std::size_t>(negative);

    // The exact indices required by the task.
    const std::size_t probes[] = {
        0,
        kLen - 1,
        kLen,
        negative_converted,
        SIZE_MAX,
    };

    for (std::size_t probe : probes) {
        int* resolved = &table.at(probe);
        check(table.within_allocation(resolved),
              "hooked at() resolves inside allocation");
        const std::size_t folded =
            netsec::mask_index(probe, table.capacity_mask());
        check(resolved == table.data() + folded,
              "hooked at() resolves to the masked slot");
        check(folded < table.capacity(),
              "folded slot index is within capacity");
    }

    // Signed entry point covering the negative conversion explicitly.
    int* signed_resolved = &table.at_signed(negative);
    check(table.within_allocation(signed_resolved),
          "negative signed access stays inside allocation under hook");
    check(signed_resolved == table.data() +
              netsec::mask_index(negative_converted, table.capacity_mask()),
          "negative signed access matches unsigned mask result");

    // The hook is consulted on every at() call and decides per index whether
    // the check is bypassed, so it observes each raw, pre-mask index in order.
    check(seen.size() == 6, "five probes plus the signed call hit the hook");
    check(seen[0] == 0, "hook sees raw index 0 before masking");
    check(seen[1] == kLen - 1, "hook sees raw len-1 index before masking");
    check(seen[2] == kLen, "hook sees raw len index before masking");
    check(seen[3] == negative_converted,
          "hook sees raw negative-converted index before masking");
    check(seen[4] == SIZE_MAX, "hook sees raw SIZE_MAX before masking");
    check(seen[5] == negative_converted,
          "signed entry feeds the same converted index to the hook");

    // Removing the hook restores strict architectural checking.
    table.clear_skip_check_hook();
    check(!table.has_skip_check_hook(), "hook cleared");
    bool threw = false;
    try {
        (void)table.at(kLen);
    } catch (const std::out_of_range&) {
        threw = true;
    }
    check(threw, "clearing the hook makes at(len) throw again");
}

void test_always_skip_hook_and_padding_sentinels() {
    constexpr std::size_t kLen = 5;
    netsec::PaddedTable<int> table(kLen, 0);
    table.set_skip_check_hook(always_skip, nullptr);

    // Fill real entries; padding slots remain the sentinel.
    for (std::size_t i = 0; i < kLen; ++i) {
        table[i] = static_cast<int>(i) + 1;
    }

    // len == 5 folds to slot 5, a padding slot holding the sentinel.
    check(table.at(5) == 0, "bypassed read of len lands in padding, not OOB");
    check(table.at(SIZE_MAX) == 0,
          "bypassed read of SIZE_MAX lands in padding slot 7");

    // Every index across a wide sweep stays inside the allocation.
    for (std::size_t probe = 0; probe < 1000; ++probe) {
        check(table.within_allocation(&table.at(probe)),
              "swept hooked access stays inside allocation");
    }
    check(table.within_allocation(&table.at(SIZE_MAX)),
          "SIZE_MAX hooked access stays inside allocation");
}

}  // namespace

int main() {
    test_padding_geometry();
    test_mask_always_in_range();
    test_normal_path_throws();
    test_hook_keeps_every_access_in_allocation();
    test_always_skip_hook_and_padding_sentinels();

    std::cout << (g_failures == 0 ? "ALL TESTS PASSED" : "TESTS FAILED")
              << " (" << g_checks << " assertions)\n";
    return g_failures == 0 ? 0 : 1;
}

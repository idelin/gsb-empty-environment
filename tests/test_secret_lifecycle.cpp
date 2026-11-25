#include "secret_lifecycle/secret_lifecycle.hpp"

#include <cassert>
#include <cstddef>
#include <iostream>
#include <span>
#include <vector>

using namespace secret_lifecycle;

namespace {

std::vector<std::byte> repeat(unsigned char value, std::size_t count) {
    return std::vector<std::byte>(count, static_cast<std::byte>(value));
}

void assert_equal(const std::vector<std::byte>& actual,
                  const std::vector<std::byte>& expected,
                  const char* message) {
    if (actual != expected) {
        std::cerr << message << '\n';
        std::abort();
    }
}

void assert_true(bool condition, const char* message) {
    if (!condition) {
        std::cerr << message << '\n';
        std::abort();
    }
}

void test_cross_page_split_and_clone_cow() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();

    auto first = repeat(1, 32);
    store.write(10, ByteRange{0, 32}, first);
    assert_equal(store.read(ByteRange{0, 32}).materialize(), first, "cross-page read failed");

    auto clone = store.clone();
    auto clone_before = clone.read(ByteRange{0, 32});
    assert_equal(clone_before.materialize(), first, "clone inheritance failed");

    auto replacement = repeat(2, 8);
    store.write(11, ByteRange{12, 8}, replacement);
    assert_equal(clone_before.materialize(), first, "clone view changed after first write");

    auto expected_store = first;
    for (std::size_t i = 12; i < 20; ++i) expected_store[i] = static_cast<std::byte>(2);
    assert_equal(store.read(ByteRange{0, 32}).materialize(), expected_store,
                 "store COW update failed");
    assert_equal(clone.read(ByteRange{0, 32}).materialize(), first,
                 "clone must retain original physical page");
    assert_true(allocator.allocation_count() >= 3, "first write did not allocate replacement pages");
}

void test_clone_release_does_not_reuse_still_mapped_page() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(100, ByteRange{0, 16}, repeat(100, 16));
    auto clone = store.clone();
    auto clone_view = clone.read(ByteRange{0, 16});

    store.write(101, ByteRange{0, 4}, repeat(101, 4));
    clone = {};
    assert_equal(clone_view.materialize(), repeat(100, 16),
                 "released clone changed outstanding borrowed bytes");
    clone_view = {};

    auto replacement = host.create_store();
    replacement.write(102, ByteRange{0, 16}, repeat(102, 16));
    auto borrowed = store.read(ByteRange{0, 16});
    auto materialized = borrowed.materialize();
    std::vector<std::byte> prefix(materialized.begin(), materialized.begin() + 4);
    assert_equal(prefix, repeat(101, 4),
                 "store generation changed unexpectedly");
}

void test_partial_rotation_preserves_adjacent_generation() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();

    store.write(1, ByteRange{0, 16}, repeat(1, 16));
    store.write(2, ByteRange{4, 8}, repeat(2, 8));

    auto view_one = store.read(ByteRange{0, 4});
    auto view_three = store.read(ByteRange{12, 4});
    eraser.pause();
    auto barrier = host.destroy_generation(2);
    assert_true(host.proof(2) == nullptr, "proof issued before erase completion");

    bool rejected = false;
    try {
        (void)store.read(ByteRange{4, 8});
    } catch (const BorrowRejected&) {
        rejected = true;
    }
    assert_true(rejected, "deleted generation accepted a new borrow");

    assert_equal(view_one.materialize(), repeat(1, 4), "adjacent generation changed");
    assert_equal(view_three.materialize(), repeat(1, 4), "adjacent trailing generation changed");

    assert_true(eraser.pending_count() == 1, "partial erase request not paused");
    eraser.resume(allocator);
    auto proof = host.proof(2);
    assert_true(proof != nullptr, "destroy proof missing after no outstanding borrows");
    assert_true(proof->borrow_barrier == barrier, "proof barrier mismatch");
    assert_true(proof->logical_intervals == std::vector<ByteRange>{ByteRange{4, 8}},
                "proof logical interval mismatch");
    assert_true(allocator.is_zero(proof->physical_receipts.front().page,
                                  proof->physical_receipts.front().range),
                "deleted generation bytes not erased");
    assert_true(!allocator.is_zero(proof->physical_receipts.front().page, ByteRange{0, 16}),
                "active adjacent bytes were zeroed");
}

void test_same_page_multiple_generations_erase_exact_slices() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(90, ByteRange{0, 16}, repeat(1, 16));
    store.write(91, ByteRange{4, 4}, repeat(2, 4));
    store.write(92, ByteRange(8, 4), repeat(3, 4));
    eraser.pause();
    host.destroy_generation(91);
    eraser.resume(allocator);

    auto proof = host.proof(91);
    assert_true(proof != nullptr, "middle generation proof missing");
    assert_true(proof->physical_receipts.size() == 1, "middle slice should use one receipt");
    const auto& receipt = proof->physical_receipts.front();
    assert_true(receipt.range == ByteRange{4, 4}, "erase receipt escaped generation slice");
    assert_true(allocator.is_zero(receipt.page, ByteRange{4, 4}), "slice not erased");
    assert_true(!allocator.is_zero(receipt.page, ByteRange{0, 4}), "prefix generation erased");
    assert_true(!allocator.is_zero(receipt.page, ByteRange{8, 8}), "suffix generation erased");
}

void test_overwritten_generation_proof_excludes_retired_interval() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(110, ByteRange{0, 16}, repeat(1, 16));
    store.write(111, ByteRange{4, 8}, repeat(2, 8));
    eraser.pause();
    host.destroy_generation(110);
    eraser.resume(allocator);

    auto proof = host.proof(110);
    assert_true(proof != nullptr, "overwritten generation proof missing");
    std::vector<ByteRange> expected{ByteRange{0, 4}, ByteRange{12, 4}};
    assert_true(proof->logical_intervals == expected,
                "retired logical interval included in proof");

    EventAcceptor acceptor;
    for (const auto& event : host.events()) acceptor.observe(event);
    assert_true(acceptor.accept(*proof), "retirement-aware proof rejected");
}

void test_paused_erase_borrow_barrier_and_duplicate_receipt() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();

    store.write(7, ByteRange{0, 16}, repeat(7, 16));
    auto view = store.read(ByteRange{0, 16});
    eraser.pause();
    auto barrier = host.destroy_generation(7);
    assert_true(eraser.pending_count() == 0, "borrowed bytes were queued for early erase");
    assert_true(host.proof(7) == nullptr, "proof issued while borrow remains");

    view = {};
    assert_true(eraser.pending_count() == 1, "erase did not start at release point");
    assert_true(host.proof(7) == nullptr, "proof issued before paused erase completed");

    eraser.resume(allocator);
    auto proof = host.proof(7);
    assert_true(proof != nullptr, "proof missing after resume");
    assert_true(proof->borrow_barrier == barrier, "barrier not bound to proof");

    auto duplicate = proof->physical_receipts.front();
    host.process_erase_receipt(duplicate);
    assert_true(host.proof(7) != nullptr, "duplicate receipt destabilized proof");
}

void test_scratch_failure_erases_copied_secret_and_rolls_back() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(3, ByteRange{0, 32}, repeat(3, 32));
    auto clone = store.clone();
    auto stable = clone.read(ByteRange{0, 32});

    allocator.failure_policy().fail_after_scratch_copies = allocator.scratch_count();
    bool injected = false;
    try {
        store.write(4, ByteRange{0, 4}, repeat(4, 4));
    } catch (const InjectedScratchFailure&) {
        injected = true;
    }
    assert_true(injected, "scratch failure was not injected");
    assert_true(allocator.scratches().empty(), "failed scratch was not erased and released");
    assert_equal(store.read(ByteRange{0, 32}).materialize(), repeat(3, 32),
                 "failed rotation changed committed state");
    assert_equal(stable.materialize(), repeat(3, 32), "borrowed bytes changed after failure");
}

void test_allocation_failure_releases_fresh_secret_pages() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    allocator.failure_policy().fail_after_allocations = 1;

    bool failed = false;
    try {
        store.write(5, ByteRange{0, 32}, repeat(5, 32));
    } catch (const InjectedAllocationFailure&) {
        failed = true;
    }
    assert_true(failed, "allocation failure was not injected");
    assert_true(allocator.all_live_secret_memory_zero(),
                "fresh allocated secret bytes survived allocation failure");
}

void test_allocation_failure_after_scratch_cleans_both_copies() {
    TrackingPageAllocator allocator(8);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(70, ByteRange{0, 16}, repeat(70, 16));
    auto clone = store.clone();
    auto pinned = clone.read(ByteRange{0, 16});

    allocator.failure_policy().fail_after_allocations = allocator.allocation_count();
    bool failed = false;
    try {
        store.write(71, ByteRange{0, 8}, repeat(71, 8));
    } catch (const InjectedAllocationFailure&) {
        failed = true;
    }
    assert_true(failed, "post-scratch allocation failure not injected");
    assert_true(allocator.scratches().empty(), "scratch survived post-scratch allocation failure");
    assert_equal(pinned.materialize(), repeat(70, 16), "borrow changed during rollback");
}

void test_event_acceptor_rejects_bad_intervals_and_stale_receipts() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();

    store.write(9, ByteRange{0, 8}, repeat(9, 8));
    store.write(9, ByteRange{20, 8}, repeat(9, 8));
    eraser.pause();
    auto barrier = host.destroy_generation(9);
    eraser.resume(allocator);

    auto proof = host.proof(9);
    assert_true(proof != nullptr, "multi-interval proof missing");
    EventAcceptor acceptor;
    for (const auto& event : host.events()) acceptor.observe(event);
    assert_true(acceptor.accept(*proof), "valid proof rejected");

    auto missing_interval = *proof;
    missing_interval.logical_intervals = {ByteRange{0, 8}};
    assert_true(!acceptor.accept(missing_interval), "acceptor missed logical interval omission");

    auto wrong_barrier = *proof;
    wrong_barrier.borrow_barrier = barrier + 100;
    assert_true(!acceptor.accept(wrong_barrier), "acceptor accepted wrong borrow barrier");

    auto stale = *proof;
    if (!stale.physical_receipts.empty()) {
        stale.physical_receipts.front().page.allocation_generation += 1;
        assert_true(!acceptor.accept(stale), "acceptor accepted receipt from reused address");
    }
}

void test_scratch_receipt_is_required_after_cow() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(20, ByteRange{0, 16}, repeat(20, 16));
    auto clone = store.clone();
    store.write(21, ByteRange{0, 4}, repeat(21, 4));
    eraser.pause();
    host.destroy_generation(20);
    eraser.resume(allocator);

    auto proof = host.proof(20);
    assert_true(proof != nullptr, "COW generation proof missing");
    assert_true(!proof->scratch_receipts.empty(), "scratch receipt missing from proof");

    EventAcceptor acceptor;
    for (const auto& event : host.events()) acceptor.observe(event);
    assert_true(acceptor.accept(*proof), "valid COW proof rejected");
    auto without_scratch = *proof;
    without_scratch.scratch_receipts.clear();
    assert_true(!acceptor.accept(without_scratch), "missing scratch receipt was accepted");
}

void test_paused_scratch_erase_completes_after_late_receipt() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(80, ByteRange{0, 16}, repeat(80, 16));
    auto clone = store.clone();

    eraser.pause();
    store.write(81, ByteRange{0, 4}, repeat(81, 4));
    host.destroy_generation(80);
    assert_true(host.proof(80) == nullptr, "proof issued before pending scratch erase");
    assert_true(eraser.pending_count() >= 2, "physical and scratch erase were not paused");

    eraser.resume(allocator);
    auto proof = host.proof(80);
    assert_true(proof != nullptr, "scratch erase completion did not produce proof");
    assert_true(!proof->scratch_receipts.empty(), "late scratch receipt not bound to proof");

    for (const auto& receipt : proof->scratch_receipts) {
        host.process_erase_receipt(receipt);
    }
    assert_true(host.proof(80) != nullptr, "duplicate scratch receipt invalidated proof");
}

void test_same_address_reallocation_rejects_old_receipt() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto old_store = host.create_store();
    old_store.write(30, ByteRange{0, 16}, repeat(30, 16));
    eraser.pause();
    host.destroy_generation(30);
    old_store = {};
    eraser.resume(allocator);

    auto old_proof = host.proof(30);
    assert_true(old_proof != nullptr, "old generation proof missing");
    auto old_address = old_proof->physical_receipts.front().page;

    auto new_store = host.create_store();
    new_store.write(31, ByteRange{0, 16}, repeat(31, 16));
    eraser.pause();
    host.destroy_generation(31);
    eraser.resume(allocator);
    auto new_proof = host.proof(31);
    assert_true(new_proof != nullptr, "new generation proof missing");

    bool reused_address = false;
    for (const auto& receipt : new_proof->physical_receipts) {
        if (receipt.page.address == old_address.address &&
            receipt.page.allocation_generation != old_address.allocation_generation) {
            reused_address = true;
        }
    }
    assert_true(reused_address, "test did not observe same-address reallocation");

    EventAcceptor acceptor;
    for (const auto& event : host.events()) acceptor.observe(event);
    assert_true(acceptor.accept(*new_proof), "new proof rejected");
    auto forged = *new_proof;
    for (auto& receipt : forged.physical_receipts) {
        if (receipt.page.address == old_address.address) {
            receipt.page.allocation_generation = old_address.allocation_generation;
            receipt.id = old_proof->physical_receipts.front().id;
        }
    }
    assert_true(!acceptor.accept(forged), "old receipt accepted for reallocated page");
}

void test_late_receipt_after_reallocation_is_ignored() {
    TrackingPageAllocator allocator(16);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    store.write(40, ByteRange{0, 16}, repeat(40, 16));
    eraser.pause();
    host.destroy_generation(40);
    store = {};
    eraser.resume(allocator);
    auto receipt = host.proof(40)->physical_receipts.front();

    host.process_erase_receipt(receipt);
    host.process_erase_receipt(receipt);
    assert_true(host.proof(40) != nullptr, "late or duplicate receipt removed completed proof");
}

void test_cross_page_split_remerge_byte_for_byte() {
    TrackingPageAllocator allocator(8);
    PausableEraser eraser;
    SecretHost host(allocator, allocator, eraser);
    auto store = host.create_store();
    std::vector<std::byte> original;
    for (std::size_t i = 0; i < 24; ++i) original.push_back(static_cast<std::byte>(i + 1));
    store.write(50, ByteRange{0, 24}, original);

    auto middle = repeat(99, 8);
    store.write(51, ByteRange{8, 8}, middle);
    auto expected = original;
    for (std::size_t i = 8; i < 16; ++i) expected[i] = static_cast<std::byte>(99);
    assert_equal(store.read(ByteRange{0, 24}).materialize(), expected,
                 "split or remerge changed unrelated bytes");

    auto clone = store.clone();
    auto clone_view = clone.read(ByteRange{0, 24});
    store.write(52, ByteRange{12, 4}, repeat(77, 4));
    assert_equal(clone_view.materialize(), expected, "clone snapshot not byte stable");
}

void test_every_scratch_copy_failure_is_cleaned() {
    for (std::size_t attempt = 0; attempt < 3; ++attempt) {
        TrackingPageAllocator allocator(8);
        PausableEraser eraser;
        SecretHost host(allocator, allocator, eraser);
        auto store = host.create_store();
        store.write(60, ByteRange{0, 24}, repeat(60, 24));
        auto clone = store.clone();
        auto pinned = clone.read(ByteRange{0, 24});
        for (std::size_t succeeded = 0; succeeded < attempt; ++succeeded) {
            allocator.failure_policy().fail_after_scratch_copies =
                std::numeric_limits<std::size_t>::max();
            (void)succeeded;
            bool setup_failed = false;
            try {
                store.write(61, ByteRange{0, 1}, repeat(61, 1));
            } catch (const InjectedScratchFailure&) {
                setup_failed = true;
            }
            assert_true(!setup_failed, "scratch enumeration setup failed");
        }
        allocator.failure_policy().fail_next_scratch_copy = true;
        bool failed = false;
        try {
            store.write(61, ByteRange{0, 24}, repeat(61, 24));
        } catch (const InjectedScratchFailure&) {
            failed = true;
        } catch (const SecretLifecycleError& error) {
            assert_true(false, error.what());
        }
        assert_true(failed, "enumeration did not inject scratch failure");
        assert_true(allocator.scratches().empty(), "a failed scratch copy retained secret bytes");
        assert_equal(pinned.materialize(), repeat(60, 24),
                     "outstanding borrow changed after scratch failure");
    }
}

}  // namespace

int main() {
    try {
        test_cross_page_split_and_clone_cow();
        test_clone_release_does_not_reuse_still_mapped_page();
        test_same_page_multiple_generations_erase_exact_slices();
        test_overwritten_generation_proof_excludes_retired_interval();
        test_partial_rotation_preserves_adjacent_generation();
        test_paused_erase_borrow_barrier_and_duplicate_receipt();
        test_scratch_failure_erases_copied_secret_and_rolls_back();
        test_allocation_failure_releases_fresh_secret_pages();
        test_allocation_failure_after_scratch_cleans_both_copies();
        test_event_acceptor_rejects_bad_intervals_and_stale_receipts();
        test_scratch_receipt_is_required_after_cow();
        test_paused_scratch_erase_completes_after_late_receipt();
        test_same_address_reallocation_rejects_old_receipt();
        test_late_receipt_after_reallocation_is_ignored();
        test_cross_page_split_remerge_byte_for_byte();
        test_every_scratch_copy_failure_is_cleaned();
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        throw;
    }
    std::cout << "all secret lifecycle tests passed\n";
}

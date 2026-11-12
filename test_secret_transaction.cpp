#include "secret_transaction.hpp"

#include <cstdio>
#include <cstdlib>
#include <exception>
#include <map>
#include <set>
#include <type_traits>
#include <utility>
#include <vector>

namespace {

int g_checks = 0;
int g_failures = 0;
const char* g_current_test = "";

}  // namespace

#define CHECK(condition)                                                      \
    do {                                                                      \
        ++g_checks;                                                           \
        if (!(condition)) {                                                   \
            ++g_failures;                                                     \
            std::fprintf(stderr, "FAIL [%s] %s (line %d)\n", g_current_test,  \
                         #condition, __LINE__);                               \
        }                                                                     \
    } while (0)

#define CHECK_THROWS(expression, exception_type)                              \
    do {                                                                      \
        ++g_checks;                                                           \
        bool caught_ = false;                                                 \
        try {                                                                 \
            (void)(expression);                                               \
        } catch (const exception_type&) {                                     \
            caught_ = true;                                                   \
        } catch (...) {                                                       \
        }                                                                     \
        if (!caught_) {                                                       \
            ++g_failures;                                                     \
            std::fprintf(stderr, "FAIL [%s] expected %s (line %d)\n",         \
                         g_current_test, #exception_type, __LINE__);          \
        }                                                                     \
    } while (0)

namespace {

// Observing allocator. Every deallocation asserts that the pointer is live
// (no double free), its recorded size matches, and the returned storage is
// entirely zero (the uncommitted/committed bytes were really wiped).
class TrackingAllocator final : public netsec::IAllocator {
public:
    std::byte* allocate(std::size_t bytes) override {
        if (fail_after_allocs_ == 0) {
            throw std::bad_alloc{};
        }
        if (fail_after_allocs_ > 0) {
            --fail_after_allocs_;
        }
        auto* ptr = static_cast<std::byte*>(std::malloc(bytes));
        if (ptr == nullptr) {
            throw std::bad_alloc{};
        }
        {
            const bool inserted = live_.insert(ptr).second;
            if (!inserted) {
                std::fprintf(stderr, "FAIL: allocator handed out a live pointer twice\n");
                std::abort();
            }
        }
        size_of_[ptr] = bytes;
        ++alloc_count_;
        ++live_count_;
        return ptr;
    }

    void deallocate(std::byte* ptr, std::size_t bytes) noexcept override {
        const auto it = live_.find(ptr);
        if (it == live_.end()) {
            std::fprintf(stderr, "FAIL: double or unknown free of %p\n",
                         static_cast<void*>(ptr));
            std::abort();
        }
        if (size_of_[ptr] != bytes) {
            std::fprintf(stderr, "FAIL: free size mismatch (got %zu, want %zu)\n",
                         bytes, size_of_[ptr]);
            std::abort();
        }
        for (std::size_t i = 0; i < bytes; ++i) {
            if (ptr[i] != std::byte{0}) {
                std::fprintf(stderr,
                             "FAIL: freed secret at %p+%zu was not zeroized\n",
                             static_cast<void*>(ptr), i);
                std::abort();
            }
        }
        live_.erase(it);
        size_of_.erase(ptr);
        ++free_count_;
        --live_count_;
        std::free(ptr);
    }

    // Number of successful allocations remaining before the next allocate()
    // throws. -1 means never fail.
    void set_fail_after(int count) noexcept { fail_after_allocs_ = count; }

    int alloc_count() const noexcept { return alloc_count_; }
    int free_count() const noexcept { return free_count_; }
    int live_count() const noexcept { return live_count_; }

private:
    std::set<std::byte*> live_;
    std::map<std::byte*, std::size_t> size_of_;
    int alloc_count_ = 0;
    int free_count_ = 0;
    int live_count_ = 0;
    int fail_after_allocs_ = -1;
};

// Observing zeroizer: records every range that was wiped.
class TrackingZeroizer final : public netsec::IZeroizer {
public:
    void zeroize(std::byte* ptr, std::size_t bytes) noexcept override {
        volatile std::byte* cursor = ptr;
        for (std::size_t i = 0; i < bytes; ++i) {
            cursor[i] = std::byte{0};
        }
        ++call_count_;
        total_bytes_ += bytes;
        ranges_.push_back(Range{ptr, bytes});
    }

    struct Range {
        std::byte* ptr;
        std::size_t bytes;
    };

    int call_count() const noexcept { return call_count_; }
    std::size_t total_bytes() const noexcept { return total_bytes_; }
    const std::vector<Range>& ranges() const noexcept { return ranges_; }

private:
    int call_count_ = 0;
    std::size_t total_bytes_ = 0U;
    std::vector<Range> ranges_;
};

void fill_markers(netsec::SecretBuffer& buffer) {
    for (std::size_t i = 0; i < buffer.size(); ++i) {
        buffer.write(i, static_cast<std::byte>(0xA0 + (i % 16)));
    }
}

// 1. Construction point that throws leaves nothing behind.
void test_constructor_throws() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    allocator.set_fail_after(0);

    CHECK_THROWS(netsec::SecretBuffer(64,
                                      std::shared_ptr<netsec::IAllocator>(&allocator,
                                                                          [](auto*) {}),
                                      std::shared_ptr<netsec::IZeroizer>(&zeroizer,
                                                                         [](auto*) {})),
                 std::bad_alloc);
    CHECK(allocator.alloc_count() == 0);
    CHECK(allocator.free_count() == 0);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.call_count() == 0);
}

// 2. Uncommitted buffer: every byte zeroized and exactly one free on scope exit.
void test_uncommitted_zeroized_on_destruction() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    {
        netsec::SecretBuffer buffer(48,
                                    std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                    std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
        CHECK(!buffer.empty());
        fill_markers(buffer);
        CHECK(buffer.at(7) == std::byte{0xA7});
        CHECK(allocator.alloc_count() == 1);
    }
    CHECK(allocator.alloc_count() == 1);
    CHECK(allocator.free_count() == 1);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.call_count() == 1);
    CHECK(zeroizer.total_bytes() == 48U);
}

// 3. Move steals the allocation; moved-from buffer has nothing to free.
void test_move_steals_ownership() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    {
        netsec::SecretBuffer first(32,
                                   std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                   std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
        fill_markers(first);
        std::byte* original = &first.at(0);  // test-only identity probe

        netsec::SecretBuffer second(std::move(first));
        CHECK(first.empty());
        CHECK(first.size() == 0U);
        CHECK(second.size() == 32U);
        CHECK(second.at(0) == std::byte{0xA0});
        CHECK(second.at(15) == std::byte{0xAF});
        CHECK(second.at(0) == *original);
        CHECK(allocator.alloc_count() == 1);
        CHECK(allocator.free_count() == 0);
        CHECK(zeroizer.call_count() == 0);
    }
    CHECK(allocator.free_count() == 1);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.call_count() == 1);
    CHECK(zeroizer.total_bytes() == 32U);
}

// 4. Move assignment cleans the target once, then steals the source.
void test_move_assignment_cleans_target() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    auto make_sp = [&]() {
        return std::make_pair(
            std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
            std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
    };
    {
        auto seams_a = make_sp();
        netsec::SecretBuffer target(16, seams_a.first, seams_a.second);
        fill_markers(target);

        auto seams_b = make_sp();
        netsec::SecretBuffer source(24, seams_b.first, seams_b.second);
        fill_markers(source);

        target = std::move(source);
        CHECK(source.empty());
        CHECK(target.size() == 24U);
        CHECK(allocator.alloc_count() == 2);
        CHECK(allocator.free_count() == 1);  // only the old target
        CHECK(zeroizer.call_count() == 1);
        CHECK(zeroizer.total_bytes() == 16U);
    }
    CHECK(allocator.free_count() == 2);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.total_bytes() == 16U + 24U);
}

// 5. Borrow windows: bounds, leasing, and nested sub-borrows.
void test_borrow_restrictions() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    netsec::SecretBuffer buffer(64,
                                std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
    fill_markers(buffer);

    CHECK_THROWS(buffer.borrow(60, 8), std::out_of_range);
    CHECK(buffer.active_borrows() == 0U);

    {
        netsec::SecretBuffer::Borrow window = buffer.borrow(8, 16);
        CHECK(buffer.active_borrows() == 1U);
        CHECK(window.size() == 16U);
        CHECK(window.at(0) == std::byte{0xA8});
        CHECK(window.at(15) == std::byte{0xA7});
        CHECK_THROWS(window.at(16), std::out_of_range);

        netsec::SecretBuffer::Borrow nested = window.sub_borrow(4, 4);
        CHECK(buffer.active_borrows() == 2U);
        CHECK(nested.at(0) == std::byte{0xAC});
        CHECK_THROWS(nested.sub_borrow(3, 2), std::out_of_range);
        CHECK(buffer.active_borrows() == 2U);  // failed sub-borrow changed no lease

        netsec::SecretBuffer::Borrow moved_nested(std::move(nested));
        CHECK(buffer.active_borrows() == 2U);  // lease travels, not duplicated
        CHECK(moved_nested.at(3) == std::byte{0xAF});
    }
    CHECK(buffer.active_borrows() == 0U);

    // Zero-length window at the exact end is legal and still leases once.
    {
        netsec::SecretBuffer::Borrow tail = buffer.borrow(64, 0);
        CHECK(tail.empty());
        CHECK(buffer.active_borrows() == 1U);
        CHECK_THROWS(tail.at(0), std::out_of_range);
    }
    CHECK(buffer.active_borrows() == 0U);
}

// 6. A throw while nested borrows are live unwinds all leases; the buffer is
//    still owned and eventually wiped exactly once.
void test_nested_borrow_exception_unwind() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    {
        netsec::SecretBuffer buffer(32,
                                    std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                    std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
        fill_markers(buffer);
        try {
            netsec::SecretBuffer::Borrow outer = buffer.borrow(0, 20);
            netsec::SecretBuffer::Borrow inner = outer.sub_borrow(4, 8);
            CHECK(buffer.active_borrows() == 2U);
            throw std::runtime_error("simulated multi-stage failure");
        } catch (const std::runtime_error&) {
            CHECK(buffer.active_borrows() == 0U);
            CHECK(buffer.size() == 32U);
            CHECK(buffer.at(0) == std::byte{0xA0});
        }
        CHECK(allocator.free_count() == 0);
        CHECK(zeroizer.call_count() == 0);
    }
    CHECK(allocator.alloc_count() == 1);
    CHECK(allocator.free_count() == 1);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.call_count() == 1);
    CHECK(zeroizer.total_bytes() == 32U);
}

// 7. Commit transfers ownership; the buffer never wipes early, the owner does.
void test_commit_transfer_then_owner_cleans_once() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    {
        netsec::SecretBuffer buffer(40,
                                    std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                    std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
        fill_markers(buffer);

        netsec::SecretOwner owner = buffer.commit();
        CHECK(buffer.empty());
        CHECK(!owner.empty());
        CHECK(owner.size() == 40U);
        CHECK(owner.data()[9] == std::byte{0xA9});
        CHECK(allocator.alloc_count() == 1);
        CHECK(allocator.free_count() == 0);
        CHECK(zeroizer.call_count() == 0);

        netsec::SecretOwner moved_owner(std::move(owner));
        CHECK(owner.empty());
        CHECK(moved_owner.size() == 40U);
        CHECK(moved_owner.data()[0] == std::byte{0xA0});
    }
    CHECK(allocator.free_count() == 1);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.call_count() == 1);
    CHECK(zeroizer.total_bytes() == 40U);
}

// 8. Commit is refused while a borrow (or nested borrow) is active.
void test_commit_refused_with_borrow() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    netsec::SecretBuffer buffer(16,
                                std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
    fill_markers(buffer);
    {
        netsec::SecretBuffer::Borrow outer = buffer.borrow(0, 10);
        {
            netsec::SecretBuffer::Borrow inner = outer.sub_borrow(2, 4);
            CHECK_THROWS(buffer.commit(), std::logic_error);
            CHECK(buffer.active_borrows() == 2U);
        }
        CHECK_THROWS(buffer.commit(), std::logic_error);
        CHECK(buffer.active_borrows() == 1U);
    }
    CHECK(buffer.active_borrows() == 0U);

    netsec::SecretOwner owner = buffer.commit();
    CHECK(owner.size() == 16U);
    CHECK_THROWS(buffer.commit(), std::logic_error);  // empty now
    CHECK(allocator.free_count() == 0);
}

// 9. Move-assignment with an active borrow is refused and changes nothing.
void test_move_assignment_refused_with_borrow() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    netsec::SecretBuffer target(12,
                                std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
    netsec::SecretBuffer source(8,
                                std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {}),
                                std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {}));
    fill_markers(target);
    {
        netsec::SecretBuffer::Borrow lease = target.borrow(0, 4);
        CHECK_THROWS(target = std::move(source), std::logic_error);
        CHECK(target.size() == 12U);
        CHECK(source.size() == 8U);
        CHECK(target.at(3) == std::byte{0xA3});
    }
    target = std::move(source);
    CHECK(target.size() == 8U);
    CHECK(source.empty());
}

// 10. Multi-stage failure at every construction point: earlier stages are
//     unwound, each allocation wiped and freed exactly once.
void test_transaction_failure_at_each_construction_point() {
    for (int fail_point = 0; fail_point < 3; ++fail_point) {
        TrackingAllocator allocator;
        TrackingZeroizer zeroizer;
        allocator.set_fail_after(fail_point);
        auto alloc_sp = std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {});
        auto zero_sp = std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {});

        try {
            netsec::SecretTransaction txn;
            txn.add_stage(10, alloc_sp, zero_sp);
            fill_markers(txn.stage(0));
            txn.add_stage(20, alloc_sp, zero_sp);
            fill_markers(txn.stage(1));
            txn.add_stage(30, alloc_sp, zero_sp);
            fill_markers(txn.stage(2));
            CHECK(fail_point == 3);  // unreachable when a failure is armed
        } catch (const std::bad_alloc&) {
            // Expected for fail_point 0..2.
        }

        // The throwing allocation itself never returned a pointer, so the
        // number of successful allocations always equals the number of frees.
        const int expected = fail_point;
        CHECK(allocator.alloc_count() == expected);
        CHECK(allocator.free_count() == expected);
        CHECK(allocator.live_count() == 0);
        CHECK(zeroizer.call_count() == expected);
        std::size_t expected_bytes = 0U;
        if (fail_point >= 1) expected_bytes += 10U;
        if (fail_point >= 2) expected_bytes += 20U;
        CHECK(zeroizer.total_bytes() == expected_bytes);
    }
}

// 11. Committed transaction stage is emptied; other stages still self-clean.
void test_transaction_commit_and_partial_unwind() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    auto alloc_sp = std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {});
    auto zero_sp = std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {});
    {
        netsec::SecretOwner owner;
        {
            netsec::SecretTransaction txn;
            txn.add_stage(12, alloc_sp, zero_sp);
            txn.add_stage(24, alloc_sp, zero_sp);
            fill_markers(txn.stage(0));
            fill_markers(txn.stage(1));

            owner = txn.commit_stage(1);
            CHECK(owner.size() == 24U);
            CHECK(txn.stage(1).empty());
            CHECK(txn.stage(0).size() == 12U);
            CHECK(allocator.alloc_count() == 2);
            CHECK(allocator.free_count() == 0);
        }
        // Transaction destruction wiped only the uncommitted 12-byte stage.
        CHECK(allocator.free_count() == 1);
        CHECK(zeroizer.call_count() == 1);
        CHECK(zeroizer.total_bytes() == 12U);
        CHECK(owner.data()[5] == std::byte{0xA5});
    }
    // Owner destruction performs the single wipe+free of the committed bytes.
    CHECK(allocator.alloc_count() == 2);
    CHECK(allocator.free_count() == 2);
    CHECK(allocator.live_count() == 0);
    CHECK(zeroizer.call_count() == 2);
    CHECK(zeroizer.total_bytes() == 12U + 24U);
}

// 12. Zero-size buffers never allocate; defaults and copyability compile.
void test_zero_size_defaults_and_static_constraints() {
    TrackingAllocator allocator;
    TrackingZeroizer zeroizer;
    auto alloc_sp = std::shared_ptr<netsec::IAllocator>(&allocator, [](auto*) {});
    auto zero_sp = std::shared_ptr<netsec::IZeroizer>(&zeroizer, [](auto*) {});
    {
        netsec::SecretBuffer empty(0, alloc_sp, zero_sp);
        CHECK(empty.empty());
        CHECK(empty.size() == 0U);
        CHECK(allocator.alloc_count() == 0);
        CHECK(allocator.free_count() == 0);
        CHECK(zeroizer.call_count() == 0);
        CHECK_THROWS(empty.commit(), std::logic_error);
        CHECK_THROWS(empty.borrow(0, 0), std::logic_error);
    }
    CHECK(allocator.alloc_count() == 0);
    CHECK(allocator.free_count() == 0);

    // Default seams work without injected instances.
    {
        netsec::SecretBuffer buffer(8);
        buffer.write(0, std::byte{0x42});
        CHECK(buffer.at(0) == std::byte{0x42});
    }

    static_assert(!std::is_copy_constructible<netsec::SecretBuffer>::value,
                  "SecretBuffer must not be copy-constructible");
    static_assert(!std::is_copy_assignable<netsec::SecretBuffer>::value,
                  "SecretBuffer must not be copy-assignable");
    static_assert(std::is_move_constructible<netsec::SecretBuffer>::value,
                  "SecretBuffer must be move-constructible");
    static_assert(std::is_move_assignable<netsec::SecretBuffer>::value,
                  "SecretBuffer must be move-assignable");
    static_assert(!std::is_copy_constructible<netsec::SecretOwner>::value,
                  "SecretOwner must not be copy-constructible");
    static_assert(!std::is_copy_assignable<netsec::SecretOwner>::value,
                  "SecretOwner must not be copy-assignable");
    static_assert(std::is_move_constructible<netsec::SecretOwner>::value,
                  "SecretOwner must be move-constructible");
    static_assert(!std::is_copy_constructible<netsec::SecretBuffer::Borrow>::value,
                  "Borrow must not be copy-constructible");
    static_assert(!std::is_copy_assignable<netsec::SecretBuffer::Borrow>::value,
                  "Borrow must not be copy-assignable");
    static_assert(std::is_move_constructible<netsec::SecretBuffer::Borrow>::value,
                  "Borrow must be move-constructible");
    static_assert(!std::is_copy_constructible<netsec::SecretTransaction>::value,
                  "SecretTransaction must not be copy-constructible");
    static_assert(!std::is_copy_assignable<netsec::SecretTransaction>::value,
                  "SecretTransaction must not be copy-assignable");
    static_assert(std::is_move_constructible<netsec::SecretTransaction>::value,
                  "SecretTransaction must be move-constructible");
}

template <class Fn>
void run_test(const char* name, Fn fn) {
    g_current_test = name;
    try {
        fn();
    } catch (const std::exception& ex) {
        ++g_failures;
        std::fprintf(stderr, "FAIL [%s] unexpected exception: %s\n", name, ex.what());
    } catch (...) {
        ++g_failures;
        std::fprintf(stderr, "FAIL [%s] unexpected unknown exception\n", name);
    }
}

}  // namespace

int main() {
    run_test("constructor_throws", test_constructor_throws);
    run_test("uncommitted_zeroized_on_destruction",
             test_uncommitted_zeroized_on_destruction);
    run_test("move_steals_ownership", test_move_steals_ownership);
    run_test("move_assignment_cleans_target", test_move_assignment_cleans_target);
    run_test("borrow_restrictions", test_borrow_restrictions);
    run_test("nested_borrow_exception_unwind", test_nested_borrow_exception_unwind);
    run_test("commit_transfer_then_owner_cleans_once",
             test_commit_transfer_then_owner_cleans_once);
    run_test("commit_refused_with_borrow", test_commit_refused_with_borrow);
    run_test("move_assignment_refused_with_borrow",
             test_move_assignment_refused_with_borrow);
    run_test("transaction_failure_at_each_construction_point",
             test_transaction_failure_at_each_construction_point);
    run_test("transaction_commit_and_partial_unwind",
             test_transaction_commit_and_partial_unwind);
    run_test("zero_size_defaults_and_static_constraints",
             test_zero_size_defaults_and_static_constraints);

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}

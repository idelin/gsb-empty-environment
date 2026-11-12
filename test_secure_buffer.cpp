#include "netsec.hpp"
#include "observable_allocator.hpp"

#include <cstring>
#include <iostream>
#include <iterator>
#include <new>
#include <stdexcept>
#include <vector>

namespace {

using byte = unsigned char;
using alloc_t = netsec::test::observable_allocator<byte>;
using buffer_t = netsec::basic_secure_buffer<alloc_t>;
using netsec::test::allocator_state;

int g_checks = 0;
int g_failures = 0;

void record(bool condition, const char* expression, const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "CHECK failed: " << expression << " (" << file << ':' << line
                  << ")\n";
    }
}

#define CHECK(expr) record(static_cast<bool>(expr), #expr, __FILE__, __LINE__)

#define CHECK_THROWS_AS(expr, exception_type)                                     \
    do {                                                                          \
        bool caught = false;                                                      \
        try {                                                                     \
            expr;                                                                 \
        } catch (const exception_type&) {                                         \
            caught = true;                                                       \
        } catch (...) {                                                           \
            record(false, "wrong exception type for " #expr, __FILE__, __LINE__); \
        }                                                                         \
        record(caught, "no exception from " #expr, __FILE__, __LINE__);          \
    } while (false)

byte secret_byte(std::size_t index) {
    return static_cast<byte>((index * 71 + 17) & 0xFF);
}

void fill_secret(buffer_t& buffer, std::size_t count) {
    for (std::size_t index = 0; index < count; ++index) {
        buffer.push_back(secret_byte(index));
    }
}

void expect_content(const buffer_t& buffer, std::size_t count) {
    CHECK(buffer.size() == count);
    for (std::size_t index = 0; index < count; ++index) {
        CHECK(buffer[index] == secret_byte(index));
    }
}

// Every block handed back must have been securely cleared before deallocation.
void expect_all_snapshots_zeroed(const std::shared_ptr<allocator_state>& state) {
    for (const auto& snapshot : state->deallocations) {
        CHECK(snapshot.all_zero);
        if (!snapshot.all_zero) {
            std::cerr << "  leaked block of " << snapshot.size << " bytes\n";
        }
    }
}

buffer_t make_seeded_buffer(const alloc_t& allocator, std::size_t count) {
    buffer_t buffer(allocator);
    fill_secret(buffer, count);
    return buffer;
}

// Capacity growth is geometric and old blocks are zeroed on every reallocation.
void test_growth_clears_old_blocks() {
    auto state = std::make_shared<allocator_state>();
    alloc_t allocator(state);

    buffer_t buffer(allocator);
    fill_secret(buffer, 40);

    CHECK(buffer.size() == 40);
    CHECK(buffer.capacity() == 64);

    const std::size_t expected_caps[] = {16, 32, 64};
    CHECK(state->allocations.size() == 3);
    for (std::size_t index = 0; index < 3; ++index) {
        CHECK(state->allocations[index].size == expected_caps[index]);
        CHECK(!state->allocations[index].failed);
    }

    // Each superseded block is deallocated only after being cleared.
    CHECK(state->deallocations.size() == 2);
    CHECK(state->deallocations[0].size == 16);
    CHECK(state->deallocations[0].all_zero);
    CHECK(state->deallocations[1].size == 32);
    CHECK(state->deallocations[1].all_zero);

    expect_content(buffer, 40);
    expect_all_snapshots_zeroed(state);
}

// reserve() failure leaves value, size and capacity untouched.
void test_reserve_failure_preserves_value() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 20);

    const auto old_capacity = buffer.capacity();
    const auto old_allocations = state->allocation_count;
    state->fail_at = static_cast<long long>(old_allocations);

    CHECK_THROWS_AS(buffer.reserve(200), std::bad_alloc);

    state->reset_failures();
    CHECK(buffer.size() == 20);
    CHECK(buffer.capacity() == old_capacity);
    expect_content(buffer, 20);
    CHECK(state->allocation_count == old_allocations + 1);
    CHECK(state->allocations.back().failed);

    // The object remains usable after the failed growth.
    buffer.reserve(200);
    CHECK(buffer.capacity() >= 200);
    expect_content(buffer, 20);
    expect_all_snapshots_zeroed(state);
}

// resize() upward failure keeps the original object intact.
void test_resize_up_failure_preserves_value() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 10);

    const auto old_capacity = buffer.capacity();
    state->fail_at = static_cast<long long>(state->allocation_count);

    CHECK_THROWS_AS(buffer.resize(100), std::bad_alloc);

    state->reset_failures();
    CHECK(buffer.size() == 10);
    CHECK(buffer.capacity() == old_capacity);
    expect_content(buffer, 10);

    buffer.resize(50, byte{0x7F});
    CHECK(buffer.size() == 50);
    CHECK(buffer.capacity() > old_capacity);
    for (std::size_t index = 0; index < 10; ++index) {
        CHECK(buffer[index] == secret_byte(index));
    }
    for (std::size_t index = 10; index < 50; ++index) {
        CHECK(buffer[index] == 0x7F);
    }
    expect_all_snapshots_zeroed(state);
}

// push_back() failure at the growth allocation point loses nothing.
void test_push_back_failure_preserves_value() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 16);
    CHECK(buffer.capacity() == 16);

    state->fail_at = static_cast<long long>(state->allocation_count);
    CHECK_THROWS_AS(buffer.push_back(byte{0x42}), std::bad_alloc);

    state->reset_failures();
    CHECK(buffer.size() == 16);
    CHECK(buffer.capacity() == 16);
    expect_content(buffer, 16);

    buffer.push_back(byte{0x42});
    CHECK(buffer.size() == 17);
    CHECK(buffer.capacity() == 32);
    CHECK(buffer[16] == 0x42);
    expect_all_snapshots_zeroed(state);
}

// append() failure at the growth allocation point loses nothing.
void test_append_failure_preserves_value() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 12);

    const byte tail[] = {1, 2, 3, 4, 5, 6, 7, 8};
    state->fail_at = static_cast<long long>(state->allocation_count);
    CHECK_THROWS_AS(buffer.append(tail, sizeof(tail)), std::bad_alloc);

    state->reset_failures();
    CHECK(buffer.size() == 12);
    expect_content(buffer, 12);

    buffer.append(tail, sizeof(tail));
    CHECK(buffer.size() == 20);
    for (std::size_t index = 0; index < sizeof(tail); ++index) {
        CHECK(buffer[12 + index] == tail[index]);
    }
    expect_all_snapshots_zeroed(state);
}

// Shrinking clears exactly the truncated bytes; the prefix survives.
void test_shrink_clears_truncated_bytes() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 30);

    const auto capacity_before = buffer.capacity();
    const auto deallocations_before = state->deallocation_count;

    buffer.resize(10);

    CHECK(buffer.size() == 10);
    CHECK(buffer.capacity() == capacity_before);  // no reallocation on shrink
    CHECK(state->deallocation_count == deallocations_before);
    expect_content(buffer, 10);

    // The truncated range now reads zero through the still-live allocation.
    const byte* raw = buffer.data();
    for (std::size_t index = 10; index < 30; ++index) {
        CHECK(raw[index] == 0);
    }

    // Growing again must not resurrect the old truncated secret.
    buffer.resize(25);
    CHECK(buffer.size() == 25);
    for (std::size_t index = 10; index < 25; ++index) {
        CHECK(buffer[index] == 0);
    }
    expect_all_snapshots_zeroed(state);
}

// Copy constructor failure produces no partially constructed object or leak.
void test_copy_construction_failure() {
    auto source_state = std::make_shared<allocator_state>();
    buffer_t source = make_seeded_buffer(alloc_t(source_state), 24);

    auto target_state = std::make_shared<allocator_state>();
    target_state->fail_at = 0;

    CHECK_THROWS_AS(buffer_t copy(source, alloc_t(target_state)), std::bad_alloc);
    target_state->reset_failures();

    expect_content(source, 24);
    CHECK(!target_state->has_live_blocks());
    CHECK(target_state->bytes_outstanding == 0);
    CHECK(target_state->allocations.size() == 1);
    CHECK(target_state->allocations.back().failed);
}

// Copy assignment failure gives the strong guarantee.
void test_copy_assignment_failure() {
    auto left_state = std::make_shared<allocator_state>();
    buffer_t left = make_seeded_buffer(alloc_t(left_state), 18);

    auto right_state = std::make_shared<allocator_state>();
    buffer_t right = make_seeded_buffer(alloc_t(right_state), 8);
    for (std::size_t index = 0; index < right.size(); ++index) {
        right[index] = static_cast<byte>(0xA0 + index);
    }

    left_state->fail_at = static_cast<long long>(left_state->allocation_count);
    CHECK_THROWS_AS(left = right, std::bad_alloc);
    left_state->reset_failures();

    CHECK(left.size() == 18);
    expect_content(left, 18);
    CHECK(right.size() == 8);
    CHECK(left_state->bytes_outstanding == left.capacity());
    CHECK(left_state->live.size() == 1);
    expect_all_snapshots_zeroed(left_state);

    left = right;
    CHECK(left.size() == 8);
    for (std::size_t index = 0; index < 8; ++index) {
        CHECK(left[index] == static_cast<byte>(0xA0 + index));
    }
}

// Self copy assignment is a no-op with no allocation.
void test_self_copy_assignment() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 15);

    const auto allocations_before = state->allocation_count;
    buffer_t& alias = buffer;
    buffer = alias;

    CHECK(state->allocation_count == allocations_before);
    CHECK(buffer.size() == 15);
    expect_content(buffer, 15);
}

// Moved-from object cannot reach the transferred content.
void test_move_construction_and_assignment() {
    auto state = std::make_shared<allocator_state>();
    buffer_t source = make_seeded_buffer(alloc_t(state), 22);

    const byte* source_pointer = source.data();
    const auto deallocations_before = state->deallocation_count;

    buffer_t moved(std::move(source));

    CHECK(moved.data() == source_pointer);  // ownership stolen, no copy
    CHECK(moved.size() == 22);
    expect_content(moved, 22);
    CHECK(source.data() == nullptr);
    CHECK(source.size() == 0);
    CHECK(source.capacity() == 0);
    CHECK(state->deallocation_count == deallocations_before);
    CHECK_THROWS_AS(source.at(0), std::out_of_range);

    alloc_t target_allocator(state);
    buffer_t target(target_allocator);
    target = std::move(moved);

    CHECK(target.data() == source_pointer);
    CHECK(target.size() == 22);
    expect_content(target, 22);
    CHECK(moved.data() == nullptr);
    CHECK(moved.size() == 0);
    CHECK_THROWS_AS(moved.at(0), std::out_of_range);
}

// Self move assignment keeps the object valid and its content reachable.
void test_self_move_assignment() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 12);
    const byte* pointer = buffer.data();

    buffer_t& alias = buffer;
    buffer = std::move(alias);

    CHECK(buffer.data() == pointer);
    CHECK(buffer.size() == 12);
    expect_content(buffer, 12);
}

// clear() wipes the used region while retaining capacity.
void test_clear_wipes_content() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 20);

    buffer.clear();
    CHECK(buffer.size() == 0);
    CHECK(buffer.capacity() >= 20);
    const byte* raw = buffer.data();
    for (std::size_t index = 0; index < 20; ++index) {
        CHECK(raw[index] == 0);
    }
}

// Destruction securely clears the full allocation, including spare capacity.
void test_destructor_zeroes_full_block() {
    auto state = std::make_shared<allocator_state>();
    {
        alloc_t inner_allocator(state);
        buffer_t buffer(inner_allocator);
        fill_secret(buffer, 5);
        CHECK(buffer.capacity() == 16);
    }

    CHECK(state->deallocation_count == 1);
    const auto& snapshot = state->deallocations.back();
    CHECK(snapshot.size == 16);
    CHECK(snapshot.all_zero);
}

// Size-based constructor allocation failure leaks nothing.
void test_size_constructor_failure() {
    auto state = std::make_shared<allocator_state>();
    state->fail_predicate = [](std::size_t requested) { return requested >= 64; };

    CHECK_THROWS_AS(buffer_t buffer(100, alloc_t(state)), std::bad_alloc);
    state->reset_failures();
    CHECK(!state->has_live_blocks());
    CHECK(state->bytes_outstanding == 0);
}

// reserve() grows geometrically and never shrinks an existing allocation.
void test_reserve_capacity_policy() {
    auto state = std::make_shared<allocator_state>();
    alloc_t exact_allocator(state);
    buffer_t buffer(exact_allocator);

    buffer.reserve(40);
    CHECK(buffer.size() == 0);

    CHECK(buffer.capacity() == 64);
    buffer.reserve(48);
    CHECK(buffer.capacity() == 64);
    CHECK(state->allocations.size() == 1);

    buffer.reserve(100);
    CHECK(buffer.capacity() == 128);
    CHECK(state->allocations.back().size == 128);

    fill_secret(buffer, 40);
    expect_content(buffer, 40);
}

// Standard allocator path: basic end-to-end behavior with the default alias.
void test_default_allocator_smoke() {
    netsec::secure_buffer buffer;
    const char message[] = "top-secret payload";
    buffer.append(reinterpret_cast<const byte*>(message), sizeof(message));
    CHECK(buffer.size() == sizeof(message));
    CHECK(std::memcmp(buffer.data(), message, sizeof(message)) == 0);

    buffer.resize(8);
    CHECK(buffer.size() == 8);
    CHECK(std::memcmp(buffer.data(), message, 8) == 0);
}

// Empty containers survive self assignment and moves without allocations.
void test_empty_container_edge_cases() {
    auto state = std::make_shared<allocator_state>();
    alloc_t edge_allocator(state);

    {
        buffer_t empty(edge_allocator);
        buffer_t& alias = empty;
        empty = alias;
        CHECK(empty.size() == 0);
        CHECK(empty.empty());
        CHECK(empty.data() == nullptr);
        CHECK(empty.begin() == empty.end());
        CHECK_THROWS_AS(empty.at(0), std::out_of_range);

        empty = std::move(alias);
        CHECK(empty.size() == 0);
        CHECK(empty.data() == nullptr);
        CHECK(state->allocation_count == 0);
    }

    {
        buffer_t source(edge_allocator);
        buffer_t destination(std::move(source));
        CHECK(destination.size() == 0);
        CHECK(destination.data() == nullptr);
        CHECK(source.data() == nullptr);

        buffer_t assigned(edge_allocator);
        assigned = std::move(destination);
        CHECK(assigned.size() == 0);
        CHECK(destination.data() == nullptr);
    }

    CHECK(state->allocation_count == 0);
    CHECK(state->deallocation_count == 0);
}

// Iterators cover exactly the logical size and reflect cleared bytes.
void test_iterators_and_shrink_via_clear() {
    auto state = std::make_shared<allocator_state>();
    buffer_t buffer = make_seeded_buffer(alloc_t(state), 12);

    std::size_t walked = 0;
    for (byte value : buffer) {
        CHECK(value == secret_byte(walked));
        ++walked;
    }
    CHECK(walked == 12);

    buffer.resize(4);
    auto iterator = buffer.begin();
    std::advance(iterator, 4);
    CHECK(iterator == buffer.end());
    CHECK(static_cast<std::size_t>(buffer.end() - buffer.begin()) == 4);
}

}  // namespace

int main() {
    struct test_case {
        const char* name;
        void (*run)();
    };

    const test_case cases[] = {
        {"growth clears old blocks", test_growth_clears_old_blocks},
        {"reserve failure preserves value", test_reserve_failure_preserves_value},
        {"resize up failure preserves value", test_resize_up_failure_preserves_value},
        {"push_back failure preserves value", test_push_back_failure_preserves_value},
        {"append failure preserves value", test_append_failure_preserves_value},
        {"shrink clears truncated bytes", test_shrink_clears_truncated_bytes},
        {"copy construction failure", test_copy_construction_failure},
        {"copy assignment failure", test_copy_assignment_failure},
        {"self copy assignment", test_self_copy_assignment},
        {"move construction and assignment", test_move_construction_and_assignment},
        {"self move assignment", test_self_move_assignment},
        {"clear wipes content", test_clear_wipes_content},
        {"destructor zeroes full block", test_destructor_zeroes_full_block},
        {"size constructor failure", test_size_constructor_failure},
        {"reserve capacity policy", test_reserve_capacity_policy},
        {"default allocator smoke", test_default_allocator_smoke},
        {"empty container edge cases", test_empty_container_edge_cases},
        {"iterators and shrink", test_iterators_and_shrink_via_clear},
    };

    for (const auto& test : cases) {
        const int failures_before = g_failures;
        test.run();
        if (g_failures == failures_before) {
            std::cout << "[ PASS ] " << test.name << '\n';
        } else {
            std::cout << "[ FAIL ] " << test.name << '\n';
        }
    }

    std::cout << "\n" << g_checks << " checks, " << g_failures << " failures\n";
    return g_failures == 0 ? 0 : 1;
}

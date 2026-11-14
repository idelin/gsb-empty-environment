#include "netsec.hpp"

#include <array>
#include <atomic>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <limits>
#include <optional>
#include <stdexcept>
#include <string>
#include <thread>

#if !defined(_WIN32)
#  include <sys/mman.h>
#  include <unistd.h>
#  if !defined(MAP_ANONYMOUS) && defined(MAP_ANON)
#    define MAP_ANONYMOUS MAP_ANON
#  endif
#endif

namespace {

int failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            std::fprintf(                                                      \
                stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #condition);   \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

#define CHECK_EQ(actual, expected)                                             \
    do {                                                                       \
        const auto check_actual = (actual);                                    \
        const auto check_expected = (expected);                                \
        if (!(check_actual == check_expected)) {                               \
            std::fprintf(                                                      \
                stderr,                                                        \
                "FAIL %s:%d: %s == %s (got %llu, expected %llu)\n",            \
                __FILE__,                                                      \
                __LINE__,                                                      \
                #actual,                                                       \
                #expected,                                                     \
                static_cast<unsigned long long>(check_actual),                 \
                static_cast<unsigned long long>(check_expected));              \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

static_assert(noexcept(netsec::compiler_barrier()));
static_assert(noexcept(netsec::speculation_barrier()));
static_assert(noexcept(netsec::memory_barrier()));
static_assert(noexcept(netsec::enter_trust_boundary()));
static_assert(noexcept(netsec::leave_trust_boundary()));
static_assert(netsec::mask_index<std::size_t>(7, 8) == 7);
static_assert(netsec::mask_index<std::size_t>(8, 8) == 0);
static_assert(netsec::mask_index<std::size_t>(0, 0) == 0);
static_assert(netsec::mask_index<std::ptrdiff_t>(-1, 8) == 0);
static_assert(netsec::mask_from_bool<std::uintptr_t>(true) ==
              std::numeric_limits<std::uintptr_t>::max());
static_assert(netsec::mask_from_bool<std::uintptr_t>(false) == 0);

NETSEC_NOINLINE std::uint8_t masked_speculative_body(
    const std::uint8_t* base, std::size_t length, std::size_t index) noexcept;

void test_masking() {
    const std::array<std::uint8_t, 8> data{1, 2, 3, 4, 5, 6, 7, 8};

    CHECK_EQ(netsec::mask_index<std::size_t>(0, 8), 0);
    CHECK_EQ(netsec::mask_index<std::size_t>(7, 8), 7);
    CHECK_EQ(netsec::mask_index<std::size_t>(8, 8), 0);
    CHECK_EQ(netsec::mask_index<std::size_t>(1000, 8), 0);
    CHECK_EQ(netsec::mask_index<std::size_t>(
                 std::numeric_limits<std::size_t>::max(), 8),
             0);
    CHECK_EQ(netsec::mask_index<std::ptrdiff_t>(-7, 8), 0);
    CHECK_EQ(netsec::masked_index<std::size_t>(3, 8), 3);

    const auto* pointer = data.data();
    CHECK(netsec::mask_pointer(pointer, true) == pointer);
    CHECK(netsec::mask_pointer(pointer, false) == nullptr);
    CHECK(netsec::mask_pointer(pointer, false, data.data() + 1) ==
          data.data() + 1);
    CHECK(netsec::mask_pointer(pointer, 1, data.size()) == pointer);
    CHECK(netsec::mask_pointer(pointer, 8, data.size()) == nullptr);
    CHECK(netsec::masked_pointer(pointer, true) == pointer);

    auto* odd_pointer = reinterpret_cast<std::uint8_t*>(
        static_cast<std::uintptr_t>(0x12345678u));
    CHECK_EQ(reinterpret_cast<std::uintptr_t>(
                 netsec::mask_pointer_bits(odd_pointer, ~std::uintptr_t{0xF})),
             0x12345670u);
}

void test_legitimate_access() {
    const std::array<std::uint64_t, 8> values{
        0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80};

    for (std::size_t index = 0; index < values.size(); ++index) {
        CHECK_EQ(netsec::speculation_safe_load(values, index), values[index]);
        CHECK_EQ(netsec::safe_load(values.data(), values.size(), index),
                 values[index]);
        CHECK_EQ(netsec::secure_load(values.data(), values.size(), index),
                 values[index]);
        CHECK_EQ(netsec::mitigated_load(values.data(), values.size(), index),
                 values[index]);
    }

    const std::uint8_t bytes[] = {9, 8, 7, 6};
    CHECK_EQ(netsec::speculation_safe_load(bytes, 2), 7);

    const auto missing =
        netsec::speculation_safe_load_optional(values, values.size());
    CHECK(!missing.has_value());
    const auto present = netsec::speculation_safe_load_optional(values, 4);
    CHECK(present.has_value());
    CHECK_EQ(*present, 0x50);
}

void test_out_of_bounds_access() {
    const std::array<std::uint8_t, 8> data{1, 2, 3, 4, 5, 6, 7, 8};

    for (std::size_t index = data.size(); index < data.size() + 32; ++index) {
        CHECK_EQ(netsec::speculation_safe_load(data, index), 0);
    }
    CHECK_EQ(netsec::speculation_safe_load(
                 data.data(), data.size(), std::numeric_limits<std::size_t>::max()),
             0);

    CHECK_EQ(
        netsec::speculation_safe_load(
            data.data(), data.size(), 99, static_cast<std::uint8_t>(0xAA)),
        static_cast<std::uint8_t>(0xAA));

    const std::uint8_t* null_base = nullptr;
    CHECK_EQ(netsec::speculation_safe_load(null_base, 8, 0), 0);
    CHECK_EQ(netsec::speculation_safe_load(data.data(), 0, 0), 0);
}

#if !defined(_WIN32)
void test_mispredicted_load_guard_page() {
    const long page_size = ::sysconf(_SC_PAGESIZE);
    CHECK(page_size > 0);
    if (page_size <= 0) {
        return;
    }

    const std::size_t page = static_cast<std::size_t>(page_size);
    auto* region = static_cast<std::uint8_t*>(::mmap(
        nullptr,
        page * 2,
        PROT_READ | PROT_WRITE,
        MAP_PRIVATE | MAP_ANONYMOUS,
        -1,
        0));
    CHECK(region != MAP_FAILED);
    if (region == MAP_FAILED) {
        return;
    }

    CHECK_EQ(::mprotect(region + page, page, PROT_NONE), 0);

    auto* values = reinterpret_cast<std::uint64_t*>(region + page - 16);
    values[0] = 0x1111111111111111ULL;
    values[1] = 0x2222222222222222ULL;

    // A real bounds check would reject 4 and scheduled speculative execution
    // would still attempt the load.  The masked load must stay in bounds.
    const std::uint64_t out_of_bounds =
        netsec::speculation_safe_load(values, 2, 4);
    CHECK_EQ(out_of_bounds, 0);

    auto* bytes = reinterpret_cast<std::uint8_t*>(values);
    const std::uint8_t masked_value = masked_speculative_body(
        bytes, 16, std::numeric_limits<std::size_t>::max());
    CHECK_EQ(masked_value, bytes[0]);

    CHECK_EQ(::munmap(region, page * 2), 0);
}
#endif

struct Probe {
    std::array<unsigned, 256> hits{};
};

// Models the instruction stream that executes when the bounds branch is
// mispredicted: the check is gone, but the masking and barrier remain.
NETSEC_NOINLINE std::uint8_t masked_speculative_body(
    const std::uint8_t* base, std::size_t length, std::size_t index) noexcept {
    const std::size_t safe_index = netsec::mask_index(index, length);
    netsec::speculation_barrier();
    return base[safe_index];
}

// Deliberately vulnerable baseline used to prove the side-channel probe can
// observe the out-of-range byte when no mitigation is present.
NETSEC_NOINLINE std::uint8_t unmasked_speculative_body(
    const std::uint8_t* base, std::size_t, std::size_t index) noexcept {
    return base[index];
}

void touch_probe(Probe& probe, std::uint8_t value) {
    ++probe.hits[value];
}

void test_side_channel_recovery() {
    constexpr std::size_t logical_length = 16;
    std::array<std::uint8_t, logical_length + 1> secret{};
    for (std::size_t index = 0; index < logical_length; ++index) {
        secret[index] = static_cast<std::uint8_t>(index + 1);
    }
    constexpr std::uint8_t out_of_range_secret = 0xA5;
    secret[logical_length] = out_of_range_secret;

    {
        Probe probe;
        const std::uint8_t value = netsec::speculation_safe_load(
            secret.data(), logical_length, logical_length);
        touch_probe(probe, value);
        CHECK_EQ(value, 0);
        CHECK_EQ(probe.hits[out_of_range_secret], 0);
        CHECK_EQ(probe.hits[0], 1);
    }

    {
        Probe probe;
        const std::uint8_t value = masked_speculative_body(
            secret.data(), logical_length, logical_length);
        touch_probe(probe, value);
        CHECK_EQ(value, secret[0]);
        CHECK_EQ(probe.hits[out_of_range_secret], 0);
    }

    {
        Probe probe;
        const std::uint8_t value = unmasked_speculative_body(
            secret.data(), logical_length, logical_length);
        touch_probe(probe, value);
        CHECK_EQ(value, out_of_range_secret);
        CHECK_EQ(probe.hits[out_of_range_secret], 1);
    }

    {
        Probe probe;
        const std::uint8_t value = netsec::speculation_safe_load(
            secret.data(), logical_length, 3);
        touch_probe(probe, value);
        CHECK_EQ(value, secret[3]);
        CHECK_EQ(probe.hits[secret[3]], 1);
    }
}

void test_trust_boundaries() {
    netsec::enter_trust_boundary();
    netsec::leave_trust_boundary();
    netsec::trust_boundary_enter();
    netsec::trust_boundary_exit();

    {
        netsec::TrustBoundary guard;
        CHECK_EQ(netsec::call_across_trust_boundary(
                     [](int value) { return value + 1; }, 41),
                 42);
    }

    bool caught = false;
    try {
        (void)netsec::call_across_trust_boundary([]() -> int {
            throw std::runtime_error("leave boundary on unwind");
        });
    } catch (const std::runtime_error&) {
        caught = true;
    }
    CHECK(caught);

    const std::string result = netsec::across_trust_boundary(
        [](const std::string& input) { return input + "!"; }, std::string{"ok"});
    CHECK(result == "ok!");
}

void test_memory_barrier_publication() {
    for (int iteration = 0; iteration < 1000; ++iteration) {
        std::atomic<bool> ready{false};
        int payload = 0;
        bool observed = false;

        std::thread producer([&]() {
            payload = 0x5A5A;
            netsec::memory_barrier();
            ready.store(true, std::memory_order_release);
        });
        std::thread consumer([&]() {
            while (!ready.load(std::memory_order_acquire)) {
                std::this_thread::yield();
            }
            netsec::memory_barrier();
            observed = (payload == 0x5A5A);
        });

        producer.join();
        consumer.join();
        CHECK(observed);
    }
}

void test_c_abi() {
    CHECK_EQ(netsec_abi_version(), 1u);

    netsec_compiler_barrier();
    netsec_speculation_barrier();
    netsec_memory_barrier();
    netsec_full_barrier();
    netsec_trust_boundary_enter();
    netsec_trust_boundary_leave();

    CHECK_EQ(netsec_mask_index(7, 8), 7);
    CHECK_EQ(netsec_mask_index(8, 8), 0);

    int value = 0;
    void* pointer = &value;
    CHECK(netsec_mask_pointer(pointer, 1, nullptr) == pointer);
    CHECK(netsec_mask_pointer(pointer, 0, nullptr) == nullptr);

    const std::uint8_t bytes[] = {1, 2, 3, 4};
    CHECK_EQ(netsec_speculation_safe_load_u8(bytes, 4, 2), 3);
    CHECK_EQ(netsec_speculation_safe_load_u8(bytes, 4, 4), 0);

    const std::uint64_t words[] = {10, 20, 30};
    CHECK_EQ(netsec_speculation_safe_load_u64(words, 3, 1), 20);
    CHECK_EQ(netsec_speculation_safe_load_u64(words, 3, 9), 0);
}

}  // namespace

int main() {
    test_masking();
    test_legitimate_access();
    test_out_of_bounds_access();
#if !defined(_WIN32)
    test_mispredicted_load_guard_page();
#endif
    test_side_channel_recovery();
    test_trust_boundaries();
    test_memory_barrier_publication();
    test_c_abi();

    if (failures != 0) {
        std::fprintf(stderr, "%d test(s) failed\n", failures);
        return EXIT_FAILURE;
    }

    std::printf("all netsec tests passed\n");
    return EXIT_SUCCESS;
}

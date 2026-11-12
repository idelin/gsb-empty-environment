// Unit tests for the SMI communication-buffer validation library.
//
// These tests run on the host. Fake "protected" addresses are exercised only
// through the non-dereferencing validation path; end-to-end copy tests use
// real heap buffers that stand in for OS-owned communication memory.
#include "smm_buffer_guard.hpp"

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <limits>
#include <vector>

using namespace netsec::smm;

namespace {

int g_checks = 0;
int g_failures = 0;

#define CHECK(cond)                                                      \
    do {                                                                 \
        ++g_checks;                                                      \
        if (!(cond)) {                                                   \
            ++g_failures;                                                \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);  \
        }                                                                \
    } while (0)

#define CHECK_EQ(actual, expected)                                              \
    do {                                                                        \
        ++g_checks;                                                             \
        auto actual_value = (actual);                                           \
        auto expected_value = (expected);                                       \
        if (!(actual_value == expected_value)) {                                \
            ++g_failures;                                                       \
            std::printf("FAIL %s:%d: %s == %s\n", __FILE__, __LINE__, #actual,  \
                        #expected);                                             \
        }                                                                       \
    } while (0)

const Guid kAllowedGuid = {0x11223344,
                           0x5566,
                           0x7788,
                           {0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}};
const Guid kOtherGuid = {0xdeadbeef,
                         0x0000,
                         0x0000,
                         {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}};

// Fake protected layout (never dereferenced).
constexpr uintptr_t kSmramBase = 0x80000000u;
constexpr std::size_t kSmramLength = 0x00400000u;
constexpr uintptr_t kProtectedBase = 0xE0000000u;
constexpr std::size_t kProtectedLength = 0x1000u;
constexpr std::size_t kMaxBuffer = 4096u;

Result<BufferGuard> make_test_guard(IStagingAllocator* allocator = nullptr) {
    return BufferGuard::create(MemoryRange{kSmramBase, kSmramLength},
                               {MemoryRange{kProtectedBase, kProtectedLength}},
                               {kAllowedGuid}, kMaxBuffer, allocator);
}

// Builds a communication buffer the way an OS would: header followed by a
// message payload.
std::vector<uint8_t> make_comm_buffer(const Guid& guid,
                                      const uint8_t* message,
                                      std::size_t message_length) {
    std::vector<uint8_t> buffer(kCommHeaderSize + message_length, 0);
    std::memcpy(buffer.data(), &guid, sizeof(guid));
    std::memcpy(buffer.data() + offsetof(CommHeader, message_length),
                &message_length, sizeof(message_length));
    if (message != nullptr && message_length != 0) {
        std::memcpy(buffer.data() + kCommHeaderSize, message, message_length);
    }
    return buffer;
}

uintptr_t address_of(const std::vector<uint8_t>& buffer) {
    return reinterpret_cast<uintptr_t>(buffer.data());
}

// Payload placed inside a message: an embedded pointer plus its length.
struct NestedPayload {
    uintptr_t nested_base;
    std::size_t nested_length;
    uint8_t marker;
};

class NullAllocator final : public IStagingAllocator {
public:
    void* allocate(std::size_t) noexcept override { return nullptr; }
    void deallocate(void*) noexcept override {}
};

}  // namespace

// --- overflow-safe arithmetic ---------------------------------------------

static void test_checked_add_range() {
    uintptr_t end = 0;
    CHECK(checked_add_range(0x1000, 0x50, end));
    CHECK_EQ(end, static_cast<uintptr_t>(0x1050));

    // base + length wraps the address space.
    CHECK(!checked_add_range(std::numeric_limits<uintptr_t>::max(), 1, end));
    CHECK(!checked_add_range(std::numeric_limits<uintptr_t>::max() - 0xF,
                             0x100, end));
    // Null base and zero length are never valid external ranges.
    CHECK(!checked_add_range(0, 0x10, end));
    CHECK(!checked_add_range(0x1000, 0, end));

    CHECK(ranges_overlap(0x10, 0x20, 0x18, 0x30));
    CHECK(!ranges_overlap(0x10, 0x20, 0x20, 0x30));  // adjacent, no overlap
}

// --- protected-range boundaries -------------------------------------------

static void test_smram_and_protected_ranges() {
    auto guard_result = make_test_guard();
    CHECK(guard_result.ok());
    const BufferGuard& guard = guard_result.value();

    // Pointer entirely inside SMRAM.
    CHECK_EQ(guard.validate_external_range(kSmramBase + 0x100, 0x20),
             Status::InProtectedRange);
    // Pointer entirely inside the extra protected range.
    CHECK_EQ(guard.validate_external_range(kProtectedBase, 0x10),
             Status::InProtectedRange);
    // Range straddling the SMRAM boundary must be rejected as a whole.
    CHECK_EQ(guard.validate_external_range(kSmramBase - 0x10, 0x20),
             Status::InProtectedRange);
    CHECK_EQ(guard.validate_external_range(kSmramBase + kSmramLength - 0x10,
                                           0x20),
             Status::InProtectedRange);

    // Ranges adjacent to SMRAM (no overlap) are acceptable as addresses.
    CHECK_EQ(guard.validate_external_range(kSmramBase - 0x10, 0x10),
             Status::Ok);
    CHECK_EQ(guard.validate_external_range(kSmramBase + kSmramLength, 0x10),
             Status::Ok);

    // Null/zero-length external ranges.
    CHECK_EQ(guard.validate_external_range(0, 0x10),
             Status::InvalidArgument);
    CHECK_EQ(guard.validate_external_range(0x1000, 0),
             Status::InvalidArgument);

    // Length exceeding the configured maximum.
    CHECK_EQ(guard.validate_external_range(0x1000, kMaxBuffer + 1),
             Status::TooLarge);

    // Wrap-around lengths are caught before any dereference.
    CHECK_EQ(guard.validate_external_range(
                 std::numeric_limits<uintptr_t>::max() - 0xF, 0x100),
             Status::Overflow);
    CHECK_EQ(guard.validate_external_range(
                 std::numeric_limits<uintptr_t>::max(), 1),
             Status::Overflow);
}

// --- guard configuration is fail-closed -----------------------------------

static void test_configuration_validation() {
    // Empty GUID whitelist rejects construction.
    auto no_guids = BufferGuard::create(MemoryRange{kSmramBase, kSmramLength},
                                        {}, {}, kMaxBuffer);
    CHECK_EQ(no_guids.status(), Status::InvalidArgument);

    // Zero max size.
    auto zero_max = BufferGuard::create(MemoryRange{kSmramBase, kSmramLength},
                                        {}, {kAllowedGuid}, 0);
    CHECK_EQ(zero_max.status(), Status::InvalidArgument);

    // Zero-length SMRAM range.
    auto bad_smram = BufferGuard::create(MemoryRange{kSmramBase, 0},
                                         {}, {kAllowedGuid}, kMaxBuffer);
    CHECK_EQ(bad_smram.status(), Status::InvalidArgument);

    // Overflowing protected range.
    auto overflowing = BufferGuard::create(
        MemoryRange{std::numeric_limits<uintptr_t>::max(), 0x10},
        {}, {kAllowedGuid}, kMaxBuffer);
    CHECK_EQ(overflowing.status(), Status::InvalidArgument);

    // Overlapping protected ranges.
    auto overlapping = BufferGuard::create(
        MemoryRange{kSmramBase, kSmramLength},
        {MemoryRange{kSmramBase + 0x10, 0x100}}, {kAllowedGuid}, kMaxBuffer);
    CHECK_EQ(overlapping.status(), Status::InvalidArgument);

    // Adjacent protected ranges are valid.
    auto adjacent = BufferGuard::create(
        MemoryRange{kSmramBase, kSmramLength},
        {MemoryRange{kSmramBase + kSmramLength, 0x100}},
        {kAllowedGuid}, kMaxBuffer);
    CHECK(adjacent.ok());
}

// --- happy path: validate, copy, parse from the snapshot ------------------

static void test_valid_buffer_accepted() {
    auto guard_result = make_test_guard();
    CHECK(guard_result.ok());

    const uint8_t message[] = {0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x02, 0x03};
    auto os_buffer = make_comm_buffer(kAllowedGuid, message, sizeof(message));

    auto snapshot_result =
        guard_result.value().accept(address_of(os_buffer), os_buffer.size());
    CHECK(snapshot_result.ok());
    Snapshot snapshot = std::move(snapshot_result).value();

    CHECK(snapshot.guid() == kAllowedGuid);
    CHECK_EQ(snapshot.message_length(), sizeof(message));
    CHECK_EQ(snapshot.buffer().size(), os_buffer.size());
    CHECK_EQ(snapshot.message().size(), sizeof(message));
    CHECK(std::memcmp(snapshot.message().data(), message, sizeof(message)) == 0);
}

// --- TOCTOU: OS mutation after validation never reaches the handler -------

static void test_post_validation_mutation() {
    auto guard_result = make_test_guard();

    const uint8_t message[] = {1, 2, 3, 4, 5};
    auto os_buffer = make_comm_buffer(kAllowedGuid, message, sizeof(message));

    auto snapshot_result =
        guard_result.value().accept(address_of(os_buffer), os_buffer.size());
    CHECK(snapshot_result.ok());
    Snapshot snapshot = std::move(snapshot_result).value();

    // Attacker rewrites the OS buffer after Accept() returned.
    std::memset(os_buffer.data(), 0xFF, os_buffer.size());
    Guid spoofed = kOtherGuid;
    std::memcpy(os_buffer.data(), &spoofed, sizeof(spoofed));

    // The private SMRAM snapshot is untouched.
    CHECK(snapshot.guid() == kAllowedGuid);
    for (std::size_t index = 0; index < sizeof(message); ++index) {
        CHECK_EQ(snapshot.message().data()[index], message[index]);
    }
}

// --- message length and header bounds -------------------------------------

static void test_length_bounds() {
    auto guard_result = make_test_guard();
    const BufferGuard& guard = guard_result.value();

    // Declared message longer than buffer minus header.
    const std::size_t bogus_length = 64;
    auto too_long = make_comm_buffer(kAllowedGuid, nullptr, bogus_length);
    // Present only a small outer buffer while the header claims a long
    // message: the staged size must cap the accepted message length.
    auto result = guard.accept(address_of(too_long), kCommHeaderSize + 4);
    CHECK_EQ(result.status(), Status::MessageTooLong);

    // Zero-length message.
    std::vector<uint8_t> zero_message(kCommHeaderSize, 0);
    Guid guid = kAllowedGuid;
    std::size_t zero = 0;
    std::memcpy(zero_message.data(), &guid, sizeof(guid));
    std::memcpy(zero_message.data() + offsetof(CommHeader, message_length),
                &zero, sizeof(zero));
    result = guard.accept(address_of(zero_message), zero_message.size());
    CHECK_EQ(result.status(), Status::InvalidArgument);

    // Buffer smaller than the header.
    std::vector<uint8_t> tiny(kCommHeaderSize - 1, 0);
    result = guard.accept(address_of(tiny), tiny.size());
    CHECK_EQ(result.status(), Status::TooSmall);

    // Declared external length beyond the maximum (rejected before copying).
    result = guard.accept(address_of(too_long), kMaxBuffer + 1);
    CHECK_EQ(result.status(), Status::TooLarge);
}

// --- GUID whitelist --------------------------------------------------------

static void test_guid_whitelist() {
    auto guard_result = make_test_guard();
    const BufferGuard& guard = guard_result.value();

    const uint8_t message[] = {0x01};
    auto os_buffer = make_comm_buffer(kOtherGuid, message, sizeof(message));
    auto result = guard.accept(address_of(os_buffer), os_buffer.size());
    CHECK_EQ(result.status(), Status::GuidNotAllowed);
}

// --- embedded/nested pointers ---------------------------------------------

static void test_nested_pointer() {
    auto guard_result = make_test_guard();
    const BufferGuard& guard = guard_result.value();

    // OS-side nested target.
    std::vector<uint8_t> nested_source(32);
    for (std::size_t index = 0; index < nested_source.size(); ++index) {
        nested_source[index] = static_cast<uint8_t>(index);
    }

    NestedPayload payload{};
    payload.nested_base = address_of(nested_source);
    payload.nested_length = nested_source.size();
    payload.marker = 0x7f;

    auto os_buffer = make_comm_buffer(
        kAllowedGuid, reinterpret_cast<const uint8_t*>(&payload),
        sizeof(payload));

    auto snapshot_result = guard.accept(address_of(os_buffer), os_buffer.size());
    CHECK(snapshot_result.ok());
    Snapshot snapshot = std::move(snapshot_result).value();

    // Parse the embedded pointer from the PRIVATE copy only.
    NestedPayload staged_payload{};
    std::memcpy(&staged_payload, snapshot.message().data(),
                sizeof(staged_payload));
    CHECK_EQ(staged_payload.marker, 0x7f);

    // Stage the nested target through the same validation path.
    OwnedBuffer nested_copy;
    Status status = snapshot.snapshot_nested(
        staged_payload.nested_base, staged_payload.nested_length, nested_copy);
    CHECK_EQ(status, Status::Ok);
    CHECK_EQ(nested_copy.size(), nested_source.size());
    CHECK(std::memcmp(nested_copy.data(), nested_source.data(),
                      nested_source.size()) == 0);

    // TOCTOU on the nested region: later OS writes do not reach the copy.
    std::memset(nested_source.data(), 0xCC, nested_source.size());
    for (std::size_t index = 0; index < nested_copy.size(); ++index) {
        CHECK_EQ(nested_copy.data()[index], static_cast<uint8_t>(index));
    }

    // Embedded pointer aimed at SMRAM.
    OwnedBuffer rejected;
    status = snapshot.snapshot_nested(kSmramBase + 0x10, 0x10, rejected);
    CHECK_EQ(status, Status::InProtectedRange);
    CHECK_EQ(rejected.size(), static_cast<std::size_t>(0));

    // Embedded pointer with wrap-around length.
    status = snapshot.snapshot_nested(
        std::numeric_limits<uintptr_t>::max() - 0xF, 0x100, rejected);
    CHECK_EQ(status, Status::Overflow);

    // Embedded pointer exceeding the maximum buffer size.
    status = snapshot.snapshot_nested(0x1000, kMaxBuffer + 1, rejected);
    CHECK_EQ(status, Status::TooLarge);
}

// --- staging allocation failure -------------------------------------------

static void test_allocator_failure() {
    NullAllocator null_allocator;
    auto guard_result = make_test_guard(&null_allocator);
    CHECK(guard_result.ok());

    const uint8_t message[] = {0x01};
    auto os_buffer = make_comm_buffer(kAllowedGuid, message, sizeof(message));
    auto result = guard_result.value().accept(address_of(os_buffer),
                                               os_buffer.size());
    CHECK_EQ(result.status(), Status::AllocationFailed);
}

int main() {
    test_checked_add_range();
    test_smram_and_protected_ranges();
    test_configuration_validation();
    test_valid_buffer_accepted();
    test_post_validation_mutation();
    test_length_bounds();
    test_guid_whitelist();
    test_nested_pointer();
    test_allocator_failure();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}

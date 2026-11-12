#include "mock_submission_queue.hpp"

#include <array>
#include <cstdint>
#include <cstdio>
#include <functional>
#include <limits>
#include <string>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        ++g_failures;
        std::fprintf(stderr, "FAIL: %s\n", message.c_str());
    }
}

void check_eq(LeaseError actual, LeaseError expected, const std::string& message) {
    if (actual != expected) {
        ++g_failures;
        std::fprintf(stderr, "FAIL: %s (expected %s, got %s)\n", message.c_str(),
                     to_string(expected), to_string(actual));
    }
}

struct TestCase {
    std::string name;
    std::function<void()> body;
};

std::vector<TestCase>& registry() {
    static std::vector<TestCase> cases;
    return cases;
}

#define TEST_CASE(name)                                                          \
    static void name();                                                          \
    static const int name##_registered = [] {                                    \
        registry().push_back(TestCase{#name, name});                             \
        return 0;                                                                \
    }();                                                                         \
    static void name()

constexpr TenantId TENANT_A = 1001;
constexpr TenantId TENANT_B = 2002;
constexpr BufIndex BUF = 7;
constexpr BufIndex UNKNOWN_BUF = 99;

TEST_CASE(happy_path_acquire_and_full_completion) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 64> storage{};

    check_eq(manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A),
             LeaseError::Ok, "register buffer");

    LeaseGrant grant = manager.acquire(BUF, 8, 16, IoOp::ReadFixed, TENANT_A, queue);
    check(static_cast<bool>(grant), "acquire succeeds");
    check(queue.submitted().size() == 1, "one descriptor submitted");
    check(queue.submitted().front().buf_index == BUF, "descriptor carries buf_index");
    check(queue.submitted().front().offset == 8, "descriptor carries offset");
    check(queue.submitted().front().length == 16, "descriptor carries length");
    check(queue.submitted().front().op == IoOp::ReadFixed, "descriptor carries opcode");
    check(queue.submitted().front().lease_id == grant.lease_id,
          "descriptor carries lease id");
    check(manager.in_flight(BUF) == 1, "lease counts as in-flight");

    check_eq(manager.complete({grant.lease_id, 16}, TENANT_A), LeaseError::Ok,
             "full completion succeeds");
    check(manager.in_flight(BUF) == 0, "completion releases in-flight slot");
    check_eq(manager.release(grant.lease_id, TENANT_A), LeaseError::Ok,
             "completed lease can be released");
    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::Ok,
             "buffer can be unregistered once idle");
}

TEST_CASE(zero_length_lease_is_allowed) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 8> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant grant = manager.acquire(BUF, 4, 0, IoOp::WriteFixed, TENANT_A, queue);
    check(static_cast<bool>(grant), "zero length inside buffer is valid");
    check_eq(manager.complete({grant.lease_id, 0}, TENANT_A), LeaseError::Ok,
             "zero-length completion succeeds");

    LeaseGrant edge = manager.acquire(BUF, storage.size(), 0, IoOp::ReadFixed,
                                      TENANT_A, queue);
    check(static_cast<bool>(edge), "zero length at offset == size is valid");
    check_eq(manager.complete({edge.lease_id, 0}, TENANT_A), LeaseError::Ok,
             "edge zero-length completion succeeds");
}

TEST_CASE(offset_plus_length_wrap_is_rejected) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 64> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    const std::uint64_t max_u64 = std::numeric_limits<std::uint64_t>::max();
    LeaseGrant wrapped =
        manager.acquire(BUF, 16, max_u64, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(wrapped.error, LeaseError::OffsetOverflow,
             "length that wraps offset+length is rejected");

    LeaseGrant wrapped_zero =
        manager.acquire(BUF, max_u64, 1, IoOp::WriteFixed, TENANT_A, queue);
    check_eq(wrapped_zero.error, LeaseError::OffsetOverflow,
             "max offset plus one is rejected");

    check(queue.submitted().empty(), "nothing submitted after overflow rejection");
    check(manager.in_flight(BUF) == 0, "no in-flight lease after overflow rejection");
}

TEST_CASE(out_of_range_and_invalid_index_are_rejected) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 16> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant beyond =
        manager.acquire(BUF, 0, 17, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(beyond.error, LeaseError::OutOfRange, "length beyond buffer is rejected");

    LeaseGrant offset_beyond =
        manager.acquire(BUF, 16, 1, IoOp::WriteFixed, TENANT_A, queue);
    check_eq(offset_beyond.error, LeaseError::OutOfRange,
             "non-zero length past buffer end is rejected");

    LeaseGrant unknown =
        manager.acquire(UNKNOWN_BUF, 0, 1, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(unknown.error, LeaseError::InvalidBufIndex, "unknown index is rejected");

    check(queue.submitted().empty(), "no descriptor submitted for invalid requests");
}

TEST_CASE(in_flight_lease_blocks_update_and_unregister) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 32> storage{};
    std::array<std::byte, 32> replacement{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant grant = manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_A, queue);
    check(static_cast<bool>(grant), "acquire before blocking checks");

    check_eq(manager.update_buffer(BUF, replacement.data(), replacement.size(),
                                   TENANT_A),
             LeaseError::BufferBusy, "update blocked while lease in flight");
    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::BufferBusy,
             "unregister blocked while lease in flight");

    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A), LeaseError::Ok,
             "completion unblocks buffer");

    check_eq(manager.update_buffer(BUF, replacement.data(), replacement.size(),
                                   TENANT_A),
             LeaseError::Ok, "update allowed after completion");
    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::Ok,
             "unregister allowed after completion");
}

TEST_CASE(short_read_zeroes_remaining_region) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 32> storage;
    storage.fill(std::byte{0xAB});
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant grant = manager.acquire(BUF, 8, 16, IoOp::ReadFixed, TENANT_A, queue);
    check(static_cast<bool>(grant), "acquire read lease");

    check_eq(manager.complete({grant.lease_id, 6}, TENANT_A), LeaseError::Ok,
             "short read completes");

    for (std::size_t i = 0; i < storage.size(); ++i) {
        if (i >= 14 && i < 24) {
            check(storage[i] == std::byte{0}, "unread tail is zeroed");
        } else {
            check(storage[i] == std::byte{0xAB}, "bytes outside request are untouched");
        }
    }
}

TEST_CASE(full_read_and_short_write_do_not_zero) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 32> storage;
    storage.fill(std::byte{0xCD});
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant read_lease =
        manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(manager.complete({read_lease.lease_id, 8}, TENANT_A), LeaseError::Ok,
             "full read completes");
    for (auto byte : storage) {
        check(byte == std::byte{0xCD}, "full read leaves buffer untouched");
    }

    LeaseGrant write_lease =
        manager.acquire(BUF, 8, 16, IoOp::WriteFixed, TENANT_A, queue);
    check_eq(manager.complete({write_lease.lease_id, 5}, TENANT_A), LeaseError::Ok,
             "short write completes");
    for (auto byte : storage) {
        check(byte == std::byte{0xCD}, "short write never zeroes buffer");
    }
}

TEST_CASE(duplicate_completion_is_rejected) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 16> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant grant = manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A), LeaseError::Ok,
             "first completion accepted");
    check(manager.in_flight(BUF) == 0, "in-flight count drops once");

    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A),
             LeaseError::AlreadyCompleted, "duplicate completion rejected");
    check(manager.in_flight(BUF) == 0, "duplicate completion does not alter state");

    check_eq(manager.release(grant.lease_id, TENANT_A), LeaseError::Ok,
             "lease released once after duplicate completions");
    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A),
             LeaseError::UnknownLease, "completion after release is unknown");
}

TEST_CASE(cross_tenant_lease_is_denied) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 32> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant foreign =
        manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_B, queue);
    check_eq(foreign.error, LeaseError::AccessDenied,
             "foreign tenant cannot acquire lease");

    LeaseGrant grant = manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_A, queue);
    check(static_cast<bool>(grant), "owner acquires lease");

    check_eq(manager.complete({grant.lease_id, 8}, TENANT_B),
             LeaseError::AccessDenied, "foreign tenant cannot complete lease");
    check(manager.in_flight(BUF) == 1, "foreign completion leaves lease in flight");

    check_eq(manager.update_buffer(BUF, storage.data(), storage.size(), TENANT_B),
             LeaseError::AccessDenied, "foreign tenant cannot update buffer");
    check_eq(manager.unregister_buffer(BUF, TENANT_B), LeaseError::AccessDenied,
             "foreign tenant cannot unregister buffer");
    check_eq(manager.release(grant.lease_id, TENANT_B), LeaseError::AccessDenied,
             "foreign tenant cannot release lease");

    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A), LeaseError::Ok,
             "owner still completes after foreign attempts");
}

TEST_CASE(queue_rejection_rolls_back_lease) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 16> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    queue.set_reject(true);
    LeaseGrant rejected =
        manager.acquire(BUF, 0, 8, IoOp::WriteFixed, TENANT_A, queue);
    check_eq(rejected.error, LeaseError::QueueRejected, "queue rejection surfaced");
    check(queue.rejected_count() == 1, "rejection recorded by mock queue");
    check(manager.in_flight(BUF) == 0, "rejected submission does not hold the buffer");

    queue.set_reject(false);
    LeaseGrant grant =
        manager.acquire(BUF, 0, 8, IoOp::WriteFixed, TENANT_A, queue);
    check(static_cast<bool>(grant), "later acquisition succeeds once queue accepts");
    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::BufferBusy,
             "buffer is held by the new lease, proving rollback freed it earlier");
    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A), LeaseError::Ok,
             "post-rollback lease completes normally");
    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::Ok,
             "buffer unregisters once post-rollback lease completes");
}

TEST_CASE(error_and_oversized_completion_results_are_rejected) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 16> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant grant = manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(manager.complete({grant.lease_id, -5}, TENANT_A),
             LeaseError::InvalidResult, "negative completion result rejected");
    check(manager.in_flight(BUF) == 1, "failed validation keeps lease in flight");

    check_eq(manager.complete({grant.lease_id, 9}, TENANT_A),
             LeaseError::InvalidResult, "result exceeding requested length rejected");
    check(manager.in_flight(BUF) == 1, "oversized validation keeps lease in flight");

    check_eq(manager.complete({grant.lease_id, 8}, TENANT_A), LeaseError::Ok,
             "valid completion still accepted");
}

TEST_CASE(multiple_leases_track_in_flight_count) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 64> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant first = manager.acquire(BUF, 0, 8, IoOp::ReadFixed, TENANT_A, queue);
    LeaseGrant second = manager.acquire(BUF, 16, 8, IoOp::WriteFixed, TENANT_A, queue);
    check(static_cast<bool>(first) && static_cast<bool>(second),
          "two overlapping leases acquired");
    check(manager.in_flight(BUF) == 2, "in-flight count is two");

    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::BufferBusy,
             "buffer busy with two leases");

    CompletionEvent event = queue.complete_oldest(8);
    check(event.lease_id == first.lease_id, "mock queue drains oldest lease first");
    check_eq(manager.complete(event, TENANT_A), LeaseError::Ok,
             "first lease completes");
    check(manager.in_flight(BUF) == 1, "one lease remains in flight");
    check_eq(manager.unregister_buffer(BUF, TENANT_A), LeaseError::BufferBusy,
             "buffer still busy after one completion");

    event = queue.complete_oldest(8);
    check(event.lease_id == second.lease_id, "mock queue drains second lease");
    check_eq(manager.complete(event, TENANT_A), LeaseError::Ok,
             "second lease completes");
    check(manager.in_flight(BUF) == 0, "buffer idle once all complete");
}

TEST_CASE(active_lease_cannot_be_released) {
    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, 16> storage{};
    manager.register_buffer(BUF, storage.data(), storage.size(), TENANT_A);

    LeaseGrant grant = manager.acquire(BUF, 0, 4, IoOp::ReadFixed, TENANT_A, queue);
    check_eq(manager.release(grant.lease_id, TENANT_A),
             LeaseError::LeaseStillActive, "release blocked before completion");
    check_eq(manager.complete({grant.lease_id, 4}, TENANT_A), LeaseError::Ok,
             "completion accepted after release denial");
    check_eq(manager.release(grant.lease_id, TENANT_A), LeaseError::Ok,
             "release accepted after completion");
}

} // namespace

int main() {
    for (const TestCase& test : registry()) {
        test.body();
        if (g_failures == 0) {
            std::printf("[ OK ] %s\n", test.name.c_str());
        }
    }
    if (g_failures != 0) {
        std::fprintf(stderr, "%d check(s) failed across %zu test case(s)\n",
                     g_failures, registry().size());
        return 1;
    }
    std::printf("all %zu test cases passed\n", registry().size());
    return 0;
}

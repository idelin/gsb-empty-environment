#include "mock_submission_queue.hpp"

#include <array>
#include <cstdio>
#include <random>
#include <thread>
#include <vector>

using namespace netsec;

int main() {
    constexpr BufIndex index = 3;
    constexpr TenantId owner = 42;
    constexpr std::size_t buffer_size = 1 << 16;
    constexpr int worker_count = 8;
    constexpr int iterations = 2000;

    FixedBufferLeaseManager manager;
    testutil::MockSubmissionQueue queue;
    std::array<std::byte, buffer_size> storage{};
    if (manager.register_buffer(index, storage.data(), storage.size(), owner) !=
        LeaseError::Ok) {
        std::fprintf(stderr, "register failed\n");
        return 1;
    }

    auto worker = [&](unsigned seed) {
        std::mt19937 rng(seed);
        std::uniform_int_distribution<std::uint64_t> len_dist(1, 256);
        for (int i = 0; i < iterations; ++i) {
            std::uint64_t length = len_dist(rng);
            std::uint64_t offset = rng() % (buffer_size + 1);
            IoOp op = (i & 1) ? IoOp::WriteFixed : IoOp::ReadFixed;
            LeaseGrant grant =
                manager.acquire(index, offset, length, op, owner, queue);
            if (!grant) {
                continue;
            }
            std::int64_t result =
                static_cast<std::int64_t>(length - (rng() % (length + 1)));
            LeaseError done = manager.complete({grant.lease_id, result}, owner);
            if (done != LeaseError::Ok) {
                std::fprintf(stderr, "unexpected completion failure: %s\n",
                             to_string(done));
            }
            if (manager.release(grant.lease_id, owner) != LeaseError::Ok) {
                std::fprintf(stderr, "unexpected release failure\n");
            }
        }
    };

    std::vector<std::thread> threads;
    for (int i = 0; i < worker_count; ++i) {
        threads.emplace_back(worker, 1000u + static_cast<unsigned>(i));
    }

    std::thread mutator([&] {
        for (int i = 0; i < 200; ++i) {
            LeaseError error =
                manager.update_buffer(index, storage.data(), storage.size(), owner);
            if (error != LeaseError::Ok && error != LeaseError::BufferBusy) {
                std::fprintf(stderr, "unexpected update result: %s\n",
                             to_string(error));
            }
            std::this_thread::yield();
        }
    });

    for (auto& thread : threads) {
        thread.join();
    }
    mutator.join();

    if (manager.in_flight(index) != 0) {
        std::fprintf(stderr, "expected zero in-flight leases, got %zu\n",
                     manager.in_flight(index));
        return 1;
    }
    if (manager.unregister_buffer(index, owner) != LeaseError::Ok) {
        std::fprintf(stderr, "buffer should be releasable after all workers finish\n");
        return 1;
    }
    std::printf("concurrency stress test passed\n");
    return 0;
}

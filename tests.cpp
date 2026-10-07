#include <atomic>
#include <chrono>
#include <cstdio>
#include <future>
#include <stdexcept>
#include <thread>

#include "executor.hpp"
#include "resource.hpp"

namespace {

using namespace resman;
using namespace std::chrono_literals;

int g_failures = 0;

#define CHECK(cond)                                                              \
    do {                                                                         \
        if (!(cond)) {                                                           \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
            ++g_failures;                                                        \
        }                                                                        \
    } while (0)

// 1. Cleanup runs on the executor's owner thread, only when pumped, once.
void test_cleanup_runs_on_owner_thread() {
    Executor ex;  // owner: this thread
    std::atomic<int> cleanups{0};
    std::thread::id cleanup_thread;

    {
        Resource r(ex, [&](std::thread::id t) {
            cleanup_thread = t;
            ++cleanups;
        });
        // Move the handle to another thread and release it there.
        std::thread worker([r = std::move(r)]() mutable {});
        worker.join();
        // Cleanup was posted but the executor has not been pumped yet.
        CHECK(cleanups.load() == 0);
        CHECK(ex.pending_tasks() == 1);
    }

    CHECK(ex.step() == 1);
    CHECK(cleanups.load() == 1);
    CHECK(cleanup_thread == std::this_thread::get_id());
    CHECK(cleanup_thread == ex.owner_thread());

    ex.shutdown();
    CHECK(cleanups.load() == 1);  // no double cleanup during shutdown
}

// 2. Two executors: last release across threads cleans up exactly once,
//    and cleanup is routed to the correct executor only.
void test_exactly_once_and_routing() {
    Executor ex_a;
    Executor ex_b;
    std::atomic<int> cleanups_a{0};
    std::atomic<int> cleanups_b{0};

    {
        Resource a(ex_a, [&](std::thread::id) { ++cleanups_a; });
        Resource b(ex_b, [&](std::thread::id) { ++cleanups_b; });

        Resource a2 = a;
        Resource a3 = a;
        Resource b2 = b;
        std::thread t1([a2 = std::move(a2)]() mutable {});
        std::thread t2([a3 = std::move(a3)]() mutable {});
        t1.join();
        t2.join();
    }  // a, b, b2 released here on the main thread

    // Cleanups were posted but not pumped yet.
    CHECK(cleanups_a.load() == 0);
    CHECK(cleanups_b.load() == 0);
    CHECK(ex_a.pending_tasks() == 1);  // exactly one cleanup despite 3 handles
    CHECK(ex_b.pending_tasks() == 1);

    ex_a.step_all();
    CHECK(cleanups_a.load() == 1);
    CHECK(cleanups_b.load() == 0);  // executor B untouched by A's pump

    ex_b.step_all();
    CHECK(cleanups_b.load() == 1);

    ex_a.shutdown();
    ex_b.shutdown();
    CHECK(cleanups_a.load() == 1);
    CHECK(cleanups_b.load() == 1);
}

// 3. Executor owned by a second thread: cleanup still runs on that owner.
void test_foreign_owner_thread() {
    std::promise<Executor*> ex_promise;
    std::atomic<bool> stop{false};
    std::atomic<int> cleanups{0};
    std::thread::id cleanup_thread;
    std::thread::id owner_thread;

    std::thread owner([&] {
        Executor ex;  // owner: this worker thread
        owner_thread = std::this_thread::get_id();
        ex_promise.set_value(&ex);
        while (!stop.load(std::memory_order_acquire)) {
            ex.step_all();
            ex.wait_for_work(1ms);
        }
        ex.shutdown();  // drains any remaining cleanup on this thread
    });

    Executor* ex = ex_promise.get_future().get();
    {
        Resource r(*ex, [&](std::thread::id t) {
            cleanup_thread = t;
            ++cleanups;
        });
    }  // released on the main thread; cleanup must hop to the owner thread

    for (int i = 0; i < 5000 && cleanups.load() == 0; ++i)
        std::this_thread::sleep_for(1ms);

    CHECK(cleanups.load() == 1);
    CHECK(cleanup_thread == owner_thread);
    CHECK(cleanup_thread != std::this_thread::get_id());

    stop.store(true, std::memory_order_release);
    owner.join();
    CHECK(cleanups.load() == 1);
}

// 4. Shutdown race: a release racing with shutdown() is still cleaned up
//    exactly once, on the owner thread, before shutdown() returns.
void test_shutdown_drains_racing_release() {
    Executor ex;
    std::atomic<int> cleanups{0};
    std::thread::id cleanup_thread;

    Resource r(ex, [&](std::thread::id t) {
        cleanup_thread = t;
        ++cleanups;
    });

    std::thread releaser([r = std::move(r), &ex]() mutable {
        // Wait until shutdown has begun, then release the last handle.
        while (!ex.is_shutdown()) std::this_thread::yield();
    });

    ex.shutdown();  // must not return before the racing cleanup ran
    releaser.join();

    CHECK(cleanups.load() == 1);
    CHECK(cleanup_thread == std::this_thread::get_id());

    // No new resources may be created after shutdown.
    bool threw = false;
    try {
        Resource late(ex, [](std::thread::id) {});
        (void)late;
    } catch (const std::runtime_error&) {
        threw = true;
    }
    CHECK(threw);
}

// 5. Cleanup queued before shutdown is drained before shutdown() returns.
void test_shutdown_drains_queued_cleanup() {
    Executor ex;
    std::atomic<int> cleanups{0};
    {
        Resource r(ex, [&](std::thread::id) { ++cleanups; });
    }
    CHECK(cleanups.load() == 0);  // queued, not yet run
    ex.shutdown();
    CHECK(cleanups.load() == 1);
}

}  // namespace

int main() {
    test_cleanup_runs_on_owner_thread();
    test_exactly_once_and_routing();
    test_foreign_owner_thread();
    test_shutdown_drains_racing_release();
    test_shutdown_drains_queued_cleanup();

    if (g_failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d check(s) failed\n", g_failures);
    return 1;
}

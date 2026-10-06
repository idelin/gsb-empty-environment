#include <atomic>
#include <iostream>
#include <thread>
#include <vector>

#include "thread_affine.hpp"

static int failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": "    \
                      << #cond << "\n";                                    \
            ++failures;                                                    \
        }                                                                  \
    } while (0)

// Resource that records where it was created/destroyed and how many
// times its destructor ran.
struct Tracked {
    static std::atomic<int> alive;
    static std::atomic<int> destroyed;
    static std::thread::id created_on;
    static std::thread::id destroyed_on;

    static void reset_stats() {
        alive = 0;
        destroyed = 0;
        created_on = std::thread::id{};
        destroyed_on = std::thread::id{};
    }

    int id;
    explicit Tracked(int id_) : id(id_) {
        ++alive;
        created_on = std::this_thread::get_id();
    }
    ~Tracked() {
        --alive;
        ++destroyed;
        destroyed_on = std::this_thread::get_id();
    }
};
std::atomic<int> Tracked::alive{0};
std::atomic<int> Tracked::destroyed{0};
std::thread::id Tracked::created_on;
std::thread::id Tracked::destroyed_on;

// 1. Destruction must happen on the creating thread, after the handle
//    was moved to and released from a different thread.
static void test_destruction_on_owning_thread() {
    std::cout << "[test] destruction on owning thread\n";
    Tracked::reset_stats();
    ManualExecutor ex_a;
    ThreadAffine<Tracked> mgr_a(ex_a);

    const std::thread::id main_id = std::this_thread::get_id();
    auto h = mgr_a.create(1);
    CHECK(Tracked::created_on == main_id);

    // Move the handle to a worker thread and release it there.
    std::thread worker([hh = std::move(h)]() mutable { hh.reset(); });
    worker.join();

    // Released on the worker, but nothing destroyed yet: cleanup was
    // posted back to executor A.
    CHECK(Tracked::destroyed == 0);
    CHECK(Tracked::alive == 1);
    CHECK(ex_a.pending() == 1);

    // The owning (creating) thread steps the executor.
    CHECK(ex_a.run_all() == 1);
    CHECK(Tracked::destroyed == 1);
    CHECK(Tracked::alive == 0);
    CHECK(Tracked::destroyed_on == main_id);
    CHECK(Tracked::destroyed_on == Tracked::created_on);
}

// 2. Cleanup runs exactly once, no matter how many times the handle is
//    moved (including across threads) or how often the executor drains.
static void test_exactly_once_cleanup() {
    std::cout << "[test] exactly-once cleanup\n";
    Tracked::reset_stats();
    ManualExecutor ex_a;
    ThreadAffine<Tracked> mgr_a(ex_a);

    auto h1 = mgr_a.create(7);
    auto h2 = std::move(h1);
    CHECK(!h1 && h2);

    // Bounce the handle through another thread and back.
    ThreadAffine<Tracked>::Handle slot;
    std::thread ping([&] { slot = std::move(h2); });
    ping.join();
    std::thread pong([&] { h1 = std::move(slot); });
    pong.join();

    h1.reset();
    h1.reset();            // reset on empty handle is a no-op
    CHECK(Tracked::destroyed == 0);

    CHECK(ex_a.run_all() == 1);
    CHECK(Tracked::destroyed == 1);
    CHECK(ex_a.run_all() == 0);  // draining again destroys nothing more
    CHECK(Tracked::destroyed == 1);
    CHECK(Tracked::alive == 0);
}

// 3. Shutdown drains pending cleanups before returning; creation after
//    shutdown is rejected; a handle released after shutdown still cleans
    // up exactly once via the inline fallback.
static void test_shutdown_drains_and_seals() {
    std::cout << "[test] shutdown drains and seals\n";
    Tracked::reset_stats();
    ManualExecutor ex_a;
    ThreadAffine<Tracked> mgr_a(ex_a);

    auto h1 = mgr_a.create(1);
    auto h2 = mgr_a.create(2);
    auto h3 = mgr_a.create(3);
    h1.reset();
    h2.reset();
    CHECK(ex_a.pending() == 2);
    CHECK(Tracked::destroyed == 0);

    ex_a.shutdown();  // must drain both pending cleanups before returning
    CHECK(Tracked::destroyed == 2);
    CHECK(ex_a.pending() == 0);
    CHECK(ex_a.is_shutdown());

    // No new resources after shutdown.
    bool threw = false;
    try {
        auto h = mgr_a.create(4);
        (void)h;
    } catch (const std::runtime_error&) {
        threw = true;
    }
    CHECK(threw);

    // Late release: executor is gone, cleanup runs inline, still once.
    h3.reset();
    CHECK(Tracked::destroyed == 3);
    CHECK(Tracked::alive == 0);
}

// 4. Race: one thread releases handles while another shuts the executor
//    down. Every resource must be cleaned up exactly once, and shutdown
//    must not return while an accepted cleanup is still pending.
static void test_shutdown_race() {
    std::cout << "[test] shutdown race\n";
    for (int iter = 0; iter < 200; ++iter) {
        Tracked::reset_stats();
        ManualExecutor ex_a;
        ThreadAffine<Tracked> mgr_a(ex_a);

        constexpr int kResources = 64;
        std::vector<ThreadAffine<Tracked>::Handle> handles;
        handles.reserve(kResources);
        for (int i = 0; i < kResources; ++i) handles.push_back(mgr_a.create(i));

        std::atomic<bool> go{false};
        std::thread releaser([&] {
            while (!go.load(std::memory_order_acquire)) {
            }
            for (auto& h : handles) h.reset();
        });
        std::thread closer([&] {
            while (!go.load(std::memory_order_acquire)) {
            }
            ex_a.shutdown();
        });
        go.store(true, std::memory_order_release);
        releaser.join();
        closer.join();

        CHECK(Tracked::destroyed == kResources);
        CHECK(Tracked::alive == 0);
        CHECK(ex_a.pending() == 0);
        if (failures) return;  // fail fast, don't spam 200 iterations
    }
}

int main() {
    test_destruction_on_owning_thread();
    test_exactly_once_cleanup();
    test_shutdown_drains_and_seals();
    test_shutdown_race();

    if (failures == 0) {
        std::cout << "ALL TESTS PASSED\n";
        return 0;
    }
    std::cerr << failures << " check(s) failed\n";
    return 1;
}

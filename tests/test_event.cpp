#include "event.hpp"

#include <cassert>
#include <chrono>
#include <cstdio>
#include <memory>
#include <mutex>
#include <thread>
#include <vector>

using namespace std::chrono_literals;

// C++17 has no std::barrier; this is a minimal reusable phase barrier.
class barrier {
public:
    explicit barrier(std::ptrdiff_t expected)
        : expected_(expected), count_(expected) {}

    void arrive_and_wait() {
        std::unique_lock<std::mutex> lk(mu_);
        const auto gen = generation_;
        if (--count_ == 0) {
            count_ = expected_;
            ++generation_;
            cv_.notify_all();
        } else {
            cv_.wait(lk, [&] { return generation_ != gen; });
        }
    }

private:
    std::mutex mu_;
    std::condition_variable cv_;
    std::ptrdiff_t expected_;
    std::ptrdiff_t count_;
    std::size_t generation_ = 0;
};

// Removed listeners do not enter new emit rounds.
static void test_snapshot_and_removal() {
    evlib::event<int> ev;
    int a = 0, b = 0;
    auto sa = ev.subscribe([&](int) { ++a; });
    auto sb = ev.subscribe([&](int) { ++b; });
    ev.emit(1);
    assert(a == 1 && b == 1);
    sb.unsubscribe();
    ev.emit(1);
    assert(a == 2 && b == 1);
    std::puts("ok: snapshot_and_removal");
}

// A listener may unsubscribe (and wait on) itself from its own callback
// without deadlocking, and is not called again afterwards.
static void test_self_unsubscribe() {
    evlib::event<> ev;
    int calls = 0;
    evlib::subscription self;
    self = ev.subscribe([&] {
        ++calls;
        self.unsubscribe();
        self.wait(); // must not wait on the current thread
    });
    ev.emit();
    ev.emit();
    assert(calls == 1);
    std::puts("ok: self_unsubscribe");
}

// Removal takes effect immediately, even for a listener whose turn in the
// current round has not come yet. Also proves no global lock is held during
// callbacks (unsubscribe() takes the registry lock from inside a callback).
static void test_removal_takes_effect_immediately() {
    evlib::event<> ev;
    int first = 0, second = 0;
    evlib::subscription s2;
    auto s1 = ev.subscribe([&] {
        ++first;
        s2.unsubscribe();
    });
    s2 = ev.subscribe([&] { ++second; });
    ev.emit();
    assert(first == 1);
    assert(second == 0);
    std::puts("ok: removal_takes_effect_immediately");
}

// Re-entrant emit from inside a listener works (no lock held in callbacks).
static void test_nested_emit() {
    evlib::event<int> ev;
    int counts[4] = {0, 0, 0, 0};
    evlib::subscription s;
    s = ev.subscribe([&](int depth) {
        ++counts[depth];
        if (depth < 3)
            ev.emit(depth + 1);
    });
    ev.emit(0);
    assert(counts[0] == 1 && counts[1] == 1 && counts[2] == 1 &&
           counts[3] == 1);
    std::puts("ok: nested_emit");
}

// Core timing test: while a listener invocation is blocked mid-callback,
// unsubscribe() returns immediately but wait() blocks until the in-flight
// call finishes; the listener's captured state is destroyed only after the
// wait completes and the last handle reference is released. Barriers
// synchronize the exact interleaving.
static void test_awaitable_unsubscribe_and_destruction_timing() {
    evlib::event<> ev;
    barrier entered(2); // listener <-> main: callback has started
    barrier release(2); // main <-> listener: callback may return
    std::atomic<bool> destroyed{false};
    std::atomic<bool> wait_returned{false};

    struct guard {
        std::atomic<bool>& flag;
        explicit guard(std::atomic<bool>& f) : flag(f) {}
        ~guard() { flag.store(true, std::memory_order_seq_cst); }
    };

    evlib::subscription sub;
    {
        auto g = std::make_shared<guard>(destroyed);
        sub = ev.subscribe([g, &entered, &release] {
            entered.arrive_and_wait();
            release.arrive_and_wait();
        });
    } // the listener state is now the sole owner of the guard

    std::thread emitter([&] { ev.emit(); });
    entered.arrive_and_wait(); // listener is now in flight

    sub.unsubscribe(); // removal returns without waiting for the callback

    std::thread waiter([&] {
        sub.wait();
        wait_returned.store(true, std::memory_order_seq_cst);
    });

    std::this_thread::sleep_for(100ms);
    assert(!wait_returned.load()); // wait() blocks while the call is in flight
    assert(!destroyed.load());     // captured state still alive mid-callback

    release.arrive_and_wait(); // let the in-flight call finish
    emitter.join();
    waiter.join();
    assert(wait_returned.load()); // wait() unblocked exactly after completion
    assert(!destroyed.load());    // handle still owns the listener state

    sub.reset(); // last reference gone
    assert(destroyed.load()); // captured state destroyed only now
    std::puts("ok: awaitable_unsubscribe_and_destruction_timing");
}

// A second emit round started after unsubscribe() returns must not enter the
// removed listener, even while a previous round's call is still in flight.
static void test_no_new_round_after_removal() {
    evlib::event<> ev;
    barrier entered(2), release(2);
    std::atomic<int> calls{0};

    auto sub = ev.subscribe([&] {
        calls.fetch_add(1);
        entered.arrive_and_wait();
        release.arrive_and_wait();
    });

    std::thread first([&] { ev.emit(); });
    entered.arrive_and_wait(); // round 1 in flight
    sub.unsubscribe();
    ev.emit(); // round 2: snapshot no longer contains the listener
    assert(calls.load() == 1);
    release.arrive_and_wait();
    first.join();
    assert(calls.load() == 1);
    std::puts("ok: no_new_round_after_removal");
}

// Concurrent emitters plus subscribe/unsubscribe churn: no deadlock, no
// crash, handle destruction never races a running callback.
static void test_concurrent_stress() {
    evlib::event<int> ev;
    std::atomic<long> calls{0};
    std::atomic<bool> stop{false};

    std::vector<std::thread> emitters;
    for (int i = 0; i < 4; ++i)
        emitters.emplace_back([&] {
            while (!stop.load(std::memory_order_relaxed))
                ev.emit(1);
        });

    std::thread churn([&] {
        for (int i = 0; i < 5000; ++i) {
            auto s = ev.subscribe(
                [&](int) { calls.fetch_add(1, std::memory_order_relaxed); });
            if (i % 2)
                s.unsubscribe();
            std::this_thread::yield();
        } // remaining handles destroyed here: reset() waits for in-flight
    });
    churn.join();
    stop.store(true);
    for (auto& t : emitters)
        t.join();
    std::printf("ok: concurrent_stress (calls=%ld)\n", calls.load());
}

int main() {
    test_snapshot_and_removal();
    test_self_unsubscribe();
    test_removal_takes_effect_immediately();
    test_nested_emit();
    test_awaitable_unsubscribe_and_destruction_timing();
    test_no_new_round_after_removal();
    test_concurrent_stress();
    std::puts("all tests passed");
    return 0;
}

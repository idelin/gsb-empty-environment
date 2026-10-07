#include "bounded_queue.hpp"

#include <atomic>
#include <iostream>
#include <memory>
#include <numeric>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            ++g_failures;                                                  \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": "    \
                      << #cond << "\n";                                    \
        }                                                                  \
    } while (0)

struct InjectedFailure : std::runtime_error {
    using std::runtime_error::runtime_error;
};

// Move-only instrumented payload with fault injection and a live counter.
class Counted {
public:
    static inline std::atomic<long> live{0};
    static inline std::atomic<long> total_constructed{0};
    static inline std::atomic<long> total_destroyed{0};
    // When >= 0, counts down; the construction/assignment that reaches 0
    // throws InjectedFailure and the trigger resets to -1.
    static inline int fail_ctor_after = -1;
    static inline int fail_move_assign_after = -1;

    explicit Counted(int value = 0) : value_(value) {
        maybe_throw(fail_ctor_after, "ctor");
        ++live;
        ++total_constructed;
    }
    Counted(Counted&& other) noexcept : value_(other.value_) {
        other.value_ = -1;
        ++live;
        ++total_constructed;
    }
    Counted(const Counted&) = delete;
    Counted& operator=(const Counted&) = delete;
    Counted& operator=(Counted&& other) {
        maybe_throw(fail_move_assign_after, "move-assign");
        value_ = other.value_;
        other.value_ = -1;
        return *this;
    }
    ~Counted() {
        --live;
        ++total_destroyed;
    }

    int value() const { return value_; }

    static void reset_counters() {
        live = 0;
        total_constructed = 0;
        total_destroyed = 0;
        fail_ctor_after = -1;
        fail_move_assign_after = -1;
    }

private:
    static void maybe_throw(int& trigger, const char* what) {
        if (trigger < 0) return;
        if (trigger-- == 0) {
            trigger = -1;
            throw InjectedFailure(what);
        }
    }

    int value_;
};

void fifo_order() {
    BoundedQueue<Counted> q(4);
    Counted out;
    for (int round = 0; round < 3; ++round) {
        for (int i = 0; i < 4; ++i) CHECK(q.try_emplace(round * 4 + i));
        CHECK(!q.try_emplace(99));  // full
        for (int i = 0; i < 4; ++i) {
            CHECK(q.try_pop(out));
            CHECK(out.value() == round * 4 + i);
        }
        CHECK(!q.try_pop(out));  // empty
    }
    CHECK(Counted::live == 1);  // only `out`
}

void move_only_payload() {
    BoundedQueue<std::unique_ptr<int>> q(2);
    CHECK(q.try_emplace(new int(7)));
    auto p = std::make_unique<int>(8);
    CHECK(q.try_emplace(std::move(p)));
    std::unique_ptr<int> out;
    CHECK(q.try_pop(out));
    CHECK(out && *out == 7);
    CHECK(q.try_pop(out));
    CHECK(out && *out == 8);
    CHECK(!q.try_pop(out));
}

void ctor_throw_keeps_slot() {
    Counted::reset_counters();
    {
        BoundedQueue<Counted> q(3);
        CHECK(q.try_emplace(1));
        CHECK(q.try_emplace(2));
        CHECK(Counted::live == 2);

        Counted::fail_ctor_after = 0;
        bool threw = false;
        try {
            q.try_emplace(3);
        } catch (const InjectedFailure&) {
            threw = true;
        }
        CHECK(threw);
        CHECK(q.size() == 2);        // slot not consumed
        CHECK(Counted::live == 2);   // no leak, no phantom object

        // Slot is reusable and FIFO order is intact.
        CHECK(q.try_emplace(3));
        CHECK(q.size() == 3);
        Counted out;
        for (int expect = 1; expect <= 3; ++expect) {
            CHECK(q.try_pop(out));
            CHECK(out.value() == expect);
        }
        CHECK(Counted::live == 1);  // `out`
    }
    // Queue destruction ran destructors for every live slot object.
    CHECK(Counted::live == 0);
    CHECK(Counted::total_constructed == Counted::total_destroyed);
}

void blocking_emplace_throw_keeps_slot() {
    Counted::reset_counters();
    BoundedQueue<Counted> q(2);
    CHECK(q.try_emplace(10));
    Counted::fail_ctor_after = 0;
    bool threw = false;
    try {
        q.emplace(11);  // blocking variant, queue not full
    } catch (const InjectedFailure&) {
        threw = true;
    }
    CHECK(threw);
    CHECK(q.size() == 1);
    CHECK(Counted::live == 1);
    CHECK(q.try_emplace(11));
    Counted out;
    CHECK(q.wait_pop(out) && out.value() == 10);
    CHECK(q.wait_pop(out) && out.value() == 11);
}

void pop_assign_throw_keeps_element() {
    Counted::reset_counters();
    {
        BoundedQueue<Counted> q(3);
        for (int i = 1; i <= 3; ++i) CHECK(q.try_emplace(i));

        Counted out;
        Counted::fail_move_assign_after = 0;
        bool threw = false;
        try {
            q.try_pop(out);
        } catch (const InjectedFailure&) {
            threw = true;
        }
        CHECK(threw);
        CHECK(q.size() == 3);       // element not removed
        CHECK(Counted::live == 4);  // 3 in queue + `out`

        // The same element is still retrievable.
        CHECK(q.try_pop(out));
        CHECK(out.value() == 1);
        CHECK(q.wait_pop(out) && out.value() == 2);
        CHECK(q.wait_pop(out) && out.value() == 3);
        CHECK(!q.try_pop(out));
    }
    CHECK(Counted::live == 0);
    CHECK(Counted::total_constructed == Counted::total_destroyed);
}

void close_unblocks_waiters() {
    // Consumer blocked on an empty queue wakes up with false.
    {
        BoundedQueue<Counted> q(1);
        Counted out;
        std::atomic<int> drained{0};
        std::thread consumer([&] {
            if (!q.wait_pop(out)) ++drained;
        });
        std::this_thread::sleep_for(std::chrono::milliseconds(50));
        q.close();
        consumer.join();
        CHECK(drained == 1);
    }
    // Producer blocked on a full queue wakes up with false.
    {
        BoundedQueue<Counted> q(1);
        CHECK(q.try_emplace(1));
        std::atomic<int> rejected{0};
        std::thread producer([&] {
            if (!q.emplace(2)) ++rejected;
        });
        std::this_thread::sleep_for(std::chrono::milliseconds(50));
        q.close();
        producer.join();
        CHECK(rejected == 1);
        CHECK(q.size() == 1);  // queued element untouched
    }
}

void queue_destructor_cleans_slots() {
    Counted::reset_counters();
    {
        BoundedQueue<Counted> q(5);
        for (int i = 0; i < 3; ++i) CHECK(q.try_emplace(i));
        CHECK(Counted::live == 3);
    }
    CHECK(Counted::live == 0);
    CHECK(Counted::total_constructed == Counted::total_destroyed);
}

void mpmc_stress() {
    Counted::reset_counters();
    constexpr int kProducers = 4;
    constexpr int kConsumers = 4;
    constexpr int kPerProducer = 25000;
    BoundedQueue<Counted> q(64);

    std::atomic<long> consumed_sum{0};
    std::atomic<long> consumed_count{0};

    std::vector<std::thread> producers;
    for (int p = 0; p < kProducers; ++p) {
        producers.emplace_back([&, p] {
            for (int i = 0; i < kPerProducer; ++i) {
                // Values are unique across producers: [1, P*N].
                while (!q.try_emplace(p * kPerProducer + i + 1))
                    std::this_thread::yield();
            }
        });
    }
    std::vector<std::thread> consumers;
    for (int c = 0; c < kConsumers; ++c) {
        consumers.emplace_back([&] {
            Counted out;
            while (q.wait_pop(out)) {
                consumed_sum.fetch_add(out.value(),
                                       std::memory_order_relaxed);
                consumed_count.fetch_add(1, std::memory_order_relaxed);
            }
        });
    }
    for (auto& t : producers) t.join();
    q.close();
    for (auto& t : consumers) t.join();

    const long total = static_cast<long>(kProducers) * kPerProducer;
    CHECK(consumed_count == total);
    CHECK(consumed_sum == total * (total + 1) / 2);
    CHECK(q.size() == 0);
    // Every slot object was destroyed; only bookkeeping mismatch would
    // leave live != 0 or constructed != destroyed.
    CHECK(Counted::live == 0);
    CHECK(Counted::total_constructed == Counted::total_destroyed);
}

struct TestCase {
    const char* name;
    void (*fn)();
};

}  // namespace

int main() {
    const TestCase tests[] = {
        {"fifo_order", fifo_order},
        {"move_only_payload", move_only_payload},
        {"ctor_throw_keeps_slot", ctor_throw_keeps_slot},
        {"blocking_emplace_throw_keeps_slot", blocking_emplace_throw_keeps_slot},
        {"pop_assign_throw_keeps_element", pop_assign_throw_keeps_element},
        {"close_unblocks_waiters", close_unblocks_waiters},
        {"queue_destructor_cleans_slots", queue_destructor_cleans_slots},
        {"mpmc_stress", mpmc_stress},
    };
    for (const auto& t : tests) {
        const int before = g_failures;
        t.fn();
        std::cout << (g_failures == before ? "PASS " : "FAIL ") << t.name
                  << "\n";
    }
    if (g_failures) {
        std::cout << g_failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all tests passed\n";
    return 0;
}

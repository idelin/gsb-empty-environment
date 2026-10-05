// Tests for tlpool. Each TEST_* function returns void and aborts via CHECK.
#include "tlpool/pool.hpp"

#include <atomic>
#include <cstdio>
#include <cstdlib>
#include <thread>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                       \
  do {                                                                    \
    if (!(cond)) {                                                        \
      std::fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__,          \
                   __LINE__, #cond);                                      \
      ++g_failures;                                                       \
    }                                                                     \
  } while (0)

// Instrumented payload: proves exactly-once destruction.
struct Tracked {
  static std::atomic<long> live;
  static std::atomic<long> ctors;
  static std::atomic<long> dtors;
  static std::atomic<bool> doubleFree;

  int value = 0;

  Tracked() {
    live.fetch_add(1);
    ctors.fetch_add(1);
  }
  ~Tracked() {
    if (live.fetch_sub(1) <= 0) doubleFree.store(true);  // dtor ran twice
    dtors.fetch_add(1);
  }
  static void resetStats() {
    live = 0;
    ctors = 0;
    dtors = 0;
    doubleFree = false;
  }
};
std::atomic<long> Tracked::live{0};
std::atomic<long> Tracked::ctors{0};
std::atomic<long> Tracked::dtors{0};
std::atomic<bool> Tracked::doubleFree{false};

tlpool::Pool<Tracked> makePool(std::size_t threshold = 32) {
  return tlpool::Pool<Tracked>([] { return new Tracked; }, nullptr, threshold);
}

void expectClean(const char* name) {
  CHECK(!Tracked::doubleFree.load());
  CHECK(Tracked::live.load() == 0);
  CHECK(Tracked::ctors.load() == Tracked::dtors.load());
  std::printf("[%-36s] ctors=%ld dtors=%ld  OK\n", name,
              Tracked::ctors.load(), Tracked::dtors.load());
}

// 1. Returned objects are reused by later acquires.
void test_basic_reuse() {
  Tracked::resetStats();
  // Run inside a worker thread so its TLS cache is torn down (at join)
  // before we check the global counters.
  std::thread scenario([] {
    auto pool = makePool();
    Tracked* raw;
    {
      auto h = pool.acquire();
      raw = h.get();
      h->value = 42;
    }  // returned into this thread's TLS cache
    // Keep acquiring+returning; once the TLS cache fills up it flushes to the
    // global pool and `raw` becomes available for reuse.
    bool reused = false;
    for (int i = 0; i < 64 && !reused; ++i) {
      auto h = pool.acquire();
      if (h.get() == raw) reused = true;
    }
    CHECK(reused);
  });
  scenario.join();
  expectClean("basic_reuse");
}

// 2. Returns below the threshold stay in the TLS cache; the batch is handed
//    to the global pool in exactly one flush at the threshold.
void test_batch_threshold() {
  Tracked::resetStats();
  std::thread scenario([] {
    auto pool = makePool(/*threshold=*/4);
    {
      std::vector<tlpool::Ptr<Tracked>> hs;
      for (int i = 0; i < 4; ++i) hs.push_back(pool.acquire());
      for (int i = 0; i < 3; ++i) hs[i].reset();
      CHECK(pool.freeCount() == 0);       // still sitting in TLS cache
      CHECK(pool.batchesFlushed() == 0);
      hs[3].reset();                      // 4th return hits the threshold
      CHECK(pool.freeCount() == 4);       // whole batch landed at once
      CHECK(pool.batchesFlushed() == 1);
      CHECK(pool.objectsFlushed() == 4);
    }
  });
  scenario.join();
  expectClean("batch_threshold");
}

// 3. Thread-exit protocol: a thread that dies with a partial (below
//    threshold) batch in its TLS cache must flush it to the global pool.
void test_thread_exit_flush() {
  Tracked::resetStats();
  {
    auto pool = makePool(/*threshold=*/8);
    std::thread worker([&] {
      std::vector<tlpool::Ptr<Tracked>> hs;
      for (int i = 0; i < 3; ++i) hs.push_back(pool.acquire());
      for (auto& h : hs) h.reset();  // 3 < 8: stays in TLS cache
      CHECK(pool.freeCount() == 0);
      // thread exits here -> TlsEntry dtor flushes the leftover batch
    });
    worker.join();
    CHECK(pool.freeCount() == 3);  // nothing lost
    CHECK(pool.batchesFlushed() == 1);
    CHECK(pool.objectsFlushed() == 3);
  }
  expectClean("thread_exit_flush");
}

// 4. Pool-close protocol: idle objects die at close(); borrowed objects
//    returned afterwards (late return) are destroyed immediately.
void test_close_with_outstanding() {
  Tracked::resetStats();
  {
    auto pool = makePool(/*threshold=*/1);  // returns flush immediately
    auto late1 = pool.acquire();
    auto late2 = pool.acquire();
    {
      auto h = pool.acquire();
      (void)h;
    }  // returned; lands on the global idle list
    pool.close();
    CHECK(pool.created() == 3);
    CHECK(pool.destroyed() == 1);  // idle object destroyed by close()
    // The two outstanding handles are destroyed on reset after close.
    late1.reset();
    CHECK(pool.destroyed() == 2);  // late return destroyed immediately
    late2.reset();
    CHECK(pool.destroyed() == 3);
    CHECK(pool.freeCount() == 0);
  }
  expectClean("close_with_outstanding");
}

// 5. The Pool object may be destroyed while worker threads still hold
//    handles and TLS caches: SharedState is kept alive by shared ownership.
void test_pool_destroyed_before_threads() {
  Tracked::resetStats();
  std::atomic<bool> go{false};
  {
    auto pool = makePool(/*threshold=*/16);
    std::vector<std::thread> threads;
    for (int t = 0; t < 4; ++t) {
      threads.emplace_back([&] {
        while (!go.load()) std::this_thread::yield();
        for (int i = 0; i < 50; ++i) {
          auto h = pool.acquire();
          h->value = i;
        }  // returned via TLS cache
      });
    }
    go.store(true);
    for (auto& th : threads) th.join();
    // `pool` still alive here; threads' TLS caches already flushed at join.
  }  // Pool destroyed AFTER threads -- and SharedState dies with it.
  expectClean("pool_destroyed_before_threads");
}

// 5b. Stronger: pool object destroyed while handles are STILL outstanding on
//     another thread. The whole scenario runs inside a worker thread so its
//     TLS teardown happens before we check the counters.
void test_pool_dies_handles_outlive() {
  Tracked::resetStats();
  std::thread scenario([] {
    tlpool::Ptr<Tracked> h1, h2;
    {
      auto pool = makePool(/*threshold=*/8);
      h1 = pool.acquire();
      h2 = pool.acquire();
      for (int i = 0; i < 3; ++i) {
        auto h = pool.acquire();
        (void)h;
      }
    }  // Pool object destroyed here; state survives via h1/h2 + TLS entry.
    CHECK(Tracked::live.load() == 5);  // nothing destroyed yet
    h1.reset();                        // late return into TLS cache
    h2.reset();
    CHECK(Tracked::live.load() == 5);  // cached, not destroyed
    // thread exits: TLS flush returns 5 objects to the orphaned state,
    // the last shared_ptr goes away, SharedState dtor destroys them.
  });
  scenario.join();
  expectClean("pool_dies_handles_outlive");
}

// 6. Late return after close, across threads: object borrowed before close
//    is returned by a different thread after close -> destroyed once.
void test_late_return_cross_thread() {
  Tracked::resetStats();
  {
    auto pool = makePool();
    auto h = pool.acquire();
    pool.close();
    CHECK(pool.destroyed() == 0);  // h was borrowed, not idle
    std::thread t([&] { h.reset(); });
    t.join();
    CHECK(pool.destroyed() == 1);
    CHECK(Tracked::live.load() == 0);
  }
  expectClean("late_return_cross_thread");
}

// 7. Stress: many threads, random acquire/return, close mid-flight.
void test_stress() {
  Tracked::resetStats();
  {
    auto pool = makePool(/*threshold=*/16);
    std::atomic<bool> stop{false};
    std::vector<std::thread> threads;
    for (int t = 0; t < 8; ++t) {
      threads.emplace_back([&, seed = t * 7919 + 1] {
        unsigned rng = seed;
        auto next = [&] { rng = rng * 1664525u + 1013904223u; return rng; };
        std::vector<tlpool::Ptr<Tracked>> held;
        while (!stop.load()) {
          try {
            if (held.empty() || (next() & 1)) {
              held.push_back(pool.acquire());
              held.back()->value = static_cast<int>(next());
            } else {
              held[next() % held.size()].reset();
            }
          } catch (const std::logic_error&) {
            break;  // pool closed
          }
        }
      });
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(200));
    pool.close();  // close while workers are still borrowing/returning
    stop.store(true);
    for (auto& th : threads) th.join();
  }
  expectClean("stress");
}

}  // namespace

int main() {
  test_basic_reuse();
  test_batch_threshold();
  test_thread_exit_flush();
  test_close_with_outstanding();
  test_pool_destroyed_before_threads();
  test_pool_dies_handles_outlive();
  test_late_return_cross_thread();
  test_stress();
  if (g_failures) {
    std::fprintf(stderr, "%d CHECK(s) failed\n", g_failures);
    return 1;
  }
  std::printf("ALL TESTS PASSED\n");
  return 0;
}

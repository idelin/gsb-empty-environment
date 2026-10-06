// Tests for the intrusive queue: close semantics, a barrier-controlled
// read-then-retire interleaving proving deferred reclamation, and a
// multi-threaded stress test. Build with ASan/UBSan or TSan (see Makefile).
#include <atomic>
#include <cstdio>
#include <cstdlib>
#include <thread>
#include <vector>

#include "intrusive_queue.hpp"

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      std::fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__, \
                   __LINE__, #cond);                             \
      std::abort();                                              \
    }                                                            \
  } while (0)

// Simple two-phase spin barrier for deterministic interleavings.
class barrier {
public:
  explicit barrier(int parties) : parties_(parties) {}
  void arrive_and_wait() {
    int gen = gen_.load(std::memory_order_acquire);
    if (count_.fetch_add(1, std::memory_order_acq_rel) + 1 == parties_) {
      count_.store(0, std::memory_order_relaxed);
      gen_.fetch_add(1, std::memory_order_release);
    } else {
      while (gen_.load(std::memory_order_acquire) == gen)
        std::this_thread::yield();
    }
  }

private:
  const int parties_;
  std::atomic<int> count_{0};
  std::atomic<int> gen_{0};
};

struct test_node : iq::node {
  explicit test_node(int id_) : id(id_) { live.fetch_add(1); }
  ~test_node() { live.fetch_sub(1); }
  int id;
  static std::atomic<int> live;
};
std::atomic<int> test_node::live{0};

// ---------------------------------------------------------------------------
// 1. Close semantics: after close(), new enqueues fail; queued nodes drain.
// ---------------------------------------------------------------------------
static void test_close_semantics() {
  iq::hazard_domain dom;
  test_node dummy(0);
  iq::intrusive_queue q(&dummy, dom);

  test_node a(1), b(2);
  CHECK(q.enqueue(&a));
  CHECK(q.enqueue(&b));
  q.close();
  CHECK(q.closed());
  test_node c(3);
  CHECK(!q.enqueue(&c));  // new enqueue fails after close

  // Already-enqueued nodes can still be drained.
  CHECK(q.dequeue() == &dummy);
  CHECK(q.dequeue() == &a);
  CHECK(q.dequeue() == nullptr);
  CHECK(q.take_sentinel() == &b);  // final sentinel is recoverable too
  std::printf("PASS test_close_semantics\n");
}

// ---------------------------------------------------------------------------
// 2. Barrier-controlled read-then-retire interleaving.
//    Reader protects the head node; main dequeues + retires it; the node
//    must NOT be destructed while the protected reference exists, and must
//    be destructed once the reader releases it.
// ---------------------------------------------------------------------------
static std::atomic<bool> g_reader_done{false};
static std::atomic<int> g_destroyed{0};

static void checked_deleter(iq::node* n) {
  // The whole point: destruction only happens with no protected references.
  CHECK(g_reader_done.load(std::memory_order_acquire));
  g_destroyed.fetch_add(1, std::memory_order_acq_rel);
  delete static_cast<test_node*>(n);
}

static void test_read_retire_interleaving() {
  iq::hazard_domain dom;
  auto* dummy = new test_node(0);
  iq::intrusive_queue q(dummy, dom);
  auto* n1 = new test_node(1);
  CHECK(q.enqueue(n1));

  barrier b_protected(2), b_release(2);
  std::atomic<int> observed_id{-1};

  std::thread reader([&] {
    auto g = q.make_guard();
    iq::node* p = g.protect(q.head_ref());
    CHECK(p == dummy);
    b_protected.arrive_and_wait();  // protection published -> let main retire
    b_release.arrive_and_wait();    // main confirmed no destruction happened
    // Node was dequeued by main, yet still observable through our guard.
    observed_id.store(static_cast<test_node*>(p)->id, std::memory_order_relaxed);
    g.reset();                      // explicit release of read-side protection
    g_reader_done.store(true, std::memory_order_release);
  });

  b_protected.arrive_and_wait();    // reader now holds a protected reference
  iq::node* n = q.dequeue();
  CHECK(n == dummy);                // unlinked, but still observed by reader
  q.retire(n, &checked_deleter);
  CHECK(q.reclaim() == 0);          // deferred: protected reference exists
  CHECK(g_destroyed.load() == 0);
  CHECK(q.retired_count() == 1);
  b_release.arrive_and_wait();      // allow reader to drop its guard
  reader.join();

  CHECK(observed_id.load() == 0);   // reader observed the dequeued node
  CHECK(q.reclaim() == 1);          // no protected references -> destruct
  CHECK(g_destroyed.load() == 1);

  q.close();
  CHECK(q.dequeue() == nullptr);    // drained; n1 is the final sentinel
  CHECK(q.take_sentinel() == n1);
  q.retire(n1, &checked_deleter);
  CHECK(q.reclaim() == 1);
  CHECK(test_node::live.load() == 0);
  std::printf("PASS test_read_retire_interleaving\n");
}

// ---------------------------------------------------------------------------
// 3. Stress: P producers x C consumers, retire/reclaim under concurrency,
//    then close + drain. Every node must be reclaimed exactly once.
// ---------------------------------------------------------------------------
static void test_stress() {
  constexpr int kProducers = 4;
  constexpr int kConsumers = 4;
  constexpr int kPerProducer = 20000;
  constexpr int kTotal = kProducers * kPerProducer;

  iq::hazard_domain dom;
  test_node* dummy = new test_node(0);
  iq::intrusive_queue q(dummy, dom);

  std::atomic<long> dequeued{0};
  std::atomic<long> destroyed{0};
  std::atomic<long> retired{0};

  // The deleter must be a plain function pointer, so it reaches the
  // counter through a static.
  static std::atomic<long>* s_destroyed;
  s_destroyed = &destroyed;
  struct Deleter {
    static void run(iq::node* n) {
      s_destroyed->fetch_add(1, std::memory_order_acq_rel);
      delete static_cast<test_node*>(n);
    }
  };

  std::vector<std::thread> producers;
  for (int p = 0; p < kProducers; ++p) {
    producers.emplace_back([&q, p] {
      for (int i = 0; i < kPerProducer; ++i) {
        auto* n = new test_node(p * kPerProducer + i + 1);
        while (!q.enqueue(n)) std::this_thread::yield();
      }
    });
  }

  std::vector<std::thread> consumers;
  for (int c = 0; c < kConsumers; ++c) {
    consumers.emplace_back([&] {
      long local = 0;
      for (;;) {
        iq::node* n = q.dequeue();
        if (n != nullptr) {
          q.retire(n, &Deleter::run);
          retired.fetch_add(1, std::memory_order_acq_rel);
          ++local;
          if (local % 64 == 0) q.reclaim();
        } else if (q.closed()) {
          break;
        } else {
          std::this_thread::yield();
        }
      }
      dequeued.fetch_add(local, std::memory_order_acq_rel);
      q.reclaim();
    });
  }

  for (auto& t : producers) t.join();
  q.close();
  for (auto& t : consumers) t.join();

  // kTotal dequeues: the initial dummy plus all but the last enqueued
  // node, which remains as the final sentinel.
  CHECK(dequeued.load() == kTotal);
  CHECK(retired.load() == kTotal);
  CHECK(q.dequeue() == nullptr);

  // Recover and retire the final sentinel as well.
  iq::node* s = q.take_sentinel();
  q.retire(s, &Deleter::run);

  // No thread holds protected references anymore: everything reclaims.
  q.reclaim();
  CHECK(destroyed.load() == kTotal + 1);
  CHECK(q.retired_count() == 0);
  CHECK(test_node::live.load() == 0);
  std::printf("PASS test_stress (dequeued=%ld destroyed=%ld)\n",
              dequeued.load(), destroyed.load());
}

int main() {
  test_close_semantics();
  test_read_retire_interleaving();
  test_stress();
  std::printf("ALL TESTS PASSED\n");
  return 0;
}

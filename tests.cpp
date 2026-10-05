// tlpool::Pool 协议测试。
//
// 证明目标：
//   1. 每个对象恰好析构一次（Tracked 的 live-set 断言双重析构，
//      并在进程退出时校验 ctor 总数 == dtor 总数、live 集合为空）。
//   2. 线程退出不遗失批次（Sentry 把未达阈值的缓存整批刷回全局池）。
//   3. 池关闭后不再借出，迟归还的对象安全回到 Core。
//   4. Pool 句柄先于线程 / Lease 销毁也不会悬垂或丢对象。

#include "thread_local_pool.h"

#include <atomic>
#include <cassert>
#include <cstdio>
#include <cstdlib>
#include <memory>
#include <mutex>
#include <random>
#include <thread>
#include <unordered_set>
#include <vector>

namespace {

// ---- 可追踪对象：双重析构 / 泄漏检测 ------------------------------------

std::mutex g_live_mu;
std::unordered_set<void*> g_live;  // 当前存活对象地址
std::atomic<long> g_ctor{0};
std::atomic<long> g_dtor{0};

struct Tracked {
  long payload = 0;
  Tracked() {
    g_ctor.fetch_add(1, std::memory_order_relaxed);
    std::lock_guard<std::mutex> lk(g_live_mu);
    const bool inserted = g_live.insert(this).second;
    assert(inserted && "ctor on already-live address");
    (void)inserted;
  }
  ~Tracked() {
    g_dtor.fetch_add(1, std::memory_order_relaxed);
    std::lock_guard<std::mutex> lk(g_live_mu);
    const std::size_t erased = g_live.erase(this);
    assert(erased == 1 && "double destruction detected");
    (void)erased;
  }
};

Tracked* make_tracked() { return new Tracked; }
void destroy_tracked(Tracked* p) { delete p; }

#define CHECK(cond)                                                      \
  do {                                                                   \
    if (!(cond)) {                                                       \
      std::fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__,         \
                   __LINE__, #cond);                                     \
      std::abort();                                                      \
    }                                                                    \
  } while (0)

using tlpool::Pool;

// ---- 1. 基本借还：归还进线程缓存，再借命中缓存复用同一对象 ----------------

void test_basic_reuse() {
  Pool<Tracked> pool(&make_tracked, &destroy_tracked, /*cache_high=*/8,
                     /*cache_keep=*/2);
  Tracked* first;
  {
    auto a = pool.lease();
    first = a.get();
    a->payload = 42;
  }  // 归还 → 进入主线程缓存
  {
    auto b = pool.lease();  // 命中线程缓存
    CHECK(b.get() == first);
    CHECK(b->payload == 42);
  }
  pool.flush_thread_cache();
  CHECK(pool.stash_size() == 1);
  std::puts("PASS test_basic_reuse");
}

// ---- 2. 批量阈值：攒到 high 才回刷，且保留 keep 个热对象 ------------------

void test_batch_threshold() {
  constexpr std::size_t kHigh = 4, kKeep = 1;
  Pool<Tracked> pool(&make_tracked, &destroy_tracked, kHigh, kKeep);
  std::vector<Pool<Tracked>::Lease> v;
  for (int i = 0; i < 4; ++i) v.push_back(pool.lease());
  CHECK(pool.stash_size() == 0);
  v.clear();  // 第 4 个归还触发批量回刷 high-keep=3 个
  CHECK(pool.stash_size() == kHigh - kKeep);
  { auto x = pool.lease(); }  // 命中缓存中保留的热对象，stash 不变
  CHECK(pool.stash_size() == kHigh - kKeep);
  pool.flush_thread_cache();
  CHECK(pool.stash_size() == kHigh);
  std::puts("PASS test_batch_threshold");
}

// ---- 3. 线程退出：未达阈值的批次由 Sentry 整批刷回，不遗失 ----------------

void test_thread_exit_flush() {
  Pool<Tracked> pool(&make_tracked, &destroy_tracked, /*cache_high=*/64,
                     /*cache_keep=*/8);
  std::thread t([&pool] {
    std::vector<Pool<Tracked>::Lease> v;
    for (int i = 0; i < 5; ++i) v.push_back(pool.lease());
    v.clear();  // 5 个进入本线程缓存（远低于阈值，不会触发批量回刷）
    CHECK(pool.stash_size() == 0);
    // 线程退出：Sentry 析构 → 缓存整批刷回全局池
  });
  t.join();
  CHECK(pool.stash_size() == 5);  // 批次没有随线程退出而遗失
  std::puts("PASS test_thread_exit_flush");
}

// ---- 4. 池关闭协议：不再借出；迟归还绕过缓存直接回 Core -------------------

void test_close_protocol() {
  const long d0 = g_dtor.load();
  {
    Pool<Tracked> pool(&make_tracked, &destroy_tracked);
    std::vector<Pool<Tracked>::Lease> v;
    for (int i = 0; i < 3; ++i) v.push_back(pool.lease());
    pool.close();
    CHECK(pool.closed());
    CHECK(!pool.lease());  // 关闭后不再借出
    v.clear();             // 迟归还 → 绕过缓存直接回 Core
    CHECK(pool.stash_size() == 3);
  }  // 主线程无该池缓存 → Core 引用归零 → 对象统一析构
  CHECK(g_dtor.load() == d0 + 3);
  std::puts("PASS test_close_protocol");
}

// ---- 5a. 迟归还：Pool 销毁后 Lease 才释放（本线程，池已关闭） -------------

void test_late_return_after_pool_death() {
  const long d0 = g_dtor.load();
  Pool<Tracked>::Lease saved;
  auto weak = [&] {
    Pool<Tracked> pool(&make_tracked, &destroy_tracked);
    auto w = pool.core_handle();
    saved = pool.lease();
    pool.close();
    return w;
  }();  // Pool 句柄在此销毁
  CHECK(!weak.expired());        // Lease 的 deleter 持有 Core
  CHECK(g_dtor.load() == d0);    // 对象尚未析构
  saved.reset();                 // 迟归还：直接回 Core stash
  CHECK(!weak.expired());        // 空 Lease 的 deleter 仍持有 Core 引用
  CHECK(g_dtor.load() == d0);    // 对象在 stash 中等待，尚未析构
  saved = Pool<Tracked>::Lease{};  // 释放 deleter 持有的最后一个 Core 引用
  CHECK(weak.expired());         // Core 引用归零 → ~Core 统一析构 stash
  CHECK(g_dtor.load() == d0 + 1);
  std::puts("PASS test_late_return_after_pool_death");
}

// ---- 5b. 迟归还：Pool 未关闭即销毁，Lease 在另一个线程释放 ----------------

void test_late_return_cross_thread_after_pool_death() {
  const long d0 = g_dtor.load();
  Pool<Tracked>::Lease saved;
  auto weak = [&] {
    Pool<Tracked> pool(&make_tracked, &destroy_tracked);
    auto w = pool.core_handle();
    saved = pool.lease();
    return w;
  }();  // Pool 句柄销毁（未 close）
  CHECK(!weak.expired());
  // 把 Lease 移到新线程释放：对象进入该线程的缓存，
  // 线程退出时由 Sentry 刷回 Core，Core 引用归零后统一析构。
  std::thread t([l = std::move(saved)]() mutable { l.reset(); });
  t.join();
  CHECK(weak.expired());
  CHECK(g_dtor.load() == d0 + 1);
  std::puts("PASS test_late_return_cross_thread_after_pool_death");
}

// ---- 6. 池不比线程活得久：Pool 先销毁，线程退出时缓存照样安全刷回 ----------

void test_thread_outlives_pool() {
  const long c0 = g_ctor.load();
  const long d0 = g_dtor.load();
  std::atomic<bool> ready{false}, go{false};
  auto pool = std::make_unique<Pool<Tracked>>(&make_tracked, &destroy_tracked,
                                              /*cache_high=*/64, 8);
  auto weak = pool->core_handle();
  std::thread t([&pool, &ready, &go] {
    std::vector<Pool<Tracked>::Lease> v;
    for (int i = 0; i < 10; ++i) v.push_back(pool->lease());
    v.clear();  // 10 个全部留在本线程缓存
    ready.store(true);
    while (!go.load()) std::this_thread::yield();
    // 线程退出时 Pool 已销毁：缓存持有的 shared_ptr<Core> 为 Core 续命，
    // Sentry 把整批对象刷回 Core。
  });
  while (!ready.load()) std::this_thread::yield();
  pool.reset();  // Pool 先死
  CHECK(!weak.expired());         // Core 被线程缓存续命
  CHECK(g_dtor.load() == d0);     // 尚无任何对象析构
  go.store(true);
  t.join();                       // 线程退出 → 冲刷 → Core 引用归零
  CHECK(weak.expired());
  CHECK(g_ctor.load() - c0 == 10);
  CHECK(g_dtor.load() - d0 == 10);  // 10 个对象恰好各析构一次
  std::puts("PASS test_thread_outlives_pool");
}

// ---- 7. TLS 析构阶段的迟归还：Sentry 退役后 deleter 直接回 Core ------------

void test_tls_teardown_late_release() {
  Pool<Tracked> pool(&make_tracked, &destroy_tracked, /*cache_high=*/64, 8);
  std::thread t([&pool] {
    // 先于 Sentry 构造 → 在线程退出时后于 Sentry 析构。
    thread_local Pool<Tracked>::Lease tl_lease;
    auto a = pool.lease();
    auto b = pool.lease();
    a.reset();                 // 归还 #1 → 建缓存 + 建 Sentry
    tl_lease = std::move(b);   // #2 留到 TLS 析构阶段才归还
    // 析构顺序：Sentry（冲刷缓存、置 retired）→ tl_lease
    // （deleter 发现 retired → 绕过已退役的缓存，直接回 Core）。
  });
  t.join();
  CHECK(pool.stash_size() == 2);  // 两个对象都没有遗失
  std::puts("PASS test_tls_teardown_late_release");
}

// ---- 8. 跨线程归还：对象在借入线程之外的线程释放 ---------------------------

void test_cross_thread_release() {
  Pool<Tracked> pool(&make_tracked, &destroy_tracked, /*cache_high=*/64, 8);
  std::vector<Pool<Tracked>::Lease> v;
  for (int i = 0; i < 5; ++i) v.push_back(pool.lease());
  std::thread t([vv = std::move(v)]() mutable { vv.clear(); });
  t.join();  // 在子线程归还 → 子线程缓存 → 退出时刷回
  CHECK(pool.stash_size() == 5);
  std::puts("PASS test_cross_thread_release");
}

// ---- 9. 压力测试：多线程随机借还，最终 ctor == dtor，证明恰好一次 ----------

void test_stress_exactly_once() {
  constexpr int kThreads = 8;
  constexpr int kIters = 20000;
  const long c0 = g_ctor.load();
  const long d0 = g_dtor.load();
  {
    Pool<Tracked> pool(&make_tracked, &destroy_tracked, /*cache_high=*/16,
                       /*cache_keep=*/4);
    std::atomic<bool> start{false};
    std::vector<std::thread> threads;
    for (int t = 0; t < kThreads; ++t) {
      threads.emplace_back([&pool, &start, t] {
        while (!start.load()) std::this_thread::yield();
        std::mt19937 rng(static_cast<unsigned>(t * 7919 + 13));
        std::vector<Pool<Tracked>::Lease> held;
        for (int i = 0; i < kIters; ++i) {
          switch (rng() % 4) {
            case 0:
              if (!held.empty()) {
                auto it = held.begin() +
                          static_cast<long>(rng() % held.size());
                held.erase(it);  // 随机单点归还
              }
              break;
            case 1:
              held.clear();  // 批量归还（触发缓存阈值回刷）
              break;
            default: {
              auto l = pool.lease();
              if (l) {
                l->payload = i;
                held.push_back(std::move(l));
              }
              break;
            }
          }
        }
      });
    }
    start.store(true);
    for (auto& th : threads) th.join();
    pool.close();
    CHECK(!pool.lease());
  }  // 所有线程已退出（缓存已刷回），Pool 析构 → Core 引用归零
  const long created = g_ctor.load() - c0;
  const long destroyed = g_dtor.load() - d0;
  CHECK(created > 0);
  CHECK(created == destroyed);  // 每个对象恰好析构一次，不多也不少
  std::printf("PASS test_stress_exactly_once (created=destroyed=%ld)\n",
              created);
}

// 进程退出时的总账：主线程 TLS 析构（Sentry 冲刷）之后由 atexit 校验。
void final_audit() {
  if (g_ctor.load() != g_dtor.load() || !g_live.empty()) {
    std::fprintf(stderr,
                 "FINAL AUDIT FAILED: ctor=%ld dtor=%ld live=%zu\n",
                 g_ctor.load(), g_dtor.load(), g_live.size());
    std::abort();
  }
  std::printf("FINAL AUDIT OK: ctor == dtor == %ld, live set empty\n",
              g_ctor.load());
}

}  // namespace

int main() {
  std::atexit(final_audit);  // 先于一切测试注册 → 在主线程 TLS 析构后运行
  test_basic_reuse();
  test_batch_threshold();
  test_thread_exit_flush();
  test_close_protocol();
  test_late_return_after_pool_death();
  test_late_return_cross_thread_after_pool_death();
  test_thread_outlives_pool();
  test_tls_teardown_late_release();
  test_cross_thread_release();
  test_stress_exactly_once();
  std::puts("ALL TESTS PASSED");
  return 0;
}

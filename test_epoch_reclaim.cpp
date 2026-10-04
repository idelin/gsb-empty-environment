// 代次回收器验证测试:
//  - 用析构计数证明"已开始的读取结束后才释放";
//  - 用人为暂停的读者(PausedReader)制造延迟窗口;
//  - 强制分配器复用旧地址, 证明旧句柄无法读到新对象。

#include "epoch_reclaim.hpp"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <memory>
#include <optional>
#include <thread>
#include <type_traits>
#include <vector>

#if defined(__has_feature)
#if __has_feature(address_sanitizer)
#define EBR_TEST_ASAN 1
#endif
#endif
#ifndef EBR_TEST_ASAN
#define EBR_TEST_ASAN 0
#endif

namespace {

int g_failures = 0;

#define CHECK(cond)                                                           \
    do {                                                                      \
        if (!(cond)) {                                                        \
            ++g_failures;                                                     \
            std::printf("  [FAIL] %s:%d: %s\n", __FILE__, __LINE__, #cond);   \
        } else {                                                              \
            std::printf("  [ok] %s\n", #cond);                                \
        }                                                                     \
    } while (0)

struct Tracked {
    static inline int constructed = 0;
    static inline int destroyed = 0;
    static void reset_counters() { constructed = 0; destroyed = 0; }

    int value;
    char padding[60];  // 凑足 64 字节, 便于观察同尺寸地址复用

    explicit Tracked(int v) : value(v) { ++constructed; }
    ~Tracked() { ++destroyed; }
};

// 人为暂停的读者: pause() 取得守卫后一直持有, 直到 resume()。
template <typename T>
struct PausedReader {
    std::optional<ebr::Guard<T>> guard;
    bool pause(ebr::Resource<T>& res) {
        guard = res.guard();
        return guard.has_value();
    }
    void resume() { guard.reset(); }
};

void test_guard_type_constraints() {
    using G = ebr::Guard<Tracked>;
    static_assert(!std::is_copy_constructible<G>::value, "Guard 禁止复制构造");
    static_assert(!std::is_copy_assignable<G>::value, "Guard 禁止复制赋值");
    static_assert(std::is_move_constructible<G>::value, "Guard 允许移动构造");
    static_assert(std::is_move_assignable<G>::value, "Guard 允许移动赋值");
    std::puts("[test] 守卫类型约束: 可移动、禁止复制 (static_assert 通过)");
}

void test_basic_read() {
    std::puts("[test] 基本读取");
    ebr::EpochDomain domain;
    ebr::Resource<Tracked> res(domain, new Tracked(123));
    auto g = res.guard();
    CHECK(g.has_value());
    CHECK((*g)->value == 123);
    CHECK(!res.retired());
}

void test_prompt_reclaim_without_readers() {
    std::puts("[test] 无读者时退休: 同步回收, 不拖延");
    ebr::EpochDomain domain;
    Tracked::reset_counters();
    ebr::Resource<Tracked> res(domain, new Tracked(9));
    res.retire();
    CHECK(Tracked::destroyed == 1);
    CHECK(domain.pending() == 0);
    CHECK(!res.guard());
}

void test_deferred_reclaim_and_move() {
    std::puts("[test] 暂停读者延迟回收 + 守卫移动");
    ebr::EpochDomain domain;
    Tracked::reset_counters();
    ebr::Resource<Tracked> res(domain, new Tracked(42));

    PausedReader<Tracked> reader;
    CHECK(reader.pause(res));
    CHECK((*reader.guard)->value == 42);

    res.retire();
    CHECK(res.retired());
    CHECK(Tracked::destroyed == 0);  // 已开始的读取未结束: 延迟释放
    CHECK(domain.pending() == 1);
    CHECK(!res.guard());             // 退休期间不能取得新守卫

    // 守卫允许移动; 移空的守卫析构不应解除代次固定
    std::optional<ebr::Guard<Tracked>> moved;
    moved = std::move(*reader.guard);
    reader.resume();                 // 销毁移空守卫
    CHECK(Tracked::destroyed == 0);
    CHECK((*moved)->value == 42);    // 移动后读取仍然有效

    moved.reset();                   // 最后一个读者结束
    CHECK(Tracked::destroyed == 1);  // 此刻才同步回收
    CHECK(domain.pending() == 0);
}

void test_multiple_readers() {
    std::puts("[test] 多读者: 全部结束后才回收");
    ebr::EpochDomain domain;
    Tracked::reset_counters();
    ebr::Resource<Tracked> r1(domain, new Tracked(1));
    ebr::Resource<Tracked> r2(domain, new Tracked(2));

    PausedReader<Tracked> reader1;
    CHECK(reader1.pause(r1));
    r1.retire();
    {
        PausedReader<Tracked> reader2;
        CHECK(reader2.pause(r2));
        r2.retire();
        reader2.resume();                // r2 自己的读者结束
        CHECK(Tracked::destroyed == 0);  // 但 reader1 仍钉住代次, 都不能回收
        CHECK(domain.pending() == 2);
    }
    reader1.resume();
    CHECK(Tracked::destroyed == 2);      // 所有读者结束后一起回收
    CHECK(domain.pending() == 0);
}

void test_address_reuse() {
    std::puts("[test] 地址复用不会让旧句柄读到新对象");
    ebr::EpochDomain domain;
    Tracked::reset_counters();

    ebr::Resource<Tracked> old_res(domain, new Tracked(7));
    const void* old_addr = nullptr;
    {
        PausedReader<Tracked> reader;
        CHECK(reader.pause(old_res));
        old_addr = &reader.guard->get();
        old_res.retire();
        // 暂停读者还活着: 内存不会释放, 此地址不可能被复用
        CHECK(Tracked::destroyed == 0);
        reader.resume();
    }
    CHECK(Tracked::destroyed == 1);  // 读者结束后才释放

    // 反复分配同尺寸对象, 直到有一块落在旧地址上(证明复用确实发生)
    Tracked* reused = nullptr;
    std::vector<Tracked*> extras;
    for (int i = 0; i < 512 && !reused; ++i) {
        Tracked* t = new Tracked(1000 + i);
        if (static_cast<const void*>(t) == old_addr) {
            reused = t;
        } else {
            extras.push_back(t);
        }
    }
    for (Tracked* t : extras) delete t;

    if (!reused) {
        if (EBR_TEST_ASAN) {
            std::puts("  [skip] ASan 隔离区阻止地址复用, 仅验证旧句柄失效");
            CHECK(!old_res.guard());
        } else {
            CHECK(reused != nullptr);  // 非 ASan 下同尺寸分配应当复用
        }
        return;
    }
    std::printf("  [info] 旧负载地址 %p 已被新对象(value=%d)复用\n",
                old_addr, reused->value);

    // 关键断言: 旧句柄已退休, 无法取得守卫, 读不到复用地址上的新对象
    CHECK(old_res.retired());
    CHECK(!old_res.guard());

    // 新句柄可以正常读到新对象
    ebr::Resource<Tracked> new_res(domain, reused);
    auto g = new_res.guard();
    CHECK(g.has_value());
    CHECK((*g)->value >= 1000);
}

void test_concurrent_smoke() {
    std::puts("[test] 多线程冒烟: 并发读取与退休(库本身无后台线程)");
    Tracked::reset_counters();
    std::atomic<long> reads{0};
    {
        ebr::EpochDomain domain;
        constexpr int kResources = 8;
        std::vector<std::unique_ptr<ebr::Resource<Tracked>>> res;
        for (int i = 0; i < kResources; ++i)
            res.push_back(std::make_unique<ebr::Resource<Tracked>>(domain, new Tracked(i)));

        std::atomic<bool> stop{false};
        std::vector<std::thread> threads;
        for (int t = 0; t < 4; ++t) {
            threads.emplace_back([&] {
                while (!stop.load(std::memory_order_relaxed)) {
                    for (auto& r : res) {
                        if (auto g = r->guard()) {
                            (void)(*g)->value;
                            reads.fetch_add(1, std::memory_order_relaxed);
                        }
                    }
                }
            });
        }
        for (int i = 0; i < kResources / 2; ++i) res[i]->retire();
        std::this_thread::sleep_for(std::chrono::milliseconds(50));
        stop.store(true);
        for (auto& th : threads) th.join();
        res.clear();  // 句柄析构 -> 退休剩余资源
    }  // domain 析构 -> reclaim_all 兜底
    CHECK(reads.load() > 0);
    CHECK(Tracked::constructed > 0);
    CHECK(Tracked::destroyed == Tracked::constructed);  // 无泄漏、无重复析构
}

}  // namespace

int main() {
    test_guard_type_constraints();
    test_basic_read();
    test_prompt_reclaim_without_readers();
    test_deferred_reclaim_and_move();
    test_multiple_readers();
    test_address_reuse();
    test_concurrent_smoke();
    if (g_failures == 0) {
        std::puts("\n全部测试通过");
        return 0;
    }
    std::printf("\n%d 项检查失败\n", g_failures);
    return 1;
}

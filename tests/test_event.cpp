// ev::Event 语义验证测试 (C++17)
#include "event.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdio>
#include <cstdlib>
#include <mutex>
#include <optional>
#include <thread>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,   \
                         #cond);                                           \
            ++g_failures;                                                  \
        }                                                                  \
    } while (0)

// C++17 屏障(std::barrier 是 C++20)。
class Barrier {
public:
    explicit Barrier(std::size_t parties) : parties_(parties), count_(parties) {}

    void arrive_and_wait() {
        std::unique_lock<std::mutex> lk(m_);
        std::size_t gen = generation_;
        if (--count_ == 0) {
            count_ = parties_;
            ++generation_;
            cv_.notify_all();
        } else {
            cv_.wait(lk, [&] { return generation_ != gen; });
        }
    }

private:
    std::mutex m_;
    std::condition_variable cv_;
    std::size_t parties_;
    std::size_t count_;
    std::size_t generation_ = 0;
};

// 1. 快照语义: 本轮注销的监听器不再被本轮后续及后续轮次调用;
//    回调中新增的监听器下一轮才生效。
void test_snapshot_semantics() {
    ev::Event<int> ev;
    int a_calls = 0, b_calls = 0, c_calls = 0;

    std::optional<ev::Subscription<int>> sub_b;
    auto sub_a = ev.subscribe([&](int) {
        ++a_calls;
        sub_b->unsubscribe(); // 本轮注销 B
    });
    sub_b.emplace(ev.subscribe([&](int) { ++b_calls; }));

    ev.fire(1);
    CHECK(a_calls == 1);
    CHECK(b_calls == 0); // B 在快照中但回调前已被注销 -> 跳过

    ev.fire(2);
    CHECK(a_calls == 2);
    CHECK(b_calls == 0); // 新一轮也不进入

    // 回调中订阅 C: 本轮不生效, 下一轮生效
    bool c_subscribed = false;
    std::optional<ev::Subscription<int>> sub_c;
    auto sub_a2 = ev.subscribe([&](int) {
        if (!c_subscribed) {
            c_subscribed = true;
            sub_c.emplace(ev.subscribe([&](int) { ++c_calls; }));
        }
    });
    ev.fire(3);
    CHECK(c_calls == 0);
    ev.fire(4);
    CHECK(c_calls == 1);
}

// 2. 可等待注销 + 屏障验证销毁时机:
//    wait() 必须在在途回调结束后才返回; 返回后销毁资源是安全的。
void test_awaitable_unsubscribe_destruction_timing() {
    ev::Event<> ev;
    Barrier entered(2);  // 监听器已进入回调
    Barrier release(2);  // 主线程放行回调结束

    struct Resource {
        std::atomic<bool>* alive;
        ~Resource() { alive->store(false); }
    };
    std::atomic<bool> resource_alive{true};
    std::atomic<bool> callback_finished{false};

    auto sub = ev.subscribe([&, res = std::shared_ptr<Resource>(
                                 new Resource{&resource_alive})] {
        entered.arrive_and_wait();
        release.arrive_and_wait(); // 阻塞在回调中, 模拟在途调用
        // 回调最后仍在使用资源
        CHECK(res->alive->load());
        callback_finished.store(true, std::memory_order_release);
    });

    std::thread firer([&] { ev.fire(); });

    entered.arrive_and_wait(); // 确认回调已在途

    sub.unsubscribe(); // 标记注销: 不阻塞
    CHECK(!sub.active());

    std::atomic<bool> wait_returned{false};
    std::thread waiter([&] {
        sub.wait();
        wait_returned.store(true, std::memory_order_release);
    });

    // 回调仍在途: wait 不得返回, 资源必须保持存活
    std::this_thread::sleep_for(std::chrono::milliseconds(100));
    CHECK(!wait_returned.load(std::memory_order_acquire));
    CHECK(resource_alive.load());
    CHECK(!callback_finished.load());

    release.arrive_and_wait(); // 放行回调
    firer.join();
    waiter.join();

    // 销毁时机: wait 返回 <=> 在途调用已全部结束
    CHECK(callback_finished.load());
    CHECK(wait_returned.load());

    // wait 之后销毁捕获的资源是安全的(此处仅验证语义成立)
    CHECK(resource_alive.load());
}

// 3. 监听器在回调中注销自身(含句柄析构), 不得死锁。
void test_self_unsubscribe() {
    ev::Event<> ev;
    int calls = 0;

    std::optional<ev::Subscription<>> self;
    self.emplace(ev.subscribe([&] {
        ++calls;
        self->unsubscribe_and_wait(); // 自注销: wait 应立即返回
    }));

    ev.fire();
    CHECK(calls == 1);
    ev.fire();
    CHECK(calls == 1); // 已注销, 不再触发

    // 回调内直接销毁句柄(析构走 unsubscribe_and_wait)
    std::optional<ev::Subscription<>> self2;
    int calls2 = 0;
    self2.emplace(ev.subscribe([&] {
        ++calls2;
        self2.reset(); // 析构即注销, 不得自死锁
    }));
    ev.fire();
    CHECK(calls2 == 1);
    ev.fire();
    CHECK(calls2 == 1);
}

// 4. 注销返回后, 目标不再进入新一轮调用。
void test_no_new_round_after_unsubscribe() {
    ev::Event<> ev;
    std::atomic<int> calls{0};
    auto sub = ev.subscribe([&] { ++calls; });

    ev.fire();
    CHECK(calls == 1);

    sub.unsubscribe_and_wait();
    for (int i = 0; i < 10; ++i) ev.fire();
    CHECK(calls == 1);
}

// 5. 并发压力: 多线程 fire 与注销交错, 验证无死锁/崩溃且计数一致。
void test_concurrent_stress() {
    ev::Event<int> ev;
    std::atomic<long long> delivered{0};
    std::atomic<long long> fired{0};

    constexpr int kListeners = 8;
    constexpr int kFirers = 4;
    constexpr int kRounds = 2000;

    std::vector<ev::Subscription<int>> subs;
    for (int i = 0; i < kListeners; ++i) {
        subs.push_back(ev.subscribe([&](int v) { delivered += v; }));
    }

    std::vector<std::thread> firers;
    for (int t = 0; t < kFirers; ++t) {
        firers.emplace_back([&] {
            for (int i = 0; i < kRounds; ++i) {
                ev.fire(1);
                fired += kListeners; // 上界, 用于 sanity 检查
            }
        });
    }

    // 并发逐个注销
    std::thread unsubscriber([&] {
        for (auto& s : subs) s.unsubscribe_and_wait();
    });

    for (auto& f : firers) f.join();
    unsubscriber.join();

    CHECK(delivered.load() >= 0);
    CHECK(delivered.load() <= fired.load());
    CHECK(ev.size() == 0);
}

} // namespace

int main() {
    test_snapshot_semantics();
    test_awaitable_unsubscribe_destruction_timing();
    test_self_unsubscribe();
    test_no_new_round_after_unsubscribe();
    test_concurrent_stress();

    if (g_failures == 0) {
        std::puts("ALL TESTS PASSED");
        return 0;
    }
    std::fprintf(stderr, "%d check(s) failed\n", g_failures);
    return 1;
}

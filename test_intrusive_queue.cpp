// test_intrusive_queue.cpp
#include "intrusive_queue.hpp"

#include <atomic>
#include <barrier>
#include <cassert>
#include <cstdio>
#include <thread>
#include <vector>

using iqueue::IntrusiveQueue;
using iqueue::Node;

static std::atomic<int> g_dtor_count{0};

struct TestNode : Node {
    int value = 0;
    // 若设置，析构时置位（测试观察析构时机）。
    std::atomic<bool>* destroyed_flag = nullptr;
    // 若设置，析构时断言该标志已置位（验证"只在无保护引用后析构"）。
    std::atomic<bool>* require_released = nullptr;
    ~TestNode() override {
        if (require_released) {
            assert(require_released->load(std::memory_order_acquire) &&
                   "node destructed while still protected");
        }
        if (destroyed_flag) destroyed_flag->store(true, std::memory_order_release);
        g_dtor_count.fetch_add(1, std::memory_order_relaxed);
    }
};

static void test_disposer(Node* n) { delete static_cast<TestNode*>(n); }

// 1) 关闭语义：close 后新入队失败，已入队可排空，顺序保持 FIFO。
static void test_close_and_drain() {
    g_dtor_count = 0;
    {
        IntrusiveQueue q(test_disposer);
        iqueue::HazardDomain::Guard g(q.domain());
        for (int i = 0; i < 5; ++i) {
            auto* n = new TestNode;
            n->value = i;
            assert(q.enqueue(*n));
        }
        q.close();
        assert(q.closed());
        auto* late = new TestNode;
        assert(!q.enqueue(*late));  // 关闭后新入队失败
        delete late;                // 未入队节点由调用者自行处理
        g_dtor_count = 0;           // 不计入队列管理的节点
        for (int i = 0; i < 5; ++i) {  // 已入队可排空
            Node* n = q.dequeue(g);
            assert(n != nullptr);
            assert(static_cast<TestNode*>(n)->value == i);
        }
        assert(q.dequeue(g) == nullptr);
        q.reclaim();
    }  // 队列析构回收剩余节点（含仍链接的最后一个出队节点）
    assert(g_dtor_count == 5);
    std::printf("test_close_and_drain OK\n");
}

// 2) 读后退役交错：用屏障确定性触发。
//    A 出队 N1 并持保护读；B 出队 N2（使 N1 退役）并尝试回收。
//    N1 必须在 A 解除保护之后才析构。
static void test_read_then_retire_interleaving() {
    g_dtor_count = 0;
    std::atomic<bool> n1_destroyed{false};
    std::atomic<bool> a_released{false};
    {
        IntrusiveQueue q(test_disposer);
        auto* n1 = new TestNode;
        n1->value = 1;
        n1->destroyed_flag = &n1_destroyed;
        n1->require_released = &a_released;
        auto* n2 = new TestNode;
        n2->value = 2;
        assert(q.enqueue(*n1));
        assert(q.enqueue(*n2));

        std::barrier sync(2);

        std::thread a([&] {
            iqueue::HazardDomain::Guard g(q.domain());
            Node* n = q.dequeue(g);  // 返回 N1，g 保护之
            assert(n == n1);
            sync.arrive_and_wait();  // 点1：N1 已被保护
            // B 在此期间摘除 N1 并尝试回收；N1 必须仍然可读
            sync.arrive_and_wait();  // 点2：B 已完成首次 reclaim
            assert(!n1_destroyed.load(std::memory_order_acquire));
            assert(static_cast<TestNode*>(n)->value == 1);  // 受保护读
            a_released.store(true, std::memory_order_release);
            g.reset();  // 解除保护
            sync.arrive_and_wait();  // 点3：允许 B 再次回收
        });

        std::thread b([&] {
            sync.arrive_and_wait();  // 点1
            iqueue::HazardDomain::Guard g(q.domain());
            Node* n = q.dequeue(g);  // 返回 N2，同时退役 N1
            assert(n == n2);
            q.reclaim();             // N1 被 A 保护，不得析构
            assert(!n1_destroyed.load(std::memory_order_acquire));
            sync.arrive_and_wait();  // 点2
            sync.arrive_and_wait();  // 点3
            q.reclaim();             // A 已解除保护，N1 现在可回收
            assert(n1_destroyed.load(std::memory_order_acquire));
        });

        a.join();
        b.join();
        assert(n1_destroyed.load());
        q.reclaim();
    }
    assert(g_dtor_count == 2);
    std::printf("test_read_then_retire_interleaving OK\n");
}

// 3) 多生产者多消费者压力测试：关闭后排空，全部节点恰好析构一次。
static void test_stress() {
    constexpr int kProducers = 4;
    constexpr int kConsumers = 3;
    constexpr int kPerProducer = 2000;
    constexpr long kTotal = kProducers * kPerProducer;

    g_dtor_count = 0;
    std::atomic<long> dequeued{0};
    std::atomic<long> value_sum{0};
    {
        IntrusiveQueue q(test_disposer);
        std::vector<std::thread> producers;
        for (int p = 0; p < kProducers; ++p) {
            producers.emplace_back([&q, p] {
                for (int i = 0; i < kPerProducer; ++i) {
                    auto* n = new TestNode;
                    n->value = p * kPerProducer + i;
                    bool ok = q.enqueue(*n);
                    assert(ok);
                }
            });
        }
        std::vector<std::thread> consumers;
        for (int c = 0; c < kConsumers; ++c) {
            consumers.emplace_back([&] {
                iqueue::HazardDomain::Guard g(q.domain());
                long local_sum = 0;
                long local_count = 0;
                for (;;) {
                    Node* n = q.dequeue(g);
                    if (!n) {
                        if (q.closed()) break;
                        std::this_thread::yield();
                        continue;
                    }
                    local_sum += static_cast<TestNode*>(n)->value;
                    ++local_count;
                    if ((local_count & 0x3F) == 0) q.reclaim();
                }
                value_sum.fetch_add(local_sum, std::memory_order_relaxed);
                dequeued.fetch_add(local_count, std::memory_order_relaxed);
                q.reclaim();
            });
        }
        for (auto& t : producers) t.join();
        q.close();
        for (auto& t : consumers) t.join();
        q.reclaim();
    }
    assert(dequeued.load() == kTotal);
    assert(value_sum.load() == kTotal * (kTotal - 1) / 2);
    assert(g_dtor_count == kTotal);  // 每个节点恰好析构一次，无泄漏
    std::printf("test_stress OK\n");
}

int main() {
    test_close_and_drain();
    test_read_then_retire_interleaving();
    test_stress();
    std::printf("ALL TESTS PASSED\n");
    return 0;
}

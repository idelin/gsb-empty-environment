#include "../src/bounded_queue.hpp"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <numeric>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

static int g_failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            ++g_failures;                                                  \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,   \
                         #cond);                                           \
        }                                                                  \
    } while (0)

// 只移动类型，带存活对象计数与构造/赋值异常注入。
struct Widget {
    static std::atomic<long> live;
    static std::atomic<long> ctor_budget;    // >=0 时，再成功构造这么多次后下一次构造抛异常
    static std::atomic<long> assign_budget;  // 同上，针对移动赋值

    int value = 0;
    bool valid = true;

    explicit Widget(int v = 0) : value(v) {
        inject(ctor_budget);
        ++live;
    }
    Widget(const Widget&) = delete;
    Widget& operator=(const Widget&) = delete;
    Widget(Widget&& o) : value(o.value), valid(o.valid) {
        inject(ctor_budget);
        o.valid = false;
        ++live;
    }
    Widget& operator=(Widget&& o) {
        inject(assign_budget);
        value = o.value;
        valid = o.valid;
        o.valid = false;
        return *this;
    }
    ~Widget() { --live; }

    static void inject(std::atomic<long>& budget) {
        long b = budget.load();
        if (b == 0)
            throw std::runtime_error("injected");
        if (b > 0)
            --budget;
    }
    static void reset() {
        ctor_budget = -1;
        assign_budget = -1;
    }
};
std::atomic<long> Widget::live{0};
std::atomic<long> Widget::ctor_budget{-1};
std::atomic<long> Widget::assign_budget{-1};

static void test_fifo_and_capacity() {
    bounded_queue<int> q(3);
    CHECK(q.try_push(1) && q.try_push(2) && q.try_push(3));
    CHECK(!q.try_push(4));  // 满
    CHECK(q.size() == 3);
    int out = 0;
    for (int expect = 1; expect <= 3; ++expect) {
        CHECK(q.try_pop(out));
        CHECK(out == expect);
    }
    CHECK(!q.try_pop(out));  // 空
    // 环绕复用槽位
    for (int i = 0; i < 10; ++i) {
        CHECK(q.try_push(i));
        CHECK(q.try_pop(out));
        CHECK(out == i);
    }
}

static void test_move_only_and_live_count() {
    Widget::reset();
    long base = Widget::live.load();
    {
        bounded_queue<Widget> q(4);
        CHECK(q.try_emplace(10));
        CHECK(q.try_emplace(20));
        CHECK(Widget::live - base == 2);
        {
            Widget dst;
            CHECK(q.try_pop(dst));
            CHECK(dst.value == 10 && dst.valid);
        }  // dst 析构
        CHECK(Widget::live - base == 1);
        auto opt = q.try_pop();
        CHECK(opt && opt->value == 20);
        CHECK(Widget::live - base == 1);  // opt 存活
    }  // q 与 opt 析构
    CHECK(Widget::live - base == 0);  // 槽位全部析构，无泄漏/重复析构
}

static void test_ctor_throw_keeps_slot() {
    Widget::reset();
    long base = Widget::live.load();
    {
        bounded_queue<Widget> q(2);
        CHECK(q.try_emplace(1));
        Widget::ctor_budget = 0;  // 下一次构造必抛
        bool threw = false;
        try {
            q.try_emplace(2);
        } catch (const std::runtime_error&) {
            threw = true;
        }
        CHECK(threw);
        CHECK(q.size() == 1);                 // 槽位未被消费
        CHECK(Widget::live - base == 1);      // 无半成品对象
        Widget::ctor_budget = -1;
        CHECK(q.try_emplace(2));              // 队列仍可用
        CHECK(q.size() == 2);
        CHECK(!q.try_emplace(3));             // 容量语义未被异常破坏
        Widget dst;
        CHECK(q.try_pop(dst) && dst.value == 1);  // 顺序保持
        CHECK(q.try_pop(dst) && dst.value == 2);
    }
    CHECK(Widget::live - base == 0);
}

static void test_blocking_emplace_ctor_throw() {
    Widget::reset();
    bounded_queue<Widget> q(2);
    CHECK(q.emplace(7));
    Widget::ctor_budget = 0;
    bool threw = false;
    try {
        q.emplace(8);
    } catch (const std::runtime_error&) {
        threw = true;
    }
    CHECK(threw);
    CHECK(q.size() == 1);
    Widget::ctor_budget = -1;
    CHECK(q.emplace(8));
    Widget dst;
    CHECK(q.pop(dst) && dst.value == 7);
    CHECK(q.pop(dst) && dst.value == 8);
}

static void test_pop_assign_throw_keeps_element() {
    Widget::reset();
    long base = Widget::live.load();
    {
        bounded_queue<Widget> q(2);
        CHECK(q.try_emplace(42));
        CHECK(q.try_emplace(43));
        Widget dst;
        Widget::assign_budget = 0;  // 下一次移动赋值必抛
        bool threw = false;
        try {
            q.try_pop(dst);
        } catch (const std::runtime_error&) {
            threw = true;
        }
        CHECK(threw);
        CHECK(q.size() == 2);                 // 元素未丢失
        CHECK(Widget::live - base == 3);      // 队首对象未被析构
        Widget::assign_budget = -1;
        CHECK(q.try_pop(dst) && dst.value == 42);  // 同一元素仍可取出
        CHECK(q.try_pop(dst) && dst.value == 43);
        CHECK(q.size() == 0);
    }
    CHECK(Widget::live - base == 0);
}

static void test_pop_value_move_throw_keeps_element() {
    Widget::reset();
    bounded_queue<Widget> q(1);
    CHECK(q.try_emplace(9));
    Widget::ctor_budget = 0;  // 返回值移动构造必抛
    bool threw = false;
    try {
        auto opt = q.try_pop();
        (void)opt;
    } catch (const std::runtime_error&) {
        threw = true;
    }
    CHECK(threw);
    CHECK(q.size() == 1);
    Widget::ctor_budget = -1;
    auto opt = q.try_pop();
    CHECK(opt && opt->value == 9);
}

static void test_blocking_close() {
    bounded_queue<int> q_empty(1);  // 消费者在其上阻塞
    bounded_queue<int> q_full(1);   // 生产者在其上阻塞
    std::atomic<bool> popped{true};
    std::atomic<bool> pushed{true};
    CHECK(q_full.try_push(0));
    std::thread consumer([&] {
        int out = 0;
        popped = q_empty.pop(out);  // 空队列上阻塞，close 后应返回 false
    });
    std::thread producer([&] {
        pushed = q_full.push(1);  // 满队列上阻塞，close 后应返回 false
    });
    std::this_thread::sleep_for(std::chrono::milliseconds(50));  // 确保双方进入阻塞
    q_empty.close();
    q_full.close();
    consumer.join();
    producer.join();
    CHECK(!popped);
    CHECK(!pushed);
    // 关闭后入队失败、排空后出队失败
    CHECK(!q_empty.try_push(2));
    int out = 0;
    CHECK(q_full.try_pop(out) && out == 0);  // 残留元素仍可取
    CHECK(!q_full.try_pop(out));
}

static void test_mpmc() {
    Widget::reset();
    constexpr int kProducers = 4;
    constexpr int kConsumers = 3;
    constexpr long kPerProducer = 20000;
    constexpr long kTotal = kProducers * kPerProducer;
    long base = Widget::live.load();

    bounded_queue<Widget> q(64);
    std::atomic<long> sum{0};
    std::atomic<long> count{0};

    std::vector<std::thread> producers;
    for (int p = 0; p < kProducers; ++p)
        producers.emplace_back([&q, p] {
            for (long i = 1; i <= kPerProducer; ++i)
                q.emplace(static_cast<int>(p * kPerProducer + i));
        });

    std::vector<std::thread> consumers;
    for (int c = 0; c < kConsumers; ++c)
        consumers.emplace_back([&] {
            Widget dst;
            long got;
            while ((got = count.load()) < kTotal) {
                if (q.pop(dst)) {
                    sum += dst.value;
                    ++count;
                }
            }
        });

    for (auto& t : producers) t.join();
    while (count.load() < kTotal) std::this_thread::yield();
    q.close();
    for (auto& t : consumers) t.join();

    long expect_sum = 0;
    for (int p = 0; p < kProducers; ++p)
        expect_sum += p * kPerProducer * kPerProducer + kPerProducer * (kPerProducer + 1) / 2;
    CHECK(count == kTotal);
    CHECK(sum == expect_sum);  // 每个元素恰好被消费一次
    CHECK(q.size() == 0);
    CHECK(Widget::live - base == 0);  // 队列仍存活但槽位全空
}

int main() {
    test_fifo_and_capacity();
    test_move_only_and_live_count();
    test_ctor_throw_keeps_slot();
    test_blocking_emplace_ctor_throw();
    test_pop_assign_throw_keeps_element();
    test_pop_value_move_throw_keeps_element();
    test_blocking_close();
    test_mpmc();
    if (g_failures == 0) {
        std::puts("ALL TESTS PASSED");
        return 0;
    }
    std::fprintf(stderr, "%d check(s) failed\n", g_failures);
    return 1;
}

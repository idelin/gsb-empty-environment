// 独立持锁模型验证
//
// 测试侧维护一个与锁管理器实现完全独立的持锁模型（每线程本地的
// held 表 + 全局原子临界区计数器），用真实并发随机操作对比验证：
//   - 互斥：独占临界区计数器永不超过 1；共享期间独占计数为 0
//   - 重复释放：unlock 返回值必须与模型中的持有状态一致
//   - 升级语义：Cancelled/Conflict 后共享锁保留；NotHeld 判定一致
//   - 关闭句柄：close 后模型与自省接口均为空
//   - 饥饿：读者持续涌入时写者必须在有界时间内被授予（FIFO 公平）
#include "lockmgr/lock_manager.hpp"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <random>
#include <string>
#include <thread>
#include <unordered_map>
#include <vector>

using lockmgr::CancelToken;
using lockmgr::LockManager;
using lockmgr::Mode;
using lockmgr::UpgradeStatus;

namespace {

#define CHECK(cond, msg)                                                       \
    do {                                                                       \
        if (!(cond)) {                                                         \
            std::fprintf(stderr, "CHECK failed [%s:%d]: %s\n", __FILE__,       \
                         __LINE__, msg);                                       \
            std::abort();                                                      \
        }                                                                      \
    } while (0)

constexpr int kThreads = 6;
constexpr int kResources = 4;
constexpr int kOpsPerThread = 300;

std::string res(int i) { return "/m/r" + std::to_string(i); }

// 全局临界区计数器（独立于管理器实现的“真相”）。
// 规则：获取返回后递增；调用 unlock/close 之前递减。
// 计数器严格覆盖实际持有区间，计数违规必然对应真实重叠，
// 不会因“管理器已授予他人而本线程尚未递减”产生误报。
std::atomic<int> g_s[kResources];
std::atomic<int> g_x[kResources];

void enter_shared(int r) {
    g_s[r].fetch_add(1);
    CHECK(g_x[r].load() == 0, "shared entered while exclusive held");
}
void leave_shared(int r) { g_s[r].fetch_sub(1); }
void enter_excl(int r) {
    CHECK(g_x[r].fetch_add(1) == 0, "two exclusive holders");
    CHECK(g_s[r].load() == 0, "exclusive entered while shared held");
}
void leave_excl(int r) { g_x[r].fetch_sub(1); }

void worker(LockManager& mgr, int tid) {
    std::mt19937 rng(1234 + tid);
    auto session = mgr.open_session();
    std::unordered_map<int, Mode> held;  // 线程本地模型

    // 避免调用方层面的跨资源 hold-and-wait 死锁：任何可能阻塞的
    // 操作之前，先释放除目标资源外的全部持有（库不负责检测调用方
    // 造成的多资源等待环）。
    auto release_others = [&](int keep) {
        std::vector<int> drop;
        for (auto& kv : held)
            if (kv.first != keep) drop.push_back(kv.first);
        for (int r : drop) {
            if (held[r] == Mode::Shared) leave_shared(r);
            else leave_excl(r);
            bool ok = session.unlock(res(r));
            CHECK(ok, "release_others must succeed");
            held.erase(r);
        }
    };

    auto acquire_shared = [&](int r) {
        release_others(r);
        session.lock_shared(res(r));
        if (!held.count(r)) {
            held[r] = Mode::Shared;
            enter_shared(r);
        }
    };
    auto release = [&](int r) {
        auto it = held.find(r);
        if (it != held.end()) {
            if (it->second == Mode::Shared) leave_shared(r);
            else leave_excl(r);
        }
        bool ok = session.unlock(res(r));
        CHECK(ok == (it != held.end()), "unlock result must match model");
        if (ok) held.erase(it);
    };

    for (int op = 0; op < kOpsPerThread; ++op) {
        int r = rng() % kResources;
        int action = rng() % 100;
        if (action < 30) {
            acquire_shared(r);
        } else if (action < 45) {
            release_others(r);
            if (held.count(r)) {
                release(r);  // 避免隐式升级路径，显式先释放
            }
            session.lock_exclusive(res(r));
            held[r] = Mode::Exclusive;
            enter_excl(r);
        } else if (action < 55) {
            // try_lock：结果必须与随后的持有状态一致
            bool want_x = rng() % 2;
            bool ok = want_x ? session.try_lock_exclusive(res(r))
                             : session.try_lock_shared(res(r));
            Mode m;
            CHECK(session.holds(res(r), &m) || !ok, "try success implies holds");
            if (ok && !held.count(r)) {
                held[r] = want_x ? Mode::Exclusive : Mode::Shared;
                if (want_x) enter_excl(r);
                else enter_shared(r);
            } else if (ok && want_x && held[r] == Mode::Shared) {
                // 已持共享时 try 独占按规范为无操作成功？不允许：try 不做隐式升级
                // （try_lock_impl 对已持有共享请求独占返回 false 分支不会到这）
            }
        } else if (action < 70) {
            // 升级：1/4 概率使用已取消令牌
            release_others(r);
            CancelToken tok;
            if (rng() % 4 == 0) tok.cancel();
            auto st = session.upgrade(res(r), tok);
            auto it = held.find(r);
            if (it == held.end()) {
                CHECK(st == UpgradeStatus::NotHeld, "upgrade without holding");
            } else if (it->second == Mode::Exclusive) {
                CHECK(st == UpgradeStatus::Upgraded, "already exclusive");
            } else if (st == UpgradeStatus::Upgraded) {
                leave_shared(r);
                enter_excl(r);
                held[r] = Mode::Exclusive;
            } else {
                CHECK(st == UpgradeStatus::Cancelled || st == UpgradeStatus::Conflict,
                      "unexpected upgrade status");
                // 固定语义：共享锁保留
                Mode m;
                CHECK(session.holds(res(r), &m) && m == Mode::Shared,
                      "shared retained after failed upgrade");
            }
        } else if (action < 90) {
            release(r);
        } else if (action < 95) {
            // 重复释放未持有资源：必须返回 false
            int free_r = -1;
            for (int k = 0; k < kResources; ++k)
                if (!held.count(k)) free_r = k;
            if (free_r >= 0) {
                CHECK(!session.unlock(res(free_r)), "double release must fail");
                CHECK(!session.unlock(res(free_r)), "double release must fail twice");
            }
        } else {
            // 关闭并重开会话：全部持有必须被释放
            for (auto& kv : held) {
                if (kv.second == Mode::Shared) leave_shared(kv.first);
                else leave_excl(kv.first);
            }
            held.clear();
            session.close();
            CHECK(!session.valid(), "closed session invalid");
            session = mgr.open_session();
        }
        if (op % 50 == 0) std::this_thread::yield();
    }
    for (auto& kv : held) {
        if (kv.second == Mode::Shared) leave_shared(kv.first);
        else leave_excl(kv.first);
    }
    held.clear();
    session.close();
}

void test_model() {
    LockManager mgr;
    std::vector<std::thread> ts;
    for (int t = 0; t < kThreads; ++t) ts.emplace_back(worker, std::ref(mgr), t);
    for (auto& th : ts) th.join();
    for (int r = 0; r < kResources; ++r) {
        CHECK(g_s[r] == 0 && g_x[r] == 0, "all locks released at end");
        CHECK(mgr.shared_holders(res(r)) == 0, "manager agrees: no shared");
        CHECK(!mgr.exclusive_held(res(r)), "manager agrees: no exclusive");
        CHECK(mgr.queued_requests(res(r)) == 0, "no queued requests left");
    }
    std::fprintf(stderr, "  model test passed (%d threads x %d ops)\n", kThreads,
                 kOpsPerThread);
}

// 饥饿条件：读者持续涌入时写者必须在有界时间内被授予
void test_writer_starvation() {
    LockManager mgr;
    const std::string r = "/m/hot";
    auto holder = mgr.open_session();
    holder.lock_shared(r);

    std::atomic<bool> stop{false};
    std::atomic<bool> writer_done{false};
    std::thread writer([&] {
        auto s = mgr.open_session();
        s.lock_exclusive(r);  // 必须最终被授予
        writer_done = true;
        s.unlock(r);
    });

    std::vector<std::thread> readers;
    for (int i = 0; i < 4; ++i) {
        readers.emplace_back([&] {
            auto s = mgr.open_session();
            while (!stop.load(std::memory_order_relaxed)) {
                s.lock_shared(r);
                s.unlock(r);
            }
        });
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    holder.unlock(r);  // 释放初始持有者，写者开始排队

    // FIFO 公平：写者必须在有界时间内被授予，即使读者不断涌入
    for (int waited_ms = 0; waited_ms < 10000 && !writer_done.load(); waited_ms += 10)
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    CHECK(writer_done.load(), "writer starved by reader churn");
    stop = true;
    for (auto& t : readers) t.join();
    writer.join();
    std::fprintf(stderr, "  writer starvation test passed\n");
}

// 升级者在读者涌入下也不应饥饿
void test_upgrade_starvation() {
    LockManager mgr;
    const std::string r = "/m/hot2";
    auto upgrader = mgr.open_session();
    upgrader.lock_shared(r);

    std::atomic<bool> stop{false};
    std::atomic<bool> upgraded{false};
    std::thread t([&] {
        auto st = upgrader.upgrade(r);
        CHECK(st == UpgradeStatus::Upgraded, "upgrade must eventually succeed");
        upgraded = true;
        upgrader.unlock(r);
    });

    std::vector<std::thread> readers;
    for (int i = 0; i < 4; ++i) {
        readers.emplace_back([&] {
            auto s = mgr.open_session();
            while (!stop.load(std::memory_order_relaxed)) {
                s.lock_shared(r);
                s.unlock(r);
            }
        });
    }
    for (int waited_ms = 0; waited_ms < 10000 && !upgraded.load(); waited_ms += 10)
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    CHECK(upgraded.load(), "upgrader starved by reader churn");
    stop = true;
    for (auto& rd : readers) rd.join();
    t.join();
    std::fprintf(stderr, "  upgrade starvation test passed\n");
}

}  // namespace

int main() {
    test_model();
    test_writer_starvation();
    test_upgrade_starvation();
    std::fprintf(stderr, "model tests OK\n");
    return 0;
}

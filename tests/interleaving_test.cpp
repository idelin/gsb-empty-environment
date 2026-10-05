// 屏障穷举短交错测试
//
// 每个场景由若干线程组成，每个线程执行一段短操作脚本。控制器按
// “回合”调度：每回合释放一个线程执行一步，并等待该线程【完成本步】
// 或【在锁管理器内部阻塞】(park 钩子) 后才进入下一回合。对所有
// 多重集排列（交错序列）逐一执行，验证：
//   - 所有交错都能终止（无永久等待 / 死锁）
//   - 互斥不变量在每个交错中成立
//   - 升级冲突 / 取消语义符合规定，取消后共享锁保留
//   - 重复释放返回 false 且状态不变
//   - 句柄关闭释放全部持有
#include "lockmgr/lock_manager.hpp"

#include <algorithm>
#include <atomic>
#include <condition_variable>
#include <cstdio>
#include <cstdlib>
#include <functional>
#include <mutex>
#include <numeric>
#include <string>
#include <thread>
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

thread_local int tls_tid = -1;
thread_local int tls_step = -1;

// ---------------- 确定性交错调度器 ----------------
class Harness {
public:
    using Step = std::function<void()>;
    using Script = std::vector<Step>;
    // 每个交错序列使用全新的管理器；脚本工厂从中打开会话。
    using ScriptFactory = std::function<Script(LockManager&)>;

    explicit Harness(std::vector<ScriptFactory> factories)
        : factories_(std::move(factories)) {}

    void run_all(std::size_t max_scenarios = 100000) {
        // 先构造一次以确定各线程步数
        std::vector<int> seq;
        {
            LockManager probe;
            for (std::size_t t = 0; t < factories_.size(); ++t) {
                auto script = factories_[t](probe);
                for (std::size_t s = 0; s < script.size(); ++s)
                    seq.push_back(static_cast<int>(t));
            }
        }
        std::sort(seq.begin(), seq.end());
        std::size_t count = 0;
        do {
            run_sequence(seq);
            if (++count > max_scenarios) {
                std::fprintf(stderr, "scenario count exploded\n");
                std::abort();
            }
        } while (std::next_permutation(seq.begin(), seq.end()));
        std::fprintf(stderr, "  harness: %zu interleavings passed\n", count);
        total_interleavings += count;
    }

    static std::size_t total_interleavings;

private:
    void run_sequence(const std::vector<int>& seq) {
        LockManager mgr;
        std::vector<Script> scripts;
        for (auto& f : factories_) scripts.push_back(f(mgr));
        const std::size_t n = scripts.size();

        std::mutex mu;
        std::condition_variable cv;
        std::vector<int> issued(n, 0);  // 每线程已发出的回合数
        std::vector<int> done(n, 0);
        std::vector<bool> parked(n, false);
        std::vector<int> parked_step(n, -1);
        std::atomic<bool> failed{false};

        mgr.set_park_hook([&] {
            std::lock_guard<std::mutex> lk(mu);
            parked[tls_tid] = true;
            parked_step[tls_tid] = tls_step;
            cv.notify_all();
        });

        std::vector<std::thread> workers;
        for (std::size_t t = 0; t < n; ++t) {
            workers.emplace_back([&, t] {
                tls_tid = static_cast<int>(t);
                for (std::size_t s = 0; s < scripts[t].size(); ++s) {
                    {
                        std::unique_lock<std::mutex> lk(mu);
                        cv.wait(lk, [&] {
                            return issued[t] > static_cast<int>(s);
                        });
                    }
                    tls_step = static_cast<int>(s);
                    try {
                        scripts[t][s]();
                    } catch (...) {
                        failed = true;
                    }
                    {
                        std::lock_guard<std::mutex> lk(mu);
                        done[t]++;
                        cv.notify_all();
                    }
                }
            });
        }

        std::vector<int> step_of(n, 0);
        for (int t : seq) {
            int s = step_of[t]++;
            std::unique_lock<std::mutex> lk(mu);
            issued[t]++;
            cv.notify_all();
            // 若该线程仍阻塞在更早的一步（其解除依赖后续回合），
            // 不能等待它的进展，否则控制器与线程互相等待。
            bool stuck_earlier =
                parked[t] && done[t] <= parked_step[t] && parked_step[t] < s;
            if (stuck_earlier) continue;
            bool ok = cv.wait_for(lk, std::chrono::seconds(10), [&] {
                return done[t] > s || (parked[t] && parked_step[t] == s);
            });
            if (!ok) {
                std::fprintf(stderr,
                             "harness: thread %d step %d no progress\n", t, s);
                std::abort();
            }
        }

        // 所有回合已发出；等待全部线程结束（死锁检测）
        {
            std::unique_lock<std::mutex> lk(mu);
            bool ok = cv.wait_for(lk, std::chrono::seconds(10), [&] {
                for (std::size_t t = 0; t < n; ++t)
                    if (done[t] < static_cast<int>(scripts[t].size()))
                        return false;
                return true;
            });
            if (!ok) {
                std::fprintf(stderr, "harness: permanent wait detected\n");
                std::abort();
            }
        }
        for (auto& w : workers) w.join();
        CHECK(!failed, "worker step threw an exception");
    }

    std::vector<ScriptFactory> factories_;
};

std::size_t Harness::total_interleavings = 0;

// ---------------- 场景 ----------------

// 共享/独占互斥：持锁期间通过自省接口验证不变量
void scenario_mutex() {
    const char* r = "/data/a.txt";
    for (int variant = 0; variant < 3; ++variant) {
        Mode ma = variant == 0 ? Mode::Shared : Mode::Exclusive;
        Mode mb = variant == 2 ? Mode::Exclusive : Mode::Shared;
        auto make = [r](Mode m) {
            return [r, m](LockManager& mgr) -> Harness::Script {
                auto s = std::make_shared<LockManager::Session>(mgr.open_session());
                return {
                    [s, &mgr, r, m] {
                        if (m == Mode::Shared) {
                            s->lock_shared(r);
                            CHECK(s->holds(r), "must hold shared");
                            CHECK(!mgr.exclusive_held(r), "exclusive during shared");
                        } else {
                            s->lock_exclusive(r);
                            CHECK(s->holds(r), "must hold exclusive");
                            CHECK(mgr.exclusive_held(r), "exclusive not held");
                            CHECK(mgr.shared_holders(r) == 0, "shared during exclusive");
                        }
                    },
                    [s, r] { CHECK(s->unlock(r), "unlock must succeed"); }};
            };
        };
        Harness h({make(ma), make(mb)});
        h.run_all();
    }
}

// 两个读者同时升级：不能互相永久等待；互斥保持；冲突后共享锁保留
void scenario_upgrade_duo() {
    const char* r = "/data/up.txt";
    // 跨交错持续的临界区计数器：验证互斥
    std::atomic<int> s_inside{0}, x_inside{0};
    auto make = [r, &s_inside, &x_inside](LockManager& mgr) -> Harness::Script {
        auto s = std::make_shared<LockManager::Session>(mgr.open_session());
        return {
            [s, r, &s_inside, &x_inside] {
                s->lock_shared(r);
                s_inside++;
                CHECK(x_inside == 0, "shared overlaps exclusive");
            },
            [s, &mgr, r, &s_inside, &x_inside] {
                auto st = s->upgrade(r);
                CHECK(st != UpgradeStatus::NotHeld, "must hold shared");
                if (st == UpgradeStatus::Upgraded) {
                    s_inside--;
                    CHECK(x_inside.fetch_add(1) == 0, "two exclusive holders");
                    CHECK(s_inside == 0, "shared overlaps upgrade");
                    CHECK(mgr.exclusive_held(r), "must be exclusive");
                    CHECK(mgr.shared_holders(r) == 0, "no shared after upgrade");
                } else {
                    // Conflict：共享锁必须保留
                    Mode m;
                    CHECK(s->holds(r, &m) && m == Mode::Shared,
                          "shared lock must be retained after conflict");
                }
            },
            [s, r, &s_inside, &x_inside] {
                Mode m;
                CHECK(s->holds(r, &m), "must still hold");
                if (m == Mode::Exclusive) x_inside--;
                else s_inside--;
                CHECK(s->unlock(r), "unlock must succeed");
            }};
    };
    Harness h({make, make});
    h.run_all();
}

// 升级取消：取消后原共享锁保留
void scenario_upgrade_cancel() {
    const char* r = "/data/cancel.txt";
    auto tok = std::make_shared<CancelToken>();
    Harness h({
        [r, tok](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, r] { s->lock_shared(r); },
                [s, r, tok] {
                    auto st = s->upgrade(r, *tok);
                    if (st == UpgradeStatus::Cancelled) {
                        Mode m;
                        CHECK(s->holds(r, &m) && m == Mode::Shared,
                              "shared lock must be retained after cancel");
                    } else {
                        CHECK(st == UpgradeStatus::Upgraded, "unexpected status");
                    }
                },
                [s, r] { CHECK(s->unlock(r), "unlock must succeed"); }};
        },
        [r, tok](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, r] { s->lock_shared(r); },
                [tok] { tok->cancel(); },
                [s, r] { CHECK(s->unlock(r), "unlock must succeed"); }};
        },
    });
    h.run_all();
}

// 升级阻塞后来的共享请求（FIFO 公平性，防止升级饥饿）
void scenario_upgrade_blocks_new_readers() {
    const char* r = "/data/fair.txt";
    Harness h({
        [r](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, r] { s->lock_shared(r); },
                [s, r] {
                    auto st = s->upgrade(r);  // 等其他读者释放后授予
                    CHECK(st == UpgradeStatus::Upgraded, "upgrade must succeed");
                },
                [s, r] { CHECK(s->unlock(r), "unlock must succeed"); }};
        },
        [r](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {[s, r] { s->lock_shared(r); },
                    [s, r] { CHECK(s->unlock(r), "unlock must succeed"); }};
        },
        [r](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, &mgr, r] {
                    // 本共享请求若排在升级之后，必须在升级完成后才授予
                    s->lock_shared(r);
                    CHECK(!mgr.exclusive_held(r), "no exclusive during shared");
                    CHECK(s->holds(r), "must hold shared");
                },
                [s, r] { CHECK(s->unlock(r), "unlock must succeed"); }};
        },
    });
    h.run_all();
}

// 重复释放：第二次 unlock 返回 false，状态不变
void scenario_double_release() {
    const char* r = "/data/dbl.txt";
    Harness h({
        [r](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, r] { s->lock_exclusive(r); },
                [s, r] { CHECK(s->unlock(r), "first unlock ok"); },
                [s, &mgr, r] {
                    CHECK(!s->unlock(r), "double release must return false");
                    CHECK(!s->holds(r), "must not hold after release");
                    CHECK(!mgr.exclusive_held(r), "state unchanged");
                }};
        },
        [r](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, &mgr, r] {
                    s->lock_shared(r);  // 在对方释放后授予
                    CHECK(mgr.shared_holders(r) == 1, "exactly one shared holder");
                },
                [s, r] { CHECK(s->unlock(r), "unlock ok"); }};
        },
    });
    h.run_all();
}

// 句柄关闭释放全部持有；close 幂等
void scenario_close_releases_all() {
    const char* r1 = "/data/c1.txt";
    const char* r2 = "/data/c2.txt";
    Harness h({
        [r1, r2](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, r1, r2] {
                    s->lock_shared(r1);
                    s->lock_exclusive(r2);
                },
                [s, &mgr, r1, r2] {
                    s->close();
                    s->close();  // 幂等
                    CHECK(!s->holds(r1) && !s->holds(r2), "close releases all");
                    CHECK(mgr.shared_holders(r1) == 0, "r1 released");
                    CHECK(!mgr.exclusive_held(r2), "r2 released");
                }};
        },
        [r1](LockManager& mgr) -> Harness::Script {
            auto s = std::make_shared<LockManager::Session>(mgr.open_session());
            return {
                [s, &mgr, r1] {
                    s->lock_exclusive(r1);  // 若对方未关闭则阻塞，最终必须授予
                    CHECK(mgr.exclusive_held(r1), "must hold r1");
                },
                [s, r1] { CHECK(s->unlock(r1), "unlock ok"); }};
        },
    });
    h.run_all();
}

// 规范资源 ID：等价路径映射到同一把锁
void scenario_canonical_id() {
    LockManager mgr;
    auto sa = mgr.open_session();
    auto sb = mgr.open_session();
    sa.lock_exclusive("/data/./x/../y.txt");
    // 不同字符串、同一规范 ID：try_lock 必须失败
    CHECK(!sb.try_lock_shared("/data/y.txt"), "canonical id must match");
    CHECK(LockManager::canonicalize("/a/b/../c") == "/a/c", "lexically_normal");
    CHECK(LockManager::canonicalize("/a//b/") == "/a/b", "collapse slashes");
    CHECK(LockManager::canonicalize("") == "/", "empty maps to root");
    sa.unlock("/data/y.txt");  // 用另一等价写法释放
    CHECK(!sa.holds("/data/./y.txt"), "released via canonical id");
    CHECK(sb.try_lock_shared("/data/y.txt"), "lock free after release");
    sb.close();
    std::fprintf(stderr, "  canonical id checks passed\n");
}

}  // namespace

int main() {
    scenario_canonical_id();
    scenario_mutex();
    scenario_upgrade_duo();
    scenario_upgrade_cancel();
    scenario_upgrade_blocks_new_readers();
    scenario_double_release();
    scenario_close_releases_all();
    std::fprintf(stderr, "interleaving tests OK (%zu interleavings)\n",
                 Harness::total_interleavings);
    return 0;
}

#include "netsec.hpp"

#include <cstdio>
#include <cstdlib>
#include <thread>

using namespace netsec;

static int failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,   \
                         #cond);                                           \
            ++failures;                                                    \
        }                                                                  \
    } while (0)

// 场景一：异步模式下越界读后立即经系统调用发送。
// 拷贝不被阻塞（攻击窗口存在），但外部发送前的门禁必须上报并终止进程。
static void test_async_oob_read_then_send() {
    TaggedMemory mem;
    for (std::uint64_t a = 0x1000; a < 0x1010; ++a)
        mem.set_tag(a, 1); // 缓冲区标签为 1，越界区域标签为 0

    MtePolicy proc(MteMode::Async, mem);

    // 越界读：标签不符，但异步模式下拷贝继续。
    CHECK(proc.copy_from_user({0x1008, 1}, 16));
    CHECK(proc.pending_async_fault());
    CHECK(!proc.terminated());

    // 攻击者尝试把越界数据写套接字：门禁必须拦截。
    proc.before_external_send();
    CHECK(proc.terminated());
    CHECK(!proc.pending_async_fault()); // 上报后清除待决状态
    CHECK(proc.reports().size() == 1);
    CHECK(proc.reports()[0].kind == FaultKind::TagMismatch);
    CHECK(proc.reports()[0].access == AccessType::Read);
    CHECK(proc.reports()[0].deferred);

    // 进程已终止，后续发送/拷贝一律失败。
    CHECK(!proc.copy_from_user({0x1000, 1}, 4));
    proc.before_external_send();
    CHECK(proc.reports().size() == 1); // 不重复上报
}

// 异步错误也必须在系统调用返回用户态之前上报。
static void test_async_fault_before_syscall_return() {
    TaggedMemory mem; // 全部标签为 0
    MtePolicy proc(MteMode::Async, mem);

    CHECK(proc.copy_to_user({0x2000, 7}, 8)); // 写异步：拷贝继续
    CHECK(proc.pending_async_fault());

    proc.before_syscall_return();
    CHECK(proc.terminated());
    CHECK(!proc.pending_async_fault());
    CHECK(proc.reports().size() == 1);
    CHECK(proc.reports()[0].access == AccessType::Write);
}

// 场景二：非对称模式——读同步、写异步。
static void test_asymm_read_sync_write_async() {
    TaggedMemory mem; // 全部标签为 0

    // 写异步：标签不符只记录待决，返回用户态前补报。
    {
        MtePolicy proc(MteMode::Asymm, mem);
        CHECK(proc.copy_to_user({0x3000, 3}, 4));
        CHECK(!proc.terminated());
        CHECK(proc.pending_async_fault());
        proc.before_syscall_return();
        CHECK(proc.terminated());
        CHECK(proc.reports().size() == 1);
        CHECK(proc.reports()[0].access == AccessType::Write);
        CHECK(proc.reports()[0].deferred);
    }

    // 读同步：标签不符立即上报并终止，拷贝被拒绝。
    {
        MtePolicy proc(MteMode::Asymm, mem);
        CHECK(!proc.copy_from_user({0x3000, 3}, 4));
        CHECK(proc.terminated());
        CHECK(!proc.pending_async_fault());
        CHECK(proc.reports().size() == 1);
        CHECK(proc.reports()[0].access == AccessType::Read);
        CHECK(!proc.reports()[0].deferred);
    }
}

// 同步模式：任何标签不符立即终止。
static void test_sync_mode_immediate() {
    TaggedMemory mem;
    MtePolicy proc(MteMode::Sync, mem);
    CHECK(!proc.copy_from_user({0x4000, 1}, 4));
    CHECK(proc.terminated());
    CHECK(proc.reports().size() == 1);
    CHECK(!proc.reports()[0].deferred);
}

// 场景三：TCO 只能用于不访问用户内存的受限区段。
static void test_tco_misuse() {
    TaggedMemory mem;
    for (std::uint64_t a = 0x5000; a < 0x5010; ++a)
        mem.set_tag(a, 2);

    // 合法用法：TCO 区段内不触碰用户内存。
    {
        MtePolicy proc(MteMode::Sync, mem);
        {
            auto guard = proc.tco_enter();
            CHECK(proc.tco_active());
        }
        CHECK(!proc.tco_active());
        CHECK(!proc.terminated());
        CHECK(proc.copy_from_user({0x5000, 2}, 8)); // 区段外正常拷贝
        CHECK(!proc.terminated());
    }

    // 误用：TCO 区段内访问用户内存，立即上报并终止。
    {
        MtePolicy proc(MteMode::Sync, mem);
        auto guard = proc.tco_enter();
        CHECK(!proc.copy_from_user({0x5000, 2}, 8)); // 标签正确也不行
        CHECK(proc.terminated());
        CHECK(proc.reports().size() == 1);
        CHECK(proc.reports()[0].kind == FaultKind::TcoViolation);
    }
}

// 场景四：多线程待决错误按线程隔离。
static void test_multithreaded_pending() {
    TaggedMemory mem; // 全部标签为 0
    MtePolicy proc(MteMode::Async, mem);

    std::thread::id main_id = std::this_thread::get_id();
    std::thread::id worker_id;

    std::thread worker([&] {
        worker_id = std::this_thread::get_id();
        // 工作线程产生待决异步错误。
        CHECK(proc.copy_from_user({0x6000, 9}, 4));
        CHECK(proc.pending_async_fault());
        // 工作线程返回用户态前被门禁拦截。
        proc.before_syscall_return();
        CHECK(proc.terminated());
        CHECK(!proc.pending_async_fault());
    });
    worker.join();

    // 主线程在此期间无待决错误，其门禁点不上报。
    CHECK(!proc.pending_async_fault());
    CHECK(proc.terminated()); // 进程已被工作线程终止
    CHECK(proc.reports().size() == 1);
    CHECK(proc.reports()[0].thread == worker_id);
    CHECK(proc.reports()[0].thread != main_id);

    // 进程终止后，其他线程的拷贝与门禁均失效且不重复上报。
    CHECK(!proc.copy_from_user({0x6000, 0}, 4));
    proc.before_syscall_return();
    CHECK(proc.reports().size() == 1);
}

// 线程的待决错误互不影响：先到达门禁的线程上报并终止进程。
static void test_multithreaded_pending_isolation() {
    TaggedMemory mem;
    MtePolicy proc(MteMode::Async, mem);

    std::thread t1([&] { proc.copy_from_user({0x7000, 1}, 4); });
    t1.join();
    // 其他线程的待决错误不影响主线程状态。
    CHECK(!proc.pending_async_fault());
    CHECK(!proc.terminated());

    // 主线程自己的越界写产生独立待决错误。
    CHECK(proc.copy_to_user({0x8000, 2}, 4));
    CHECK(proc.pending_async_fault());
    proc.before_syscall_return();
    CHECK(proc.terminated());
    CHECK(proc.reports().size() == 1);
    CHECK(proc.reports()[0].access == AccessType::Write);
    CHECK(proc.reports()[0].thread == std::this_thread::get_id());
}

int main() {
    test_async_oob_read_then_send();
    test_async_fault_before_syscall_return();
    test_asymm_read_sync_write_async();
    test_sync_mode_immediate();
    test_tco_misuse();
    test_multithreaded_pending();
    test_multithreaded_pending_isolation();

    if (failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return EXIT_FAILURE;
    }
    std::puts("all MTE policy tests passed");
    return EXIT_SUCCESS;
}

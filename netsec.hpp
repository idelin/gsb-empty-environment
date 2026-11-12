#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <thread>
#include <unordered_map>
#include <vector>

namespace netsec {

// 每进程 MTE 检查模式。
enum class MteMode {
    Sync,  // 读写均同步检查
    Async, // 读写均异步检查
    Asymm, // 读同步、写异步
};

enum class AccessType { Read, Write };

enum class FaultKind {
    TagMismatch,  // 标签不符
    TcoViolation, // TCO 区段内访问用户内存
};

struct FaultReport {
    FaultKind kind;
    AccessType access;
    std::uint64_t addr;
    std::thread::id thread;
    bool deferred; // true 表示异步错误在门禁点补报
};

// 用户指针：地址 + 指针携带的标签。
struct UserPtr {
    std::uint64_t addr;
    std::uint8_t tag;
};

// 内核可访问的已标记内存模型：记录每个地址的分配标签。
class TaggedMemory {
public:
    void set_tag(std::uint64_t addr, std::uint8_t tag);
    std::uint8_t tag_of(std::uint64_t addr) const;

private:
    std::unordered_map<std::uint64_t, std::uint8_t> tags_;
};

// 每进程 MTE 检查策略。供系统调用入口与用户拷贝路径调用。
// 待决异步错误按线程记录；进程终止后所有后续操作直接失败。
// 所有公共方法线程安全。
class MtePolicy {
public:
    explicit MtePolicy(MteMode mode, const TaggedMemory& mem);

    MteMode mode() const { return mode_; }
    bool terminated() const;
    // 已上报错误的快照（按上报顺序）。
    std::vector<FaultReport> reports() const;

    // 用户拷贝路径：内核替用户拷贝前必须调用。
    // 返回 false 表示拷贝被拒绝（同步错误已上报并终止进程，
    // 或进程已终止）；异步错误不阻塞拷贝，只记录待决状态。
    bool copy_from_user(UserPtr ptr, std::size_t len);
    bool copy_to_user(UserPtr ptr, std::size_t len);

    // TCO 受限区段：区段内禁止访问用户内存。RAII 守卫。
    class TcoGuard {
    public:
        explicit TcoGuard(MtePolicy& policy);
        ~TcoGuard();
        TcoGuard(const TcoGuard&) = delete;
        TcoGuard& operator=(const TcoGuard&) = delete;

    private:
        MtePolicy& policy_;
    };
    TcoGuard tco_enter();
    bool tco_active() const;

    // 门禁点：异步错误必须在此上报并终止进程，上报后清除待决状态。
    void before_syscall_return();  // 系统调用返回用户态之前
    void before_external_send();   // 任何外部可见发送（如写套接字）之前

    // 当前线程是否存在待决异步错误。
    bool pending_async_fault() const;

private:
    struct ThreadState {
        unsigned tco_depth = 0;
        bool pending = false;
        FaultReport pending_report{};
    };

    bool check_access(UserPtr ptr, std::size_t len, AccessType access);
    void report_and_terminate(const FaultReport& report);
    void drain_pending();
    ThreadState& current_thread();
    const ThreadState* find_thread(std::thread::id id) const;

    MteMode mode_;
    const TaggedMemory& mem_;
    mutable std::mutex mu_;
    bool terminated_ = false;
    std::vector<FaultReport> reports_;
    std::map<std::thread::id, ThreadState> threads_;
};

} // namespace netsec

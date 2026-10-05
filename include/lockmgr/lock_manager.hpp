// lockmgr: 本地文件锁管理库
//
// 以规范资源 ID 为锁对象，支持共享 (Shared) / 独占 (Exclusive) / 升级 (Upgrade)。
//
// 固定语义（详见 README.md）：
//  1. 资源 ID 规范化：lexically_normal，等价路径映射到同一把锁。
//  2. 排队次序：每个资源一条严格 FIFO 队列；只有队首请求可被授予，
//     队首为共享请求时连续的共享请求可一并授予。写者与升级不会被
//     后来读者插队（防饥饿）。
//  3. 升级：只允许注册在空队列上。已有升级在排队、或队列中已有其它
//     等待者（如写者）时立即返回 Conflict —— 升级者自身持有共享锁，
//     若排在写者之后等待将与写者互相永久等待；两个读者同时升级也
//     不会互相永久等待。
//  4. 取消：升级可被 CancelToken 取消；取消后【原共享锁保留】。
//     Conflict 与 NotHeld 同样不影响已持有的共享锁。
//  5. 句柄关闭 (close/析构)：释放该会话持有的全部锁，幂等。
//  6. 重复释放：unlock 未持有的资源返回 false，不改变任何状态。
#pragma once

#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <string>

namespace lockmgr {

enum class Mode { Shared, Exclusive };

namespace detail {
struct TokenAccess;
}

enum class UpgradeStatus {
    Upgraded,   // 已升级为独占
    Cancelled,  // 令牌被取消；原共享锁保留
    Conflict,   // 已有其他升级在排队；原共享锁保留
    NotHeld     // 会话未持有该资源
};

// 取消令牌：可拷贝，跨线程安全。cancel() 幂等。
class CancelToken {
public:
    CancelToken() = default;
    void cancel() const noexcept;
    bool cancelled() const noexcept;

private:
    friend class LockManager;
    friend struct detail::TokenAccess;
    struct State {
        std::mutex mu;
        bool cancelled = false;
        std::function<void()> hook;  // 由 LockManager 在等待期间注册
    };
    std::shared_ptr<State> st_ = std::make_shared<State>();
};

struct LockManagerImpl;

class LockManager {
public:
    using ResourceId = std::string;

    // 把任意路径形式的资源名规范化为唯一锁对象 ID。
    static ResourceId canonicalize(const std::string& raw);

    LockManager();
    ~LockManager();
    LockManager(const LockManager&) = delete;
    LockManager& operator=(const LockManager&) = delete;

    // 会话句柄：移动语义；析构即 close()。单会话不保证并发安全，
    // 但 close() 可与其他线程中阻塞的升级等待安全竞争（等待方会
    // 以 Cancelled 返回，且共享锁已被 close 释放）。
    class Session {
    public:
        Session(Session&&) noexcept;
        Session& operator=(Session&&) noexcept;
        ~Session();

        // 阻塞获取。对已持有资源：同级重入为无操作；持有共享时
        // lock_exclusive 等价于不可取消的 upgrade（冲突时抛
        // std::runtime_error）；持有独占时任何获取均为无操作。
        void lock_shared(const std::string& resource);
        void lock_exclusive(const std::string& resource);
        bool try_lock_shared(const std::string& resource);
        bool try_lock_exclusive(const std::string& resource);

        // 升级：需已持有共享锁。返回 Upgraded 后持有独占；
        // 其余返回值均保留原共享锁。
        UpgradeStatus upgrade(const std::string& resource,
                              const CancelToken& token = CancelToken{});

        // 释放。未持有该资源（含重复释放）返回 false，状态不变。
        bool unlock(const std::string& resource);

        // 释放全部持有并作废句柄；幂等。
        void close() noexcept;

        bool holds(const std::string& resource, Mode* mode = nullptr) const;
        bool valid() const noexcept;

    private:
        friend class LockManager;
        Session(std::shared_ptr<LockManagerImpl> impl, std::uint64_t id);
        std::shared_ptr<LockManagerImpl> impl_;
        std::uint64_t id_ = 0;
    };

    Session open_session();

    // ---- 测试 / 调试自省接口 ----
    // 钩子：有请求入队且未能立即授予（线程即将阻塞）时触发，
    // 触发时持有内部锁、运行在即将阻塞的线程上。
    void set_park_hook(std::function<void()> hook);
    std::size_t shared_holders(const std::string& resource) const;
    bool exclusive_held(const std::string& resource) const;
    std::size_t queued_requests(const std::string& resource) const;

private:
    std::shared_ptr<LockManagerImpl> impl_;
};

}  // namespace lockmgr

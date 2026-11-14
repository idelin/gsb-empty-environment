#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <deque>
#include <functional>
#include <memory>
#include <string>
#include <utility>

namespace netsec {

using Clock = std::chrono::steady_clock;
using TimePoint = Clock::time_point;

// 后端连接的协议状态
enum class ProtocolState {
  kIdle,        // 空闲，可安全复用
  kInPipeline,  // 流水线中仍有未完成的命令
  kHalfClosed,  // 对端半关闭
  kError,       // 协议或 I/O 错误
  kClosed,      // 已关闭
};

// 绑定在一条连接上的租户会话状态
struct TenantContext {
  std::string principal;              // 认证主体
  std::string name_space;             // 数据库 / 命名空间
  std::uint64_t perm_generation = 0;  // 权限代次，变更后必须重建会话

  friend bool operator==(const TenantContext&, const TenantContext&) = default;

  bool anonymous() const { return principal.empty(); }
};

// 归还连接时的处置策略
enum class ResetPolicy {
  kResetOnSwitch,   // 惰性：池内保留租户态，切换主体时先重置再绑定
  kResetOnReturn,   // 急切：归还时立即重置为匿名态
  kDestroyOnReturn, // 归还即销毁，绝不复用
};

struct PoolOptions {
  ResetPolicy reset_policy = ResetPolicy::kResetOnSwitch;
  // 池中连接有效期，必须严格小于服务端会话超时（构造时强制校验）
  std::chrono::milliseconds max_idle_lifetime{std::chrono::seconds(60)};
  std::chrono::milliseconds server_session_timeout{std::chrono::seconds(300)};
  std::size_t max_idle = 8;
  // 可注入时钟，便于确定性测试
  std::function<TimePoint()> now = Clock::now;
};

// 后端连接抽象：代理由具体协议实现该接口
class IBackendConnection {
public:
  virtual ~IBackendConnection() = default;

  // 发送一条命令并读取响应（同步模型）
  virtual std::string request(const std::string& command) = 0;
  virtual ProtocolState state() const = 0;
  // 流水线中已发送但未收到响应的命令数
  virtual std::size_t pending() const = 0;
  // 未超时、未出错、未半关闭
  virtual bool healthy() const = 0;
  virtual TenantContext context() const = 0;
  // 会话建立后由池盖章绑定上下文（含权限代次）
  virtual void bind(const TenantContext& ctx) = 0;
  virtual void close() = 0;
};

struct PoolStats {
  std::uint64_t created = 0;
  std::uint64_t destroyed = 0;
  std::uint64_t reused = 0;        // 同租户零重置直接复用次数
  std::uint64_t reset_ok = 0;      // 重置成功并完成重新绑定的次数
  std::uint64_t reset_failed = 0;  // 重置失败（连接随即销毁）的次数
};

// 多租户后端连接复用池。线程模型：单线程事件循环使用；
// 多线程场景由调用方在外部串行化 acquire/release。
class ConnectionPool {
public:
  using Factory = std::function<std::unique_ptr<IBackendConnection>()>;
  // 会话初始化器：在匿名连接上认证并绑定租户，返回是否成功
  using Initializer = std::function<bool(IBackendConnection&, const TenantContext&)>;

  explicit ConnectionPool(PoolOptions options, Factory factory,
                          Initializer initializer = {});

  ConnectionPool(const ConnectionPool&) = delete;
  ConnectionPool& operator=(const ConnectionPool&) = delete;

  // RAII 租约：析构即归还；归还时按策略重置或销毁
  class Lease {
  public:
    Lease() = default;
    Lease(Lease&& other) noexcept { *this = std::move(other); }
    Lease& operator=(Lease&& other) noexcept {
      if (this != &other) {
        reset();
        pool_ = other.pool_;
        conn_ = std::move(other.conn_);
        created_at_ = other.created_at_;
        discard_ = other.discard_;
        other.pool_ = nullptr;
        other.discard_ = false;
      }
      return *this;
    }
    Lease(const Lease&) = delete;
    Lease& operator=(const Lease&) = delete;
    ~Lease() { reset(); }

    IBackendConnection& get() { return *conn_; }
    IBackendConnection* operator->() { return conn_.get(); }
    IBackendConnection& operator*() { return *conn_; }
    explicit operator bool() const { return static_cast<bool>(conn_); }

    // 标记本连接不可复用（如中途出错），归还时直接销毁
    void discard() { discard_ = true; }
    // 提前归还
    void reset();

  private:
    friend class ConnectionPool;
    Lease(ConnectionPool* pool, std::unique_ptr<IBackendConnection> conn,
          TimePoint created_at)
        : pool_(pool), conn_(std::move(conn)), created_at_(created_at) {}

    ConnectionPool* pool_ = nullptr;
    std::unique_ptr<IBackendConnection> conn_;
    TimePoint created_at_{};
    bool discard_ = false;
  };

  // 取出一个绑定到指定租户上下文的连接
  Lease acquire(const TenantContext& tenant);

  const PoolStats& stats() const { return stats_; }
  std::size_t idle_count() const { return idle_.size(); }

private:
  struct IdleEntry {
    std::unique_ptr<IBackendConnection> conn;
    TimePoint created_at;
  };

  void release(std::unique_ptr<IBackendConnection> conn, TimePoint created_at,
               bool discard);
  bool reusable(const IBackendConnection& conn, TimePoint created_at) const;
  bool reset_and_bind(IBackendConnection& conn, const TenantContext& tenant);

  PoolOptions options_;
  Factory factory_;
  Initializer initializer_;
  std::deque<IdleEntry> idle_;
  PoolStats stats_{};
};

}  // namespace netsec

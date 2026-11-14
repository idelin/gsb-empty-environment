#include "netsec.hpp"

#include <stdexcept>

namespace netsec {
namespace {

bool response_ok(const std::string& resp) { return resp.rfind("+OK", 0) == 0; }

// 连接处于可安全复用的协议状态：健康、空闲、无流水线残留
bool protocol_clean(const IBackendConnection& conn) {
  return conn.healthy() && conn.pending() == 0 &&
         conn.state() == ProtocolState::kIdle;
}

// 默认会话初始化：AUTH 认证主体 + SELECT 命名空间，逐条核对响应，
// 全部通过后盖章绑定上下文并回读校验
bool default_initialize(IBackendConnection& conn, const TenantContext& tenant) {
  if (!response_ok(conn.request("AUTH " + tenant.principal))) return false;
  if (!tenant.name_space.empty() &&
      !response_ok(conn.request("SELECT " + tenant.name_space))) {
    return false;
  }
  conn.bind(tenant);
  return protocol_clean(conn) && conn.context() == tenant;
}

}  // namespace

ConnectionPool::ConnectionPool(PoolOptions options, Factory factory,
                               Initializer initializer)
    : options_(std::move(options)),
      factory_(std::move(factory)),
      initializer_(std::move(initializer)) {
  if (!factory_) {
    throw std::invalid_argument("netsec: connection factory is required");
  }
  if (options_.max_idle_lifetime <= std::chrono::milliseconds::zero()) {
    throw std::invalid_argument("netsec: max_idle_lifetime must be positive");
  }
  // 池中连接有效期必须严格短于服务端会话超时
  if (options_.max_idle_lifetime >= options_.server_session_timeout) {
    throw std::invalid_argument(
        "netsec: max_idle_lifetime must be shorter than server_session_timeout");
  }
  if (!initializer_) {
    initializer_ = default_initialize;
  }
}

bool ConnectionPool::reusable(const IBackendConnection& conn,
                              TimePoint created_at) const {
  if (!protocol_clean(conn)) return false;
  return options_.now() - created_at < options_.max_idle_lifetime;
}

// 切换主体：先重置认证状态并核对响应与回读状态，再重新认证绑定；
// 任何一步失败都返回 false，由调用方销毁连接
bool ConnectionPool::reset_and_bind(IBackendConnection& conn,
                                    const TenantContext& tenant) {
  const bool reset_ok = response_ok(conn.request("RESET")) &&
                        conn.context().anonymous() && protocol_clean(conn);
  if (!reset_ok) {
    ++stats_.reset_failed;
    return false;
  }
  ++stats_.reset_ok;
  return initializer_(conn, tenant);
}

ConnectionPool::Lease ConnectionPool::acquire(const TenantContext& tenant) {
  while (!idle_.empty()) {
    IdleEntry entry = std::move(idle_.front());
    idle_.pop_front();
    // 超时、错误、半关闭、流水线残留的连接直接丢弃
    if (!reusable(*entry.conn, entry.created_at)) {
      ++stats_.destroyed;
      continue;
    }
    // 同租户同代次：零重置直接复用
    if (entry.conn->context() == tenant) {
      ++stats_.reused;
      return Lease(this, std::move(entry.conn), entry.created_at);
    }
    // 跨主体 / 跨命名空间 / 跨权限代次：重置核对后重新绑定
    if (reset_and_bind(*entry.conn, tenant)) {
      return Lease(this, std::move(entry.conn), entry.created_at);
    }
    ++stats_.destroyed;  // 重置失败，状态绝不可跨主体
  }

  auto conn = factory_();
  if (!conn) {
    throw std::runtime_error("netsec: connection factory returned null");
  }
  const TimePoint created_at = options_.now();
  ++stats_.created;
  if (!initializer_(*conn, tenant)) {
    ++stats_.destroyed;
    throw std::runtime_error("netsec: failed to initialize backend session");
  }
  return Lease(this, std::move(conn), created_at);
}

void ConnectionPool::release(std::unique_ptr<IBackendConnection> conn,
                             TimePoint created_at, bool discard) {
  if (!conn) return;
  // 显式丢弃、超时、错误、半关闭、流水线残留、超龄、策略要求销毁
  if (discard || !protocol_clean(*conn) ||
      options_.now() - created_at >= options_.max_idle_lifetime ||
      options_.reset_policy == ResetPolicy::kDestroyOnReturn) {
    ++stats_.destroyed;
    return;
  }
  // 急切重置策略：归还时即恢复匿名态，核对失败则销毁
  if (options_.reset_policy == ResetPolicy::kResetOnReturn &&
      !conn->context().anonymous()) {
    const bool ok = response_ok(conn->request("RESET")) &&
                    conn->context().anonymous() && protocol_clean(*conn);
    if (!ok) {
      ++stats_.reset_failed;
      ++stats_.destroyed;
      return;
    }
    ++stats_.reset_ok;
  }
  if (idle_.size() >= options_.max_idle) {
    ++stats_.destroyed;
    return;
  }
  idle_.push_back(IdleEntry{std::move(conn), created_at});
}

void ConnectionPool::Lease::reset() {
  if (pool_ && conn_) {
    pool_->release(std::move(conn_), created_at_, discard_);
  }
  pool_ = nullptr;
  discard_ = false;
}

}  // namespace netsec

#include "netsec.hpp"

#include <chrono>
#include <iostream>
#include <string>
#include <vector>

using namespace netsec;
using namespace std::chrono_literals;
using std::string;

static int g_failures = 0;

#define CHECK(cond)                                                  \
  do {                                                               \
    if (!(cond)) {                                                   \
      std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << "  "    \
                << #cond << "\n";                                    \
      ++g_failures;                                                  \
    }                                                                \
  } while (0)

// 所有连接共享的命令日志，连接销毁后日志仍可检查
struct Journal {
  std::vector<string> entries;
  void add(int id, const string& cmd) {
    entries.push_back(std::to_string(id) + ":" + cmd);
  }
  // 连接 id 的最后一条命令
  string last_of(int id) const {
    const string prefix = std::to_string(id) + ":";
    for (auto it = entries.rbegin(); it != entries.rend(); ++it) {
      if (it->rfind(prefix, 0) == 0) return *it;
    }
    return {};
  }
};

// 脚本化假后端：模拟 AUTH/SELECT/RESET/GET 及各类故障
class FakeBackend final : public IBackendConnection {
public:
  FakeBackend(int id, Journal& journal) : id_(id), journal_(journal) {}

  std::string request(const string& command) override {
    journal_.add(id_, command);
    if (state_ == ProtocolState::kError || state_ == ProtocolState::kHalfClosed ||
        state_ == ProtocolState::kClosed || timed_out_) {
      return "-ERR io";
    }
    if (command == "RESET") {
      ++reset_calls_;
      if (fail_reset_) {
        state_ = ProtocolState::kError;
        return "-ERR reset failed";
      }
      ctx_ = TenantContext{};
      state_ = ProtocolState::kIdle;
      return "+OK";
    }
    if (command.rfind("AUTH ", 0) == 0) {
      const string p = command.substr(5);
      if (p == "mallory") {
        state_ = ProtocolState::kError;
        return "-ERR auth denied";
      }
      ctx_.principal = p;
      return "+OK";
    }
    if (command.rfind("SELECT ", 0) == 0) {
      ctx_.name_space = command.substr(7);
      return "+OK";
    }
    if (command.rfind("GET", 0) == 0) {
      return "$" + ctx_.principal + "@" + ctx_.name_space;
    }
    return "+OK";
  }

  ProtocolState state() const override { return state_; }
  std::size_t pending() const override { return pending_; }
  bool healthy() const override {
    return !timed_out_ && (state_ == ProtocolState::kIdle ||
                           state_ == ProtocolState::kInPipeline);
  }
  TenantContext context() const override { return ctx_; }
  void bind(const TenantContext& ctx) override { ctx_ = ctx; }
  void close() override { state_ = ProtocolState::kClosed; }

  void simulate_pipeline(std::size_t n) {
    pending_ = n;
    state_ = ProtocolState::kInPipeline;
  }
  void simulate_error() { state_ = ProtocolState::kError; }
  void simulate_half_close() { state_ = ProtocolState::kHalfClosed; }
  void simulate_timeout() { timed_out_ = true; }
  void fail_reset() { fail_reset_ = true; }
  int reset_calls() const { return reset_calls_; }

private:
  int id_;
  Journal& journal_;
  TenantContext ctx_;
  ProtocolState state_ = ProtocolState::kIdle;
  std::size_t pending_ = 0;
  bool timed_out_ = false;
  bool fail_reset_ = false;
  int reset_calls_ = 0;
};

struct FakeFactory {
  Journal journal;
  std::vector<FakeBackend*> live;  // 仅可解引用仍存活的连接
  int next_id = 0;

  ConnectionPool::Factory factory() {
    return [this]() -> std::unique_ptr<IBackendConnection> {
      auto conn = std::make_unique<FakeBackend>(next_id++, journal);
      live.push_back(conn.get());
      return conn;
    };
  }
};

static FakeBackend* fake(ConnectionPool::Lease& lease) {
  return static_cast<FakeBackend*>(&lease.get());
}

// 1. AUTH 继承：前一租户认证态不得泄漏给下一租户
void test_auth_state_not_inherited() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  const TenantContext alice{"alice", "db0", 1};
  const TenantContext bob{"bob", "db0", 1};
  {
    auto lease = pool.acquire(alice);
    CHECK(lease->request("GET k").find("alice@db0") != string::npos);
  }
  {
    auto lease = pool.acquire(bob);
    CHECK(pool.stats().created == 1);  // 物理连接被复用
    CHECK(pool.stats().reset_ok == 1);
    CHECK(lease->context() == bob);  // 主体已切换为 bob
    const string resp = lease->request("GET k");
    CHECK(resp.find("bob@db0") != string::npos);
    CHECK(resp.find("alice") == string::npos);  // 无 alice 残留
  }
}

// 2. 跨命名空间复用：同主体不同命名空间也必须重置并重新 SELECT
void test_cross_namespace_reuse() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  {
    auto lease = pool.acquire(TenantContext{"alice", "db0", 1});
    CHECK(lease->request("GET k").find("alice@db0") != string::npos);
  }
  {
    auto lease = pool.acquire(TenantContext{"alice", "db1", 1});
    CHECK(pool.stats().created == 1);
    CHECK(pool.stats().reset_ok == 1);
    CHECK(lease->context().name_space == "db1");
    const string resp = lease->request("GET k");
    CHECK(resp.find("alice@db1") != string::npos);
    CHECK(resp.find("db0") == string::npos);
  }
}

// 3. 流水线残留：带未完成命令的连接归还时必须销毁，不得跨租户
void test_pipeline_residue_discarded() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  {
    auto lease = pool.acquire(TenantContext{"alice", "db0", 1});
    fake(lease)->simulate_pipeline(2);  // 两条命令未读响应
  }
  CHECK(pool.stats().destroyed == 1);
  CHECK(pool.idle_count() == 0);
  {
    auto lease = pool.acquire(TenantContext{"bob", "db0", 1});
    CHECK(pool.stats().created == 2);  // 新连接
    CHECK(lease->context().principal == "bob");
    // bob 不会读到 alice 流水线残留的响应
    CHECK(lease->request("GET k").find("bob@db0") != string::npos);
  }
}

// 4. 错误 / 半关闭 / 超时连接直接丢弃；空闲期变坏的连接取出时丢弃
void test_broken_connections_discarded() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  const TenantContext alice{"alice", "db0", 1};
  {
    auto lease = pool.acquire(alice);
    fake(lease)->simulate_error();
  }
  CHECK(pool.stats().destroyed == 1 && pool.idle_count() == 0);
  {
    auto lease = pool.acquire(alice);
    fake(lease)->simulate_half_close();
  }
  CHECK(pool.stats().destroyed == 2 && pool.idle_count() == 0);
  {
    auto lease = pool.acquire(alice);
    fake(lease)->simulate_timeout();
  }
  CHECK(pool.stats().destroyed == 3 && pool.idle_count() == 0);
  {
    auto lease = pool.acquire(alice);  // 健康归还，进入空闲池
  }
  CHECK(pool.idle_count() == 1);
  ff.live[3]->simulate_error();  // 空闲期间变坏
  {
    auto lease = pool.acquire(TenantContext{"bob", "db0", 1});
    CHECK(pool.stats().destroyed == 4);
    CHECK(pool.stats().created == 5);
    CHECK(lease->context().principal == "bob");
  }
}

// 5. 合法同租户复用：同主体、同命名空间、同代次零重置直接复用
void test_same_tenant_reuse() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  const TenantContext alice{"alice", "db0", 1};
  { auto lease = pool.acquire(alice); }
  {
    auto lease = pool.acquire(alice);
    CHECK(pool.stats().created == 1);
    CHECK(pool.stats().reused == 1);
    CHECK(ff.live[0]->reset_calls() == 0);  // 未发生重置
    CHECK(lease->context() == alice);
    CHECK(lease->request("GET k").find("alice@db0") != string::npos);
  }
}

// 6. 重置失败：连接销毁，状态绝不跨主体
void test_reset_failure_never_leaks() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  {
    auto lease = pool.acquire(TenantContext{"alice", "db0", 1});
    CHECK(lease->request("GET k").find("alice@db0") != string::npos);
  }
  ff.live[0]->fail_reset();  // RESET 将失败
  {
    auto lease = pool.acquire(TenantContext{"bob", "db0", 1});
    CHECK(pool.stats().reset_failed == 1);
    CHECK(pool.stats().destroyed == 1);
    CHECK(pool.stats().created == 2);  // 为新主体新建连接
    CHECK(lease->context().principal == "bob");
    CHECK(lease->request("GET k").find("bob@db0") != string::npos);
  }
  // 旧连接在 RESET 失败后再未执行任何命令
  CHECK(ff.journal.last_of(0) == "0:RESET");
}

// 权限代次变更视同主体切换，必须重置重建会话
void test_perm_generation_rollover() {
  FakeFactory ff;
  ConnectionPool pool(PoolOptions{}, ff.factory());
  { auto lease = pool.acquire(TenantContext{"alice", "db0", 1}); }
  {
    auto lease = pool.acquire(TenantContext{"alice", "db0", 2});
    CHECK(pool.stats().reset_ok == 1);
    CHECK(ff.live[0]->reset_calls() == 1);
    CHECK(lease->context().perm_generation == 2);
  }
}

// 池中连接有效期必须短于服务端会话超时
void test_idle_lifetime_and_option_validation() {
  FakeFactory ff;
  const auto base = Clock::now();
  std::chrono::milliseconds offset{0};
  PoolOptions opts;
  opts.max_idle_lifetime = 50ms;
  opts.server_session_timeout = 100ms;
  opts.now = [&] { return base + offset; };
  ConnectionPool pool(opts, ff.factory());
  { auto lease = pool.acquire(TenantContext{"alice", "db0", 1}); }
  CHECK(pool.idle_count() == 1);
  offset = 60ms;  // 超过池有效期但仍小于服务端会话超时
  {
    auto lease = pool.acquire(TenantContext{"alice", "db0", 1});
    CHECK(pool.stats().destroyed == 1);  // 超龄连接被丢弃
    CHECK(pool.stats().created == 2);
  }
  // 有效期 >= 服务端会话超时的配置被拒绝
  PoolOptions bad;
  bad.max_idle_lifetime = 100ms;
  bad.server_session_timeout = 100ms;
  bool threw = false;
  try {
    ConnectionPool invalid(bad, ff.factory());
  } catch (const std::invalid_argument&) {
    threw = true;
  }
  CHECK(threw);
}

// 归还策略：急切重置与归还即销毁
void test_return_policies() {
  {
    FakeFactory ff;
    PoolOptions opts;
    opts.reset_policy = ResetPolicy::kResetOnReturn;
    ConnectionPool pool(opts, ff.factory());
    const TenantContext alice{"alice", "db0", 1};
    { auto lease = pool.acquire(alice); }
    CHECK(ff.live[0]->reset_calls() == 1);  // 归还时即重置
    CHECK(ff.live[0]->context().anonymous());
    {
      auto lease = pool.acquire(alice);  // 匿名连接重新认证
      CHECK(pool.stats().created == 1);
      CHECK(lease->context() == alice);
    }
  }
  {
    FakeFactory ff;
    PoolOptions opts;
    opts.reset_policy = ResetPolicy::kDestroyOnReturn;
    ConnectionPool pool(opts, ff.factory());
    { auto lease = pool.acquire(TenantContext{"alice", "db0", 1}); }
    CHECK(pool.stats().destroyed == 1);
    CHECK(pool.idle_count() == 0);
  }
}

int main() {
  test_auth_state_not_inherited();
  test_cross_namespace_reuse();
  test_pipeline_residue_discarded();
  test_broken_connections_discarded();
  test_same_tenant_reuse();
  test_reset_failure_never_leaks();
  test_perm_generation_rollover();
  test_idle_lifetime_and_option_validation();
  test_return_policies();
  if (g_failures == 0) {
    std::cout << "all netsec tests passed\n";
    return 0;
  }
  std::cerr << g_failures << " check(s) failed\n";
  return 1;
}

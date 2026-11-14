#include "netsec.hpp"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <thread>
#include <vector>

namespace {

using netsec::ObjectHandle;
using netsec::SessionError;
using netsec::SessionHandle;
using netsec::SessionManager;

int failures = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,        \
                   __LINE__, #cond);                             \
      ++failures;                                                \
    }                                                            \
  } while (0)

// 手动时钟，用于确定性超时测试。
struct ManualClock {
  std::chrono::steady_clock::time_point t =
      std::chrono::steady_clock::time_point{};
  netsec::TimePoint now() const { return t; }
  void advance(std::chrono::milliseconds d) { t += d; }
};

SessionManager::NowFn clock_fn(ManualClock& clock) {
  return [&clock] { return clock.now(); };
}

void test_legal_calls() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s, "app-a", "pin-123") == SessionError::ok);

  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "signing-key", true, key) ==
        SessionError::ok);
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::ok);
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::decrypt) ==
        SessionError::ok);
  CHECK(mgr.destroy_object(s, "app-a", key) == SessionError::ok);
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::object_not_found);
  CHECK(mgr.logout(s, "app-a") == SessionError::ok);
}

void test_reuse_after_logout() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::ok);
  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) == SessionError::ok);
  CHECK(mgr.logout(s, "app-a") == SessionError::ok);

  // 登出后复用会话句柄与对象句柄一律拒绝。
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::invalid_session);
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::invalid_session);
  ObjectHandle other;
  CHECK(mgr.create_object(s, "app-a", "x", true, other) ==
        SessionError::invalid_session);
  // 登出时对象一并销毁。
  CHECK(mgr.destroy_object(s, "app-a", key) == SessionError::invalid_session);
}

void test_cross_application_handle() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::ok);
  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) == SessionError::ok);

  // 另一应用持有同一数值句柄也不得使用。
  CHECK(mgr.operate(s, "app-b", key, netsec::KeyOperation::sign) ==
        SessionError::cross_application);
  CHECK(mgr.logout(s, "app-b") == SessionError::cross_application);
  // 原应用不受影响。
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::ok);
}

void test_cross_session_object() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s1 = mgr.open_session("app-a", "alice");
  SessionHandle s2 = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s1, "app-a", "pin") == SessionError::ok);
  CHECK(mgr.login(s2, "app-a", "pin") == SessionError::ok);
  ObjectHandle key;
  CHECK(mgr.create_object(s1, "app-a", "priv", true, key) == SessionError::ok);

  // 对象句柄按会话隔离：同应用不同会话同样拒绝。
  CHECK(mgr.operate(s2, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::cross_session_object);
  CHECK(mgr.destroy_object(s2, "app-a", key) ==
        SessionError::cross_session_object);
  CHECK(mgr.operate(s1, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::ok);
}

void test_timeout() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::ok);
  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) == SessionError::ok);

  clock.advance(std::chrono::minutes(16));
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::session_expired);
  // 超时后句柄永久失效，后续调用一律拒绝。
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::invalid_session);
}

void test_permission_requires_login() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  // 未登录不能创建对象。
  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) ==
        SessionError::not_logged_in);
  CHECK(mgr.login(s, "app-a", "") == SessionError::permission_denied);
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::ok);
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) == SessionError::ok);
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::ok);
}

void test_device_reset() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::ok);
  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) == SessionError::ok);

  mgr.reset();
  // 重置后所有旧句柄失效。
  CHECK(mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) ==
        SessionError::invalid_session);
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::invalid_session);
  CHECK(mgr.active_session_count() == 0);

  // 重置后新会话（新代次）正常工作，与旧句柄互不干扰。
  SessionHandle s2 = mgr.open_session("app-a", "bob");
  CHECK(s2.generation != s.generation);
  CHECK(mgr.login(s2, "app-a", "pin") == SessionError::ok);
  ObjectHandle key2;
  CHECK(mgr.create_object(s2, "app-a", "priv", true, key2) ==
        SessionError::ok);
  CHECK(mgr.operate(s2, "app-a", key2, netsec::KeyOperation::sign) ==
        SessionError::ok);
}

void test_concurrent_access() {
  ManualClock clock;
  SessionManager mgr(std::chrono::minutes(15), clock_fn(clock));
  SessionHandle s = mgr.open_session("app-a", "alice");
  CHECK(mgr.login(s, "app-a", "pin") == SessionError::ok);
  ObjectHandle key;
  CHECK(mgr.create_object(s, "app-a", "priv", true, key) == SessionError::ok);

  std::atomic<int> bad{0};
  std::vector<std::thread> threads;
  for (int i = 0; i < 8; ++i) {
    threads.emplace_back([&] {
      for (int j = 0; j < 200; ++j) {
        if (mgr.operate(s, "app-a", key, netsec::KeyOperation::sign) !=
            SessionError::ok) {
          ++bad;
        }
        if (mgr.operate(s, "app-b", key, netsec::KeyOperation::sign) !=
            SessionError::cross_application) {
          ++bad;
        }
      }
    });
  }
  for (auto& t : threads) {
    t.join();
  }
  CHECK(bad.load() == 0);
}

}  // namespace

int main() {
  test_legal_calls();
  test_reuse_after_logout();
  test_cross_application_handle();
  test_cross_session_object();
  test_timeout();
  test_permission_requires_login();
  test_device_reset();
  test_concurrent_access();
  if (failures == 0) {
    std::puts("all session lifecycle tests passed");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", failures);
  return 1;
}

#pragma once

#include <chrono>
#include <cstdint>
#include <functional>
#include <string>

namespace netsec {

using AppId = std::string;
using UserId = std::string;
using TimePoint = std::chrono::steady_clock::time_point;

enum class SessionError {
  ok,
  invalid_session,       // 句柄不存在或已关闭
  stale_session,         // 代次不匹配：登出/重置后的旧句柄
  session_expired,       // 会话超时
  not_logged_in,         // 未登录，无权执行私钥操作
  permission_denied,     // 逐调用权限校验失败
  object_not_found,      // 对象句柄无效
  cross_session_object,  // 对象不属于该会话
  cross_application,     // 句柄被其他应用复用
};

enum class KeyOperation { get_public, sign, decrypt, derive };

struct SessionHandle {
  std::uint64_t id = 0;
  std::uint64_t generation = 0;
  friend bool operator==(const SessionHandle&, const SessionHandle&) = default;
};

struct ObjectHandle {
  std::uint64_t id = 0;
  std::uint64_t session_id = 0;
  std::uint64_t generation = 0;
  friend bool operator==(const ObjectHandle&, const ObjectHandle&) = default;
};

// 供密钥设备中间件调用的会话生命周期组件。
// 会话绑定应用、用户、登录状态与代次；登出、超时、设备重置均使句柄失效。
// 对象句柄按会话隔离，所有校验逐调用进行，内部状态由互斥锁保护，可并发调用。
class SessionManager {
public:
  using NowFn = std::function<TimePoint()>;

  explicit SessionManager(
      std::chrono::milliseconds session_timeout = std::chrono::minutes(15),
      NowFn now_fn = nullptr);
  ~SessionManager();
  SessionManager(const SessionManager&) = delete;
  SessionManager& operator=(const SessionManager&) = delete;

  SessionHandle open_session(AppId app, UserId user);
  SessionError login(const SessionHandle& session, const AppId& app,
                     const std::string& credential);
  SessionError logout(const SessionHandle& session, const AppId& app);

  SessionError create_object(const SessionHandle& session, const AppId& app,
                             std::string label, bool is_private,
                             ObjectHandle& out);
  SessionError destroy_object(const SessionHandle& session, const AppId& app,
                              const ObjectHandle& object);
  SessionError operate(const SessionHandle& session, const AppId& app,
                       const ObjectHandle& object, KeyOperation op);

  // 设备重置：全部会话与对象句柄立即失效。
  void reset();

  std::size_t active_session_count() const;

private:
  struct Impl;
  Impl* impl_;
};

}  // namespace netsec

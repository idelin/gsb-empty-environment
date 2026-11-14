#include "netsec.hpp"

#include <mutex>
#include <unordered_map>
#include <unordered_set>
#include <utility>

namespace netsec {

namespace {

bool requires_login(KeyOperation op) {
  // 私钥操作必须处于登录态；读取公钥不要求登录。
  return op != KeyOperation::get_public;
}

}  // namespace

struct SessionManager::Impl {
  struct SessionState {
    AppId app;
    UserId user;
    std::uint64_t generation = 0;
    bool logged_in = false;
    bool closed = false;
    TimePoint last_activity;
    std::unordered_set<std::uint64_t> objects;
  };

  struct ObjectState {
    std::uint64_t session_id = 0;
    std::uint64_t generation = 0;
    std::string label;
    bool is_private = false;
  };

  explicit Impl(std::chrono::milliseconds timeout, NowFn now_fn)
      : session_timeout(timeout),
        now(now_fn ? std::move(now_fn)
                   : NowFn([] { return std::chrono::steady_clock::now(); })) {}

  // 逐调用校验：存在性、代次、应用归属、关闭/超时状态。
  SessionError validate_session_locked(const SessionHandle& handle,
                                       const AppId& app) {
    auto it = sessions.find(handle.id);
    if (it == sessions.end() || it->second.closed) {
      return SessionError::invalid_session;
    }
    SessionState& state = it->second;
    if (handle.generation != state.generation ||
        handle.generation != device_generation) {
      return SessionError::stale_session;
    }
    if (state.app != app) {
      return SessionError::cross_application;
    }
    const TimePoint current = now();
    if (current - state.last_activity > session_timeout) {
      state.closed = true;  // 超时即刻失效
      return SessionError::session_expired;
    }
    state.last_activity = current;
    return SessionError::ok;
  }

  mutable std::mutex mu;
  std::chrono::milliseconds session_timeout;
  NowFn now;
  std::uint64_t device_generation = 1;
  std::uint64_t next_session_id = 1;
  std::uint64_t next_object_id = 1;
  std::unordered_map<std::uint64_t, SessionState> sessions;
  std::unordered_map<std::uint64_t, ObjectState> objects;
};

SessionManager::SessionManager(std::chrono::milliseconds session_timeout,
                               NowFn now_fn)
    : impl_(new Impl(session_timeout, std::move(now_fn))) {}

SessionManager::~SessionManager() { delete impl_; }

SessionHandle SessionManager::open_session(AppId app, UserId user) {
  std::lock_guard lock(impl_->mu);
  Impl::SessionState state;
  state.app = std::move(app);
  state.user = std::move(user);
  state.generation = impl_->device_generation;
  state.last_activity = impl_->now();
  const std::uint64_t id = impl_->next_session_id++;
  impl_->sessions.emplace(id, std::move(state));
  return SessionHandle{id, impl_->device_generation};
}

SessionError SessionManager::login(const SessionHandle& session,
                                   const AppId& app,
                                   const std::string& credential) {
  std::lock_guard lock(impl_->mu);
  if (SessionError err = impl_->validate_session_locked(session, app);
      err != SessionError::ok) {
    return err;
  }
  if (credential.empty()) {
    return SessionError::permission_denied;
  }
  impl_->sessions.at(session.id).logged_in = true;
  return SessionError::ok;
}

SessionError SessionManager::logout(const SessionHandle& session,
                                    const AppId& app) {
  std::lock_guard lock(impl_->mu);
  if (SessionError err = impl_->validate_session_locked(session, app);
      err != SessionError::ok) {
    return err;
  }
  Impl::SessionState& state = impl_->sessions.at(session.id);
  state.logged_in = false;
  state.closed = true;  // 登出即关闭，句柄此后一律拒绝
  for (std::uint64_t object_id : state.objects) {
    impl_->objects.erase(object_id);
  }
  state.objects.clear();
  return SessionError::ok;
}

SessionError SessionManager::create_object(const SessionHandle& session,
                                           const AppId& app, std::string label,
                                           bool is_private,
                                           ObjectHandle& out) {
  std::lock_guard lock(impl_->mu);
  if (SessionError err = impl_->validate_session_locked(session, app);
      err != SessionError::ok) {
    return err;
  }
  Impl::SessionState& state = impl_->sessions.at(session.id);
  if (!state.logged_in) {
    return SessionError::not_logged_in;
  }
  const std::uint64_t id = impl_->next_object_id++;
  Impl::ObjectState object;
  object.session_id = session.id;
  object.generation = impl_->device_generation;
  object.label = std::move(label);
  object.is_private = is_private;
  impl_->objects.emplace(id, std::move(object));
  state.objects.insert(id);
  out = ObjectHandle{id, session.id, impl_->device_generation};
  return SessionError::ok;
}

SessionError SessionManager::destroy_object(const SessionHandle& session,
                                            const AppId& app,
                                            const ObjectHandle& object) {
  std::lock_guard lock(impl_->mu);
  if (SessionError err = impl_->validate_session_locked(session, app);
      err != SessionError::ok) {
    return err;
  }
  Impl::SessionState& state = impl_->sessions.at(session.id);
  if (!state.logged_in) {
    return SessionError::not_logged_in;
  }
  auto it = impl_->objects.find(object.id);
  if (it == impl_->objects.end() ||
      object.generation != it->second.generation) {
    return SessionError::object_not_found;
  }
  if (it->second.session_id != session.id ||
      object.session_id != session.id) {
    return SessionError::cross_session_object;
  }
  state.objects.erase(object.id);
  impl_->objects.erase(it);
  return SessionError::ok;
}

SessionError SessionManager::operate(const SessionHandle& session,
                                     const AppId& app,
                                     const ObjectHandle& object,
                                     KeyOperation op) {
  std::lock_guard lock(impl_->mu);
  if (SessionError err = impl_->validate_session_locked(session, app);
      err != SessionError::ok) {
    return err;
  }
  auto it = impl_->objects.find(object.id);
  if (it == impl_->objects.end() ||
      object.generation != it->second.generation) {
    return SessionError::object_not_found;
  }
  // 对象句柄按会话隔离：跨会话使用一律拒绝。
  if (it->second.session_id != session.id ||
      object.session_id != session.id) {
    return SessionError::cross_session_object;
  }
  const Impl::SessionState& state = impl_->sessions.at(session.id);
  if (it->second.is_private && requires_login(op) && !state.logged_in) {
    return SessionError::not_logged_in;
  }
  return SessionError::ok;
}

void SessionManager::reset() {
  std::lock_guard lock(impl_->mu);
  ++impl_->device_generation;  // 代次递增，所有旧句柄变为 stale
  impl_->sessions.clear();
  impl_->objects.clear();
}

std::size_t SessionManager::active_session_count() const {
  std::lock_guard lock(impl_->mu);
  std::size_t count = 0;
  for (const auto& [id, state] : impl_->sessions) {
    if (!state.closed) {
      ++count;
    }
  }
  return count;
}

}  // namespace netsec

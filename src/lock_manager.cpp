#include "lockmgr/lock_manager.hpp"

#include <algorithm>
#include <condition_variable>
#include <deque>
#include <filesystem>
#include <stdexcept>
#include <unordered_map>

namespace lockmgr {

namespace detail {
struct TokenAccess {
    static CancelToken::State& state(const CancelToken& t) { return *t.st_; }
};
}  // namespace detail

void CancelToken::cancel() const noexcept {
    std::function<void()> hook;
    {
        std::lock_guard<std::mutex> lk(st_->mu);
        st_->cancelled = true;
        hook = st_->hook;
    }
    if (hook) hook();
}

bool CancelToken::cancelled() const noexcept {
    std::lock_guard<std::mutex> lk(st_->mu);
    return st_->cancelled;
}

struct LockManagerImpl {
    struct Request {
        std::uint64_t session;
        Mode mode;
        bool is_upgrade;
        bool granted = false;
        bool cancelled = false;
        bool dequeued = false;
    };
    struct ResourceState {
        std::deque<std::shared_ptr<Request>> queue;
        std::uint64_t shared_count = 0;
        bool exclusive_held = false;
        bool upgrade_queued = false;
    };
    struct SessionState {
        std::unordered_map<std::string, Mode> held;
        std::size_t queued = 0;  // 该会话在队列中的请求数
        bool closed = false;
    };

    std::mutex mu;
    std::condition_variable cv;
    std::unordered_map<std::string, ResourceState> resources;
    std::unordered_map<std::uint64_t, SessionState> sessions;
    std::uint64_t next_session = 1;
    std::function<void()> park_hook;
};

namespace {

using Impl = LockManagerImpl;
using Request = Impl::Request;
using ResourceState = Impl::ResourceState;

// 严格 FIFO 授予：只扫描队首；已取消的队首请求直接出队。
// 调用方须持有 impl.mu；授予后由调用方负责 notify。
void grant_pass(Impl& im, const std::string& rid, ResourceState& rs) {
    while (!rs.queue.empty()) {
        auto& req = rs.queue.front();
        if (req->cancelled) {
            req->dequeued = true;
            if (req->is_upgrade) rs.upgrade_queued = false;
            im.sessions[req->session].queued--;
            rs.queue.pop_front();
            continue;
        }
        bool can;
        if (req->is_upgrade) {
            // 升级者自身持有唯一共享锁时即可转换
            can = !rs.exclusive_held && rs.shared_count == 1;
        } else if (req->mode == Mode::Shared) {
            can = !rs.exclusive_held;
        } else {
            can = !rs.exclusive_held && rs.shared_count == 0;
        }
        if (!can) break;
        req->granted = true;
        req->dequeued = true;
        rs.queue.pop_front();
        auto& ss = im.sessions[req->session];
        ss.queued--;
        if (req->is_upgrade) {
            rs.upgrade_queued = false;
            rs.shared_count -= 1;
            rs.exclusive_held = true;
            ss.held[rid] = Mode::Exclusive;
        } else if (req->mode == Mode::Shared) {
            rs.shared_count += 1;
            ss.held[rid] = Mode::Shared;
        } else {
            rs.exclusive_held = true;
            ss.held[rid] = Mode::Exclusive;
        }
    }
}

void release_one(Impl& im, Impl::SessionState& ss, const std::string& rid, Mode mode) {
    auto& rs = im.resources[rid];
    if (mode == Mode::Shared) {
        rs.shared_count -= 1;
    } else {
        rs.exclusive_held = false;
    }
    ss.held.erase(rid);
    grant_pass(im, rid, rs);
}

// 阻塞等待 req 被授予或被取消。调用方须持有 impl.mu。
void wait_request(Impl& im, std::unique_lock<std::mutex>& lk,
                  const std::shared_ptr<Request>& req) {
    if (!req->granted && !req->cancelled && im.park_hook) im.park_hook();
    im.cv.wait(lk, [&] { return req->granted || req->cancelled; });
}

// 把已取消请求从队列中移除（若尚未被 grant_pass 出队）。
void dequeue_cancelled(Impl& im, const std::string& rid, ResourceState& rs,
                       const std::shared_ptr<Request>& req) {
    if (req->dequeued) return;
    req->dequeued = true;
    auto it = std::find(rs.queue.begin(), rs.queue.end(), req);
    if (it != rs.queue.end()) rs.queue.erase(it);
    if (req->is_upgrade) rs.upgrade_queued = false;
    im.sessions[req->session].queued--;
    grant_pass(im, rid, rs);
}

Impl::SessionState& live_session(Impl& im, std::uint64_t sid) {
    auto& ss = im.sessions[sid];
    if (ss.closed) throw std::logic_error("lockmgr: session is closed");
    return ss;
}

// 升级主流程。调用方须持有 impl.mu。token 为空表示不可取消（隐式升级）。
// 返回 Upgraded 时持有独占；其余返回值均保留原共享锁。
UpgradeStatus upgrade_locked(Impl& im, std::unique_lock<std::mutex>& lk,
                             std::uint64_t sid, const std::string& rid,
                             const CancelToken* token,
                             const std::shared_ptr<Impl>& impl_keepalive) {
    auto& ss = im.sessions[sid];
    auto it = ss.held.find(rid);
    if (it == ss.held.end()) return UpgradeStatus::NotHeld;
    if (it->second == Mode::Exclusive) return UpgradeStatus::Upgraded;

    auto& rs = im.resources[rid];
    // 已有升级在排队，或队列中已有其它请求（如等待中的写者）时立即
    // 冲突返回：升级者自身持有共享锁，若排在写者之后等待，写者又在
    // 等升级者释放共享锁 —— 双方会互相永久等待。升级只允许注册在
    // 空队列上，之后的等待者都排在自己后面。
    if (rs.upgrade_queued || !rs.queue.empty())
        return UpgradeStatus::Conflict;  // 共享锁保留

    auto req = std::make_shared<Request>(Request{sid, Mode::Exclusive, true});
    rs.queue.push_back(req);
    rs.upgrade_queued = true;
    ss.queued++;

    if (token) {
        auto& ts = detail::TokenAccess::state(*token);
        std::lock_guard<std::mutex> tl(ts.mu);
        std::weak_ptr<Impl> weak_impl = impl_keepalive;
        ts.hook = [weak_impl, req] {
            if (auto keep = weak_impl.lock()) {
                std::lock_guard<std::mutex> g(keep->mu);
                req->cancelled = true;
                keep->cv.notify_all();
            }
        };
        if (ts.cancelled) req->cancelled = true;  // 注册前已取消
    }

    grant_pass(im, rid, rs);
    wait_request(im, lk, req);

    if (token) {
        auto& ts = detail::TokenAccess::state(*token);
        std::lock_guard<std::mutex> tl(ts.mu);
        ts.hook = nullptr;
    }
    if (req->granted) return UpgradeStatus::Upgraded;
    dequeue_cancelled(im, rid, rs, req);
    im.cv.notify_all();
    return UpgradeStatus::Cancelled;  // 共享锁保留
}

void lock_impl(Impl& im, std::uint64_t sid, const std::string& rid, Mode mode,
               const std::shared_ptr<Impl>& impl_keepalive) {
    std::unique_lock<std::mutex> lk(im.mu);
    auto& ss = live_session(im, sid);
    auto it = ss.held.find(rid);
    if (it != ss.held.end()) {
        if (it->second == Mode::Exclusive || mode == Mode::Shared) return;
        // 持有共享、请求独占：隐式升级（不可取消）
        auto status = upgrade_locked(im, lk, sid, rid, nullptr, impl_keepalive);
        if (status == UpgradeStatus::Conflict)
            throw std::runtime_error("lockmgr: implicit upgrade conflicts with pending upgrade");
        if (status == UpgradeStatus::Cancelled)
            throw std::logic_error("lockmgr: session closed while upgrading");
        return;
    }
    auto& rs = im.resources[rid];
    auto req = std::make_shared<Request>(Request{sid, mode, false});
    rs.queue.push_back(req);
    ss.queued++;
    grant_pass(im, rid, rs);
    wait_request(im, lk, req);
    if (!req->granted) {
        // 等待期间会话被其他线程 close()：请求已被取消
        dequeue_cancelled(im, rid, rs, req);
        im.cv.notify_all();
        throw std::logic_error("lockmgr: session closed while waiting");
    }
}

bool try_lock_impl(Impl& im, std::uint64_t sid, const std::string& rid, Mode mode) {
    std::unique_lock<std::mutex> lk(im.mu);
    auto& ss = live_session(im, sid);
    auto it = ss.held.find(rid);
    if (it != ss.held.end())
        return it->second == Mode::Exclusive || mode == Mode::Shared;
    auto& rs = im.resources[rid];
    auto req = std::make_shared<Request>(Request{sid, mode, false});
    rs.queue.push_back(req);
    ss.queued++;
    grant_pass(im, rid, rs);
    if (req->granted) return true;
    dequeue_cancelled(im, rid, rs, req);  // 未授予：直接移出队列
    return false;
}

}  // namespace

LockManager::ResourceId LockManager::canonicalize(const std::string& raw) {
    namespace fs = std::filesystem;
    std::string s = fs::path(raw).lexically_normal().generic_string();
    if (s.empty()) s = "/";
    while (s.size() > 1 && s.back() == '/') s.pop_back();
    return s;
}

LockManager::LockManager() : impl_(std::make_shared<LockManagerImpl>()) {}
LockManager::~LockManager() = default;

LockManager::Session LockManager::open_session() {
    std::lock_guard<std::mutex> lk(impl_->mu);
    std::uint64_t id = impl_->next_session++;
    impl_->sessions[id];  // 默认构造空会话
    return Session(impl_, id);
}

void LockManager::set_park_hook(std::function<void()> hook) {
    std::lock_guard<std::mutex> lk(impl_->mu);
    impl_->park_hook = std::move(hook);
}

std::size_t LockManager::shared_holders(const std::string& resource) const {
    std::lock_guard<std::mutex> lk(impl_->mu);
    auto it = impl_->resources.find(canonicalize(resource));
    return it == impl_->resources.end() ? 0 : it->second.shared_count;
}

bool LockManager::exclusive_held(const std::string& resource) const {
    std::lock_guard<std::mutex> lk(impl_->mu);
    auto it = impl_->resources.find(canonicalize(resource));
    return it != impl_->resources.end() && it->second.exclusive_held;
}

std::size_t LockManager::queued_requests(const std::string& resource) const {
    std::lock_guard<std::mutex> lk(impl_->mu);
    auto it = impl_->resources.find(canonicalize(resource));
    return it == impl_->resources.end() ? 0 : it->second.queue.size();
}

// ---------------- Session ----------------

LockManager::Session::Session(std::shared_ptr<LockManagerImpl> impl, std::uint64_t id)
    : impl_(std::move(impl)), id_(id) {}

LockManager::Session::Session(Session&& o) noexcept
    : impl_(std::move(o.impl_)), id_(o.id_) {
    o.id_ = 0;
}

LockManager::Session& LockManager::Session::operator=(Session&& o) noexcept {
    if (this != &o) {
        close();
        impl_ = std::move(o.impl_);
        id_ = o.id_;
        o.id_ = 0;
    }
    return *this;
}

LockManager::Session::~Session() { close(); }

void LockManager::Session::lock_shared(const std::string& resource) {
    lock_impl(*impl_, id_, LockManager::canonicalize(resource), Mode::Shared, impl_);
}

void LockManager::Session::lock_exclusive(const std::string& resource) {
    lock_impl(*impl_, id_, LockManager::canonicalize(resource), Mode::Exclusive, impl_);
}

bool LockManager::Session::try_lock_shared(const std::string& resource) {
    return try_lock_impl(*impl_, id_, LockManager::canonicalize(resource), Mode::Shared);
}

bool LockManager::Session::try_lock_exclusive(const std::string& resource) {
    return try_lock_impl(*impl_, id_, LockManager::canonicalize(resource), Mode::Exclusive);
}

UpgradeStatus LockManager::Session::upgrade(const std::string& resource,
                                            const CancelToken& token) {
    std::unique_lock<std::mutex> lk(impl_->mu);
    live_session(*impl_, id_);
    return upgrade_locked(*impl_, lk, id_, LockManager::canonicalize(resource),
                          &token, impl_);
}

bool LockManager::Session::unlock(const std::string& resource) {
    std::lock_guard<std::mutex> lk(impl_->mu);
    auto sit = impl_->sessions.find(id_);
    if (sit == impl_->sessions.end() || sit->second.closed) return false;
    auto rid = LockManager::canonicalize(resource);
    auto it = sit->second.held.find(rid);
    if (it == sit->second.held.end()) return false;  // 重复释放：无操作
    release_one(*impl_, sit->second, rid, it->second);
    impl_->cv.notify_all();
    return true;
}

void LockManager::Session::close() noexcept {
    if (!impl_ || id_ == 0) return;
    {
        std::lock_guard<std::mutex> lk(impl_->mu);
        auto& ss = impl_->sessions[id_];
        if (!ss.closed) {
            ss.closed = true;
            // 取消该会话所有排队中的请求（如其他线程正阻塞在 upgrade）
            for (auto& kv : impl_->resources) {
                for (auto& req : kv.second.queue) {
                    if (req->session == id_) req->cancelled = true;
                }
            }
            // 释放全部持有
            auto held = std::move(ss.held);
            ss.held.clear();
            for (auto& kv : held) release_one(*impl_, ss, kv.first, kv.second);
            impl_->cv.notify_all();
        }
    }
    id_ = 0;
}

bool LockManager::Session::holds(const std::string& resource, Mode* mode) const {
    std::lock_guard<std::mutex> lk(impl_->mu);
    auto sit = impl_->sessions.find(id_);
    if (sit == impl_->sessions.end() || sit->second.closed) return false;
    auto it = sit->second.held.find(LockManager::canonicalize(resource));
    if (it == sit->second.held.end()) return false;
    if (mode) *mode = it->second;
    return true;
}

bool LockManager::Session::valid() const noexcept { return impl_ != nullptr && id_ != 0; }

}  // namespace lockmgr

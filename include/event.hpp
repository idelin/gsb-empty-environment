#pragma once

// ev::Event — 快照分发型事件监听库 (C++17)
//
// 语义:
//  * fire() 开始时对监听器集合取快照, 之后在无全局锁的情况下逐个回调。
//  * unsubscribe() 返回后, 该监听器不会进入任何新一轮分发;
//    本轮已被准入(已开始)的调用可以正常结束。
//  * Subscription 是可等待的注销句柄: wait() 阻塞至在途调用全部结束,
//    之后可安全销毁回调捕获的资源。
//  * 监听器可以在自己的回调里注销自身(不会自死锁)。

#include <atomic>
#include <condition_variable>
#include <functional>
#include <memory>
#include <mutex>
#include <utility>
#include <vector>

namespace ev {

namespace detail {
// 当前线程正在执行的监听器状态(用于检测"回调内注销自身")。
// 分发嵌套(回调里再 fire 其它事件)时由调用方保存/恢复旧值。
inline thread_local const void* tls_active_state = nullptr;
} // namespace detail

template <typename... Args>
class Event;

template <typename... Args>
class Subscription {
public:
    Subscription() = default;
    ~Subscription() { unsubscribe_and_wait(); }

    Subscription(const Subscription&) = delete;
    Subscription& operator=(const Subscription&) = delete;

    Subscription(Subscription&& other) noexcept
        : state_(std::move(other.state_)) {}

    Subscription& operator=(Subscription&& other) noexcept {
        if (this != &other) {
            unsubscribe_and_wait();
            state_ = std::move(other.state_);
        }
        return *this;
    }

    // 标记注销。返回后, 该监听器不会再进入新一轮分发。
    // 不阻塞; 在途调用继续执行直至结束。
    void unsubscribe() {
        if (state_) {
            state_->removed.store(true, std::memory_order_release);
        }
    }

    // 等待该监听器的所有在途调用结束。
    // 若从该监听器自身的回调中调用, 立即返回(否则必然自死锁)。
    void wait() {
        auto state = state_;
        if (!state) return;
        if (detail::tls_active_state == state.get()) return; // 自注销: 本轮调用自然结束
        std::unique_lock<std::mutex> lk(state->m);
        state->cv.wait(lk, [&] { return state->in_flight == 0; });
    }

    // 注销并等待在途调用全部结束。返回后,
    // 回调不会再被触发, 捕获的资源可安全销毁。
    void unsubscribe_and_wait() {
        unsubscribe();
        wait();
    }

    // 是否仍可能收到新调用(未注销)。
    bool active() const {
        return state_ && !state_->removed.load(std::memory_order_acquire);
    }

    explicit operator bool() const { return active(); }

    // 解除句柄与监听器的关联(不注销)。主要用于自注销场景:
    // 在回调内 release 后, 句柄析构不再触碰状态。
    void release() { state_.reset(); }

private:
    struct State {
        std::function<void(Args...)> fn;
        std::atomic<bool> removed{false};
        std::mutex m;
        std::condition_variable cv;
        int in_flight = 0; // 由 m 保护: 已准入但未结束的调用数
    };

    explicit Subscription(std::shared_ptr<State> state) : state_(std::move(state)) {}

    std::shared_ptr<State> state_;

    friend class Event<Args...>;
};

template <typename... Args>
class Event {
    using State = typename Subscription<Args...>::State;

public:
    Event() = default;
    ~Event() = default;

    Event(const Event&) = delete;
    Event& operator=(const Event&) = delete;

    // 注册监听器, 返回可等待的注销句柄。
    Subscription<Args...> subscribe(std::function<void(Args...)> fn) {
        auto state = std::make_shared<State>();
        state->fn = std::move(fn);
        std::lock_guard<std::mutex> lk(m_);
        purge_locked();
        states_.push_back(state);
        return Subscription<Args...>(std::move(state));
    }

    // 快照分发: 先在锁内拷贝存活监听器, 再在锁外逐个回调。
    // 回调期间不持有任何锁, 监听器可自由订阅/注销(包括自身)。
    void fire(const Args&... args) {
        std::vector<std::shared_ptr<State>> snapshot;
        {
            std::lock_guard<std::mutex> lk(m_);
            purge_locked();
            snapshot.reserve(states_.size());
            for (const auto& s : states_) {
                if (!s->removed.load(std::memory_order_acquire)) {
                    snapshot.push_back(s);
                }
            }
        }

        for (const auto& s : snapshot) {
            // 准入检查与计数在同一临界区: unsubscribe 的标记
            // 要么先于检查(跳过), 要么晚于计数(本轮跑完, wait 会等)。
            {
                std::lock_guard<std::mutex> lk(s->m);
                if (s->removed.load(std::memory_order_acquire)) continue;
                ++s->in_flight;
            }

            const void* prev = detail::tls_active_state;
            detail::tls_active_state = s.get();
            try {
                s->fn(args...);
            } catch (...) {
                detail::tls_active_state = prev;
                finish(*s);
                throw;
            }
            detail::tls_active_state = prev;
            finish(*s);
        }
    }

    // 当前存活(未注销)的监听器数量。
    std::size_t size() const {
        std::lock_guard<std::mutex> lk(m_);
        std::size_t n = 0;
        for (const auto& s : states_) {
            if (!s->removed.load(std::memory_order_acquire)) ++n;
        }
        return n;
    }

private:
    static void finish(State& s) {
        {
            std::lock_guard<std::mutex> lk(s.m);
            --s.in_flight;
        }
        s.cv.notify_all();
    }

    // 惰性清理已注销且无常驻引用的状态。
    void purge_locked() {
        for (auto it = states_.begin(); it != states_.end();) {
            if ((*it)->removed.load(std::memory_order_acquire)) {
                it = states_.erase(it);
            } else {
                ++it;
            }
        }
    }

    mutable std::mutex m_;
    std::vector<std::shared_ptr<State>> states_;
};

} // namespace ev

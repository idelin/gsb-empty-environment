#pragma once

// Snapshot-dispatched event listener library (C++17).
//
// Semantics:
//  - emit() dispatches on a snapshot of the listener collection taken at the
//    start of the call. No lock is held while listeners run, so listeners may
//    subscribe / unsubscribe / emit re-entrantly.
//  - After subscription::unsubscribe() returns, the listener is never entered
//    by a new emit round; invocations already in flight may run to completion.
//  - subscription::wait() blocks until in-flight invocations of the listener
//    finish. Called from within the listener's own callback it does not wait
//    on the current thread (self-unsubscription never deadlocks).
//  - The subscription destructor performs unsubscribe() + wait(), so captured
//    state is never destroyed while its listener is still executing.

#include <atomic>
#include <condition_variable>
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <shared_mutex>
#include <vector>

namespace evlib {

namespace detail {

struct listener_state {
    std::atomic<bool> removed{false};
    std::atomic<long> in_flight{0};
    std::mutex mu;
    std::condition_variable cv;
    virtual ~listener_state() = default;
};

struct registry {
    std::shared_mutex mu;
    // Ordered by id so dispatch order is subscription order.
    std::map<std::uint64_t, std::shared_ptr<listener_state>> listeners;
    std::uint64_t next_id = 0; // guarded by mu
};

// Stack of listener states currently executing on this thread. Used by
// subscription::wait() to avoid waiting on itself.
inline thread_local std::vector<const listener_state*> tls_active;

inline void release_in_flight(const std::shared_ptr<listener_state>& st) {
    if (st->in_flight.fetch_sub(1, std::memory_order_seq_cst) == 1) {
        // Lock before notifying so a waiter cannot miss the wakeup.
        std::lock_guard<std::mutex> lk(st->mu);
        st->cv.notify_all();
    }
}

} // namespace detail

// Awaitable unsubscription handle. Const member functions only touch the
// shared subscription state, so unsubscribe()/wait() may be called through a
// handle shared with a running callback.
class subscription {
public:
    subscription() = default;
    ~subscription() { reset(); }

    subscription(subscription&& other) noexcept { move_from(other); }
    subscription& operator=(subscription&& other) noexcept {
        if (this != &other) {
            reset();
            move_from(other);
        }
        return *this;
    }
    subscription(const subscription&) = delete;
    subscription& operator=(const subscription&) = delete;

    bool valid() const noexcept { return static_cast<bool>(state_); }
    explicit operator bool() const noexcept { return valid(); }

    // Removes the listener from the event. After this returns the listener
    // is not entered by any new emit round; invocations already in flight
    // may finish. Idempotent and safe to call from the listener itself.
    void unsubscribe() const {
        const auto st = state_;
        if (!st)
            return;
        bool expected = false;
        if (st->removed.compare_exchange_strong(expected, true,
                                                std::memory_order_seq_cst)) {
            if (const auto reg = registry_.lock()) {
                std::unique_lock<std::shared_mutex> lk(reg->mu);
                reg->listeners.erase(id_);
            }
        }
    }

    // Blocks until no in-flight invocation of the listener remains. If called
    // from within the listener's own callback, the current thread's own
    // (possibly nested) invocations are not waited on.
    void wait() const {
        const auto st = state_;
        if (!st)
            return;
        long self = 0;
        for (const auto* active : detail::tls_active)
            if (active == st.get())
                ++self;
        std::unique_lock<std::mutex> lk(st->mu);
        st->cv.wait(lk, [&] {
            return st->in_flight.load(std::memory_order_seq_cst) <= self;
        });
    }

    // unsubscribe() + wait(), then releases the handle.
    void reset() {
        unsubscribe();
        wait();
        state_.reset();
        registry_.reset();
        id_ = 0;
    }

private:
    template <typename...>
    friend class event;

    subscription(std::weak_ptr<detail::registry> reg,
                 std::shared_ptr<detail::listener_state> st, std::uint64_t id)
        : registry_(std::move(reg)), state_(std::move(st)), id_(id) {}

    void move_from(subscription& other) noexcept {
        registry_ = std::move(other.registry_);
        state_ = std::move(other.state_);
        id_ = other.id_;
        other.id_ = 0;
    }

    std::weak_ptr<detail::registry> registry_;
    std::shared_ptr<detail::listener_state> state_;
    std::uint64_t id_ = 0;
};

template <typename... Args>
class event {
public:
    using listener = std::function<void(const Args&...)>;

    event() : reg_(std::make_shared<detail::registry>()) {}
    event(const event&) = delete;
    event& operator=(const event&) = delete;

    subscription subscribe(listener fn) {
        auto st = std::make_shared<typed_state>();
        st->fn = std::move(fn);
        std::uint64_t id;
        {
            std::unique_lock<std::shared_mutex> lk(reg_->mu);
            id = reg_->next_id++;
            reg_->listeners.emplace(id, st);
        }
        return subscription(reg_, std::move(st), id);
    }

    // Dispatches on a snapshot of the listener collection. A listener removed
    // before its turn (even mid-round) is skipped; one already running is
    // allowed to finish. A throwing listener propagates after cleanup and
    // skips the remaining listeners.
    void emit(const Args&... args) {
        std::vector<std::shared_ptr<detail::listener_state>> snapshot;
        {
            std::shared_lock<std::shared_mutex> lk(reg_->mu);
            snapshot.reserve(reg_->listeners.size());
            for (const auto& kv : reg_->listeners)
                snapshot.push_back(kv.second);
        }
        for (const auto& st : snapshot) {
            // Seq-cst protocol with unsubscribe(): whichever of fetch_add /
            // removed-store comes first in the total order decides whether
            // this invocation happens, so wait() can never return while an
            // invocation of this listener may still start.
            st->in_flight.fetch_add(1, std::memory_order_seq_cst);
            if (st->removed.load(std::memory_order_seq_cst)) {
                detail::release_in_flight(st);
                continue;
            }
            struct invocation_guard {
                const std::shared_ptr<detail::listener_state>& st;
                explicit invocation_guard(
                    const std::shared_ptr<detail::listener_state>& s)
                    : st(s) {
                    detail::tls_active.push_back(st.get());
                }
                ~invocation_guard() {
                    detail::tls_active.pop_back();
                    detail::release_in_flight(st);
                }
            } guard(st);
            static_cast<typed_state*>(st.get())->fn(args...);
        }
    }

    // Removes all listeners; in-flight invocations may finish. Handles keep
    // working and can still be wait()ed on.
    void clear() {
        std::unique_lock<std::shared_mutex> lk(reg_->mu);
        for (auto& kv : reg_->listeners)
            kv.second->removed.store(true, std::memory_order_seq_cst);
        reg_->listeners.clear();
    }

private:
    struct typed_state : detail::listener_state {
        listener fn;
    };

    std::shared_ptr<detail::registry> reg_;
};

} // namespace evlib

#pragma once

#include <cstddef>
#include <deque>
#include <functional>
#include <mutex>

// A single-threaded, manually stepped executor.
// Tasks are posted from any thread and run only when the owning thread
// calls run_one()/run_all()/shutdown().
class ManualExecutor {
public:
    ManualExecutor() = default;
    ManualExecutor(const ManualExecutor&) = delete;
    ManualExecutor& operator=(const ManualExecutor&) = delete;

    // Enqueue a task. Returns false (task rejected) after shutdown.
    bool post(std::function<void()> fn) {
        std::lock_guard<std::mutex> lk(mu_);
        if (shutdown_) return false;
        queue_.push_back(std::move(fn));
        return true;
    }

    // Run at most one queued task. Returns true if a task ran.
    bool run_one() {
        std::function<void()> fn;
        {
            std::lock_guard<std::mutex> lk(mu_);
            if (queue_.empty()) return false;
            fn = std::move(queue_.front());
            queue_.pop_front();
        }
        fn();
        return true;
    }

    // Drain every currently queued task. Returns the number run.
    std::size_t run_all() {
        std::size_t n = 0;
        while (run_one()) ++n;
        return n;
    }

    std::size_t pending() const {
        std::lock_guard<std::mutex> lk(mu_);
        return queue_.size();
    }

    bool is_shutdown() const {
        std::lock_guard<std::mutex> lk(mu_);
        return shutdown_;
    }

    // Stop accepting new tasks, then drain all remaining tasks before
    // returning. Tasks posted concurrently after the cutoff are rejected
    // (post() returns false) so callers can apply their fallback.
    void shutdown() {
        {
            std::lock_guard<std::mutex> lk(mu_);
            shutdown_ = true;
        }
        run_all();
    }

private:
    mutable std::mutex mu_;
    std::deque<std::function<void()>> queue_;
    bool shutdown_ = false;
};

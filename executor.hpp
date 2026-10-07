#pragma once

#include <chrono>
#include <condition_variable>
#include <cstddef>
#include <functional>
#include <mutex>
#include <queue>
#include <stdexcept>
#include <thread>

namespace resman {

// Single-threaded, manually pumped executor.
//
// Tasks are enqueued from any thread via post()/post_cleanup() and run only
// when the owner thread calls step()/step_all()/shutdown(). shutdown()
// blocks until every registered resource has been cleaned up exactly once,
// so an executor is always fully drained before it is destroyed.
class Executor {
public:
    using Task = std::function<void()>;

    explicit Executor(std::thread::id owner = std::this_thread::get_id()) : owner_(owner) {}

    Executor(const Executor&) = delete;
    Executor& operator=(const Executor&) = delete;

    // Callers must have invoked shutdown() (and therefore drained all
    // cleanup work) before destroying the executor.
    ~Executor() = default;

    std::thread::id owner_thread() const { return owner_; }

    bool is_shutdown() const {
        std::lock_guard<std::mutex> lk(mu_);
        return shutdown_;
    }

    std::size_t outstanding_resources() const {
        std::lock_guard<std::mutex> lk(mu_);
        return outstanding_;
    }

    std::size_t pending_tasks() const {
        std::lock_guard<std::mutex> lk(mu_);
        return queue_.size();
    }

    // Registers a live resource. Called by Resource on construction.
    // Throws std::runtime_error once the executor is shut down.
    void register_resource() {
        std::lock_guard<std::mutex> lk(mu_);
        if (shutdown_) throw std::runtime_error("cannot create resource: executor is shut down");
        ++outstanding_;
    }

    // Posts a cleanup task. May be called from any thread, including while
    // shutdown() is draining. The task is guaranteed to run on the owner
    // thread before shutdown() returns.
    void post_cleanup(Task task) {
        enqueue([this, task = std::move(task)]() mutable {
            task();
            {
                std::lock_guard<std::mutex> lk(mu_);
                --outstanding_;
            }
            cv_.notify_all();
        });
    }

    // Posts a plain task (not tracked as resource cleanup).
    void post(Task task) { enqueue(std::move(task)); }

    // Runs up to `max` queued tasks on the caller. Owner thread only.
    std::size_t step(std::size_t max = 1) {
        check_owner();
        std::size_t ran = 0;
        while (ran < max) {
            Task task;
            {
                std::lock_guard<std::mutex> lk(mu_);
                if (queue_.empty()) break;
                task = std::move(queue_.front());
                queue_.pop();
            }
            task();
            ++ran;
        }
        return ran;
    }

    std::size_t step_all() { return step(~std::size_t{0}); }

    // Blocks the owner thread until work is available or the timeout elapses.
    template <class Rep, class Period>
    void wait_for_work(std::chrono::duration<Rep, Period> timeout) {
        check_owner();
        std::unique_lock<std::mutex> lk(mu_);
        cv_.wait_for(lk, timeout, [&] { return !queue_.empty(); });
    }

    // Drains all cleanup work, returning only after every resource
    // registered on this executor has been cleaned up exactly once.
    // Owner thread only.
    void shutdown() {
        check_owner();
        {
            std::lock_guard<std::mutex> lk(mu_);
            shutdown_ = true;
        }
        cv_.notify_all();
        for (;;) {
            Task task;
            {
                std::unique_lock<std::mutex> lk(mu_);
                cv_.wait(lk, [&] { return !queue_.empty() || outstanding_ == 0; });
                if (queue_.empty()) return;  // implies outstanding_ == 0
                task = std::move(queue_.front());
                queue_.pop();
            }
            task();
        }
    }

private:
    void enqueue(Task task) {
        {
            std::lock_guard<std::mutex> lk(mu_);
            queue_.push(std::move(task));
        }
        cv_.notify_all();
    }

    void check_owner() const {
        if (std::this_thread::get_id() != owner_)
            throw std::logic_error("executor: called from a non-owner thread");
    }

    const std::thread::id owner_;
    mutable std::mutex mu_;
    std::condition_variable cv_;
    std::queue<Task> queue_;
    std::size_t outstanding_ = 0;
    bool shutdown_ = false;
};

}  // namespace resman

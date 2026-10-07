#pragma once

#include <functional>
#include <memory>
#include <thread>
#include <utility>

#include "executor.hpp"

namespace resman {

// Handle to a resource owned by an Executor.
//
// Handles are copyable/movable and may travel freely across threads, but the
// underlying resource obeys thread affinity: construction registers with the
// owning executor (failing once it is shut down), and when the last handle
// is released the cleanup callback is posted back to the owning executor and
// runs exactly once on its owner thread.
class Resource {
public:
    using CleanupCallback = std::function<void(std::thread::id /*cleanup thread*/)>;

    Resource(Executor& executor, CleanupCallback on_cleanup) {
        auto* raw = new State{std::move(on_cleanup)};
        try {
            executor.register_resource();  // throws if the executor is shut down
        } catch (...) {
            delete raw;
            throw;
        }
        executor_ = &executor;
        Executor* ex = &executor;
        state_ = std::shared_ptr<State>(raw, [ex](State* state) {
            // Runs on whichever thread releases the last handle; the actual
            // cleanup is deferred to the owning executor's owner thread.
            auto on_cleanup = std::move(state->on_cleanup);
            delete state;
            ex->post_cleanup([on_cleanup = std::move(on_cleanup)]() mutable {
                on_cleanup(std::this_thread::get_id());
            });
        });
    }

    Resource(Resource&&) noexcept = default;
    Resource& operator=(Resource&&) noexcept = default;
    Resource(const Resource&) = default;
    Resource& operator=(const Resource&) = default;

    Executor& executor() { return *executor_; }
    const Executor& executor() const { return *executor_; }

private:
    struct State {
        CleanupCallback on_cleanup;
    };

    std::shared_ptr<State> state_;
    Executor* executor_ = nullptr;  // kept for executor(); the deleter owns the posting
};

}  // namespace resman

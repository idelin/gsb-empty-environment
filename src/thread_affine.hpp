#pragma once

#include <stdexcept>
#include <thread>
#include <utility>

#include "manual_executor.hpp"

// Thread-affine resource factory.
//
//  - Resources may only be destroyed on the thread that created them:
//    releasing a handle never destroys inline, it posts the cleanup back
//    to the executor that owns the creating thread.
//  - Handles are move-only and may freely move across threads.
//  - Cleanup runs exactly once per resource (unique ownership + move).
//  - create() throws once the owning executor has shut down.
//  - If a handle outlives its executor, reset() falls back to inline
//    destruction (the owning thread is gone; still exactly once).
template <typename T>
class ThreadAffine {
public:
    class Handle {
    public:
        Handle() = default;
        Handle(const Handle&) = delete;
        Handle& operator=(const Handle&) = delete;

        Handle(Handle&& other) noexcept : ctrl_(std::exchange(other.ctrl_, nullptr)) {}

        Handle& operator=(Handle&& other) noexcept {
            if (this != &other) {
                reset();
                ctrl_ = std::exchange(other.ctrl_, nullptr);
            }
            return *this;
        }

        ~Handle() { reset(); }

        T& get() { return ctrl_->value; }
        const T& get() const { return ctrl_->value; }
        explicit operator bool() const { return ctrl_ != nullptr; }

        // Release the resource. The actual destruction is posted to the
        // owning executor and runs when that executor is stepped.
        void reset() {
            Control* c = std::exchange(ctrl_, nullptr);
            if (!c) return;
            if (!c->executor->post([c] { destroy(c); })) {
                destroy(c);  // executor already shut down: inline fallback
            }
        }

    private:
        friend class ThreadAffine;

        struct Control {
            ManualExecutor* executor;
            std::thread::id owner;
            T value;
        };

        explicit Handle(Control* c) : ctrl_(c) {}

        static void destroy(Control* c) { delete c; }

        Control* ctrl_ = nullptr;
    };

    explicit ThreadAffine(ManualExecutor& executor) : executor_(&executor) {}

    template <typename... Args>
    Handle create(Args&&... args) {
        if (executor_->is_shutdown()) {
            throw std::runtime_error("ThreadAffine: executor shut down, cannot create resource");
        }
        auto* c = new typename Handle::Control{
            executor_, std::this_thread::get_id(), T(std::forward<Args>(args)...)};
        return Handle(c);
    }

private:
    ManualExecutor* executor_;
};

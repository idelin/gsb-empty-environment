#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <mutex>
#include <utility>
#include <vector>

namespace drc {

// Epoch-based deferred reclaimer.
//
// Objects are first marked retired and pushed onto a limbo list stamped with
// the current epoch. They are destroyed only after the global epoch has
// advanced twice past the retire epoch, which guarantees that every thread
// that could have observed the object has left its read-side critical
// section. Reclamation happens synchronously inside retire_fn()/collect()
// on the calling thread; no background thread is ever spawned.
class EpochReclaimer {
    struct ThreadState;

public:
    EpochReclaimer() = default;
    EpochReclaimer(const EpochReclaimer&) = delete;
    EpochReclaimer& operator=(const EpochReclaimer&) = delete;

    ~EpochReclaimer() {
        // Reclaim whatever is left and detach thread registrations so the
        // thread-local Registration destructors (which run at thread exit)
        // never touch a dead reclaimer.
        for (auto& bag : limbo_) {
            for (auto& fn : bag) fn();
            bag.clear();
        }
        std::lock_guard<std::mutex> lock(registry_mutex_);
        for (ThreadState* state = registry_; state != nullptr; state = state->next) {
            state->orphaned = true;
        }
        registry_ = nullptr;
    }

    // Leaked-on-purpose process-wide reclaimer, safe against static
    // destruction order issues.
    static EpochReclaimer& global() {
        static EpochReclaimer* instance = new EpochReclaimer();
        return *instance;
    }

    // RAII epoch pin: while engaged, the thread blocks reclamation of the
    // epoch it observed. Move-only, so a critical section has one owner.
    class Pin {
    public:
        Pin() noexcept = default;
        Pin(const Pin&) = delete;
        Pin& operator=(const Pin&) = delete;

        Pin(Pin&& other) noexcept : state_(std::exchange(other.state_, nullptr)) {}

        Pin& operator=(Pin&& other) noexcept {
            if (this != &other) {
                reset();
                state_ = std::exchange(other.state_, nullptr);
            }
            return *this;
        }

        ~Pin() { reset(); }

        void reset() noexcept {
            if (state_) {
                state_->epoch.store(kUnpinned, std::memory_order_release);
                state_ = nullptr;
            }
        }

        bool engaged() const noexcept { return state_ != nullptr; }

    private:
        friend class EpochReclaimer;
        explicit Pin(ThreadState* state) : state_(state) {}
        ThreadState* state_ = nullptr;
    };

    // Enter a read-side critical section.
    Pin pin() {
        ThreadState* state = local_state();
        std::uint64_t epoch = global_epoch_.load(std::memory_order_relaxed);
        state->epoch.store(epoch, std::memory_order_seq_cst);
        // If the global epoch moved while we were publishing our pin,
        // re-publish the fresher value so we never sit on a stale epoch.
        std::uint64_t current = global_epoch_.load(std::memory_order_relaxed);
        if (current != epoch) {
            state->epoch.store(current, std::memory_order_seq_cst);
        }
        return Pin(state);
    }

    // Defer `delete ptr` until every reader that could have seen it finished.
    template <typename T>
    void retire(T* ptr) {
        retire_fn([ptr] { delete ptr; });
    }

    void retire_fn(std::function<void()> reclaim) {
        std::uint64_t epoch = global_epoch_.load(std::memory_order_acquire);
        {
            std::lock_guard<std::mutex> lock(limbo_mutex_);
            limbo_[epoch % kLimboCount].push_back(std::move(reclaim));
        }
        collect();
    }

    // Try to advance the global epoch by one and reclaim the limbo list that
    // became unreachable. A no-op while any thread lags behind.
    void collect() {
        std::uint64_t epoch = global_epoch_.load(std::memory_order_acquire);
        {
            std::lock_guard<std::mutex> lock(registry_mutex_);
            for (ThreadState* state = registry_; state != nullptr; state = state->next) {
                std::uint64_t local = state->epoch.load(std::memory_order_acquire);
                if (local != kUnpinned && local != epoch) {
                    return; // a reader still pins an older epoch
                }
            }
        }
        std::uint64_t expected = epoch;
        if (global_epoch_.compare_exchange_strong(expected, epoch + 1,
                                                  std::memory_order_acq_rel)) {
            // Everyone is now at `epoch` or `epoch + 1`; nobody can still
            // hold pointers retired during epoch `epoch - 1` (== epoch + 2
            // mod 3).
            reclaim_limbo((epoch + 2) % kLimboCount);
        }
    }

    // Number of retired-but-not-yet-reclaimed callbacks (for tests).
    std::size_t pending() const {
        std::lock_guard<std::mutex> lock(limbo_mutex_);
        std::size_t total = 0;
        for (const auto& bag : limbo_) total += bag.size();
        return total;
    }

    std::uint64_t global_epoch() const {
        return global_epoch_.load(std::memory_order_acquire);
    }

private:
    static constexpr std::uint64_t kUnpinned = ~std::uint64_t{0};
    static constexpr std::size_t kLimboCount = 3;

    struct ThreadState {
        std::atomic<std::uint64_t> epoch{kUnpinned};
        ThreadState* next = nullptr;
        bool orphaned = false; // set when the reclaimer dies first
    };

    ThreadState* local_state() {
        thread_local struct Registration {
            EpochReclaimer* owner;
            ThreadState state;
            explicit Registration(EpochReclaimer* reclaimer) : owner(reclaimer) {
                std::lock_guard<std::mutex> lock(owner->registry_mutex_);
                state.next = owner->registry_;
                owner->registry_ = &state;
            }
            ~Registration() {
                if (state.orphaned) return;
                std::lock_guard<std::mutex> lock(owner->registry_mutex_);
                ThreadState** link = &owner->registry_;
                while (*link != &state) link = &(*link)->next;
                *link = state.next;
            }
        } registration(this);
        return &registration.state;
    }

    void reclaim_limbo(std::size_t index) {
        std::vector<std::function<void()>> doomed;
        {
            std::lock_guard<std::mutex> lock(limbo_mutex_);
            doomed.swap(limbo_[index]);
        }
        for (auto& fn : doomed) fn();
    }

    std::atomic<std::uint64_t> global_epoch_{0};
    std::mutex registry_mutex_;
    ThreadState* registry_ = nullptr;
    mutable std::mutex limbo_mutex_;
    std::vector<std::function<void()>> limbo_[kLimboCount];
};

} // namespace drc

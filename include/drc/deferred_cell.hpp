#pragma once

#include <atomic>
#include <cstdint>
#include <functional>
#include <optional>
#include <utility>

#include "epoch_reclaimer.hpp"

namespace drc {

template <typename T>
class DeferredCell;
template <typename T>
class Handle;
template <typename T>
class ReaderGuard;
template <typename T>
std::optional<ReaderGuard<T>> try_read(const Handle<T>& handle);

// Read guard: keeps the epoch pinned (and therefore the object alive) until
// released. Move-only — reads have exactly one owner; copies are forbidden.
template <typename T>
class ReaderGuard {
public:
    ReaderGuard() noexcept = default;
    ReaderGuard(const ReaderGuard&) = delete;
    ReaderGuard& operator=(const ReaderGuard&) = delete;
    ReaderGuard(ReaderGuard&&) noexcept = default;
    ReaderGuard& operator=(ReaderGuard&&) noexcept = default;

    const T& operator*() const noexcept { return *ptr_; }
    const T* operator->() const noexcept { return ptr_; }
    const T* get() const noexcept { return ptr_; }
    explicit operator bool() const noexcept { return ptr_ != nullptr; }

private:
    friend std::optional<ReaderGuard<T>> try_read<>(const Handle<T>&);
    ReaderGuard(EpochReclaimer::Pin pin, const T* ptr)
        : pin_(std::move(pin)), ptr_(ptr) {}

    EpochReclaimer::Pin pin_;
    const T* ptr_ = nullptr;
};

// Copyable snapshot token. It names a (cell, generation) pair and becomes
// permanently invalid once the cell is retired or republished, so a stale
// handle can never observe a newer object — even one placed at the same
// address.
template <typename T>
class Handle {
public:
    Handle() = default;

private:
    friend class DeferredCell<T>;
    friend std::optional<ReaderGuard<T>> try_read<>(const Handle<T>&);
    Handle(const DeferredCell<T>* cell, std::uint64_t generation)
        : cell_(cell), generation_(generation) {}

    const DeferredCell<T>* cell_ = nullptr;
    std::uint64_t generation_ = 0;
};

// Owns the currently published object. retire() unlinks it and defers its
// destruction to the reclaimer; afterwards no handle can yield a new guard.
template <typename T>
class DeferredCell {
public:
    using Deleter = std::function<void(T*)>;

    explicit DeferredCell(EpochReclaimer& reclaimer)
        : DeferredCell(reclaimer, [](T* ptr) { delete ptr; }) {}

    DeferredCell(EpochReclaimer& reclaimer, Deleter deleter)
        : reclaimer_(reclaimer), deleter_(std::move(deleter)) {}

    ~DeferredCell() {
        T* ptr = ptr_.load(std::memory_order_relaxed);
        if (ptr) deleter_(ptr);
    }

    DeferredCell(const DeferredCell&) = delete;
    DeferredCell& operator=(const DeferredCell&) = delete;

    // Publish a new object. Bumping the generation invalidates every handle
    // handed out earlier.
    void publish(T* ptr) {
        T* old = ptr_.exchange(ptr, std::memory_order_acq_rel);
        generation_.fetch_add(1, std::memory_order_release);
        if (old) deleter_(old);
    }

    // Mark the current object retired: existing handles stop yielding guards
    // immediately, and the memory is released once readers that started
    // before the retire have finished.
    void retire() {
        T* old = ptr_.exchange(nullptr, std::memory_order_acq_rel);
        generation_.fetch_add(1, std::memory_order_release);
        if (old) {
            reclaimer_.retire_fn([old, deleter = deleter_] { deleter(old); });
        }
    }

    bool retired() const {
        return ptr_.load(std::memory_order_acquire) == nullptr;
    }

    Handle<T> handle() const {
        return Handle<T>(this, generation_.load(std::memory_order_acquire));
    }

private:
    friend std::optional<ReaderGuard<T>> try_read<>(const Handle<T>&);

    EpochReclaimer& reclaimer_;
    Deleter deleter_;
    std::atomic<T*> ptr_{nullptr};
    std::atomic<std::uint64_t> generation_{0};
};

// Acquire a read guard for a handle. Returns nullopt if the handle was
// retired, or if the cell was retired/republished after the handle was
// taken (generation mismatch — this is what stops an old handle from
// reading a new object that reused the old one's address).
template <typename T>
std::optional<ReaderGuard<T>> try_read(const Handle<T>& handle) {
    if (handle.cell_ == nullptr) return std::nullopt;
    const DeferredCell<T>* cell = handle.cell_;

    // Pin first: from here on the reclaimer cannot free anything this
    // epoch can still reach.
    EpochReclaimer::Pin pin = cell->reclaimer_.pin();

    std::uint64_t gen_before = cell->generation_.load(std::memory_order_acquire);
    T* ptr = cell->ptr_.load(std::memory_order_acquire);
    if (ptr == nullptr) return std::nullopt; // retired
    std::uint64_t gen_after = cell->generation_.load(std::memory_order_acquire);
    if (gen_before != gen_after || gen_before != handle.generation_) {
        return std::nullopt; // raced with retire/publish, or stale handle
    }
    return ReaderGuard<T>(std::move(pin), ptr);
}

} // namespace drc

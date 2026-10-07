#pragma once

#include <condition_variable>
#include <cstddef>
#include <mutex>
#include <new>
#include <utility>

// Bounded blocking MPMC queue with in-place slot construction.
//
// Exception guarantees:
//  - emplace/try_emplace: if element construction throws, the slot is NOT
//    consumed (size/tail unchanged) and the queue state is untouched.
//  - wait_pop/try_pop: if move-assigning into the out parameter throws, the
//    front element stays in its slot and remains retrievable by a later pop.
template <typename T>
class BoundedQueue {
public:
    explicit BoundedQueue(std::size_t capacity)
        : capacity_(capacity),
          storage_(static_cast<std::byte*>(
              ::operator new(sizeof(T) * capacity,
                             std::align_val_t(alignof(T))))) {}

    BoundedQueue(const BoundedQueue&) = delete;
    BoundedQueue& operator=(const BoundedQueue&) = delete;

    ~BoundedQueue() {
        for (std::size_t i = 0; i < size_; ++i)
            slot((head_ + i) % capacity_)->~T();
        ::operator delete(storage_, std::align_val_t(alignof(T)));
    }

    std::size_t capacity() const { return capacity_; }

    std::size_t size() const {
        std::lock_guard<std::mutex> lk(mutex_);
        return size_;
    }

    // Blocking push. Returns false if the queue was closed while waiting.
    // Strong guarantee: a throwing T constructor leaves the queue unchanged.
    template <typename... Args>
    bool emplace(Args&&... args) {
        std::unique_lock<std::mutex> lk(mutex_);
        not_full_.wait(lk, [&] { return size_ < capacity_ || closed_; });
        if (closed_) return false;
        construct_tail(std::forward<Args>(args)...);
        lk.unlock();
        not_empty_.notify_one();
        return true;
    }

    // Non-blocking push. Returns false if full or closed.
    template <typename... Args>
    bool try_emplace(Args&&... args) {
        {
            std::lock_guard<std::mutex> lk(mutex_);
            if (closed_ || size_ == capacity_) return false;
            construct_tail(std::forward<Args>(args)...);
        }
        not_empty_.notify_one();
        return true;
    }

    // Blocking pop. Returns false only when closed and drained.
    // If `out = std::move(front)` throws, the element is NOT removed.
    bool wait_pop(T& out) {
        std::unique_lock<std::mutex> lk(mutex_);
        not_empty_.wait(lk, [&] { return size_ > 0 || closed_; });
        if (size_ == 0) return false;
        out = std::move(*slot(head_));  // may throw: slot/head/size untouched
        destroy_head();
        lk.unlock();
        not_full_.notify_one();
        return true;
    }

    // Non-blocking pop. Returns false when empty (or closed and drained).
    bool try_pop(T& out) {
        {
            std::lock_guard<std::mutex> lk(mutex_);
            if (size_ == 0) return false;
            out = std::move(*slot(head_));  // may throw: element stays
            destroy_head();
        }
        not_full_.notify_one();
        return true;
    }

    // Unblocks waiters; emplace fails and wait_pop drains then returns false.
    void close() {
        {
            std::lock_guard<std::mutex> lk(mutex_);
            closed_ = true;
        }
        not_empty_.notify_all();
        not_full_.notify_all();
    }

private:
    T* slot(std::size_t index) {
        return std::launder(reinterpret_cast<T*>(storage_) + index);
    }

    // Precondition: mutex held, size_ < capacity_.
    template <typename... Args>
    void construct_tail(Args&&... args) {
        std::size_t pos = (head_ + size_) % capacity_;
        ::new (static_cast<void*>(slot(pos))) T(std::forward<Args>(args)...);
        ++size_;  // only reached on successful construction
    }

    // Precondition: mutex held, size_ > 0, element already moved out.
    void destroy_head() {
        slot(head_)->~T();
        head_ = (head_ + 1) % capacity_;
        --size_;
    }

    const std::size_t capacity_;
    std::byte* const storage_;
    std::size_t head_ = 0;
    std::size_t size_ = 0;
    bool closed_ = false;
    mutable std::mutex mutex_;
    std::condition_variable not_empty_;
    std::condition_variable not_full_;
};

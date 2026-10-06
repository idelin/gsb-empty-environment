#pragma once

#include <condition_variable>
#include <cstddef>
#include <memory>
#include <mutex>
#include <new>
#include <optional>
#include <utility>

// 有界环形队列：槽位原地构造/析构，支持只移动类型。
// 异常保证：
//  - emplace/try_emplace：元素构造抛异常时槽位不被消费，队列状态不变；
//  - pop/try_pop(out)：目标赋值抛异常时元素仍留在队首，可再次取出。
template <typename T>
class bounded_queue {
public:
    explicit bounded_queue(std::size_t capacity)
        : buf_(capacity ? static_cast<std::byte*>(::operator new(capacity * sizeof(T))) : nullptr),
          cap_(capacity) {}

    bounded_queue(const bounded_queue&) = delete;
    bounded_queue& operator=(const bounded_queue&) = delete;

    ~bounded_queue() {
        // 析构所有存活槽位，再释放原始存储。
        for (std::size_t i = 0; i < size_; ++i)
            ptr((head_ + i) % cap_)->~T();
        ::operator delete(buf_);
    }

    std::size_t capacity() const { return cap_; }

    std::size_t size() const {
        std::lock_guard<std::mutex> lk(m_);
        return size_;
    }

    bool empty() const { return size() == 0; }

    // 非阻塞入队；满时返回 false。构造抛异常时槽位不消费。
    template <typename... Args>
    bool try_emplace(Args&&... args) {
        std::lock_guard<std::mutex> lk(m_);
        if (closed_ || size_ == cap_)
            return false;
        construct_tail(std::forward<Args>(args)...);  // 抛异常则状态不变
        not_empty_.notify_one();
        return true;
    }

    // 阻塞入队；关闭或（仅当容量为 0）时返回 false。
    template <typename... Args>
    bool emplace(Args&&... args) {
        std::unique_lock<std::mutex> lk(m_);
        not_full_.wait(lk, [&] { return closed_ || size_ < cap_; });
        if (closed_)
            return false;
        construct_tail(std::forward<Args>(args)...);  // 抛异常则状态不变
        not_empty_.notify_one();
        return true;
    }

    bool try_push(const T& v) { return try_emplace(v); }
    bool try_push(T&& v) { return try_emplace(std::move(v)); }
    bool push(const T& v) { return emplace(v); }
    bool push(T&& v) { return emplace(std::move(v)); }

    // 非阻塞出队到 out；空时返回 false。
    // 赋值抛异常时元素保留在队首，槽位不释放。
    bool try_pop(T& out) {
        std::lock_guard<std::mutex> lk(m_);
        if (size_ == 0)
            return false;
        out = std::move(*ptr(head_));  // 抛异常则元素仍在队首
        destroy_head();
        not_full_.notify_one();
        return true;
    }

    // 阻塞出队到 out；关闭且空时返回 false。赋值抛异常时元素保留。
    bool pop(T& out) {
        std::unique_lock<std::mutex> lk(m_);
        not_empty_.wait(lk, [&] { return closed_ || size_ > 0; });
        if (size_ == 0)
            return false;
        out = std::move(*ptr(head_));  // 抛异常则元素仍在队首
        destroy_head();
        not_full_.notify_one();
        return true;
    }

    // 返回值版本：移动构造抛异常时元素同样保留在队首。
    std::optional<T> try_pop() {
        std::lock_guard<std::mutex> lk(m_);
        if (size_ == 0)
            return std::nullopt;
        std::optional<T> out(std::move(*ptr(head_)));  // 抛异常则元素仍在队首
        destroy_head();
        not_full_.notify_one();
        return out;
    }

    // 关闭队列：唤醒所有阻塞者；入队返回 false，出队排空后返回 false。
    void close() {
        {
            std::lock_guard<std::mutex> lk(m_);
            closed_ = true;
        }
        not_empty_.notify_all();
        not_full_.notify_all();
    }

    bool closed() const {
        std::lock_guard<std::mutex> lk(m_);
        return closed_;
    }

private:
    T* ptr(std::size_t i) {
        return std::launder(reinterpret_cast<T*>(buf_ + i * sizeof(T)));
    }

    template <typename... Args>
    void construct_tail(Args&&... args) {
        ::new (static_cast<void*>(ptr(tail_))) T(std::forward<Args>(args)...);
        tail_ = (tail_ + 1) % cap_;
        ++size_;
    }

    void destroy_head() {
        ptr(head_)->~T();
        head_ = (head_ + 1) % cap_;
        --size_;
    }

    std::byte* buf_;
    std::size_t cap_;
    std::size_t head_ = 0;
    std::size_t tail_ = 0;
    std::size_t size_ = 0;
    bool closed_ = false;
    mutable std::mutex m_;
    std::condition_variable not_empty_;
    std::condition_variable not_full_;
};

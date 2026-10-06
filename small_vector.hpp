#pragma once

// small_vector<T, N>: a vector-like sequence container with inline storage
// for N elements. Once more than N elements are stored, the contents migrate
// to a heap-allocated buffer.
//
// Exception-safety policy:
//  - Growth (reallocation) and reallocating insert build the new sequence in
//    a fresh buffer and only commit on success: any exception thrown by T's
//    constructors leaves the original contents AND capacity untouched
//    (strong guarantee).
//  - In-place insert performs its single construction before mutating any
//    existing element, so a throwing constructor still leaves *this unchanged.
//  - Elements are relocated with std::move_if_noexcept: move is preferred
//    only when it is noexcept (or no copy exists); otherwise elements are
//    copied.
//  - Self-referential insertion (insert/push_back of a reference to an
//    element of *this) is supported by copying the aliased value before any
//    mutation or reallocation.

#include <cstddef>
#include <cstdint>
#include <new>
#include <type_traits>
#include <utility>

template <class T, std::size_t N>
class small_vector {
    static_assert(N > 0, "small_vector requires an inline capacity of at least 1");

    alignas(T) unsigned char inline_buf_[N * sizeof(T)];
    T* data_ = inline_ptr();
    std::size_t size_ = 0;
    std::size_t cap_ = N;

    T* inline_ptr() { return reinterpret_cast<T*>(inline_buf_); }
    const T* inline_ptr() const { return reinterpret_cast<const T*>(inline_buf_); }
    bool is_inline() const { return data_ == inline_ptr(); }

    static T* allocate(std::size_t n) { return static_cast<T*>(::operator new(n * sizeof(T))); }

    static void destroy_range(T* p, std::size_t n) {
        for (std::size_t i = n; i > 0; --i) p[i - 1].~T();
    }

    template <class... Args>
    static void construct_at(T* p, Args&&... args) {
        ::new (static_cast<void*>(p)) T(std::forward<Args>(args)...);
    }

    // Prefer move only when it cannot throw; otherwise fall back to copy.
    static void relocate_construct(T* dst, T& src) { construct_at(dst, std::move_if_noexcept(src)); }

    static void relocate_assign(T& dst, T& src) {
        if constexpr (std::is_nothrow_move_assignable_v<T> || !std::is_copy_assignable_v<T>)
            dst = std::move(src);
        else
            dst = src;
    }

    bool aliases(const T* p) const {
        const auto base = reinterpret_cast<std::uintptr_t>(data_);
        const auto addr = reinterpret_cast<std::uintptr_t>(p);
        return addr >= base && addr < base + size_ * sizeof(T);
    }

    // Strong guarantee: relocate into a fresh buffer, commit only on success.
    void grow_to(std::size_t new_cap) {
        T* fresh = allocate(new_cap);
        std::size_t built = 0;
        try {
            for (; built < size_; ++built) relocate_construct(fresh + built, data_[built]);
        } catch (...) {
            destroy_range(fresh, built);
            ::operator delete(fresh);
            throw;
        }
        destroy_range(data_, size_);
        if (!is_inline()) ::operator delete(data_);
        data_ = fresh;
        cap_ = new_cap;
    }

    // Append atomically: when growth is needed, the new element is
    // constructed in the fresh buffer before anything is committed, so a
    // throwing constructor leaves contents AND capacity untouched.
    template <class... Args>
    void append_atomic(Args&&... args) {
        if (size_ == cap_) {
            std::size_t new_cap = cap_ * 2;
            T* fresh = allocate(new_cap);
            std::size_t built = 0;
            try {
                for (; built < size_; ++built) relocate_construct(fresh + built, data_[built]);
                construct_at(fresh + size_, std::forward<Args>(args)...);
            } catch (...) {
                destroy_range(fresh, built);
                ::operator delete(fresh);
                throw;
            }
            destroy_range(data_, size_);
            if (!is_inline()) ::operator delete(data_);
            data_ = fresh;
            cap_ = new_cap;
            ++size_;
            return;
        }
        construct_at(data_ + size_, std::forward<Args>(args)...);
        ++size_;
    }

    template <class U>
    T* insert_impl(std::size_t idx, U&& value) {
        if (size_ == cap_) {
            // Reallocating insert: build [prefix, value, suffix] in a fresh
            // buffer; any throw leaves *this completely untouched.
            std::size_t new_cap = cap_ * 2;
            if (new_cap < size_ + 1) new_cap = size_ + 1;
            T* fresh = allocate(new_cap);
            std::size_t built = 0;
            try {
                for (; built < idx; ++built) relocate_construct(fresh + built, data_[built]);
                construct_at(fresh + idx, std::forward<U>(value));
                ++built;
                for (std::size_t i = idx; i < size_; ++i, ++built)
                    relocate_construct(fresh + i + 1, data_[i]);
            } catch (...) {
                destroy_range(fresh, built);
                ::operator delete(fresh);
                throw;
            }
            destroy_range(data_, size_);
            if (!is_inline()) ::operator delete(data_);
            data_ = fresh;
            cap_ = new_cap;
            ++size_;
            return data_ + idx;
        }
        if (idx == size_) {
            construct_at(data_ + size_, std::forward<U>(value));  // throw -> unchanged
            ++size_;
            return data_ + idx;
        }
        // In-place insert: the only construction happens before any existing
        // element is modified, so a throwing constructor leaves *this
        // unchanged. Shifting uses (nothrow-move-or-copy) assignment.
        construct_at(data_ + size_, std::move_if_noexcept(data_[size_ - 1]));
        for (std::size_t i = size_ - 1; i > idx; --i) relocate_assign(data_[i], data_[i - 1]);
        data_[idx] = std::forward<U>(value);
        ++size_;
        return data_ + idx;
    }

public:
    using value_type = T;
    using iterator = T*;
    using const_iterator = const T*;

    small_vector() = default;

    small_vector(const small_vector& other) {
        if (other.size_ > N) {
            data_ = allocate(other.size_);
            cap_ = other.size_;
        }
        std::size_t built = 0;
        try {
            for (; built < other.size_; ++built) construct_at(data_ + built, other.data_[built]);
        } catch (...) {
            destroy_range(data_, built);
            if (!is_inline()) ::operator delete(data_);
            throw;
        }
        size_ = other.size_;
    }

    small_vector(small_vector&& other) noexcept(std::is_nothrow_move_constructible_v<T>) {
        if (other.is_inline()) {
            std::size_t built = 0;
            try {
                for (; built < other.size_; ++built)
                    construct_at(data_ + built, std::move(other.data_[built]));
            } catch (...) {
                destroy_range(data_, built);
                throw;
            }
            size_ = other.size_;
            other.clear();
        } else {
            data_ = other.data_;
            size_ = other.size_;
            cap_ = other.cap_;
            other.data_ = other.inline_ptr();
            other.size_ = 0;
            other.cap_ = N;
        }
    }

    small_vector& operator=(const small_vector& other) {
        if (this == &other) return *this;
        small_vector tmp(other);
        *this = std::move(tmp);
        return *this;
    }

    small_vector& operator=(small_vector&& other) noexcept(std::is_nothrow_move_constructible_v<T>) {
        if (this == &other) return *this;
        clear();
        if (!is_inline()) {
            ::operator delete(data_);
            data_ = inline_ptr();
            cap_ = N;
        }
        if (other.is_inline()) {
            for (std::size_t i = 0; i < other.size_; ++i) {
                construct_at(data_ + i, std::move(other.data_[i]));
                ++size_;
            }
            other.clear();
        } else {
            data_ = other.data_;
            size_ = other.size_;
            cap_ = other.cap_;
            other.data_ = other.inline_ptr();
            other.size_ = 0;
            other.cap_ = N;
        }
        return *this;
    }

    ~small_vector() {
        clear();
        if (!is_inline()) ::operator delete(data_);
    }

    std::size_t size() const { return size_; }
    std::size_t capacity() const { return cap_; }
    bool empty() const { return size_ == 0; }

    T* data() { return data_; }
    const T* data() const { return data_; }

    iterator begin() { return data_; }
    iterator end() { return data_ + size_; }
    const_iterator begin() const { return data_; }
    const_iterator end() const { return data_ + size_; }
    const_iterator cbegin() const { return data_; }
    const_iterator cend() const { return data_ + size_; }

    T& operator[](std::size_t i) { return data_[i]; }
    const T& operator[](std::size_t i) const { return data_[i]; }
    T& front() { return data_[0]; }
    const T& front() const { return data_[0]; }
    T& back() { return data_[size_ - 1]; }
    const T& back() const { return data_[size_ - 1]; }

    void clear() {
        destroy_range(data_, size_);
        size_ = 0;
    }

    void reserve(std::size_t n) {
        if (n > cap_) grow_to(n);
    }

    void pop_back() { data_[--size_].~T(); }

    void push_back(const T& value) {
        if (aliases(&value)) {
            T tmp(value);  // copy before any reallocation invalidates the reference
            append_atomic(std::move(tmp));
        } else {
            append_atomic(value);
        }
    }

    void push_back(T&& value) {
        if (aliases(&value)) {
            T tmp(std::move(value));
            append_atomic(std::move(tmp));
        } else {
            append_atomic(std::move(value));
        }
    }

    template <class... Args>
    void emplace_back(Args&&... args) {
        // Like std::vector::emplace_back, arguments must not alias *this.
        append_atomic(std::forward<Args>(args)...);
    }

    iterator insert(const_iterator pos, const T& value) {
        const std::size_t idx = static_cast<std::size_t>(pos - data_);
        if (aliases(&value)) {
            T tmp(value);  // copy before any mutation; also survives reallocation
            return insert_impl(idx, static_cast<const T&>(tmp));
        }
        return insert_impl(idx, value);
    }

    iterator insert(const_iterator pos, T&& value) {
        const std::size_t idx = static_cast<std::size_t>(pos - data_);
        if (aliases(&value)) {
            T tmp(std::move(value));
            return insert_impl(idx, std::move(tmp));
        }
        return insert_impl(idx, std::move(value));
    }

    template <class... Args>
    iterator emplace(const_iterator pos, Args&&... args) {
        T tmp(std::forward<Args>(args)...);  // materialize first: alias-safe
        return insert(pos, std::move(tmp));
    }
};

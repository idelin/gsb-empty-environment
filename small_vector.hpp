#pragma once

// small_vector<T, N>: a vector-like container with inline storage for up to N
// elements. Once more than N elements are stored, the contents migrate to a
// heap-allocated buffer.
//
// Exception-safety guarantees:
//   * insert / push_back / emplace_back / reserve provide the strong
//     guarantee: if an element constructor (or the allocation) throws, the
//     container keeps its original contents, size and capacity.
//   * Elements are relocated with std::move_if_noexcept: move construction is
//     preferred only when it is noexcept (or the type is not copyable);
//     otherwise elements are copied.
//   * Self-referential insertion (v.insert(pos, v[i]), v.push_back(v[0]),
//     including the rvalue forms) is supported by copying the referenced
//     element to a temporary before any mutation takes place.

#include <cstddef>
#include <functional>
#include <memory>
#include <new>
#include <type_traits>
#include <utility>

template <typename T, std::size_t N>
class small_vector {
    static_assert(N > 0, "small_vector requires a positive inline capacity");

public:
    using value_type = T;
    using size_type = std::size_t;
    using iterator = T*;
    using const_iterator = const T*;

    small_vector() noexcept : data_(inline_ptr()), size_(0), cap_(N) {}

    small_vector(const small_vector& other) : small_vector() {
        const size_type n = other.size_;
        T* dst = inline_ptr();
        if (n > N) {
            dst = allocate(n);
            data_ = dst;
            cap_ = n;
        }
        size_type done = 0;
        try {
            for (; done < n; ++done) ::new (dst + done) T(other.data_[done]);
        } catch (...) {
            std::destroy_n(dst, done);
            if (dst != inline_ptr()) {
                deallocate(dst);
                data_ = inline_ptr();
                cap_ = N;
            }
            throw;
        }
        size_ = n;
    }

    small_vector(small_vector&& other) noexcept(std::is_nothrow_move_constructible_v<T>)
        : small_vector() {
        if (other.uses_inline_storage()) {
            size_type done = 0;
            try {
                for (; done < other.size_; ++done)
                    ::new (data_ + done) T(std::move(other.data_[done]));
            } catch (...) {
                std::destroy_n(data_, done);
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
        if (this != &other) {
            small_vector tmp(other);
            *this = std::move(tmp);
        }
        return *this;
    }

    small_vector& operator=(small_vector&& other) noexcept(
        std::is_nothrow_move_constructible_v<T>) {
        if (this == &other) return *this;
        clear_and_release();
        if (other.uses_inline_storage()) {
            size_type done = 0;
            try {
                for (; done < other.size_; ++done)
                    ::new (data_ + done) T(std::move_if_noexcept(other.data_[done]));
            } catch (...) {
                std::destroy_n(data_, done);
                size_ = 0;
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
        return *this;
    }

    ~small_vector() { clear_and_release(); }

    size_type size() const noexcept { return size_; }
    size_type capacity() const noexcept { return cap_; }
    bool empty() const noexcept { return size_ == 0; }
    bool uses_inline_storage() const noexcept { return data_ == inline_ptr(); }

    iterator begin() noexcept { return data_; }
    const_iterator begin() const noexcept { return data_; }
    iterator end() noexcept { return data_ + size_; }
    const_iterator end() const noexcept { return data_ + size_; }

    T& operator[](size_type i) noexcept { return data_[i]; }
    const T& operator[](size_type i) const noexcept { return data_[i]; }
    T& front() noexcept { return data_[0]; }
    const T& front() const noexcept { return data_[0]; }
    T& back() noexcept { return data_[size_ - 1]; }
    const T& back() const noexcept { return data_[size_ - 1]; }
    T* data() noexcept { return data_; }
    const T* data() const noexcept { return data_; }

    void clear() noexcept {
        std::destroy_n(data_, size_);
        size_ = 0;
    }

    void reserve(size_type new_cap) {
        if (new_cap > cap_) reallocate(new_cap);
    }

    template <typename... Args>
    T& emplace_back(Args&&... args) {
        if (size_ == cap_) reallocate(cap_ * 2);
        ::new (data_ + size_) T(std::forward<Args>(args)...);
        ++size_;
        return data_[size_ - 1];
    }

    void push_back(const T& value) {
        if (aliases(value)) {
            T tmp(value);
            emplace_back(std::move(tmp));
        } else {
            emplace_back(value);
        }
    }

    void push_back(T&& value) {
        if (aliases(value)) {
            T tmp(value);
            emplace_back(std::move(tmp));
        } else {
            emplace_back(std::move(value));
        }
    }

    iterator insert(const_iterator pos, const T& value) {
        // Copying to a temporary first handles self-references and guarantees
        // that a throwing copy constructor leaves the container untouched.
        T tmp(value);
        return insert_value(index_of(pos), std::move(tmp));
    }

    iterator insert(const_iterator pos, T&& value) {
        if (aliases(value)) {
            T tmp(value);
            return insert_value(index_of(pos), std::move(tmp));
        }
        return insert_value(index_of(pos), std::move(value));
    }

private:
    alignas(T) unsigned char inline_storage_[N * sizeof(T)];
    T* data_;
    size_type size_;
    size_type cap_;

    T* inline_ptr() noexcept {
        return std::launder(reinterpret_cast<T*>(inline_storage_));
    }
    const T* inline_ptr() const noexcept {
        return std::launder(reinterpret_cast<const T*>(inline_storage_));
    }

    static T* allocate(size_type n) {
        if constexpr (alignof(T) > __STDCPP_DEFAULT_NEW_ALIGNMENT__) {
            return static_cast<T*>(
                ::operator new(n * sizeof(T), std::align_val_t(alignof(T))));
        } else {
            return static_cast<T*>(::operator new(n * sizeof(T)));
        }
    }

    static void deallocate(T* p) noexcept {
        if constexpr (alignof(T) > __STDCPP_DEFAULT_NEW_ALIGNMENT__) {
            ::operator delete(p, std::align_val_t(alignof(T)));
        } else {
            ::operator delete(p);
        }
    }

    bool aliases(const T& value) const noexcept {
        const T* p = std::addressof(value);
        return std::less_equal<const T*>()(data_, p) &&
               std::less<const T*>()(p, data_ + size_);
    }

    size_type index_of(const_iterator pos) const noexcept {
        return static_cast<size_type>(pos - data_);
    }

    void clear_and_release() noexcept {
        std::destroy_n(data_, size_);
        if (!uses_inline_storage()) deallocate(data_);
        data_ = inline_ptr();
        size_ = 0;
        cap_ = N;
    }

    // Strong guarantee: builds the relocated contents in a fresh buffer and
    // only commits (destroying the old elements) once everything succeeded.
    void reallocate(size_type new_cap) {
        T* buf = allocate(new_cap);
        size_type done = 0;
        try {
            for (; done < size_; ++done)
                ::new (buf + done) T(std::move_if_noexcept(data_[done]));
        } catch (...) {
            std::destroy_n(buf, done);
            deallocate(buf);
            throw;
        }
        std::destroy_n(data_, size_);
        if (!uses_inline_storage()) deallocate(data_);
        data_ = buf;
        cap_ = new_cap;
    }

    // Builds [0, idx) + value + [idx, size) in a fresh buffer of capacity
    // new_cap, then commits. On any throw the original container is untouched.
    iterator insert_into_fresh(size_type idx, T&& value, size_type new_cap) {
        T* buf = allocate(new_cap);
        size_type prefix = 0;
        size_type suffix = 0;
        bool middle = false;
        try {
            for (; prefix < idx; ++prefix)
                ::new (buf + prefix) T(std::move_if_noexcept(data_[prefix]));
            ::new (buf + idx) T(std::move(value));
            middle = true;
            for (; suffix < size_ - idx; ++suffix)
                ::new (buf + idx + 1 + suffix)
                    T(std::move_if_noexcept(data_[idx + suffix]));
        } catch (...) {
            std::destroy_n(buf + idx + 1, suffix);
            if (middle) std::destroy_at(buf + idx);
            std::destroy_n(buf, prefix);
            deallocate(buf);
            throw;
        }
        std::destroy_n(data_, size_);
        if (!uses_inline_storage()) deallocate(data_);
        data_ = buf;
        cap_ = new_cap;
        ++size_;
        return data_ + idx;
    }

    iterator insert_value(size_type idx, T&& value) {
        if (size_ == cap_) {
            size_type new_cap = cap_ * 2;
            if (new_cap < size_ + 1) new_cap = size_ + 1;
            return insert_into_fresh(idx, std::move(value), new_cap);
        }
        if (idx == size_) {
            // Appending into spare capacity: a throw changes nothing.
            ::new (data_ + size_) T(std::move(value));
            ++size_;
            return data_ + idx;
        }
        if constexpr (std::is_nothrow_move_constructible_v<T> &&
                      std::is_nothrow_move_assignable_v<T>) {
            // Every operation below is noexcept, so the in-place shift cannot
            // fail and the strong guarantee holds trivially.
            ::new (data_ + size_) T(std::move(data_[size_ - 1]));
            for (size_type i = size_ - 1; i > idx; --i)
                data_[i] = std::move(data_[i - 1]);
            data_[idx] = std::move(value);
            ++size_;
            return data_ + idx;
        } else {
            // Shifting in place could throw halfway through an assignment,
            // leaving the contents modified. Rebuild in a fresh buffer of the
            // same capacity instead, keeping the strong guarantee.
            return insert_into_fresh(idx, std::move(value), cap_);
        }
    }
};

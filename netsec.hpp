#pragma once

#if defined(__APPLE__) || defined(__STDC_LIB_EXT1__)
#ifndef __STDC_WANT_LIB_EXT1__
#define __STDC_WANT_LIB_EXT1__ 1
#endif
#endif

#include <cstddef>
#include <memory>
#include <stdexcept>
#include <type_traits>
#include <utility>

#if defined(_WIN32)
#include <windows.h>
#elif defined(__APPLE__) || defined(__STDC_LIB_EXT1__)
#include <string.h>
#endif

namespace netsec {

namespace detail {

// Non-elidable memory clearing.
//
// A plain std::memset() on a buffer that is never read again may be removed by
// the optimizer as a dead store. Every path below performs writes the compiler
// is not allowed to discard: platform secure-clear primitives where available,
// otherwise volatile stores (volatile accesses are observable side effects),
// plus a compiler barrier so the loop cannot be reordered or merged away.
inline void secure_zero_memory(void* address, std::size_t bytes) noexcept {
    if (address == nullptr || bytes == 0) {
        return;
    }

#if defined(_WIN32)
    SecureZeroMemory(address, bytes);
#elif defined(__STDC_LIB_EXT1__) || defined(__APPLE__)
    // C11 Annex K: runtime-constrained memset that must not be optimized out.
    (void)memset_s(address, bytes, 0, bytes);
#else
    volatile unsigned char* cursor = static_cast<volatile unsigned char*>(address);
    volatile unsigned char* end = cursor + bytes;
    while (cursor != end) {
        *cursor++ = 0;
    }
#endif

#if defined(__GNUC__) || defined(__clang__)
    asm volatile("" : : "g"(address) : "memory");
#elif defined(_MSC_VER)
    _ReadWriteBarrier();
#endif
}

}  // namespace detail

// Byte container that guarantees secret bytes do not survive in abandoned
// memory blocks.
//
// Growth strategy: allocate a new block first, copy into it, and only after the
// switch succeeds securely clear (non-elidable zero fill) and release the old
// block. If allocation or copying throws, the original object is untouched
// (strong exception guarantee). Shrinking clears the truncated bytes in place.
// A moved-from object retains no pointer to the moved content.
template <class Allocator = std::allocator<unsigned char>>
class basic_secure_buffer {
public:
    using value_type = unsigned char;
    using allocator_type = Allocator;
    using size_type = std::size_t;
    using difference_type = std::ptrdiff_t;
    using reference = unsigned char&;
    using const_reference = const unsigned char&;
    using pointer = typename std::allocator_traits<Allocator>::pointer;
    using const_pointer = typename std::allocator_traits<Allocator>::const_pointer;
    using iterator = pointer;
    using const_iterator = const_pointer;

private:
    using alloc_traits = std::allocator_traits<Allocator>;

    static constexpr size_type initial_capacity = 16;

    Allocator allocator_;
    pointer data_ = nullptr;
    size_type size_ = 0;
    size_type capacity_ = 0;

    pointer allocate_block(size_type count) {
        return alloc_traits::allocate(allocator_, count);
    }

    void deallocate_block(pointer block, size_type count) noexcept {
        alloc_traits::deallocate(allocator_, block, count);
    }

    // Securely clear the full block (used region plus any retained tail) and
    // return it to the allocator.
    void dispose_storage() noexcept {
        if (data_ != nullptr) {
            detail::secure_zero_memory(std::addressof(*data_), capacity_);
            deallocate_block(data_, capacity_);
            data_ = nullptr;
            size_ = 0;
            capacity_ = 0;
        }
    }

    size_type max_size() const noexcept {
        return alloc_traits::max_size(allocator_);
    }

    size_type next_capacity(size_type required) const noexcept {
        size_type candidate = capacity_ == 0 ? initial_capacity : capacity_;
        while (candidate < required) {
            if (candidate > max_size() / 2) {
                return required;
            }
            candidate *= 2;
        }
        return candidate;
    }

    // Strong guarantee: on any exception, this object is unchanged and the old
    // block remains allocated with its original content.
    void grow(size_type required) {
        if (required <= capacity_) {
            return;
        }

        const size_type new_capacity = next_capacity(required);
        pointer new_block = allocate_block(new_capacity);
        try {
            for (size_type index = 0; index < size_; ++index) {
                alloc_traits::construct(allocator_,
                                        std::addressof(*(new_block + index)),
                                        data_[index]);
            }
        } catch (...) {
            detail::secure_zero_memory(std::addressof(*new_block), new_capacity);
            deallocate_block(new_block, new_capacity);
            throw;
        }

        pointer old_block = data_;
        const size_type old_capacity = capacity_;

        data_ = new_block;
        capacity_ = new_capacity;

        if (old_block != nullptr) {
            detail::secure_zero_memory(std::addressof(*old_block), old_capacity);
            deallocate_block(old_block, old_capacity);
        }
    }

    void check_index(size_type index) const {
        if (index >= size_) {
            throw std::out_of_range("netsec::basic_secure_buffer index out of range");
        }
    }

public:
    basic_secure_buffer() noexcept(std::is_nothrow_default_constructible<Allocator>::value)
        : allocator_() {}

    explicit basic_secure_buffer(const Allocator& alloc) noexcept
        : allocator_(alloc) {}

    explicit basic_secure_buffer(size_type count,
                                 const Allocator& alloc = Allocator())
        : allocator_(alloc) {
        if (count != 0) {
            capacity_ = next_capacity(count);
            data_ = allocate_block(capacity_);
            try {
                for (size_type index = 0; index < count; ++index) {
                    alloc_traits::construct(allocator_,
                                            std::addressof(*(data_ + index)),
                                            static_cast<unsigned char>(0));
                }
            } catch (...) {
                detail::secure_zero_memory(std::addressof(*data_), capacity_);
                deallocate_block(data_, capacity_);
                data_ = nullptr;
                capacity_ = 0;
                throw;
            }
            size_ = count;
        }
    }

    basic_secure_buffer(size_type count, unsigned char fill,
                        const Allocator& alloc = Allocator())
        : allocator_(alloc) {
        if (count != 0) {
            capacity_ = next_capacity(count);
            data_ = allocate_block(capacity_);
            try {
                for (size_type index = 0; index < count; ++index) {
                    alloc_traits::construct(allocator_,
                                            std::addressof(*(data_ + index)),
                                            fill);
                }
            } catch (...) {
                detail::secure_zero_memory(std::addressof(*data_), capacity_);
                deallocate_block(data_, capacity_);
                data_ = nullptr;
                capacity_ = 0;
                throw;
            }
            size_ = count;
        }
    }

    basic_secure_buffer(const unsigned char* source, size_type count,
                        const Allocator& alloc = Allocator())
        : allocator_(alloc) {
        if (count != 0) {
            capacity_ = next_capacity(count);
            data_ = allocate_block(capacity_);
            try {
                for (size_type index = 0; index < count; ++index) {
                    alloc_traits::construct(allocator_,
                                            std::addressof(*(data_ + index)),
                                            source[index]);
                }
            } catch (...) {
                detail::secure_zero_memory(std::addressof(*data_), capacity_);
                deallocate_block(data_, capacity_);
                data_ = nullptr;
                capacity_ = 0;
                throw;
            }
            size_ = count;
        }
    }

    basic_secure_buffer(const basic_secure_buffer& other,
                        const Allocator& alloc)
        : allocator_(alloc) {
        if (other.size_ != 0) {
            capacity_ = other.size_;
            data_ = allocate_block(capacity_);
            try {
                for (size_type index = 0; index < other.size_; ++index) {
                    alloc_traits::construct(allocator_,
                                            std::addressof(*(data_ + index)),
                                            other.data_[index]);
                }
            } catch (...) {
                detail::secure_zero_memory(std::addressof(*data_), capacity_);
                deallocate_block(data_, capacity_);
                data_ = nullptr;
                capacity_ = 0;
                throw;
            }
            size_ = other.size_;
        }
    }

    basic_secure_buffer(const basic_secure_buffer& other)
        : basic_secure_buffer(other,
                              alloc_traits::select_on_container_copy_construction(
                                  other.allocator_)) {}

    basic_secure_buffer(basic_secure_buffer&& other) noexcept
        : allocator_(std::move(other.allocator_)),
          data_(other.data_),
          size_(other.size_),
          capacity_(other.capacity_) {
        other.data_ = nullptr;
        other.size_ = 0;
        other.capacity_ = 0;
    }

    basic_secure_buffer(basic_secure_buffer&& other, const Allocator& alloc) noexcept(
        std::is_nothrow_move_constructible<Allocator>::value)
        : allocator_(alloc) {
        if (allocator_ == other.allocator_) {
            data_ = other.data_;
            size_ = other.size_;
            capacity_ = other.capacity_;
            other.data_ = nullptr;
            other.size_ = 0;
            other.capacity_ = 0;
        } else {
            if (other.size_ != 0) {
                capacity_ = other.size_;
                data_ = allocate_block(capacity_);
                try {
                    for (size_type index = 0; index < other.size_; ++index) {
                        alloc_traits::construct(allocator_,
                                                std::addressof(*(data_ + index)),
                                                other.data_[index]);
                    }
                } catch (...) {
                    detail::secure_zero_memory(std::addressof(*data_), capacity_);
                    deallocate_block(data_, capacity_);
                    data_ = nullptr;
                    capacity_ = 0;
                    throw;
                }
                size_ = other.size_;
            }
        }
    }

    ~basic_secure_buffer() {
        dispose_storage();
    }

    basic_secure_buffer& operator=(const basic_secure_buffer& other) {
        if (this == std::addressof(other)) {
            return *this;
        }
        // Copy-and-swap: any allocation failure happens before *this changes.
        basic_secure_buffer temporary(other, allocator_);
        swap(temporary);
        return *this;
    }

    basic_secure_buffer& operator=(basic_secure_buffer&& other) {
        if (this == std::addressof(other)) {
            return *this;
        }

        if (allocator_ == other.allocator_) {
            dispose_storage();
            data_ = other.data_;
            size_ = other.size_;
            capacity_ = other.capacity_;
            if (alloc_traits::propagate_on_container_move_assignment::value) {
                allocator_ = std::move(other.allocator_);
            }
            other.data_ = nullptr;
            other.size_ = 0;
            other.capacity_ = 0;
            return *this;
        }

        // Allocators incompatible: transfer through a copy so every block is
        // released through the allocator that owns it.
        basic_secure_buffer temporary(other, allocator_);
        swap(temporary);
        return *this;
    }

    void reserve(size_type new_capacity) {
        grow(new_capacity);
    }

    void resize(size_type new_size) {
        resize(new_size, static_cast<unsigned char>(0));
    }

    void resize(size_type new_size, unsigned char fill) {
        if (new_size < size_) {
            // Shrink in place: clear exactly the truncated prefix.
            detail::secure_zero_memory(std::addressof(*(data_ + new_size)),
                                       size_ - new_size);
            size_ = new_size;
            return;
        }
        if (new_size > size_) {
            const size_type old_size = size_;
            grow(new_size);
            for (size_type index = old_size; index < new_size; ++index) {
                alloc_traits::construct(allocator_,
                                        std::addressof(*(data_ + index)), fill);
            }
            size_ = new_size;
        }
    }

    void push_back(unsigned char value) {
        if (size_ == capacity_) {
            grow(size_ + 1);
        }
        alloc_traits::construct(allocator_, std::addressof(*(data_ + size_)), value);
        ++size_;
    }

    void append(const unsigned char* source, size_type count) {
        if (count == 0) {
            return;
        }
        if (count > capacity_ - size_) {
            grow(size_ + count);
        }
        for (size_type index = 0; index < count; ++index) {
            alloc_traits::construct(allocator_,
                                    std::addressof(*(data_ + size_ + index)),
                                    source[index]);
        }
        size_ += count;
    }

    // Drops the logical size but keeps the allocation; the used region is
    // securely cleared so no secret remains reachable.
    void clear() noexcept {
        if (data_ != nullptr && size_ != 0) {
            detail::secure_zero_memory(std::addressof(*data_), size_);
        }
        size_ = 0;
    }

    reference at(size_type index) {
        check_index(index);
        return data_[index];
    }

    const_reference at(size_type index) const {
        check_index(index);
        return data_[index];
    }

    reference operator[](size_type index) noexcept {
        return data_[index];
    }

    const_reference operator[](size_type index) const noexcept {
        return data_[index];
    }

    reference front() noexcept {
        return data_[0];
    }

    const_reference front() const noexcept {
        return data_[0];
    }

    reference back() noexcept {
        return data_[size_ - 1];
    }

    const_reference back() const noexcept {
        return data_[size_ - 1];
    }

    pointer data() noexcept {
        return data_;
    }

    const_pointer data() const noexcept {
        return data_;
    }

    bool empty() const noexcept {
        return size_ == 0;
    }

    size_type size() const noexcept {
        return size_;
    }

    size_type capacity() const noexcept {
        return capacity_;
    }

    Allocator get_allocator() const noexcept {
        return allocator_;
    }

    iterator begin() noexcept {
        return data_;
    }

    const_iterator begin() const noexcept {
        return data_;
    }

    const_iterator cbegin() const noexcept {
        return data_;
    }

    iterator end() noexcept {
        return data_ + size_;
    }

    const_iterator end() const noexcept {
        return data_ + size_;
    }

    const_iterator cend() const noexcept {
        return data_ + size_;
    }

    void swap(basic_secure_buffer& other) noexcept {
        using std::swap;
        swap(allocator_, other.allocator_);
        swap(data_, other.data_);
        swap(size_, other.size_);
        swap(capacity_, other.capacity_);
    }
};

template <class Allocator>
void swap(basic_secure_buffer<Allocator>& left,
          basic_secure_buffer<Allocator>& right) noexcept {
    left.swap(right);
}

using secure_buffer = basic_secure_buffer<std::allocator<unsigned char>>;

}  // namespace netsec

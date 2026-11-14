#pragma once

#include "netsec.h"

#include <array>
#include <cstddef>
#include <cstdint>
#include <exception>
#include <limits>
#include <span>
#include <stdexcept>
#include <string>
#include <type_traits>
#include <utility>

namespace netsec {

using tag_t = std::uint64_t;
using trap_handler = netsec_trap_handler;

inline constexpr tag_t no_tag = 0;

enum class access_kind : std::uint32_t {
    read = NETSEC_ACCESS_READ,
    write = NETSEC_ACCESS_WRITE,
    read_write = NETSEC_ACCESS_READ_WRITE,
    syscall_read = NETSEC_ACCESS_SYSCALL_READ,
    syscall_write = NETSEC_ACCESS_SYSCALL_WRITE
};

enum class trap_reason : std::uint32_t {
    none = NETSEC_TRAP_NONE,
    untagged = NETSEC_TRAP_UNTAGGED,
    stale_object = NETSEC_TRAP_STALE_OBJECT,
    out_of_bounds = NETSEC_TRAP_OUT_OF_BOUNDS,
    tag_mismatch = NETSEC_TRAP_TAG_MISMATCH,
    unregistered = NETSEC_TRAP_UNREGISTERED,
    arithmetic_overflow = NETSEC_TRAP_ARITHMETIC_OVERFLOW,
    provenance_mismatch = NETSEC_TRAP_PROVENANCE_MISMATCH,
    invalid_argument = NETSEC_TRAP_INVALID_ARGUMENT
};

inline const char* reason_name(trap_reason reason) noexcept {
    return ::netsec_reason_name(static_cast<int>(reason));
}

struct trap_info {
    trap_reason reason = trap_reason::none;
    std::uintptr_t address = 0;
    std::size_t bytes = 0;
    tag_t expected_tag = no_tag;
    tag_t actual_tag = no_tag;
    const char* operation = "access";
};

class tag_violation : public std::runtime_error {
public:
    tag_violation(const trap_info& info, std::string message)
        : std::runtime_error(std::move(message)), info_(info) {}

    const trap_info& info() const noexcept { return info_; }
    trap_reason reason() const noexcept { return info_.reason; }

private:
    trap_info info_;
};

inline void set_trap_handler(trap_handler handler) noexcept {
    ::netsec_set_trap_handler(handler);
}

inline trap_handler get_trap_handler() noexcept {
    return ::netsec_get_trap_handler();
}

namespace detail {

inline netsec_ptr make_descriptor(std::uintptr_t address,
                                  std::uintptr_t object_base,
                                  std::size_t object_size,
                                  tag_t tag,
                                  std::uint64_t generation,
                                  bool tagged) noexcept {
    netsec_ptr output{};
    output.address = address;
    output.object_base = object_base;
    output.object_size = object_size;
    output.tag = tag;
    output.generation = generation;
    output.tagged = tagged && tag != no_tag ? 1U : 0U;
    return output;
}

inline netsec_ptr descriptor_from_object(netsec_object object) noexcept {
    return ::netsec_ptr_from_object(object);
}

[[noreturn]] inline void arithmetic_overflow(std::uintptr_t address,
                                             const char* operation) {
    ::netsec_raise_trap(NETSEC_TRAP_ARITHMETIC_OVERFLOW,
                        address,
                        0,
                        no_tag,
                        no_tag,
                        operation);
    std::terminate();
}

template <class T>
constexpr std::ptrdiff_t checked_element_offset(std::ptrdiff_t elements,
                                                std::uintptr_t address,
                                                const char* operation) {
    if constexpr (sizeof(T) == 1) {
        return elements;
    } else {
        constexpr auto element_size =
            static_cast<std::ptrdiff_t>(sizeof(T));
        if (elements > std::numeric_limits<std::ptrdiff_t>::max() /
                           element_size ||
            elements < std::numeric_limits<std::ptrdiff_t>::min() /
                           element_size) {
            arithmetic_overflow(address, operation);
        }
        return elements * element_size;
    }
}

template <class T>
constexpr std::size_t checked_byte_count(std::size_t count,
                                         std::uintptr_t address,
                                         const char* operation) {
    if (count > std::numeric_limits<std::size_t>::max() / sizeof(T)) {
        arithmetic_overflow(address, operation);
    }
    return count * sizeof(T);
}

}  // namespace detail

struct object_scope;

template <class T>
class tagged_ptr {
public:
    using element_type = T;
    using pointer = T*;
    using reference = T&;

    constexpr tagged_ptr() noexcept = default;
    constexpr tagged_ptr(std::nullptr_t) noexcept {}
    constexpr tagged_ptr(const tagged_ptr&) noexcept = default;
    constexpr tagged_ptr(tagged_ptr&&) noexcept = default;
    constexpr tagged_ptr& operator=(const tagged_ptr&) noexcept = default;
    constexpr tagged_ptr& operator=(tagged_ptr&&) noexcept = default;
    constexpr tagged_ptr& operator=(std::nullptr_t) noexcept {
        *this = tagged_ptr();
        return *this;
    }

    /*
     * Implicit conversions between tagged pointer types keep the object tag
     * and byte provenance.  A compiler lowering a C++ implicit pointer
     * conversion can emit this constructor (or implicit_cast below) instead
     * of stripping the tag with a raw pointer cast.
     */
    template <class U,
              std::enable_if_t<!std::is_same_v<T, U>, int> = 0>
    constexpr tagged_ptr(const tagged_ptr<U>& other) noexcept
        : address_(reinterpret_cast<T*>(other.address_)),
          object_base_(other.object_base_),
          object_size_(other.object_size_),
          tag_(other.tag_),
          generation_(other.generation_),
          tagged_(other.tagged_) {}

    template <class U,
              std::enable_if_t<!std::is_same_v<T, U>, int> = 0>
    constexpr tagged_ptr& operator=(const tagged_ptr<U>& other) noexcept {
        address_ = reinterpret_cast<T*>(other.address_);
        object_base_ = other.object_base_;
        object_size_ = other.object_size_;
        tag_ = other.tag_;
        generation_ = other.generation_;
        tagged_ = other.tagged_;
        return *this;
    }

    static constexpr tagged_ptr from_descriptor(netsec_ptr descriptor) noexcept {
        tagged_ptr output;
        output.address_ = reinterpret_cast<T*>(descriptor.address);
        output.object_base_ = descriptor.object_base;
        output.object_size_ = descriptor.object_size;
        output.tag_ = descriptor.tag;
        output.generation_ = descriptor.generation;
        output.tagged_ = descriptor.tagged != 0 && descriptor.tag != no_tag;
        return output;
    }

    static tagged_ptr from_object(netsec_object object) noexcept {
        return from_descriptor(::netsec_ptr_from_object(object));
    }

    static tagged_ptr from_object_at(netsec_object object,
                                     std::size_t byte_offset) {
        return from_descriptor(
            ::netsec_ptr_from_object_at(object, byte_offset));
    }

    /*
     * Compiler lowering for a raw-to-tagged implicit conversion: recover the
     * live object tag for the address instead of carrying no provenance.
     * Addresses outside every registered object produce an untagged value,
     * which fails closed on first use.
     */
    static tagged_ptr from_address(T* raw,
                                   std::size_t bytes = sizeof(T)) {
        return from_descriptor(::netsec_ptr_from_address(raw, bytes));
    }

    /* Explicit re-association after a raw/spilled pointer crosses a boundary. */
    static tagged_ptr from_raw(T* raw, tag_t tag, std::size_t bytes = 0) {
        netsec_ptr descriptor{};
        descriptor.address = reinterpret_cast<std::uintptr_t>(raw);
        descriptor.tag = tag;
        descriptor.tagged = tag != no_tag ? 1U : 0U;
        return from_descriptor(::netsec_ptr_reassociate(descriptor, tag, bytes));
    }

    netsec_ptr descriptor() const noexcept {
        return detail::make_descriptor(
            reinterpret_cast<std::uintptr_t>(address_),
            object_base_,
            object_size_,
            tag_,
            generation_,
            tagged_);
    }

    std::uintptr_t address() const noexcept {
        return reinterpret_cast<std::uintptr_t>(address_);
    }

    /*
     * Raw pointer materialization is deliberately explicit.  Untrusted code
     * should never call this; syscall copy helpers use the validated
     * netsec_syscall_map boundary instead.
     */
    T* raw_address() const noexcept { return address_; }

    tag_t tag() const noexcept { return tag_; }
    bool has_tag() const noexcept { return tagged_ && tag_ != no_tag; }
    std::uint64_t generation() const noexcept { return generation_; }
    std::uintptr_t object_base() const noexcept { return object_base_; }
    std::size_t object_size() const noexcept { return object_size_; }

    std::size_t offset() const noexcept {
        const auto current = address();
        return current >= object_base_ ? current - object_base_ : 0;
    }

    std::size_t remaining() const noexcept {
        const auto used = offset();
        return used <= object_size_ ? object_size_ - used : 0;
    }

    bool check(std::size_t bytes = sizeof(T)) const noexcept {
        return ::netsec_ptr_check(descriptor(), bytes) != 0;
    }

    bool in_bounds(std::size_t bytes = sizeof(T)) const noexcept {
        return check(bytes);
    }

    bool bounds_check(std::size_t bytes = sizeof(T)) const noexcept {
        return check(bytes);
    }

    void require(std::size_t bytes = sizeof(T),
                 access_kind kind = access_kind::read_write) const {
        ::netsec_ptr_require(descriptor(), bytes, static_cast<int>(kind));
    }

    void bounds_require(std::size_t bytes = sizeof(T)) const {
        ::netsec_bounds_require(descriptor(), bytes);
    }

    tagged_ptr add(std::ptrdiff_t byte_offset) const {
        return from_descriptor(::netsec_ptr_add(descriptor(), byte_offset));
    }

    tagged_ptr sub(std::ptrdiff_t byte_offset) const {
        return from_descriptor(::netsec_ptr_sub(descriptor(), byte_offset));
    }

    /* Arithmetic variants that also reject leaving the object immediately. */
    tagged_ptr checked_add(std::ptrdiff_t byte_offset) const {
        return from_descriptor(
            ::netsec_ptr_add_checked(descriptor(), byte_offset));
    }

    tagged_ptr checked_sub(std::ptrdiff_t byte_offset) const {
        return from_descriptor(
            ::netsec_ptr_sub_checked(descriptor(), byte_offset));
    }

    tagged_ptr operator+(std::ptrdiff_t elements) const {
        return add(detail::checked_element_offset<T>(
            elements, address(), "tagged pointer addition"));
    }

    tagged_ptr operator-(std::ptrdiff_t elements) const {
        return sub(detail::checked_element_offset<T>(
            elements, address(), "tagged pointer subtraction"));
    }

    tagged_ptr& operator+=(std::ptrdiff_t elements) {
        *this = *this + elements;
        return *this;
    }

    tagged_ptr& operator-=(std::ptrdiff_t elements) {
        *this = *this - elements;
        return *this;
    }

    tagged_ptr& operator++() {
        *this += 1;
        return *this;
    }

    tagged_ptr operator++(int) {
        const auto previous = *this;
        *this += 1;
        return previous;
    }

    tagged_ptr& operator--() {
        *this -= 1;
        return *this;
    }

    tagged_ptr operator--(int) {
        const auto previous = *this;
        *this -= 1;
        return previous;
    }

    std::ptrdiff_t operator-(const tagged_ptr& other) const {
        if constexpr (sizeof(T) == 1) {
            return ::netsec_ptr_distance(descriptor(), other.descriptor());
        } else {
            const auto distance =
                ::netsec_ptr_distance(descriptor(), other.descriptor());
            constexpr auto element_size =
                static_cast<std::ptrdiff_t>(sizeof(T));
            if (distance % element_size != 0) {
                detail::arithmetic_overflow(address(),
                                            "tagged pointer difference");
            }
            return distance / element_size;
        }
    }

    template <class U>
    tagged_ptr<U> reinterpret() const noexcept {
        return tagged_ptr<U>(*this);
    }

    template <class U>
    tagged_ptr<U> cast() const noexcept {
        return tagged_ptr<U>(*this);
    }

    /*
     * Simulate a tag-stripping attack.  The returned value keeps only the
     * address; the first dereference or bounds check fails closed.
     */
    tagged_ptr strip_tag() const noexcept {
        return from_descriptor(detail::make_descriptor(
            reinterpret_cast<std::uintptr_t>(address_),
            object_base_,
            object_size_,
            no_tag,
            generation_,
            false));
    }

    reference operator*() const {
        require(sizeof(T), access_kind::read_write);
        return *address_;
    }

    T* operator->() const {
        require(sizeof(T), access_kind::read_write);
        return address_;
    }

    reference operator[](std::ptrdiff_t index) const {
        return *(*this + index);
    }

    explicit operator bool() const noexcept { return has_tag(); }

    friend bool operator==(const tagged_ptr& left,
                           const tagged_ptr& right) noexcept {
        return left.address_ == right.address_ &&
               left.tag_ == right.tag_ &&
               left.generation_ == right.generation_;
    }

    friend bool operator!=(const tagged_ptr& left,
                           const tagged_ptr& right) noexcept {
        return !(left == right);
    }

    friend bool operator==(const tagged_ptr& pointer,
                           std::nullptr_t) noexcept {
        return pointer.address_ == nullptr && !pointer.has_tag();
    }

    friend bool operator!=(const tagged_ptr& pointer,
                           std::nullptr_t) noexcept {
        return !(pointer == nullptr);
    }

private:
    T* address_ = nullptr;
    std::uintptr_t object_base_ = 0;
    std::size_t object_size_ = 0;
    tag_t tag_ = no_tag;
    std::uint64_t generation_ = 0;
    bool tagged_ = false;

    template <class>
    friend class tagged_ptr;
    friend struct object_scope;
    template <class U>
    friend tagged_ptr<U> tag_object(U*, std::size_t);
};

template <class T>
tagged_ptr<T> tag_object(T* pointer, std::size_t count) {
    if (pointer == nullptr && count != 0) {
        ::netsec_raise_trap(NETSEC_TRAP_INVALID_ARGUMENT,
                            0,
                            0,
                            no_tag,
                            no_tag,
                            "tag_object");
        std::terminate();
    }
    const auto bytes = detail::checked_byte_count<T>(
        count,
        reinterpret_cast<std::uintptr_t>(pointer),
        "tag_object size multiplication");
    return tagged_ptr<T>::from_object(
        ::netsec_register_object(pointer, bytes));
}

template <class T>
tagged_ptr<T> operator+(std::ptrdiff_t elements,
                        const tagged_ptr<T>& pointer) {
    return pointer + elements;
}

template <class T, std::size_t N>
tagged_ptr<T> tag_object(T (&array)[N]) {
    return tag_object<T>(array, N);
}

template <class T, std::size_t N>
tagged_ptr<T> tag_object(std::array<T, N>& array) {
    return tag_object(array.data(), array.size());
}

template <class T, std::size_t N>
tagged_ptr<const T> tag_object(const std::array<T, N>& array) {
    return tag_object<const T>(array.data(), array.size());
}

template <class T>
tagged_ptr<T> tag_object(std::span<T> span) {
    return tag_object(span.data(), span.size());
}

template <class T>
tagged_ptr<T> tag_memory(T* pointer, std::size_t count) {
    return tag_object(pointer, count);
}

template <class T, std::size_t N>
tagged_ptr<T> tag_memory(T (&array)[N]) {
    return tag_object(array);
}

inline tagged_ptr<std::byte> tag_bytes(void* pointer, std::size_t bytes) {
    return tagged_ptr<std::byte>::from_object(
        ::netsec_register_object(pointer, bytes));
}

template <class T>
tagged_ptr<T> make_tagged(T* pointer, std::size_t count) {
    return tag_object(pointer, count);
}

template <class T, class U>
constexpr tagged_ptr<T> implicit_cast(const tagged_ptr<U>& pointer) noexcept {
    return tagged_ptr<T>(pointer);
}

template <class T>
tagged_ptr<T> retag(T* raw, tag_t tag, std::size_t bytes = 0) {
    return tagged_ptr<T>::from_raw(raw, tag, bytes);
}

template <class T>
tagged_ptr<T> tag_address(T* raw, std::size_t bytes = sizeof(T)) {
    return tagged_ptr<T>::from_address(raw, bytes);
}

inline netsec_object register_object(void* base, std::size_t size) {
    return ::netsec_register_object(base, size);
}

inline bool unregister_object(tag_t tag) noexcept {
    return ::netsec_unregister_object(tag) != 0;
}

inline bool object_alive(tag_t tag) noexcept {
    return ::netsec_object_alive(tag) != 0;
}

inline bool ptr_check(const netsec_ptr& pointer,
                      std::size_t bytes) noexcept {
    return ::netsec_ptr_check(pointer, bytes) != 0;
}

inline bool bounds_check(const netsec_ptr& pointer,
                         std::size_t bytes) noexcept {
    return ::netsec_bounds_check(pointer, bytes) != 0;
}

inline void bounds_require(const netsec_ptr& pointer, std::size_t bytes) {
    ::netsec_bounds_require(pointer, bytes);
}

struct object_scope {
    object_scope() = default;

    object_scope(void* base, std::size_t size)
        : object_(::netsec_register_object(base, size)), active_(true) {}

    object_scope(const object_scope&) = delete;
    object_scope& operator=(const object_scope&) = delete;

    object_scope(object_scope&& other) noexcept
        : object_(other.object_), active_(other.active_) {
        other.active_ = false;
    }

    object_scope& operator=(object_scope&& other) noexcept {
        if (this != &other) {
            release();
            object_ = other.object_;
            active_ = other.active_;
            other.active_ = false;
        }
        return *this;
    }

    ~object_scope() { release(); }

    void release() noexcept {
        if (active_) {
            ::netsec_unregister_object(object_.tag);
            active_ = false;
        }
    }

    bool active() const noexcept { return active_; }
    tag_t tag() const noexcept { return active_ ? object_.tag : no_tag; }
    const netsec_object& descriptor() const noexcept { return object_; }

    template <class T>
    tagged_ptr<T> as() const noexcept {
        return tagged_ptr<T>::from_object(object_);
    }

    template <class T>
    tagged_ptr<T> as_at(std::size_t byte_offset) const {
        return tagged_ptr<T>::from_object_at(object_, byte_offset);
    }

private:
    netsec_object object_{};
    bool active_ = false;
};

template <class T>
class tagged_span {
public:
    tagged_span() = default;

    tagged_span(tagged_ptr<T> pointer, std::size_t count)
        : pointer_(pointer), count_(count) {}

    tagged_ptr<T> pointer() const noexcept { return pointer_; }
    std::size_t size() const noexcept { return count_; }
    bool empty() const noexcept { return count_ == 0; }
    std::size_t size_bytes() const {
        return detail::checked_byte_count<T>(
            count_, pointer_.address(), "tagged span size");
    }
    std::uintptr_t address() const noexcept { return pointer_.address(); }

    void require() const {
        pointer_.require(detail::checked_byte_count<T>(
            count_, pointer_.address(), "tagged span size"));
    }

    T& operator[](std::size_t index) const {
        if (index >= count_) {
            ::netsec_raise_trap(NETSEC_TRAP_OUT_OF_BOUNDS,
                                pointer_.address(),
                                sizeof(T),
                                pointer_.tag(),
                                no_tag,
                                "tagged span index");
            std::terminate();
        }
        return pointer_[static_cast<std::ptrdiff_t>(index)];
    }

private:
    tagged_ptr<T> pointer_{};
    std::size_t count_ = 0;
};

template <class T>
tagged_span<T> make_span(tagged_ptr<T> pointer, std::size_t count) {
    return tagged_span<T>(pointer, count);
}

template <class T>
std::span<T> as_span(tagged_ptr<T> pointer, std::size_t count) {
    pointer.require(detail::checked_byte_count<T>(
        count, pointer.address(), "span conversion"));
    return std::span<T>(pointer.raw_address(), count);
}

/* Syscall-boundary revalidation. */
template <class T>
void syscall_revalidate(const tagged_ptr<T>& pointer,
                        std::size_t count,
                        bool kernel_writes) {
    const auto bytes = detail::checked_byte_count<T>(
        count, pointer.address(), "syscall range");
    ::netsec_syscall_revalidate(pointer.descriptor(),
                                bytes,
                                kernel_writes ? 1 : 0);
}

template <class T>
tagged_span<T> syscall_buffer(const tagged_ptr<T>& pointer,
                              std::size_t count,
                              bool kernel_writes) {
    syscall_revalidate(pointer, count, kernel_writes);
    return tagged_span<T>(pointer, count);
}

template <class T>
void copy_from_user(void* destination,
                    const tagged_ptr<T>& source,
                    std::size_t bytes) {
    ::netsec_copy_from_user(destination, source.descriptor(), bytes);
}

template <class T>
void copy_to_user(const tagged_ptr<T>& destination,
                  const void* source,
                  std::size_t bytes) {
    ::netsec_copy_to_user(destination.descriptor(), source, bytes);
}

inline std::uint64_t syscall_validation_count() noexcept {
    return ::netsec_syscall_validation_count();
}

}  // namespace netsec

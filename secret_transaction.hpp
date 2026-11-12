#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <new>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace netsec {

// Injectable seams: a host can route/observe every allocation, deallocation
// and zeroization. Implementations are kept alive for the whole transaction.

class IAllocator {
public:
    virtual ~IAllocator() = default;

    // Allocate exactly `bytes` bytes. nullptr is not allowed for bytes != 0;
    // failure must be reported by throwing std::bad_alloc.
    virtual std::byte* allocate(std::size_t bytes) = 0;

    // Each successful allocate() must be matched by exactly one
    // deallocate(). Must never throw.
    virtual void deallocate(std::byte* ptr, std::size_t bytes) noexcept = 0;
};

class IZeroizer {
public:
    virtual ~IZeroizer() = default;

    // Overwrite `bytes` at `ptr` with zero in a way the optimizer must not
    // elide. Must never throw.
    virtual void zeroize(std::byte* ptr, std::size_t bytes) noexcept = 0;
};

class DefaultAllocator final : public IAllocator {
public:
    std::byte* allocate(std::size_t bytes) override {
        return static_cast<std::byte*>(::operator new(bytes));
    }

    void deallocate(std::byte* ptr, std::size_t /*bytes*/) noexcept override {
        ::operator delete(ptr);
    }
};

class DefaultZeroizer final : public IZeroizer {
public:
    void zeroize(std::byte* ptr, std::size_t bytes) noexcept override {
        volatile std::byte* cursor = ptr;
        while (bytes-- != 0U) {
            *cursor++ = std::byte{0};
        }
    }
};

inline IAllocator& default_allocator() {
    static DefaultAllocator instance;
    return instance;
}

inline IZeroizer& default_zeroizer() {
    static DefaultZeroizer instance;
    return instance;
}

namespace detail {

inline std::shared_ptr<IAllocator> non_owning(IAllocator& allocator) noexcept {
    return std::shared_ptr<IAllocator>{&allocator, [](IAllocator*) noexcept {}};
}

inline std::shared_ptr<IZeroizer> non_owning(IZeroizer& zeroizer) noexcept {
    return std::shared_ptr<IZeroizer>{&zeroizer, [](IZeroizer*) noexcept {}};
}

}  // namespace detail

// SecretOwner: move-only RAII handle for a committed secret. The transaction
// never zeroizes committed bytes early: exactly one zeroize()+deallocate()
// happens when the final owner is destroyed.

class SecretOwner {
public:
    SecretOwner() noexcept = default;

    SecretOwner(const SecretOwner&) = delete;
    SecretOwner& operator=(const SecretOwner&) = delete;

    SecretOwner(SecretOwner&& other) noexcept
        : data_(other.data_),
          size_(other.size_),
          allocator_(std::move(other.allocator_)),
          zeroizer_(std::move(other.zeroizer_)) {
        other.release_refs();
    }

    SecretOwner& operator=(SecretOwner&& other) noexcept {
        if (this != &other) {
            destroy_owned();
            data_ = other.data_;
            size_ = other.size_;
            allocator_ = std::move(other.allocator_);
            zeroizer_ = std::move(other.zeroizer_);
            other.release_refs();
        }
        return *this;
    }

    ~SecretOwner() {
        destroy_owned();
    }

    std::byte* data() noexcept { return data_; }
    const std::byte* data() const noexcept { return data_; }
    std::size_t size() const noexcept { return size_; }
    bool empty() const noexcept { return data_ == nullptr; }

private:
    friend class SecretBuffer;

    SecretOwner(std::byte* data,
                std::size_t size,
                std::shared_ptr<IAllocator> allocator,
                std::shared_ptr<IZeroizer> zeroizer_in) noexcept
        : data_(data),
          size_(size),
          allocator_(std::move(allocator)),
          zeroizer_(std::move(zeroizer_in)) {}

    void destroy_owned() noexcept {
        if (data_ != nullptr) {
            if (zeroizer_) {
                zeroizer_->zeroize(data_, size_);
            }
            if (allocator_) {
                allocator_->deallocate(data_, size_);
            }
            release_refs();
        }
    }

    void release_refs() noexcept {
        data_ = nullptr;
        size_ = 0U;
    }

    std::byte* data_ = nullptr;
    std::size_t size_ = 0U;
    std::shared_ptr<IAllocator> allocator_;
    std::shared_ptr<IZeroizer> zeroizer_;
};

// SecretBuffer: move-only, non-copyable transactional holder of decoded
// secret bytes.
//
// Uncommitted: the buffer owns the bytes. Destruction, throwing construction
// unwind, or move-assignment over a live buffer zeroizes and frees exactly
// once.
// Committed: commit() hands a SecretOwner to the caller; the buffer becomes
// empty and never touches the bytes again. The owner performs the single
// zeroize()+deallocate().
//
// Borrows are restricted, bounds-checked, move-only views that register a
// lease. commit() and move-assignment are refused while any borrow (including
// nested ones) is alive.

class SecretBuffer {
public:
    class Borrow;

    SecretBuffer() noexcept = default;

    explicit SecretBuffer(std::size_t size,
                          std::shared_ptr<IAllocator> allocator = nullptr,
                          std::shared_ptr<IZeroizer> zeroizer = nullptr)
        : allocator_(allocator ? std::move(allocator)
                               : detail::non_owning(default_allocator())),
          zeroizer_(zeroizer ? std::move(zeroizer)
                             : detail::non_owning(default_zeroizer())) {
        if (size != 0U) {
            // If allocate() throws, the constructor exits with data_ ==
            // nullptr, so the destructor performs no extra cleanup: there
            // is exactly zero outstanding allocation.
            data_ = allocator_->allocate(size);
            size_ = size;
        }
    }

    SecretBuffer(const SecretBuffer&) = delete;
    SecretBuffer& operator=(const SecretBuffer&) = delete;

    // Steals every resource, including the lease counter. Precondition: no
    // borrow is held against `other`. noexcept keeps vector reallocation
    // move-based.
    SecretBuffer(SecretBuffer&& other) noexcept
        : data_(other.data_),
          size_(other.size_),
          allocator_(std::move(other.allocator_)),
          zeroizer_(std::move(other.zeroizer_)),
          active_borrows_(other.active_borrows_) {
        other.release_refs();
    }

    SecretBuffer& operator=(SecretBuffer&& other) {
        if (this != &other) {
            require_no_borrows("move assignment");
            reset_uncommitted();
            data_ = other.data_;
            size_ = other.size_;
            allocator_ = std::move(other.allocator_);
            zeroizer_ = std::move(other.zeroizer_);
            active_borrows_ = other.active_borrows_;
            other.release_refs();
        }
        return *this;
    }

    ~SecretBuffer() {
        reset_uncommitted();
    }

    // Hands ownership of the uncommitted bytes to the caller. Throws if the
    // buffer is empty or any borrow is still active. On success the buffer is
    // empty and will not touch the bytes again; the returned owner performs
    // the single zeroize()+deallocate().
    SecretOwner commit() {
        if (data_ == nullptr) {
            throw std::logic_error("SecretBuffer::commit() called on an empty buffer");
        }
        require_no_borrows("commit()");
        SecretOwner owner(data_, size_, std::move(allocator_), std::move(zeroizer_));
        release_refs();
        return owner;
    }

    // Opens a restricted window [offset, offset + length). Throws
    // std::out_of_range if the window exceeds the buffer without leasing.
    // Defined after Borrow is complete.
    Borrow borrow(std::size_t offset, std::size_t length);

    std::size_t size() const noexcept { return size_; }
    bool empty() const noexcept { return data_ == nullptr; }
    std::size_t active_borrows() const noexcept { return active_borrows_; }

    std::byte& at(std::size_t index) {
        check_index(index);
        return data_[index];
    }

    const std::byte& at(std::size_t index) const {
        check_index(index);
        return data_[index];
    }

    void write(std::size_t index, std::byte value) {
        check_index(index);
        data_[index] = value;
    }

private:
    friend class Borrow;

    void check_index(std::size_t index) const {
        if (data_ == nullptr) {
            throw std::logic_error("SecretBuffer access on empty buffer");
        }
        if (index >= size_) {
            throw std::out_of_range("SecretBuffer index out of range");
        }
    }

    void require_no_borrows(const char* operation) const {
        if (active_borrows_ != 0U) {
            throw std::logic_error(std::string("SecretBuffer::") + operation +
                                   " refused while a borrow is active");
        }
    }

    // Zeroizes then frees uncommitted storage exactly once.
    void reset_uncommitted() noexcept {
        if (data_ != nullptr) {
            if (zeroizer_) {
                zeroizer_->zeroize(data_, size_);
            }
            if (allocator_) {
                allocator_->deallocate(data_, size_);
            }
            release_refs();
        }
    }

    void release_refs() noexcept {
        data_ = nullptr;
        size_ = 0U;
        active_borrows_ = 0U;
    }

    std::byte* data_ = nullptr;
    std::size_t size_ = 0U;
    std::shared_ptr<IAllocator> allocator_;
    std::shared_ptr<IZeroizer> zeroizer_;
    std::size_t active_borrows_ = 0U;
};

// SecretBuffer::Borrow: move-only, bounds-checked view over a sub-window. The
// constructor registers a lease; the destructor releases it, so exceptions
// propagating through nested-borrow code always restore the lease count.
// Nested borrows address the root buffer with root-relative offsets.

class SecretBuffer::Borrow {
public:
    Borrow(const Borrow&) = delete;
    Borrow& operator=(const Borrow&) = delete;
    Borrow& operator=(Borrow&&) = delete;

    Borrow(Borrow&& other) noexcept
        : root_(other.root_),
          offset_(other.offset_),
          length_(other.length_) {
        // The single lease travels with the view; neutralize the
        // moved-from destructor.
        other.root_ = nullptr;
    }

    ~Borrow() {
        if (root_ != nullptr) {
            --root_->active_borrows_;
        }
    }

    // Derives a narrower window. Throws std::out_of_range without touching
    // any lease if the sub-window does not fit.
    Borrow sub_borrow(std::size_t relative_offset, std::size_t length) {
        ensure_live();
        if (relative_offset > length_ || length > length_ - relative_offset) {
            throw std::out_of_range("Borrow::sub_borrow() window exceeds parent window");
        }
        return Borrow(*root_, offset_ + relative_offset, length);
    }

    std::size_t size() const noexcept { return length_; }
    bool empty() const noexcept { return length_ == 0U; }

    std::byte& at(std::size_t index) {
        check_index(index);
        return root_->data_[offset_ + index];
    }

    const std::byte& at(std::size_t index) const {
        check_index(index);
        return root_->data_[offset_ + index];
    }

    void write(std::size_t index, std::byte value) {
        check_index(index);
        root_->data_[offset_ + index] = value;
    }

    // Valid only while this borrow and the root SecretBuffer are alive.
    std::byte* data() {
        ensure_live();
        return root_->data_ + offset_;
    }

    const std::byte* data() const {
        ensure_live();
        return root_->data_ + offset_;
    }

private:
    friend class SecretBuffer;

    Borrow(SecretBuffer& root, std::size_t offset, std::size_t length) noexcept
        : root_(&root),
          offset_(offset),
          length_(length) {
        ++root_->active_borrows_;
    }

    void ensure_live() const {
        if (root_ == nullptr) {
            throw std::logic_error("Borrow used after being moved from");
        }
    }

    void check_index(std::size_t index) const {
        ensure_live();
        if (index >= length_) {
            throw std::out_of_range("Borrow index out of range");
        }
    }

    SecretBuffer* root_ = nullptr;
    std::size_t offset_ = 0U;
    std::size_t length_ = 0U;
};

inline SecretBuffer::Borrow SecretBuffer::borrow(std::size_t offset,
                                                  std::size_t length) {
    if (data_ == nullptr) {
        throw std::logic_error("SecretBuffer::borrow() called on an empty buffer");
    }
    if (offset > size_ || length > size_ - offset) {
        throw std::out_of_range("SecretBuffer::borrow() window exceeds buffer");
    }
    return Borrow(*this, offset, length);
}

// SecretTransaction: an ordered sequence of SecretBuffer stages modelling a
// multi-stage decode. If add_stage() throws (the allocator refuses at any
// construction point), every previously built stage is still owned by the
// transaction and is unwound by the transaction destructor: each stage
// zeroizes and frees exactly once. A committed stage is emptied and performs
// no cleanup; its SecretOwner owns the bytes instead.
//
// Stage reallocation only moves empty/borrow-free buffers. Do not hold
// references or borrows across add_stage().

class SecretTransaction {
public:
    SecretTransaction() noexcept = default;

    SecretTransaction(const SecretTransaction&) = delete;
    SecretTransaction& operator=(const SecretTransaction&) = delete;
    SecretTransaction(SecretTransaction&&) noexcept = default;
    SecretTransaction& operator=(SecretTransaction&&) noexcept = default;
    ~SecretTransaction() = default;

    // Appends a stage. Throws if allocation fails; earlier stages survive and
    // remain owned until transaction destruction.
    SecretBuffer& add_stage(std::size_t size,
                            std::shared_ptr<IAllocator> allocator = nullptr,
                            std::shared_ptr<IZeroizer> zeroizer = nullptr) {
        stages_.emplace_back(size, std::move(allocator), std::move(zeroizer));
        return stages_.back();
    }

    std::size_t stage_count() const noexcept {
        return stages_.size();
    }

    SecretBuffer& stage(std::size_t index) {
        if (index >= stages_.size()) {
            throw std::out_of_range("SecretTransaction stage index out of range");
        }
        return stages_[index];
    }

    const SecretBuffer& stage(std::size_t index) const {
        if (index >= stages_.size()) {
            throw std::out_of_range("SecretTransaction stage index out of range");
        }
        return stages_[index];
    }

    // Commits one stage, returning its owner handle. Throws if the stage is
    // empty or has active borrows; other stages are unaffected.
    SecretOwner commit_stage(std::size_t index) {
        return stage(index).commit();
    }

private:
    std::vector<SecretBuffer> stages_;
};

}  // namespace netsec

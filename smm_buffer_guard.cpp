#include "smm_buffer_guard.hpp"

#include <algorithm>
#include <cstring>
#include <limits>
#include <new>

namespace netsec::smm {

namespace {

// Portable overflow-checked addition over the address width. Compiler
// builtins are used when available so the compiler cannot reason the overflow
// branch away; the fallback is the standard library checked operation.
bool add_overflow(uintptr_t base, std::size_t length, uintptr_t& end) noexcept {
#if defined(__GNUC__) || defined(__clang__)
    return __builtin_add_overflow(base, length, &end);
#else
    if (length > std::numeric_limits<uintptr_t>::max() - base) {
        return true;
    }
    end = base + length;
    return false;
#endif
}

// Byte copy that never dereferences a null source. Both regions have already
// been validated by the caller.
void copy_bytes(uint8_t* destination, uintptr_t source, std::size_t size) noexcept {
    if (size == 0) {
        return;
    }
    std::memcpy(static_cast<void*>(destination),
                reinterpret_cast<const void*>(source), size);
}

}  // namespace

bool Guid::operator==(const Guid& other) const noexcept {
    if (data1 != other.data1 || data2 != other.data2 || data3 != other.data3) {
        return false;
    }
    return std::memcmp(data4, other.data4, sizeof(data4)) == 0;
}

bool Guid::operator!=(const Guid& other) const noexcept {
    return !(*this == other);
}

const char* status_string(Status status) noexcept {
    switch (status) {
        case Status::Ok:
            return "Ok";
        case Status::InvalidArgument:
            return "InvalidArgument";
        case Status::Overflow:
            return "Overflow";
        case Status::InProtectedRange:
            return "InProtectedRange";
        case Status::TooLarge:
            return "TooLarge";
        case Status::TooSmall:
            return "TooSmall";
        case Status::MessageTooLong:
            return "MessageTooLong";
        case Status::GuidNotAllowed:
            return "GuidNotAllowed";
        case Status::AllocationFailed:
            return "AllocationFailed";
    }
    return "Unknown";
}

void* HeapStagingAllocator::allocate(std::size_t size) noexcept {
    return ::operator new(size, std::nothrow);
}

void HeapStagingAllocator::deallocate(void* ptr) noexcept {
    ::operator delete(ptr, std::nothrow);
}

bool checked_add_range(uintptr_t base, std::size_t length, uintptr_t& end) noexcept {
    if (base == 0 || length == 0) {
        return false;
    }
    return !add_overflow(base, length, end);
}

bool ranges_overlap(uintptr_t a_base, uintptr_t a_end,
                    uintptr_t b_base, uintptr_t b_end) noexcept {
    // Half-open ranges overlap only when each starts before the other ends.
    return a_base < b_end && b_base < a_end;
}

Status is_range_outside_protected(uintptr_t base,
                                  std::size_t length,
                                  std::size_t max_buffer_size,
                                  const MemoryRange* protected_ranges,
                                  std::size_t range_count) noexcept {
    if (base == 0 || length == 0) {
        return Status::InvalidArgument;
    }
    if (length > max_buffer_size) {
        return Status::TooLarge;
    }

    uintptr_t end = 0;
    if (add_overflow(base, length, end)) {
        return Status::Overflow;
    }

    for (std::size_t index = 0; index < range_count; ++index) {
        const MemoryRange& range = protected_ranges[index];
        uintptr_t protected_end = 0;
        // Configuration ranges were validated at construction; guard anyway
        // so a bad caller of this primitive cannot trigger an unchecked add.
        if (range.base == 0 || range.length == 0 ||
            add_overflow(range.base, range.length, protected_end)) {
            return Status::InvalidArgument;
        }
        if (ranges_overlap(base, end, range.base, protected_end)) {
            return Status::InProtectedRange;
        }
    }
    return Status::Ok;
}

// OwnedBuffer ----------------------------------------------------------------

OwnedBuffer::OwnedBuffer(IStagingAllocator* allocator, void* data,
                         std::size_t size) noexcept
    : allocator_(allocator), data_(static_cast<uint8_t*>(data)), size_(size) {}

OwnedBuffer::~OwnedBuffer() {
    reset();
}

OwnedBuffer::OwnedBuffer(OwnedBuffer&& other) noexcept
    : allocator_(other.allocator_),
      data_(other.data_),
      size_(other.size_) {
    other.allocator_ = nullptr;
    other.data_ = nullptr;
    other.size_ = 0;
}

OwnedBuffer& OwnedBuffer::operator=(OwnedBuffer&& other) noexcept {
    if (this != &other) {
        reset();
        allocator_ = other.allocator_;
        data_ = other.data_;
        size_ = other.size_;
        other.allocator_ = nullptr;
        other.data_ = nullptr;
        other.size_ = 0;
    }
    return *this;
}

void OwnedBuffer::reset() noexcept {
    if (data_ != nullptr && allocator_ != nullptr) {
        allocator_->deallocate(data_);
    }
    allocator_ = nullptr;
    data_ = nullptr;
    size_ = 0;
}

// Snapshot -------------------------------------------------------------------

Snapshot::Snapshot(OwnedBuffer storage, const Guid& guid,
                   std::size_t message_length) noexcept
    : storage_(std::move(storage)),
      guid_(guid),
      message_length_(message_length) {}

Span Snapshot::buffer() const noexcept {
    return Span(storage_.data(), storage_.size());
}

Span Snapshot::message() const noexcept {
    if (storage_.data() == nullptr || storage_.size() < kCommHeaderSize) {
        return Span();
    }
    return Span(storage_.data() + kCommHeaderSize, message_length_);
}

Status Snapshot::snapshot_nested(uintptr_t external_base,
                                 std::size_t external_length,
                                 OwnedBuffer& out_copy) const noexcept {
    if (guard_ == nullptr) {
        return Status::InvalidArgument;
    }

    // 1. Fully validate the embedded pointer's target before dereferencing.
    Status status = guard_->validate_external_range(external_base, external_length);
    if (status != Status::Ok) {
        return status;
    }

    // 2. Allocate private staging memory and perform the single copy. Any OS
    //    write after this point only affects the original, not this copy.
    void* private_copy = guard_->allocator_->allocate(external_length);
    if (private_copy == nullptr) {
        return Status::AllocationFailed;
    }
    copy_bytes(static_cast<uint8_t*>(private_copy), external_base,
               external_length);
    out_copy = OwnedBuffer(guard_->allocator_, private_copy, external_length);
    return Status::Ok;
}

// BufferGuard ----------------------------------------------------------------

Result<BufferGuard> BufferGuard::create(MemoryRange smram,
                                        std::vector<MemoryRange> protected_ranges,
                                        std::vector<Guid> allowed_guids,
                                        std::size_t max_buffer_size,
                                        IStagingAllocator* allocator) {
    if (max_buffer_size == 0) {
        return Result<BufferGuard>::fail(Status::InvalidArgument);
    }
    // Fail closed: a guard with no allowed GUID would have to reject every
    // request anyway, so refuse to construct one.
    if (allowed_guids.empty()) {
        return Result<BufferGuard>::fail(Status::InvalidArgument);
    }

    std::vector<MemoryRange> all_ranges;
    all_ranges.reserve(protected_ranges.size() + 1);
    all_ranges.push_back(smram);
    for (const MemoryRange& range : protected_ranges) {
        all_ranges.push_back(range);
    }

    // Validate every protected range and reject overlapping/overflowing
    // configuration so the hot path only needs overlap tests.
    for (const MemoryRange& range : all_ranges) {
        uintptr_t end = 0;
        if (range.base == 0 || range.length == 0 ||
            add_overflow(range.base, range.length, end)) {
            return Result<BufferGuard>::fail(Status::InvalidArgument);
        }
    }
    std::sort(all_ranges.begin(), all_ranges.end(),
              [](const MemoryRange& lhs, const MemoryRange& rhs) {
                  return lhs.base < rhs.base;
              });
    for (std::size_t index = 1; index < all_ranges.size(); ++index) {
        uintptr_t previous_end = 0;
        uintptr_t current_end = 0;
        add_overflow(all_ranges[index - 1].base, all_ranges[index - 1].length,
                     previous_end);
        add_overflow(all_ranges[index].base, all_ranges[index].length, current_end);
        if (ranges_overlap(all_ranges[index - 1].base, previous_end,
                           all_ranges[index].base, current_end)) {
            return Result<BufferGuard>::fail(Status::InvalidArgument);
        }
    }

    BufferGuard guard;
    guard.smram_ = smram;
    guard.protected_ranges_ = std::move(all_ranges);
    guard.allowed_guids_ = std::move(allowed_guids);
    guard.max_buffer_size_ = max_buffer_size;
    if (allocator != nullptr) {
        guard.allocator_ = allocator;
    } else {
        guard.owned_default_allocator_ = std::make_unique<HeapStagingAllocator>();
        guard.allocator_ = guard.owned_default_allocator_.get();
    }
    return Result<BufferGuard>(std::move(guard));
}

Status BufferGuard::validate_external_range(uintptr_t base,
                                            std::size_t length) const noexcept {
    return is_range_outside_protected(base, length, max_buffer_size_,
                                      protected_ranges_.data(),
                                      protected_ranges_.size());
}

Result<Snapshot> BufferGuard::accept(uintptr_t external_base,
                                     std::size_t external_length) const noexcept {
    if (allocator_ == nullptr) {
        return Result<Snapshot>::fail(Status::InvalidArgument);
    }

    // 1. Validate the whole OS buffer up front: null/zero, max size,
    //    overflow-safe end, and full exclusion from protected ranges.
    Status status = validate_external_range(external_base, external_length);
    if (status != Status::Ok) {
        return Result<Snapshot>::fail(status);
    }

    // 2. Allocate private SMRAM staging memory and copy the ENTIRE buffer
    //    before parsing any field. This is the TOCTOU boundary: every later
    //    read in this function and in the handler comes from the snapshot.
    void* private_bytes = allocator_->allocate(external_length);
    if (private_bytes == nullptr) {
        return Result<Snapshot>::fail(Status::AllocationFailed);
    }
    OwnedBuffer staged(allocator_, private_bytes, external_length);
    copy_bytes(staged.mutable_data(), external_base, external_length);

    // 3. Header fit check.
    if (external_length < kCommHeaderSize) {
        return Result<Snapshot>::fail(Status::TooSmall);
    }

    // 4. Parse only the private copy.
    Guid staged_guid;
    std::memcpy(&staged_guid, staged.data(), sizeof(Guid));
    std::size_t staged_message_length = 0;
    std::memcpy(&staged_message_length,
                staged.data() + offsetof(CommHeader, message_length),
                sizeof(staged_message_length));

    // A zero-length message carries no handler payload.
    if (staged_message_length == 0) {
        return Result<Snapshot>::fail(Status::InvalidArgument);
    }

    // message_length must not exceed (buffer size - header size).
    const std::size_t available = external_length - kCommHeaderSize;
    if (staged_message_length > available) {
        return Result<Snapshot>::fail(Status::MessageTooLong);
    }

    // 5. GUID whitelist; unknown GUIDs are dropped.
    const bool guid_allowed =
        std::any_of(allowed_guids_.begin(), allowed_guids_.end(),
                    [&staged_guid](const Guid& allowed) {
                        return allowed == staged_guid;
                    });
    if (!guid_allowed) {
        return Result<Snapshot>::fail(Status::GuidNotAllowed);
    }

    Snapshot snapshot(std::move(staged), staged_guid, staged_message_length);
    snapshot.guard_ = this;
    return Result<Snapshot>(std::move(snapshot));
}

}  // namespace netsec::smm

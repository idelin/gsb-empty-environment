// SPDX-License-Identifier: BSD-2-Clause-Patent
// SMI communication-buffer validation library for C++17 MM services.
//
// Threat model
// ------------
// The communication buffer handed to an SMI handler lives in untrusted OS
// memory. The OS may:
//   * point embedded pointers at SMRAM or other protected ranges;
//   * supply lengths that wrap around when added to the base address;
//   * mutate buffer contents after validation but before the handler reads
//     them (TOCTOU);
//   * place nested pointers and out-of-range lengths inside the payload.
//
// Defence implemented here
// -------------------------
//   1. Every external range (the whole communication buffer plus every
//      embedded/nested pointer) is proven to be fully outside SMRAM and all
//      configured protected ranges before it is dereferenced.
//   2. base + length is computed with overflow-checked arithmetic.
//   3. The entire communication buffer is copied into private SMRAM staging
//      memory before any field is parsed; the handler works only on the
//      private snapshot, so post-validation writes by the OS cannot affect it.
//   4. Header/message lengths are checked against the staged buffer size.
//   5. The message GUID must be on the handler whitelist or the request is
//      dropped (fail-closed: an empty whitelist rejects everything).
#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <vector>

namespace netsec::smm {

// 16-byte EFI_GUID layout, standard-layout so it can be used against the raw
// communication-buffer bytes. Matches { uint32_t, uint16_t, uint16_t,
// uint8_t[8] }.
struct Guid {
    uint32_t data1;
    uint16_t data2;
    uint16_t data3;
    uint8_t data4[8];

    bool operator==(const Guid& other) const noexcept;
    bool operator!=(const Guid& other) const noexcept;
};

// Half-open physical address range [base, base + length).
struct MemoryRange {
    uintptr_t base = 0;
    std::size_t length = 0;
};

enum class Status {
    Ok = 0,
    InvalidArgument,   // Null/zero-length external range or bad configuration.
    Overflow,          // base + length wraps the address space.
    InProtectedRange,  // Range intersects SMRAM or another protected range.
    TooLarge,          // Range exceeds the configured maximum buffer size.
    TooSmall,          // Buffer cannot hold the declared header.
    MessageTooLong,    // Message length exceeds buffer size minus header.
    GuidNotAllowed,    // Message GUID is not on the handler whitelist.
    AllocationFailed,  // Private SMRAM staging memory unavailable.
};

const char* status_string(Status status) noexcept;

// Non-owning view over bytes held inside the private SMRAM snapshot.
class Span {
public:
    Span() noexcept = default;
    Span(const uint8_t* data, std::size_t size) noexcept : data_(data), size_(size) {}

    const uint8_t* data() const noexcept { return data_; }
    std::size_t size() const noexcept { return size_; }
    bool empty() const noexcept { return size_ == 0; }

private:
    const uint8_t* data_ = nullptr;
    std::size_t size_ = 0;
};

// MM communication header, mirroring EFI_MM_COMMUNICATE_HEADER. Only the
// header size is architecture dependent; the fields themselves are fixed.
struct CommHeader {
    Guid header_guid;
    std::size_t message_length;
    uint8_t data[1];
};

inline constexpr std::size_t kCommHeaderSize = offsetof(CommHeader, data);

// Staging allocator: carves private SMRAM for the snapshot. The firmware
// integration implements this over SmmAllocatePool / SmiMalloc; the host
// implementation below uses regular heap memory and is suitable for unit
// tests only.
class IStagingAllocator {
public:
    virtual ~IStagingAllocator() = default;
    // Returns nullptr on failure; must never throw.
    virtual void* allocate(std::size_t size) noexcept = 0;
    virtual void deallocate(void* ptr) noexcept = 0;
};

// Host/test allocator. Do NOT use this in an SMI handler build.
class HeapStagingAllocator final : public IStagingAllocator {
public:
    void* allocate(std::size_t size) noexcept override;
    void deallocate(void* ptr) noexcept override;
};

// Move-only ownership of a private SMRAM staging allocation.
class OwnedBuffer {
public:
    OwnedBuffer() noexcept = default;
    OwnedBuffer(IStagingAllocator* allocator, void* data, std::size_t size) noexcept;
    ~OwnedBuffer();

    OwnedBuffer(const OwnedBuffer&) = delete;
    OwnedBuffer& operator=(const OwnedBuffer&) = delete;
    OwnedBuffer(OwnedBuffer&& other) noexcept;
    OwnedBuffer& operator=(OwnedBuffer&& other) noexcept;

    const uint8_t* data() const noexcept { return data_; }
    uint8_t* mutable_data() noexcept { return data_; }
    std::size_t size() const noexcept { return size_; }

private:
    void reset() noexcept;

    IStagingAllocator* allocator_ = nullptr;
    uint8_t* data_ = nullptr;
    std::size_t size_ = 0;
};

class BufferGuard;

// Parsed, private copy of a validated communication buffer. All views point
// into SMRAM staging memory owned by this object; the OS can no longer touch
// the bytes after Accept() returns.
class Snapshot {
public:
    Snapshot() noexcept = default;
    Snapshot(OwnedBuffer storage, const Guid& guid, std::size_t message_length) noexcept;

    Snapshot(Snapshot&&) noexcept = default;
    Snapshot& operator=(Snapshot&&) noexcept = default;
    Snapshot(const Snapshot&) = delete;
    Snapshot& operator=(const Snapshot&) = delete;

    const Guid& guid() const noexcept { return guid_; }
    std::size_t message_length() const noexcept { return message_length_; }

    // Whole staged communication buffer.
    Span buffer() const noexcept;
    // Message bytes that follow the header.
    Span message() const noexcept;

    // Re-validate an embedded/nested pointer found inside the staged message
    // and copy its target into a fresh private staging buffer. The OS-side
    // range is fully range-checked (overflow + protected ranges) before the
    // single copy, and the returned copy is immune to later OS writes.
    Status snapshot_nested(uintptr_t external_base,
                           std::size_t external_length,
                           OwnedBuffer& out_copy) const noexcept;

private:
    friend class BufferGuard;

    OwnedBuffer storage_;
    Guid guid_{};
    std::size_t message_length_ = 0;
    const BufferGuard* guard_ = nullptr;
};

// Minimal value-or-error result used instead of throwing across the SMI
// boundary.
template <typename T>
class Result {
public:
    Result(T value) : value_(std::move(value)), status_(Status::Ok) {}
    static Result fail(Status status) { return Result(status); }

    bool ok() const noexcept { return status_ == Status::Ok; }
    explicit operator bool() const noexcept { return ok(); }
    Status status() const noexcept { return status_; }

    T& value() & { return *value_; }
    const T& value() const& { return *value_; }
    T&& value() && { return std::move(*value_); }

private:
    explicit Result(Status status) : status_(status) {}

    std::optional<T> value_;
    Status status_;
};

class BufferGuard {
public:
    friend class Snapshot;

    // Builds a guard. Fails (fail-closed) on an empty GUID whitelist or on a
    // malformed protected range (zero length, overflow, overlapping entries).
    static Result<BufferGuard> create(
        MemoryRange smram,
        std::vector<MemoryRange> protected_ranges,
        std::vector<Guid> allowed_guids,
        std::size_t max_buffer_size,
        IStagingAllocator* allocator = nullptr);

    // Validates and stages one communication buffer:
    //   1. overflow-checked range validation of the full OS buffer;
    //   2. whole-buffer copy into private SMRAM staging memory;
    //   3. header/message length checks against the staged size;
    //   4. GUID whitelist check.
    // No field of the OS buffer is parsed until the private copy exists.
    Result<Snapshot> accept(uintptr_t external_base,
                            std::size_t external_length) const noexcept;

    // Validates an arbitrary OS-supplied pointer/length pair without copying
    // it. Use snapshot_nested() on Snapshot for the copy-in variant.
    Status validate_external_range(uintptr_t base, std::size_t length) const noexcept;

    std::size_t max_buffer_size() const noexcept { return max_buffer_size_; }

private:
    BufferGuard() = default;

    MemoryRange smram_{};
    std::vector<MemoryRange> protected_ranges_;
    std::vector<Guid> allowed_guids_;
    std::size_t max_buffer_size_ = 0;
    IStagingAllocator* allocator_ = nullptr;
    std::unique_ptr<IStagingAllocator> owned_default_allocator_;
};

// Overflow-checked base + length. Returns false if the addition wraps the
// address space or the base is null; on success writes the exclusive end.
bool checked_add_range(uintptr_t base,
                       std::size_t length,
                       uintptr_t& end) noexcept;

// True when half-open ranges [a_base, a_end) and [b_base, b_end) overlap.
// Adjacent ranges (a_end == b_base) do not overlap.
bool ranges_overlap(uintptr_t a_base, uintptr_t a_end,
                    uintptr_t b_base, uintptr_t b_end) noexcept;

// Primitive range check against an explicit protected-range list. Exposed for
// unit tests and for handlers that need a stateless check.
Status is_range_outside_protected(uintptr_t base,
                                  std::size_t length,
                                  std::size_t max_buffer_size,
                                  const MemoryRange* protected_ranges,
                                  std::size_t range_count) noexcept;

}  // namespace netsec::smm

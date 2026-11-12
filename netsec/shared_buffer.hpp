#pragma once

// Embeddable, header-only C++17 component for a Trusted World (Secure Side)
// TA that consumes request buffers living in attacker-controlled Normal World
// (NW) shared memory.
//
// Threat model and guarantees
// ---------------------------
// The NW owns the physical backing of the shared window and can rewrite any
// byte at any time, including between two reads performed by the TA
// (TOCTOU). The TA therefore must never:
//   * trust a length/offset read a second time after validating it,
//   * dereference a NW pointer directly, or
//   * let a NW-supplied reference alias a Secure World memory range.
//
// This component enforces:
//   1. Reference validation before any copy: required alignment, no integer
//      overflow/wraparound (offset+size and absolute base+size), containment
//      in the configured shared window, and disjointness from every Secure
//      World range.
//   2. Single snapshot copy: validated NW data is copied exactly once into a
//      Secure World buffer; all subsequent processing uses that copy and
//      never re-reads the NW.
//   3. Bounded output writeback: an output window is validated once against
//      the caller-declared capacity; every response length is clamped so a
//      write can never exceed the declared capacity.
//
// All NW access goes through the INormalWorldMemory indirection so a memory
// hook can be injected for testing (and so a real TA can route reads/writes
// through its TEE driver / cache maintenance helpers).

#include <cstddef>
#include <cstdint>
#include <cstring>
#include <limits>
#include <new>
#include <stdexcept>
#include <type_traits>
#include <vector>

namespace netsec {

enum class Error {
    kOk = 0,
    kMisaligned,        // reference does not meet the required alignment
    kWraparound,        // offset+size or base+size overflows the address space
    kOutOfWindow,       // reference is not fully inside the shared window
    kOverlapsSecure,    // reference touches a Secure World range
    kSizeTooLarge,      // size exceeds the policy maximum object size
    kInvalidPolicy,     // misconfigured policy (bad window / alignment)
    kWorldAccessFault,  // injected or real NW access failure
    kOutOfSecureMemory, // Secure World snapshot allocation failed
    kCapacityExceeded,  // fixed-buffer snapshot target too small
    kBadObject,         // POD view/parse of the secure copy failed
};

inline const char* ToString(Error e) noexcept {
    switch (e) {
        case Error::kOk: return "ok";
        case Error::kMisaligned: return "misaligned reference";
        case Error::kWraparound: return "address arithmetic wraparound";
        case Error::kOutOfWindow: return "reference outside shared window";
        case Error::kOverlapsSecure: return "reference overlaps secure memory";
        case Error::kSizeTooLarge: return "size exceeds maximum object size";
        case Error::kInvalidPolicy: return "invalid memory policy";
        case Error::kWorldAccessFault: return "normal world access fault";
        case Error::kOutOfSecureMemory: return "out of secure memory";
        case Error::kCapacityExceeded: return "secure destination capacity exceeded";
        case Error::kBadObject: return "malformed object in secure copy";
    }
    return "unknown error";
}

// Minimal value-or-error result. On failure `value` is intentionally absent.
template <typename T>
struct Result {
    Error error = Error::kOk;
    T value{};

    constexpr bool ok() const noexcept { return error == Error::kOk; }
    constexpr explicit operator bool() const noexcept { return ok(); }

    static Result success(T v) { return Result{Error::kOk, std::move(v)}; }
    static Result failure(Error e) { return Result{e, T{}}; }
};

template <>
struct Result<void> {
    Error error = Error::kOk;

    constexpr bool ok() const noexcept { return error == Error::kOk; }
    constexpr explicit operator bool() const noexcept { return ok(); }

    static Result success() { return Result{Error::kOk}; }
    static Result failure(Error e) { return Result{e}; }
};

// Half-open address interval [base, base+size). Empty ranges overlap nothing.
struct Range {
    std::uintptr_t base = 0;
    std::size_t size = 0;

    static bool overlaps(const Range& a, const Range& b) noexcept {
        if (a.size == 0 || b.size == 0) return false;
        // Both ranges are already known not to wrap, so plain half-open
        // interval comparison is safe.
        return a.base < (b.base + b.size) && b.base < (a.base + a.size);
    }
};

// Abstraction over attacker-controlled memory. A production TA implements
// this over its shared-memory mapping (with any required cache coherency
// operations). Tests inject a hooked implementation.
class INormalWorldMemory {
public:
    virtual ~INormalWorldMemory() = default;

    // Copies `size` bytes from NW address `src` into the Secure World
    // destination `dst`. Returns false on an access fault. `dst` always
    // points into Secure World memory.
    virtual bool read(void* dst, std::uintptr_t src, std::size_t size) noexcept = 0;

    // Copies `size` bytes from the Secure World source `src` into NW address
    // `dst`. Returns false on an access fault.
    virtual bool write(std::uintptr_t dst, const void* src,
                      std::size_t size) noexcept = 0;
};

// Immutable configuration describing the shared window, the Secure World
// ranges that must never be reachable from a NW reference, and the alignment
// contract imposed on references.
class MemoryPolicy {
public:
    MemoryPolicy(Range shared_window,
                 std::vector<Range> secure_ranges,
                 std::size_t alignment = sizeof(std::uint64_t),
                 std::size_t max_object_size = (std::size_t{1} << 30))
        : shared_window_(shared_window),
          secure_ranges_(std::move(secure_ranges)),
          alignment_(alignment),
          max_object_size_(max_object_size) {
        if (alignment_ == 0 || (alignment_ & (alignment_ - 1)) != 0) {
            throw std::invalid_argument("alignment must be a non-zero power of two");
        }
        if (range_wraps(shared_window_)) {
            throw std::invalid_argument("shared window wraps the address space");
        }
        for (const Range& r : secure_ranges_) {
            if (range_wraps(r)) {
                throw std::invalid_argument("secure range wraps the address space");
            }
        }
    }

    std::size_t alignment() const noexcept { return alignment_; }
    std::size_t maxObjectSize() const noexcept { return max_object_size_; }
    const Range& sharedWindow() const noexcept { return shared_window_; }
    const std::vector<Range>& secureRanges() const noexcept { return secure_ranges_; }

    // Overflow-checked addition, exposed so embedding code and tests use the
    // same wraparound-safe primitive as the policy itself.
    template <typename UInt>
    static bool add_overflow(UInt a, UInt b, UInt& out) noexcept {
#if defined(__GNUC__) || defined(__clang__)
        return __builtin_add_overflow(a, b, &out);
#else
        if (a > (std::numeric_limits<UInt>::max)() - b) return true;
        out = a + b;
        return false;
#endif
    }

    // Validate a reference expressed as (offset, size) relative to the shared
    // window base. Pure arithmetic: no NW memory is touched here.
    Result<void> validateReference(std::uint64_t offset,
                                   std::uint64_t size) const noexcept {
        std::uint64_t absolute_base = 0;
        std::uint64_t end_offset = 0;
        if (add_overflow(static_cast<std::uint64_t>(shared_window_.base),
                         offset, absolute_base) ||
            add_overflow(offset, size, end_offset)) {
            return Result<void>::failure(Error::kWraparound);
        }
        if (size > max_object_size_) {
            return Result<void>::failure(Error::kSizeTooLarge);
        }
        return validateRange(absolute_base, static_cast<std::size_t>(size));
    }

    // Validate a reference expressed as an absolute NW address and size.
    Result<void> validateAbsolute(std::uintptr_t base,
                                  std::size_t size) const noexcept {
        if (size > max_object_size_) {
            return Result<void>::failure(Error::kSizeTooLarge);
        }
        return validateRange(base, size);
    }

    // Clamp a requested response length to the caller-declared capacity.
    // Used for every length writeback so the output can never run past the
    // capacity the caller explicitly declared.
    std::size_t clampedLength(std::size_t requested,
                              std::size_t declared_capacity) const noexcept {
        return requested > declared_capacity ? declared_capacity : requested;
    }

private:
    static bool range_wraps(const Range& r) noexcept {
        std::uintptr_t end = 0;
        return add_overflow(r.base, r.size, end);
    }

    Result<void> validateRange(std::uintptr_t base,
                               std::size_t size) const noexcept {
        if (size == 0) {
            // An empty reference carries no data and cannot alias anything.
            return Result<void>::success();
        }

        // Alignment of the reference start.
        if ((base & (alignment_ - 1)) != 0) {
            return Result<void>::failure(Error::kMisaligned);
        }

        std::uintptr_t end = 0;
        if (add_overflow(base, size, end)) {
            return Result<void>::failure(Error::kWraparound);
        }

        // Containment in the shared window.
        if (base < shared_window_.base || end > shared_window_.base + shared_window_.size) {
            return Result<void>::failure(Error::kOutOfWindow);
        }

        // Defense in depth: reject anything that would touch Secure World.
        const Range candidate{base, size};
        for (const Range& secure : secure_ranges_) {
            if (Range::overlaps(candidate, secure)) {
                return Result<void>::failure(Error::kOverlapsSecure);
            }
        }
        return Result<void>::success();
    }

    Range shared_window_;
    std::vector<Range> secure_ranges_;
    std::size_t alignment_;
    std::size_t max_object_size_;
};

// Non-owning view of bytes held in Secure World memory. It deliberately has
// no path back to the NW: once a snapshot is taken only these secure bytes
// are ever consulted.
struct SecureSpan {
    const std::uint8_t* data = nullptr;
    std::size_t size = 0;

    const std::uint8_t* begin() const noexcept { return data; }
    const std::uint8_t* end() const noexcept { return data + size; }

    // Typed, alignment-free view of a POD prefix of the secure copy. Returns
    // kBadObject rather than performing an unaligned/aliasing reinterpret.
    template <typename T>
    Result<T> viewAs() const noexcept {
        static_assert(std::is_trivially_copyable_v<T>,
                      "viewAs requires a trivially copyable type");
        if (size < sizeof(T)) {
            return Result<T>::failure(Error::kBadObject);
        }
        T object{};
        std::memcpy(&object, data, sizeof(T));
        return Result<T>::success(object);
    }
};

// Owning Secure World copy of one validated NW object. The NW source is read
// exactly once; the owning storage is never aliased to NW memory.
class SecureSnapshot {
public:
    SecureSnapshot() = default;

    // Validate (offset, size) against the policy and perform the single copy.
    static Result<SecureSnapshot> take(const MemoryPolicy& policy,
                                       INormalWorldMemory& world,
                                       std::uint64_t offset,
                                       std::uint64_t size) noexcept {
        Result<void> check = policy.validateReference(offset, size);
        if (!check.ok()) {
            return Result<SecureSnapshot>::failure(check.error);
        }
        if (size == 0) {
            return Result<SecureSnapshot>::success(SecureSnapshot{});
        }

        SecureSnapshot snapshot;
        try {
            snapshot.bytes_.resize(static_cast<std::size_t>(size));
        } catch (const std::bad_alloc&) {
            return Result<SecureSnapshot>::failure(Error::kOutOfSecureMemory);
        }

        const std::uintptr_t source =
            policy.sharedWindow().base + static_cast<std::uintptr_t>(offset);
        if (!world.read(snapshot.bytes_.data(), source, snapshot.bytes_.size())) {
            return Result<SecureSnapshot>::failure(Error::kWorldAccessFault);
        }
        return Result<SecureSnapshot>::success(std::move(snapshot));
    }

    // Zero-allocation variant for pre-reserved Secure World buffers. Fails
    // with kCapacityExceeded instead of allocating.
    static Result<SecureSpan> takeInto(const MemoryPolicy& policy,
                                       INormalWorldMemory& world,
                                       std::uint64_t offset,
                                       std::uint64_t size,
                                       void* secure_dst,
                                       std::size_t secure_capacity) noexcept {
        Result<void> check = policy.validateReference(offset, size);
        if (!check.ok()) {
            return Result<SecureSpan>::failure(check.error);
        }
        if (static_cast<std::uint64_t>(secure_capacity) < size) {
            return Result<SecureSpan>::failure(Error::kCapacityExceeded);
        }
        if (size == 0) {
            return Result<SecureSpan>::success(SecureSpan{});
        }
        const std::uintptr_t source =
            policy.sharedWindow().base + static_cast<std::uintptr_t>(offset);
        if (!world.read(secure_dst, source, static_cast<std::size_t>(size))) {
            return Result<SecureSpan>::failure(Error::kWorldAccessFault);
        }
        return Result<SecureSpan>::success(
            SecureSpan{static_cast<const std::uint8_t*>(secure_dst),
                       static_cast<std::size_t>(size)});
    }

    SecureSpan span() const noexcept {
        return SecureSpan{bytes_.data(), bytes_.size()};
    }
    const std::uint8_t* data() const noexcept { return bytes_.data(); }
    std::size_t size() const noexcept { return bytes_.size(); }

private:
    std::vector<std::uint8_t> bytes_;
};

// Bounded, validated output channel back into NW shared memory. The output
// window is validated once at bind() time against the caller-declared
// capacity; every write is length-checked and clamped so writeback can never
// exceed that capacity.
class OutputWriter {
public:
    OutputWriter() = default;

    static Result<OutputWriter> bind(const MemoryPolicy& policy,
                                     INormalWorldMemory& world,
                                     std::uint64_t offset,
                                     std::uint64_t declared_capacity) noexcept {
        // The declared capacity is itself a NW reference and must survive the
        // same alignment / wraparound / disjointness checks.
        Result<void> check = policy.validateReference(offset, declared_capacity);
        if (!check.ok()) {
            return Result<OutputWriter>::failure(check.error);
        }
        OutputWriter writer;
        writer.policy_ = &policy;
        writer.world_ = &world;
        writer.base_ =
            policy.sharedWindow().base + static_cast<std::uintptr_t>(offset);
        writer.capacity_ = static_cast<std::size_t>(declared_capacity);
        writer.used_ = 0;
        return Result<OutputWriter>::success(std::move(writer));
    }

    std::size_t capacity() const noexcept { return capacity_; }
    std::size_t used() const noexcept { return used_; }

    // Fail-closed write: if the payload does not fit within the remaining
    // declared capacity, nothing is written and kCapacityExceeded is returned.
    Result<void> write(const void* secure_src, std::size_t size) noexcept {
        if (size > capacity_ - used_) {
            return Result<void>::failure(Error::kCapacityExceeded);
        }
        if (size != 0 &&
            !world_->write(base_ + used_, secure_src, size)) {
            return Result<void>::failure(Error::kWorldAccessFault);
        }
        used_ += size;
        return Result<void>::success();
    }

    // Convenience for a response whose length the NW attacker tries to
    // inflate: the amount written is clamped to the declared capacity. Use
    // write() when a too-large response must instead be rejected.
    std::size_t writeClamped(const void* secure_src,
                             std::size_t requested_size) noexcept {
        const std::size_t allowed =
            policy_->clampedLength(requested_size, capacity_ - used_);
        if (allowed != 0 && !world_->write(base_ + used_, secure_src, allowed)) {
            return 0;
        }
        used_ += allowed;
        return allowed;
    }

private:
    const MemoryPolicy* policy_ = nullptr;
    INormalWorldMemory* world_ = nullptr;
    std::uintptr_t base_ = 0;
    std::size_t capacity_ = 0;
    std::size_t used_ = 0;
};

// Explicit little-endian decoders for the secure copy. Parsing byte-by-byte
// avoids unaligned access and strict-aliasing issues regardless of the TA
// architecture.
inline std::uint32_t loadLe32(const std::uint8_t* p) noexcept {
    return (std::uint32_t)p[0] | (std::uint32_t(p[1]) << 8) |
           (std::uint32_t(p[2]) << 16) | (std::uint32_t(p[3]) << 24);
}

inline std::uint64_t loadLe64(const std::uint8_t* p) noexcept {
    std::uint64_t low = loadLe32(p);
    std::uint64_t high = loadLe32(p + 4);
    return low | (high << 32);
}

// Example fixed wire header of a TA request, 32 bytes, little endian. It only
// carries attacker-controlled offsets/sizes; all fields are decoded from the
// secure snapshot, never re-read from the NW.
struct TaRequestHeader {
    std::uint32_t magic = 0;
    std::uint32_t flags = 0;
    std::uint64_t payload_offset = 0;
    std::uint64_t payload_size = 0;
    std::uint64_t response_capacity = 0;

    // Wire layout: magic@0 (u32), flags@4 (u32), payload_offset@8 (u64),
    // payload_size@16 (u64), response_capacity@24 (u64).
    static constexpr std::size_t kWireSize = 32;
    static constexpr std::uint32_t kMagic = 0x54415245; // "ERAT"

    static Result<TaRequestHeader> decode(SecureSpan secure) noexcept {
        if (secure.size < kWireSize) {
            return Result<TaRequestHeader>::failure(Error::kBadObject);
        }
        const std::uint8_t* p = secure.data;
        TaRequestHeader header;
        header.magic = loadLe32(p);
        header.flags = loadLe32(p + 4);
        header.payload_offset = loadLe64(p + 8);
        header.payload_size = loadLe64(p + 16);
        header.response_capacity = loadLe64(p + 24);
        if (header.magic != kMagic) {
            return Result<TaRequestHeader>::failure(Error::kBadObject);
        }
        return Result<TaRequestHeader>::success(header);
    }
};

} // namespace netsec

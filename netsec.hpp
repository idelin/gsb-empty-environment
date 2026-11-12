#pragma once
#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <unordered_map>
#include <vector>

namespace netsec {

// Monotonic identifier of an immutable file generation. Every lease is bound
// to exactly one version; replacing or truncating a file publishes a new
// version and never mutates data reachable through existing leases.
using VersionId = std::uint64_t;

enum class PageStatus {
    Ok,          // The full page (or its valid tail inside the file) was read.
    Unavailable, // The page is outside the current valid file range.
    IoError,     // The backing source failed; caller may retry later.
};

struct PageRead {
    PageStatus status = PageStatus::Unavailable;
    // Number of bytes actually written into dst. Valid only when status is Ok.
    // A short read on the final in-range page reflects the file tail.
    std::size_t bytes = 0;
};

// Injectable data source. Implementations wrap mmap, pread, network stores,
// fault-injecting fakes, etc. The library never dereferences a mapping itself:
// bytes only ever cross this boundary by being copied into a caller buffer.
//
// offset is always page aligned; dst is valid for dst_size bytes.
// Implementations must be safe to call concurrently.
class IPageReader {
public:
    virtual ~IPageReader() = default;
    virtual PageRead readPage(VersionId version,
                              std::uint64_t offset,
                              std::byte* dst,
                              std::size_t dst_size) = 0;
};

enum class ReadOutcome {
    Complete,   // Every requested byte was copied.
    Truncated,  // Hit EOF / an unavailable page; prefix holds valid bytes.
    ReadError,  // Backing I/O failure; prefix holds bytes read before it.
    Cancelled,  // Cancellation observed; prefix holds bytes already copied.
    BadRange,   // offset + len overflowed or len was zero.
    Expired,    // The lease was released.
};

struct ReadResult {
    ReadOutcome outcome = ReadOutcome::ReadError;
    std::size_t bytes = 0; // Exact prefix length delivered into the buffer.
};

// A read-only, fixed-version lease over a byte range.
//
// Pages fetched through a lease are pinned for its whole lifetime, so cache
// eviction and volume-level version replacement cannot invalidate in-flight
// reads or leave a dangling reference. The lease is movable but not copyable.
class Lease {
public:
    Lease() = default;
    ~Lease();
    Lease(Lease&& other) noexcept;
    Lease& operator=(Lease&& other) noexcept;
    Lease(const Lease&) = delete;
    Lease& operator=(const Lease&) = delete;

    // Copies up to len bytes starting at absolute file offset into dst.
    // Reads crossing page boundaries advance page by page; on any failure the
    // result reports the exact prefix already copied. dst must hold len bytes.
    ReadResult read(std::uint64_t offset, void* dst, std::size_t len);

    // Requests cancellation. Already running reads stop at the next page
    // boundary and return their completed prefix; subsequent reads fail with
    // Cancelled without touching the backing source.
    void cancel();
    bool cancelled() const;
    bool valid() const;
    void release();

    VersionId version() const;
    std::uint64_t start() const;
    std::uint64_t end() const; // Exclusive.

    class Impl;
    explicit Lease(std::shared_ptr<Impl> impl) noexcept;

private:
    std::shared_ptr<Impl> impl_;
};

struct VolumeConfig {
    std::size_t page_size = 4096;
    std::size_t cache_capacity_pages = 64; // LRU limit; clamped to >= 1.
};

// Owns version metadata, the shared LRU page cache and lease bookkeeping.
// A Volume is the service-facing entry point; the parser only receives
// Lease objects, never mapping pointers.
class Volume {
public:
    virtual ~Volume() = default;

    // Publishes a new immutable generation. length is the authoritative file
    // size for that version; readers only ever see pages inside [0, length).
    // Returns the assigned version id (starts at 1).
    virtual VersionId publishVersion(std::shared_ptr<IPageReader> reader,
                                     std::uint64_t length) = 0;

    // Truncates in place: publishes a new version backed by the same reader
    // but with a shorter length, modelling a file being shortened.
    virtual VersionId truncateTo(std::uint64_t length) = 0;

    // Atomic replacement: publishes a new generation with a different reader
    // and length, modelling file replacement. Old versions stay reachable
    // through existing leases until the last one is released.
    virtual VersionId replaceWith(std::shared_ptr<IPageReader> reader,
                                  std::uint64_t length) = 0;

    // Acquires a fixed-version lease over [start, start+length).
    // Returns an invalid lease if the range is empty or exceeds uint64 range.
    // Ranges extending past EOF are accepted; reads there return Truncated.
    virtual Lease acquireLease(std::uint64_t start, std::uint64_t length) = 0;

    virtual VersionId currentVersion() const = 0;
    virtual std::size_t pageSize() const = 0;

    // Observability, mainly for tests.
    virtual std::size_t cachedPageCount() const = 0;
    virtual std::size_t liveLeaseCount() const = 0;
};

std::unique_ptr<Volume> makeVolume(const VolumeConfig& config = {});

} // namespace netsec

#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <vector>

namespace netsec {

inline constexpr std::uint64_t kDefaultPageSize = 4096U;
inline constexpr std::size_t kContentHashSize = 32U;

/// SHA-256-sized digest of a page's current contents.  The security kernel
/// measures the bytes itself; VTL0 must never be allowed to supply this value.
using ContentHash = std::array<std::uint8_t, kContentHashSize>;
using PageHash = ContentHash;

enum class PageType : std::uint8_t {
    Code = 0,
    Data = 1,
};

/// Complete set of permissions requested for a range.  This is not a delta:
/// when a request is granted, the range is expected to end up with exactly
/// these permissions.
enum class PageAccess : std::uint8_t {
    None = 0,
    Read = 1U << 0U,
    Write = 1U << 1U,
    Execute = 1U << 2U,
    ReadWrite = (1U << 0U) | (1U << 1U),
    ReadExecute = (1U << 0U) | (1U << 2U),
    WriteExecute = (1U << 1U) | (1U << 2U),
    ReadWriteExecute = (1U << 0U) | (1U << 1U) | (1U << 2U),
};

constexpr PageAccess operator|(PageAccess lhs, PageAccess rhs) noexcept {
    return static_cast<PageAccess>(static_cast<std::uint8_t>(lhs) |
                                   static_cast<std::uint8_t>(rhs));
}

constexpr PageAccess &operator|=(PageAccess &lhs, PageAccess rhs) noexcept {
    lhs = lhs | rhs;
    return lhs;
}

constexpr bool has_access(PageAccess permissions, PageAccess flag) noexcept {
    return (static_cast<std::uint8_t>(permissions) &
            static_cast<std::uint8_t>(flag)) ==
           static_cast<std::uint8_t>(flag);
}

/// A range expressed in pages.  The starting address must be page-aligned;
/// page_count is already a page count, so the byte span is page-sized by
/// construction.  The exclusive end (base + page_count * page_size) must not
/// wrap around the address space.
struct PageRange {
    std::uint64_t base = 0;
    std::uint64_t page_count = 0;
};

enum class DenyReason : std::uint8_t {
    None = 0,
    InvalidPageSize,
    EmptyRange,
    MisalignedStart,
    RangeOverflow,
    HashCountMismatch,
    InvalidAccess,
    WriteExecuteConflict,
    UnverifiedExecute,
    DataPageExecute,
    ContentHashMismatch,
    VerifiedCodeWrite,
    OverlappingRegistration,
    InvalidPageType,
};

struct AuditResult {
    bool allowed = false;
    DenyReason reason = DenyReason::None;
    std::uint64_t offending_page = 0;

    explicit operator bool() const noexcept { return allowed; }
};

/// Permission change supplied by VTL0 through a hypercall.
///
/// `observed_hashes`, when present, is the digest of every page in the range,
/// in ascending address order.  The caller must measure these pages from
/// memory that VTL1 owns or can inspect directly; copying them from the
/// hypercall payload defeats the integrity check.  Hashes are required only
/// when Execute is requested.
struct PagePermissionRequest {
    PageRange range{};
    PageAccess access = PageAccess::None;
    std::vector<ContentHash> observed_hashes;
};

class PagePermissionAuditor {
public:
    explicit PagePermissionAuditor(std::uint64_t page_size = kDefaultPageSize) noexcept;

    std::uint64_t page_size() const noexcept { return page_size_; }

    /// Register a signed, verified image section.  `page_hashes` must contain
    /// one digest per page, in ascending address order.  Registration rejects
    /// unaligned or overflowing ranges, hash-count mismatches, and any overlap
    /// with an already registered range.
    AuditResult register_verified_pages(PageRange range, PageType type,
                                        std::vector<ContentHash> page_hashes);

    /// Audit a VTL0 permission-change hypercall.  On success the requested
    /// permissions may be applied; on failure none of them may be applied.
    AuditResult audit_hypercall(const PagePermissionRequest &request) const noexcept;

    bool is_verified_page(std::uint64_t page_base) const noexcept;

    std::uint64_t registered_page_count() const noexcept;

private:
    struct VerifiedRange {
        PageRange range;
        PageType type;
        std::vector<ContentHash> hashes;
    };

    AuditResult validate_range(PageRange range) const noexcept;
    const VerifiedRange *find_range(std::uint64_t address) const noexcept;

    std::uint64_t page_size_;
    bool page_size_valid_;
    std::vector<VerifiedRange> verified_ranges_;
};

} // namespace netsec

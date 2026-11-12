#include "netsec.hpp"

#include <algorithm>
#include <limits>
#include <utility>

namespace {

netsec::AuditResult allow() noexcept {
    return netsec::AuditResult{true, netsec::DenyReason::None, 0U};
}

netsec::AuditResult deny(netsec::DenyReason reason, std::uint64_t page) noexcept {
    return netsec::AuditResult{false, reason, page};
}

} // namespace

namespace netsec {

PagePermissionAuditor::PagePermissionAuditor(std::uint64_t page_size) noexcept
    : page_size_(page_size), page_size_valid_(page_size != 0U) {}

AuditResult PagePermissionAuditor::validate_range(PageRange range) const noexcept {
    if (!page_size_valid_) {
        return deny(DenyReason::InvalidPageSize, range.base);
    }
    if (range.page_count == 0U) {
        return deny(DenyReason::EmptyRange, range.base);
    }
    if ((range.base % page_size_) != 0U) {
        return deny(DenyReason::MisalignedStart, range.base);
    }

    // Check page_count * page_size first, then the exclusive end.  Both
    // operations must be overflow-free, otherwise the hypercall range is not
    // representable and must be rejected rather than wrapped.
    if (range.page_count > std::numeric_limits<std::uint64_t>::max() / page_size_) {
        return deny(DenyReason::RangeOverflow, range.base);
    }
    const std::uint64_t span = range.page_count * page_size_;
    if (range.base > std::numeric_limits<std::uint64_t>::max() - span) {
        return deny(DenyReason::RangeOverflow, range.base);
    }

    return allow();
}

AuditResult PagePermissionAuditor::register_verified_pages(
    PageRange range, PageType type, std::vector<ContentHash> page_hashes) {
    const AuditResult validation = validate_range(range);
    if (!validation.allowed) {
        return validation;
    }
    if (type != PageType::Code && type != PageType::Data) {
        return deny(DenyReason::InvalidPageType, range.base);
    }
    if (page_hashes.size() != range.page_count) {
        return deny(DenyReason::HashCountMismatch, range.base);
    }

    const std::uint64_t end = range.base + range.page_count * page_size_;
    for (const VerifiedRange &existing : verified_ranges_) {
        const std::uint64_t existing_end =
            existing.range.base + existing.range.page_count * page_size_;
        if (range.base < existing_end && existing.range.base < end) {
            return deny(DenyReason::OverlappingRegistration,
                        std::max(range.base, existing.range.base));
        }
    }

    verified_ranges_.push_back(
        VerifiedRange{range, type, std::move(page_hashes)});
    return allow();
}

const PagePermissionAuditor::VerifiedRange *
PagePermissionAuditor::find_range(std::uint64_t address) const noexcept {
    for (const VerifiedRange &range : verified_ranges_) {
        const std::uint64_t end =
            range.range.base + range.range.page_count * page_size_;
        if (address >= range.range.base && address < end) {
            return &range;
        }
    }
    return nullptr;
}

bool PagePermissionAuditor::is_verified_page(std::uint64_t page_base) const noexcept {
    if (!page_size_valid_ || (page_base % page_size_) != 0U) {
        return false;
    }
    return find_range(page_base) != nullptr;
}

std::uint64_t PagePermissionAuditor::registered_page_count() const noexcept {
    const std::uint64_t max = std::numeric_limits<std::uint64_t>::max();
    std::uint64_t total = 0U;
    for (const VerifiedRange &range : verified_ranges_) {
        if (range.range.page_count > max - total) {
            return max;
        }
        total += range.range.page_count;
    }
    return total;
}

AuditResult
PagePermissionAuditor::audit_hypercall(const PagePermissionRequest &request) const noexcept {
    const AuditResult validation = validate_range(request.range);
    if (!validation.allowed) {
        return validation;
    }

    const bool wants_write = has_access(request.access, PageAccess::Write);
    const bool wants_execute = has_access(request.access, PageAccess::Execute);

    const std::uint8_t known_access =
        static_cast<std::uint8_t>(PageAccess::Read) |
        static_cast<std::uint8_t>(PageAccess::Write) |
        static_cast<std::uint8_t>(PageAccess::Execute);
    if ((static_cast<std::uint8_t>(request.access) & ~known_access) != 0U) {
        return deny(DenyReason::InvalidAccess, request.range.base);
    }

    // W^X is absolute.  Deny before considering whether the pages are
    // registered so that a W+X request can never be used as a probe.
    if (wants_write && wants_execute) {
        return deny(DenyReason::WriteExecuteConflict, request.range.base);
    }

    const std::uint64_t count = request.range.page_count;

    if (wants_execute) {
        // First establish that every page is verified code.  This also keeps
        // denial reasons stable when the caller omits hashes for a request
        // that is invalid for an unrelated reason.
        for (std::uint64_t i = 0U; i < count; ++i) {
            const std::uint64_t page = request.range.base + i * page_size_;
            const VerifiedRange *verified = find_range(page);
            if (verified == nullptr) {
                return deny(DenyReason::UnverifiedExecute, page);
            }
            if (verified->type != PageType::Code) {
                return deny(DenyReason::DataPageExecute, page);
            }
        }

        if (request.observed_hashes.size() != count) {
            return deny(DenyReason::HashCountMismatch, request.range.base);
        }

        for (std::uint64_t i = 0U; i < count; ++i) {
            const std::uint64_t page = request.range.base + i * page_size_;
            const VerifiedRange *verified = find_range(page);
            const std::uint64_t index = (page - verified->range.base) / page_size_;
            if (request.observed_hashes[static_cast<std::size_t>(i)] !=
                verified->hashes[static_cast<std::size_t>(index)]) {
                return deny(DenyReason::ContentHashMismatch, page);
            }
        }
    }

    if (wants_write) {
        for (std::uint64_t i = 0U; i < count; ++i) {
            const std::uint64_t page = request.range.base + i * page_size_;
            const VerifiedRange *verified = find_range(page);
            if (verified != nullptr && verified->type == PageType::Code) {
                return deny(DenyReason::VerifiedCodeWrite, page);
            }
        }
    }

    return allow();
}

} // namespace netsec

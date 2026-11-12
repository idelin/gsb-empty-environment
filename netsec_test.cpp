#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <limits>
#include <utility>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char *message, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << message << '\n';
        ++failures;
    }
}

void check_result(const netsec::AuditResult &result, bool allowed,
                  netsec::DenyReason reason, int line) {
    if (result.allowed != allowed || result.reason != reason) {
        std::cerr << "FAIL line " << line << ": expected allowed=" << allowed
                  << " reason=" << static_cast<int>(reason)
                  << ", got allowed=" << result.allowed
                  << " reason=" << static_cast<int>(result.reason)
                  << " page=0x" << std::hex << result.offending_page << std::dec
                  << '\n';
        ++failures;
    }
}

#define CHECK(condition) check((condition), #condition, __LINE__)
#define CHECK_RESULT(result, allowed, reason) \
    check_result((result), (allowed), (reason), __LINE__)

netsec::ContentHash make_hash(std::uint64_t seed) {
    netsec::ContentHash hash{};
    for (std::size_t i = 0U; i < hash.size(); ++i) {
        hash[i] = static_cast<std::uint8_t>((seed + (i * 131U)) & 0xFFU);
    }
    hash[0] = static_cast<std::uint8_t>(seed & 0xFFU);
    return hash;
}

std::vector<netsec::ContentHash> make_hashes(std::uint64_t first_seed,
                                            std::uint64_t count) {
    std::vector<netsec::ContentHash> hashes;
    hashes.reserve(static_cast<std::size_t>(count));
    for (std::uint64_t i = 0U; i < count; ++i) {
        hashes.push_back(make_hash(first_seed + i));
    }
    return hashes;
}

netsec::PagePermissionRequest make_request(
    netsec::PageRange range, netsec::PageAccess access,
    std::vector<netsec::ContentHash> hashes = {}) {
    netsec::PagePermissionRequest request;
    request.range = range;
    request.access = access;
    request.observed_hashes = std::move(hashes);
    return request;
}

void register_fixture(netsec::PagePermissionAuditor &auditor,
                      const netsec::PageRange &code_range,
                      const netsec::PageRange &data_range) {
    CHECK_RESULT(auditor.register_verified_pages(code_range,
                                                 netsec::PageType::Code,
                                                 make_hashes(100U, 3U)),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.register_verified_pages(data_range,
                                                 netsec::PageType::Data,
                                                 make_hashes(200U, 2U)),
                 true, netsec::DenyReason::None);
}

void test_write_execute_is_always_rejected() {
    const std::uint64_t page_size = netsec::kDefaultPageSize;
    const netsec::PageRange code{0x10000000U, 3U};
    const netsec::PageRange data{0x20000000U, 2U};
    const netsec::PageRange unverified{0x30000000U, 1U};

    netsec::PagePermissionAuditor auditor(page_size);
    register_fixture(auditor, code, data);

    // W+X is rejected for verified code, verified data, and unverified pages.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 1U}, netsec::PageAccess::WriteExecute)),
                 false, netsec::DenyReason::WriteExecuteConflict);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {data.base, 1U}, netsec::PageAccess::WriteExecute)),
                 false, netsec::DenyReason::WriteExecuteConflict);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     unverified, netsec::PageAccess::ReadWriteExecute)),
                 false, netsec::DenyReason::WriteExecuteConflict);

    // Undefined permission bits must not be silently ignored.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 1U},
                     static_cast<netsec::PageAccess>(0x80U))),
                 false, netsec::DenyReason::InvalidAccess);

    // A W+X request must not be accepted merely because the page is data.
    CHECK_RESULT(
        auditor.audit_hypercall(make_request(
            {unverified.base, 1U}, netsec::PageAccess::WriteExecute)),
        false, netsec::DenyReason::WriteExecuteConflict);
}

void test_verified_code_is_never_writable() {
    const netsec::PageRange code{0x10000000U, 3U};
    const netsec::PageRange data{0x20000000U, 2U};

    netsec::PagePermissionAuditor auditor;
    register_fixture(auditor, code, data);

    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 1U}, netsec::PageAccess::Write)),
                 false, netsec::DenyReason::VerifiedCodeWrite);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 2U}, netsec::PageAccess::ReadWrite)),
                 false, netsec::DenyReason::VerifiedCodeWrite);

    // Writing a verified data page is legal.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {data.base, 2U}, netsec::PageAccess::ReadWrite)),
                 true, netsec::DenyReason::None);
}

void test_modified_verified_page_cannot_execute() {
    const netsec::PageRange code{0x10000000U, 3U};
    const netsec::PageRange data{0x20000000U, 2U};

    netsec::PagePermissionAuditor auditor;
    register_fixture(auditor, code, data);

    auto unchanged = make_hashes(100U, 3U);

    // Simulate VTL0 rewriting the second page after verification.
    unchanged[1] = make_hash(9999U);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     code, netsec::PageAccess::Execute, unchanged)),
                 false, netsec::DenyReason::ContentHashMismatch);

    // The initial page is intact but the range still contains a rewritten
    // page, so the entire request is rejected.
    const netsec::AuditResult result = auditor.audit_hypercall(make_request(
        {code.base, 2U}, netsec::PageAccess::ReadExecute,
        {unchanged[0], unchanged[1]}));
    CHECK_RESULT(result, false, netsec::DenyReason::ContentHashMismatch);
    CHECK(result.offending_page == code.base + auditor.page_size());

    // A request with the original hashes is accepted.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     code, netsec::PageAccess::Execute, make_hashes(100U, 3U))),
                 true, netsec::DenyReason::None);
}

void test_data_page_execute_is_always_rejected() {
    const netsec::PageRange code{0x10000000U, 3U};
    const netsec::PageRange data{0x20000000U, 2U};
    const netsec::PageRange unverified{0x30000000U, 1U};

    netsec::PagePermissionAuditor auditor;
    register_fixture(auditor, code, data);

    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {data.base, 1U}, netsec::PageAccess::Execute,
                     make_hashes(200U, 1U))),
                 false, netsec::DenyReason::DataPageExecute);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {data.base, 1U}, netsec::PageAccess::ReadExecute,
                     make_hashes(200U, 1U))),
                 false, netsec::DenyReason::DataPageExecute);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     unverified, netsec::PageAccess::Execute)),
                 false, netsec::DenyReason::UnverifiedExecute);
}

void test_legal_code_page_remap() {
    const netsec::PageRange code{0x10000000U, 3U};
    const netsec::PageRange data{0x20000000U, 2U};
    const netsec::PageRange unverified{0x30000000U, 1U};

    netsec::PagePermissionAuditor auditor;
    register_fixture(auditor, code, data);

    // Re-protecting a verified, unchanged code page as executable is legal.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 1U}, netsec::PageAccess::Execute,
                     {make_hash(100U)})),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     code, netsec::PageAccess::ReadExecute,
                     make_hashes(100U, 3U))),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base + 2U * auditor.page_size(), 1U},
                     netsec::PageAccess::ReadExecute,
                     {make_hash(102U)})),
                 true, netsec::DenyReason::None);

    // Data may be made writable, and read-only remaps do not require hashes.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {data.base, 1U}, netsec::PageAccess::Write)),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     unverified, netsec::PageAccess::Write)),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 1U}, netsec::PageAccess::Read)),
                 true, netsec::DenyReason::None);
}

void test_range_alignment_and_overflow() {
    const std::uint64_t page_size = netsec::kDefaultPageSize;
    const netsec::PageRange code{0x10000000U, 3U};
    const netsec::PageRange data{0x20000000U, 2U};

    netsec::PagePermissionAuditor auditor(page_size);
    register_fixture(auditor, code, data);

    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base + 1U, 1U}, netsec::PageAccess::Read)),
                 false, netsec::DenyReason::MisalignedStart);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {code.base, 0U}, netsec::PageAccess::Read)),
                 false, netsec::DenyReason::EmptyRange);

    // base + page_count * page_size == 2^64 must be rejected, not wrapped to 0.
    const std::uint64_t last_page =
        std::numeric_limits<std::uint64_t>::max() - (page_size - 1U);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {last_page, 1U}, netsec::PageAccess::Read)),
                 false, netsec::DenyReason::RangeOverflow);

    // The page immediately before it ends just below 2^64 and is valid.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {last_page - page_size, 1U}, netsec::PageAccess::Read)),
                 true, netsec::DenyReason::None);

    // Two pages ending past the address space are also rejected.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {last_page - page_size, 2U}, netsec::PageAccess::Read)),
                 false, netsec::DenyReason::RangeOverflow);

    // page_count * page_size itself must not overflow.
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     {0U, std::numeric_limits<std::uint64_t>::max()},
                     netsec::PageAccess::Read)),
                 false, netsec::DenyReason::RangeOverflow);

    // The same checks apply at registration time.
    CHECK_RESULT(auditor.register_verified_pages(
                     {last_page, 1U}, netsec::PageType::Code,
                     make_hashes(300U, 1U)),
                 false, netsec::DenyReason::RangeOverflow);
    CHECK(!auditor.is_verified_page(last_page));
    CHECK(!auditor.is_verified_page(code.base + 1U));
}

void test_registration_validation() {
    const netsec::PageRange code{0x10000000U, 3U};

    netsec::PagePermissionAuditor auditor;

    CHECK_RESULT(auditor.register_verified_pages(
                     code, netsec::PageType::Code, make_hashes(1U, 2U)),
                 false, netsec::DenyReason::HashCountMismatch);
    CHECK_RESULT(auditor.register_verified_pages(
                     {code.base + 1U, 1U}, netsec::PageType::Code,
                     make_hashes(1U, 1U)),
                 false, netsec::DenyReason::MisalignedStart);
    CHECK_RESULT(auditor.register_verified_pages(
                     code, netsec::PageType::Code, make_hashes(1U, 3U)),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.register_verified_pages(
                     {code.base + 2U * auditor.page_size(), 2U},
                     netsec::PageType::Data, make_hashes(2U, 2U)),
                 false, netsec::DenyReason::OverlappingRegistration);
    CHECK_RESULT(auditor.register_verified_pages(
                     {code.base + 3U * auditor.page_size(), 1U},
                     netsec::PageType::Data, make_hashes(3U, 1U)),
                 true, netsec::DenyReason::None);
    CHECK_RESULT(auditor.register_verified_pages(
                     {0x40000000U, 1U}, static_cast<netsec::PageType>(9U),
                     make_hashes(5U, 1U)),
                 false, netsec::DenyReason::InvalidPageType);

    netsec::PagePermissionAuditor invalid(0U);
    CHECK_RESULT(invalid.register_verified_pages(
                     {0U, 1U}, netsec::PageType::Code, make_hashes(4U, 1U)),
                 false, netsec::DenyReason::InvalidPageSize);
    CHECK_RESULT(invalid.audit_hypercall(make_request(
                     {0U, 1U}, netsec::PageAccess::Read)),
                 false, netsec::DenyReason::InvalidPageSize);
}

void test_hash_requirements() {
    const netsec::PageRange code{0x10000000U, 3U};

    netsec::PagePermissionAuditor auditor;
    CHECK_RESULT(auditor.register_verified_pages(
                     code, netsec::PageType::Code, make_hashes(10U, 3U)),
                 true, netsec::DenyReason::None);

    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     code, netsec::PageAccess::Execute)),
                 false, netsec::DenyReason::HashCountMismatch);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     code, netsec::PageAccess::Execute, make_hashes(10U, 2U))),
                 false, netsec::DenyReason::HashCountMismatch);
    CHECK_RESULT(auditor.audit_hypercall(make_request(
                     code, netsec::PageAccess::Execute, make_hashes(10U, 4U))),
                 false, netsec::DenyReason::HashCountMismatch);
}

} // namespace

int main() {
    test_write_execute_is_always_rejected();
    test_verified_code_is_never_writable();
    test_modified_verified_page_cannot_execute();
    test_data_page_execute_is_always_rejected();
    test_legal_code_page_remap();
    test_range_alignment_and_overflow();
    test_registration_validation();
    test_hash_requirements();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "netsec: all page-permission tests passed\n";
    return 0;
}

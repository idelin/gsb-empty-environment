#include "ccm/address_space.hpp"
#include "ccm/auditor.hpp"
#include "ccm/checked_math.hpp"

#include "test_framework.hpp"

using namespace ccm;

namespace {

AddressSpace make_space() {
    auto result = AddressSpace::create(
        0x1000, 0x2000,
        std::vector<Region>{Region{0x4000, 0x1000},
                            Region{0x8000, 0x1000}});
    return result.value();
}

}  // namespace

CCM_TEST(checked_add_detects_wrap) {
    addr_t out = 0;
    CCM_CHECK(checked_add<addr_t>(
        static_cast<addr_t>(-1), 1, &out));
    CCM_CHECK(!checked_add<addr_t>(1, 2, &out));
    CCM_CHECK_EQ(out, static_cast<addr_t>(3));

    len_t product = 0;
    CCM_CHECK(checked_mul<len_t>(
        static_cast<len_t>(-1), 2, &product));
}

CCM_TEST(audit_accepts_fully_external_span) {
    AddressSpace space = make_space();
    PointerAuditor auditor(space);
    CCM_CHECK_STATUS_OK(auditor.audit(0x4000, 0x1000));
    CCM_CHECK_STATUS_OK(auditor.audit(0x8800, 0x100));
    CCM_CHECK_STATUS_OK(auditor.audit_array(0x4000, 0x10, 0x80));
}

CCM_TEST(audit_rejects_enclave_overlap) {
    AddressSpace space = make_space();
    PointerAuditor auditor(space);

    CCM_CHECK_STATUS(StatusCode::OverlapsEnclave,
                     auditor.audit(0x1800, 0x10));
    // Span straddles the enclave boundary.
    CCM_CHECK_STATUS(StatusCode::OverlapsEnclave,
                     auditor.audit(0x1F00, 0x200));
    // Pointer directly into private memory.
    CCM_CHECK_STATUS(StatusCode::OverlapsEnclave,
                     auditor.audit_pointer(0x1000, 8));
}

CCM_TEST(audit_rejects_outside_and_overflow) {
    AddressSpace space = make_space();
    PointerAuditor auditor(space);

    CCM_CHECK_STATUS(StatusCode::OutsideExternal,
                     auditor.audit(0x5000, 0x10));
    CCM_CHECK_STATUS(StatusCode::OutsideExternal,
                     auditor.audit(0x4F00, 0x200));
    CCM_CHECK_STATUS(StatusCode::Overflow,
                     auditor.audit(static_cast<addr_t>(-16), 32));
    CCM_CHECK_STATUS(StatusCode::Overflow,
                     auditor.audit_array(0x4000, 0x100,
                                         static_cast<len_t>(-1)));
    // Multiplication wraps before any containment check could run.
    CCM_CHECK_STATUS(
        StatusCode::Overflow,
        auditor.audit_array(0x4000, static_cast<len_t>(-1) / 2, 3));
}

CCM_TEST(output_buffers_are_equally_restricted) {
    AddressSpace space = make_space();
    PointerAuditor auditor(space);
    CCM_CHECK_STATUS(StatusCode::OverlapsEnclave,
                     auditor.audit_output(0x1000, 4));
    CCM_CHECK_STATUS_OK(auditor.audit_output(0x8000, 4));
}

CCM_TEST(internal_offset_bounds) {
    AddressSpace space = make_space();
    PointerAuditor auditor(space);
    CCM_CHECK_STATUS_OK(auditor.audit_internal_offset(32, 8, 16));
    CCM_CHECK_STATUS(StatusCode::OutOfBoundsOffset,
                     auditor.audit_internal_offset(32, 28, 8));
}

CCM_TEST(address_space_creation_rejects_bad_layout) {
    auto overlap = AddressSpace::create(
        0x1000, 0x2000, std::vector<Region>{Region{0x1500, 0x100}});
    CCM_CHECK(!overlap.ok());

    auto pool_overlap = AddressSpace::create(
        0x1000, 0x2000,
        std::vector<Region>{Region{0x4000, 0x100},
                            Region{0x4080, 0x100}});
    CCM_CHECK(!pool_overlap.ok());
}

int main() { return ccm_test::run_all("auditor"); }

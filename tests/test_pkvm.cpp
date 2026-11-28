#include "pkvm/pkvm.hpp"

#include <cstdint>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string>
#include <utility>

namespace {

using pkvm::Access;
using pkvm::EntityKind;
using pkvm::ErrorCode;
using pkvm::HvcOperation;
using pkvm::Machine;
using pkvm::MachineConfig;
using pkvm::PageState;
using pkvm::Pfn;

constexpr Pfn kPageA = 0x100U;
constexpr Pfn kPageB = 0x101U;
constexpr Pfn kPageC = 0x102U;
constexpr Pfn kPageD = 0x110U;
constexpr Pfn kPageE = 0x111U;
constexpr Pfn kPageF = 0x112U;

void expect(bool condition, const std::string& message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

pkvm::EntityConfig entity_config(pkvm::EntityId id, EntityKind kind,
                                 std::string name) {
    pkvm::EntityConfig config{};
    config.id = id;
    config.kind = kind;
    config.stage2_range = pkvm::PhysRange{0x100000U, 0x100000U};
    config.isolated = true;
    config.name = std::move(name);
    return config;
}

MachineConfig base_config() {
    MachineConfig config{};
    config.page_size = pkvm::kDefaultPageSize;
    config.entities.push_back(
        entity_config(pkvm::kHostEntity, EntityKind::Host, "host"));
    config.entities.push_back(
        entity_config(pkvm::kHypEntity, EntityKind::Hyp, "hyp"));
    config.entities.push_back(entity_config(2U, EntityKind::Guest, "pvm-1"));
    config.entities.push_back(entity_config(3U, EntityKind::Guest, "pvm-2"));
    for (Pfn pfn : {kPageA, kPageB, kPageC, kPageD, kPageE, kPageF}) {
        pkvm::PageConfig page{};
        page.pfn = pfn;
        page.owner = pkvm::kHostEntity;
        page.confidential = false;
        page.scrubbed = true;
        page.map_to_owner = true;
        config.pages.push_back(page);
    }
    return config;
}

void test_double_borrow_is_rejected() {
    Machine machine(base_config());

    const auto share_first = machine.execute(pkvm::host_share_guest(kPageA, 2U));
    expect(share_first.ok(), "first guest share must succeed");
    expect(share_first.step == 1U, "first HVC must be step 1");

    const auto first_page = machine.page(kPageA);
    expect(first_page.has_value(), "page A must exist");
    expect(first_page->state == pkvm::PKVM_PAGE_SHARED_OWNED,
           "page A must be shared-owned");
    expect(first_page->owner == pkvm::kHostEntity, "Host must remain owner");
    expect(first_page->borrower == 2U, "pVM 1 must be the borrower");
    expect(first_page->owner_access == Access::ReadWrite,
           "owner must retain RW access");
    expect(first_page->borrower_access == Access::ReadOnly,
           "borrower must be read-only");
    expect(machine.state_for(2U, kPageA) ==
               pkvm::PKVM_PAGE_SHARED_BORROWED,
           "borrower view must be SHARED_BORROWED");
    expect(machine.state_for(3U, kPageA) == pkvm::PKVM_NOPAGE,
           "uninvolved pVM must have NOPAGE");

    const auto share_second =
        machine.execute(pkvm::host_share_guest(kPageA, 3U));
    expect(!share_second.ok(), "second borrower must be rejected");
    expect(share_second.error == ErrorCode::AlreadyShared,
           "double borrow must report AlreadyShared");
    expect(share_second.witness.has_value(), "rejection must carry a witness");
    expect(share_second.witness->step == 2U,
           "double borrow witness must be step 2");
    expect(share_second.witness->pfn == kPageA,
           "double borrow witness must identify page A");
    expect(!machine.access_for(3U, kPageA).has_value(),
           "second pVM must not gain a mapping");

    const auto rejected_reclaim = machine.execute(pkvm::reclaim(kPageA));
    expect(!rejected_reclaim.ok(), "shared page cannot be reclaimed");
    expect(rejected_reclaim.error == ErrorCode::BorrowerStillMapped,
           "reclaim must reject an active borrower");

    const auto first_violation = machine.first_violation();
    expect(first_violation.has_value(), "violation ledger must be populated");
    expect(first_violation->step == 2U,
           "minimum violating step must be the double-borrow step");
    expect(first_violation->pfn == kPageA,
           "minimum violation must identify page A");
    expect(machine.verify().ok(), "rejected HVCs must not corrupt state");
    expect(machine.hvc_history().size() == 3U,
           "history must retain accepted and rejected HVC attempts");
    expect(!machine.hvc_history()[1].ok(),
           "history must expose the rejected double-borrow step");
}

void test_non_owner_and_range_checks() {
    MachineConfig config = base_config();
    config.entities[2].stage2_range = pkvm::PhysRange{0x180000U, 0x80000U};
    Machine machine(config);

    pkvm::HvcRequest guest_share{};
    guest_share.operation = HvcOperation::HOST_SHARE_GUEST;
    guest_share.caller = 2U;
    guest_share.target = 3U;
    guest_share.pfn = kPageB;
    const auto non_owner = machine.execute(guest_share);
    expect(!non_owner.ok(), "guest must not issue a Host share");
    expect(non_owner.error == ErrorCode::InvalidCaller,
           "non-owner caller must be rejected");

    const auto outside_target =
        machine.execute(pkvm::host_share_guest(kPageB, 2U));
    expect(!outside_target.ok(), "out-of-range guest target must fail");
    expect(outside_target.error == ErrorCode::OutOfRange,
           "guest range escape must be reported as OutOfRange");
    expect(outside_target.witness.has_value(), "range failure needs witness");
    expect(outside_target.witness->pfn == kPageB,
           "range witness must identify page B");

    const auto donate_to_1 =
        machine.execute(pkvm::host_donate_guest(kPageC, 3U));
    expect(donate_to_1.ok(), "Host must be able to donate to pVM 2");

    const auto redonate = machine.execute(pkvm::host_donate_guest(kPageC, 3U));
    expect(!redonate.ok(), "donated page must not be donated again");
    expect(redonate.error == ErrorCode::NotOwner,
           "Host is no longer the page owner");
}

void test_host_mapping_revoked_by_donation() {
    Machine machine(base_config());
    const auto donated =
        machine.execute(pkvm::host_donate_guest(kPageB, 2U));
    expect(donated.ok(), "Host donation must succeed");
    expect(!machine.access_for(pkvm::kHostEntity, kPageB).has_value(),
           "Host mapping must be revoked by donation");
    expect(machine.access_for(2U, kPageB) == Access::ReadWrite,
           "pVM must own a RW mapping");
    expect(machine.state_for(pkvm::kHostEntity, kPageB) ==
               pkvm::PKVM_NOPAGE,
           "Host must have NOPAGE after donation");
    expect(machine.state_for(2U, kPageB) == pkvm::PKVM_PAGE_OWNED,
           "pVM must exclusively own the page");
    expect(machine.verify().ok(), "valid donation must satisfy invariants");

    const auto stale_host =
        machine.simulate_stage2_map_page(pkvm::kHostEntity, kPageB,
                                         Access::ReadWrite);
    expect(!stale_host.ok(), "stale Host mapping must be detected");
    expect(stale_host.error == ErrorCode::IsolationViolation,
           "stale Host mapping must be an isolation violation");
    expect(stale_host.step == 2U, "stale mapping must be step 2");
    expect(stale_host.witness.has_value(), "stale mapping needs a witness");
    expect(stale_host.witness->pfn == kPageB,
           "stale mapping witness must identify page B");
    expect(stale_host.witness->entity == pkvm::kHostEntity,
           "witness must identify Host as the offending entity");

    const auto current = machine.verify();
    expect(!current.ok(), "global verifier must see the stale Host mapping");
    expect(current.witness.has_value(),
           "global verifier must produce a witness");
    expect(current.witness->pfn == kPageB,
           "global verifier must identify page B");
}

void test_unscrubbed_reclaim_is_rejected() {
    MachineConfig config = base_config();
    config.pages[1].confidential = true;
    config.pages[1].scrubbed = true;
    Machine machine(config);

    const auto donated =
        machine.execute(pkvm::host_donate_guest(kPageB, 2U));
    expect(donated.ok(), "confidential donation must succeed");
    expect(!machine.page(kPageB)->scrubbed,
           "guest ownership must clear the scrub marker");

    const auto reclaim_before_scrub = machine.execute(pkvm::reclaim(kPageB));
    expect(!reclaim_before_scrub.ok(),
           "unscrubbed confidential reclaim must fail");
    expect(reclaim_before_scrub.error == ErrorCode::NotScrubbed,
           "unscrubbed reclaim must report NotScrubbed");
    expect(reclaim_before_scrub.step == 2U,
           "unscrubbed reclaim witness must be step 2");
    expect(reclaim_before_scrub.witness.has_value(),
           "unscrubbed reclaim needs witness");
    expect(reclaim_before_scrub.witness->pfn == kPageB,
           "unscrubbed reclaim witness must identify page B");
    expect(machine.state_for(2U, kPageB) == pkvm::PKVM_PAGE_OWNED,
           "failed reclaim must leave pVM ownership intact");

    const auto scrubbed = machine.scrub(kPageB);
    expect(scrubbed.ok(), "explicit scrub must succeed");
    expect(scrubbed.step == 3U, "scrub must occupy its own audit step");
    expect(machine.page(kPageB)->scrubbed, "scrub marker must be set");

    const auto reclaimed = machine.execute(pkvm::reclaim(kPageB));
    expect(reclaimed.ok(), "scrubbed confidential page must be reclaimable");
    expect(reclaimed.step == 4U, "reclaim must be step 4");
    expect(machine.access_for(pkvm::kHostEntity, kPageB) ==
               Access::ReadWrite,
           "reclaim must restore Host RW mapping");
    expect(!machine.access_for(2U, kPageB).has_value(),
           "reclaim must revoke pVM mapping");
    expect(machine.verify().ok(), "reclaimed state must satisfy invariants");
}

void test_unshare_requires_no_extra_borrowers_and_scrub() {
    MachineConfig config = base_config();
    config.pages[0].confidential = true;
    config.pages[0].scrubbed = true;
    Machine machine(config);

    expect(machine.execute(pkvm::host_share_guest(kPageA, 2U)).ok(),
           "confidential guest share must succeed");

    const auto unshare_before_scrub =
        machine.execute(pkvm::guest_unshare_host(kPageA, 2U));
    expect(!unshare_before_scrub.ok(),
           "confidential unshare must require a scrub");
    expect(unshare_before_scrub.error == ErrorCode::NotScrubbed,
           "unscrubbed unshare must report NotScrubbed");

    expect(machine.scrub(kPageA).ok(), "page A scrub must succeed");
    const auto unshared =
        machine.execute(pkvm::guest_unshare_host(kPageA, 2U));
    expect(unshared.ok(), "scrubbed guest unshare must succeed");
    expect(!machine.access_for(2U, kPageA).has_value(),
           "guest mapping must disappear after unshare");
    expect(machine.access_for(pkvm::kHostEntity, kPageA) ==
               Access::ReadWrite,
           "Host owner must retain its mapping");
    expect(machine.state_for(pkvm::kHostEntity, kPageA) ==
               pkvm::PKVM_PAGE_OWNED,
           "page must return to exclusive Host ownership");

    Machine extra_borrower(base_config());
    expect(extra_borrower.execute(pkvm::host_share_guest(kPageA, 2U)).ok(),
           "first share for extra-borrower test must succeed");
    const auto foreign =
        extra_borrower.simulate_stage2_map_page(3U, kPageA, Access::ReadOnly);
    expect(!foreign.ok(), "a second borrower mapping must be detected");
    expect(foreign.error == ErrorCode::IsolationViolation,
           "second borrower must be an isolation violation");

    const auto blocked_unshare =
        extra_borrower.execute(pkvm::guest_unshare_host(kPageA, 2U));
    expect(!blocked_unshare.ok(), "unshare with another borrower must fail");
    expect(blocked_unshare.error == ErrorCode::IsolationViolation,
           "unshare must explicitly reject extra borrowers");
    expect(extra_borrower.access_for(2U, kPageA).has_value(),
           "failed unshare must leave the legitimate borrower mapped");
}

void test_permission_violation_is_detected() {
    Machine machine(base_config());
    expect(machine.execute(pkvm::host_share_guest(kPageA, 2U)).ok(),
           "guest share must succeed");

    const auto removed =
        machine.simulate_stage2_unmap_page(2U, kPageA);
    expect(!removed.ok(), "removing a borrower mapping is an invariant fault");
    expect(removed.error == ErrorCode::MissingMapping,
           "missing borrower mapping must be diagnosed");

    const auto writable =
        machine.simulate_stage2_map_page(2U, kPageA, Access::ReadWrite);
    expect(!writable.ok(), "writable borrower must be rejected");
    expect(writable.error == ErrorCode::PermissionViolation,
           "writable borrower must be a permission violation");
    expect(writable.witness.has_value(), "permission fault needs witness");
    expect(writable.witness->pfn == kPageA,
           "permission witness must identify page A");
    expect(writable.witness->entity == 2U,
           "permission witness must identify the borrower");
}

void test_sparse_mapping_split_on_donation() {
    MachineConfig config = base_config();
    for (pkvm::PageConfig& page : config.pages) {
        page.map_to_owner = false;
    }
    Machine machine(config);

    const auto installed = machine.simulate_stage2_map(
        pkvm::kHostEntity,
        pkvm::PhysRange{static_cast<std::uint64_t>(kPageA) *
                            pkvm::kDefaultPageSize,
                        3U * pkvm::kDefaultPageSize},
        Access::ReadWrite);
    expect(installed.ok(), "three-page Host interval setup must succeed");

    const auto donated =
        machine.execute(pkvm::host_donate_guest(kPageB, 2U));
    expect(donated.ok(), "single-page donation must succeed");
    expect(machine.mappings(pkvm::kHostEntity).size() == 2U,
           "donation must split the surrounding sparse intervals");
    expect(machine.access_for(pkvm::kHostEntity, kPageA) == Access::ReadWrite,
           "left neighbor must remain mapped");
    expect(!machine.access_for(pkvm::kHostEntity, kPageB).has_value(),
           "donated page must disappear from Host");
    expect(machine.access_for(pkvm::kHostEntity, kPageC) == Access::ReadWrite,
           "right neighbor must remain mapped");
    expect(machine.access_for(2U, kPageB) == Access::ReadWrite,
           "guest must receive the donated page");
    expect(machine.verify().ok(), "split interval must stay invariant-safe");
}

void test_full_legal_lifecycle() {
    MachineConfig config = base_config();
    config.pages[0].confidential = true;
    config.pages[0].scrubbed = true;
    Machine machine(config);

    const auto shared = machine.execute(pkvm::host_share_guest(kPageA, 2U));
    expect(shared.ok(), "share guest must start the lifecycle");
    expect(machine.verify().ok(), "shared state must be consistent");

    const auto unshared =
        machine.execute(pkvm::guest_unshare_host(kPageA, 2U, true));
    expect(unshared.ok(), "unshare must accept an atomic scrub request");
    expect(machine.page(kPageA)->scrubbed,
           "atomic unshare scrub must persist the marker");
    expect(machine.verify().ok(), "unshared state must be consistent");

    const auto share_hyp = machine.execute(pkvm::host_share_hyp(kPageD));
    expect(share_hyp.ok(), "Host must be able to share with Hyp");
    expect(machine.access_for(pkvm::kHypEntity, kPageD) == Access::ReadOnly,
           "Hyp borrower must be read-only");
    expect(machine.verify().ok(), "Host-to-Hyp state must be consistent");

    const auto donate_hyp = machine.execute(pkvm::host_donate_hyp(kPageE));
    expect(donate_hyp.ok(), "Host must be able to donate to Hyp");
    expect(!machine.access_for(pkvm::kHostEntity, kPageE).has_value(),
           "Hyp donation must revoke the Host mapping");
    expect(machine.access_for(pkvm::kHypEntity, kPageE) == Access::ReadWrite,
           "Hyp donation must install a RW mapping");

    const auto reclaim_hyp = machine.execute(pkvm::reclaim(kPageE));
    expect(reclaim_hyp.ok(), "Host must reclaim a Hyp-owned page");
    expect(machine.verify().ok(), "reclaimed Hyp page must be consistent");

    const auto donate_guest =
        machine.execute(pkvm::host_donate_guest(kPageF, 3U));
    expect(donate_guest.ok(), "Host must be able to donate to pVM 2");
    expect(!machine.access_for(pkvm::kHostEntity, kPageF).has_value(),
           "guest donation must revoke the Host mapping");
    expect(machine.state_for(3U, kPageF) == pkvm::PKVM_PAGE_OWNED,
           "pVM 2 must exclusively own the donated page");

    const auto reclaim_guest = machine.execute(pkvm::reclaim(kPageF, true));
    expect(reclaim_guest.ok(), "Host must reclaim a guest-owned page");
    expect(machine.state_for(pkvm::kHostEntity, kPageF) ==
               pkvm::PKVM_PAGE_OWNED,
           "reclaimed guest page must return to Host");
    expect(machine.verify().ok(), "full lifecycle must be consistent");
    expect(!machine.first_violation().has_value(),
           "legal lifecycle must not produce a violation");
}

} // namespace

int main() {
    try {
        test_double_borrow_is_rejected();
        test_non_owner_and_range_checks();
        test_host_mapping_revoked_by_donation();
        test_unscrubbed_reclaim_is_rejected();
        test_unshare_requires_no_extra_borrowers_and_scrub();
        test_permission_violation_is_detected();
        test_sparse_mapping_split_on_donation();
        test_full_legal_lifecycle();
    } catch (const std::exception& error) {
        std::cerr << "pKVM audit test failure: " << error.what() << '\n';
        return 1;
    }

    std::cout << "pKVM audit tests: all checks passed\n";
    return 0;
}

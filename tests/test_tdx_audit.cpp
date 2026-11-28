// Dependency-free test suite for the TDX PAMT / SEPT / TDVMCALL verifier.
//
// Covers the four required attack scenarios plus the positive state-machine
// flow, epoch ordering, 2M superpages and metadata ownership.

#include <cstdio>

#include "tdx_audit/tdx_audit.hpp"

namespace {

using tdx_audit::Gpa;
using tdx_audit::Granularity;
using tdx_audit::Hpa;
using tdx_audit::PageType;
using tdx_audit::Result;
using tdx_audit::TdxAuditor;
using tdx_audit::TdId;
using tdx_audit::Violation;

constexpr Hpa kRamBase = 0x10000000ULL;
constexpr std::uint64_t kRamSize = 32ULL * 1024ULL * 1024ULL;  // 32 MiB

constexpr TdId kTdA = 1;
constexpr TdId kTdB = 2;

constexpr Gpa kGpaPriv0 = 0x00000000ULL;
constexpr Gpa kGpaPriv1 = 0x00001000ULL;
constexpr Gpa kGpaShared0 = tdx_audit::kGpaSharedBit;
constexpr Gpa kGpaShared1 = tdx_audit::kGpaSharedBit + 0x1000ULL;

int g_checks = 0;
int g_failures = 0;

void check(bool condition, const char* expression, const char* file,
           int line) {
  ++g_checks;
  if (!condition) {
    ++g_failures;
    std::fprintf(stderr, "FAIL %s:%d: %s\n", file, line, expression);
  }
}

void check_violation(const Result& result, Violation expected,
                     const char* tag, const char* file, int line) {
  ++g_checks;
  if (result.ok() || result.violation != expected) {
    ++g_failures;
    std::fprintf(stderr,
                 "FAIL %s:%d: %s expected violation %s, got %s (%s)\n",
                 file, line, tag,
                 tdx_audit::violation_name(expected),
                 tdx_audit::violation_name(result.violation), result.detail);
  }
}

#define CHECK(expr) check((expr), #expr, __FILE__, __LINE__)
#define CHECK_VIOLATION(result, expected) \
  check_violation((result), (expected), #result, __FILE__, __LINE__)

// Drives a SEPT entry through BLOCK -> full MEM.TRACK -> REMOVE.
Result block_track_remove(TdxAuditor& auditor, TdId td_id,
                          std::uint32_t vcpu_count, Gpa gpa) {
  Result result = auditor.sept_block(td_id, gpa);
  if (!result.ok()) {
    return result;
  }
  result = auditor.mem_track_start(td_id);
  if (!result.ok()) {
    return result;
  }
  for (std::uint32_t vcpu = 0; vcpu < vcpu_count; ++vcpu) {
    result = auditor.vcpu_flush(td_id, vcpu);
    if (!result.ok()) {
      return result;
    }
  }
  result = auditor.mem_track_finish(td_id);
  if (!result.ok()) {
    return result;
  }
  return auditor.page_remove(td_id, gpa);
}

TdxAuditor make_auditor() {
  TdxAuditor auditor;
  CHECK(auditor.register_ram(kRamBase, kRamSize).ok());
  CHECK(auditor.create_td(kTdA, 2).ok());
  CHECK(auditor.create_td(kTdB, 1).ok());
  return auditor;
}

// Emulates TDH.MEM.PAGE.ADD over every 4K frame that will back a 2M page.
void add_2m_frames(TdxAuditor& auditor, TdId td_id, Hpa huge_hpa) {
  for (std::uint64_t index = 0; index < tdx_audit::kFramesPer2M; ++index) {
    CHECK(auditor.page_add(td_id,
                           huge_hpa + index * tdx_audit::kPageSize4K)
              .ok());
  }
}

// Positive flow: ADD -> AUG -> ACCEPT -> guest access -> BLOCK -> TRACK
// -> REMOVE -> scrub -> reuse.
void test_happy_path() {
  TdxAuditor auditor = make_auditor();
  const Hpa hpa = kRamBase;

  CHECK(auditor.page_add(kTdA, hpa).ok());
  CHECK(auditor.page_aug(kTdA, kGpaPriv0, hpa, Granularity::k4K).ok());

  const auto* sept = auditor.find_sept(kTdA, kGpaPriv0);
  CHECK(sept != nullptr);
  CHECK(sept->state == tdx_audit::SeptState::kPending);

  // Pending pages are not guest accessible.
  CHECK_VIOLATION(auditor.guest_private_read(kTdA, kGpaPriv0),
                  Violation::kSeptWrongState);

  CHECK(auditor.page_accept(kTdA, kGpaPriv0, Granularity::k4K).ok());
  CHECK(auditor.guest_private_read(kTdA, kGpaPriv0).ok());
  CHECK(auditor.guest_private_write(kTdA, kGpaPriv0).ok());

  CHECK(block_track_remove(auditor, kTdA, 2, kGpaPriv0).ok());

  // After removal the HPA is back to unscrubbed NDA.
  const auto* pamt = auditor.find_pamt_region(hpa);
  CHECK(pamt != nullptr);
  CHECK(pamt->type == PageType::kNda);
  CHECK(!pamt->scrubbed);

  // Reuse before scrub is rejected; after scrub it succeeds.
  CHECK_VIOLATION(auditor.page_add(kTdA, hpa),
                  Violation::kPageNotScrubbed);
  CHECK(auditor.scrub_page(hpa).ok());
  CHECK(auditor.page_add(kTdB, hpa).ok());
  const auto* reused = auditor.find_pamt_region(hpa);
  CHECK(reused != nullptr && reused->owner_tdr == kTdB);
}

// Attack 1: reclaim / reassign a page without completing TDH.MEM.TRACK.
void test_trackless_reclaim_race() {
  TdxAuditor auditor = make_auditor();
  const Hpa hpa = kRamBase + 0x1000ULL;

  CHECK(auditor.page_add(kTdA, hpa).ok());
  CHECK(auditor.page_aug(kTdA, kGpaPriv0, hpa, Granularity::k4K).ok());
  CHECK(auditor.page_accept(kTdA, kGpaPriv0, Granularity::k4K).ok());
  CHECK(auditor.sept_block(kTdA, kGpaPriv0).ok());

  // No MEM.TRACK at all.
  CHECK_VIOLATION(auditor.page_remove(kTdA, kGpaPriv0),
                  Violation::kTrackingNotComplete);

  // A tracking window that does not flush every vCPU cannot finish.
  CHECK(auditor.mem_track_start(kTdA).ok());
  CHECK(auditor.vcpu_flush(kTdA, 0).ok());  // only 1 of 2 vCPUs
  CHECK_VIOLATION(auditor.mem_track_finish(kTdA),
                  Violation::kTrackingNotComplete);
  CHECK_VIOLATION(auditor.page_remove(kTdA, kGpaPriv0),
                  Violation::kTrackingInProgress);

  // Complete the barrier properly; now removal is legal.
  CHECK(auditor.vcpu_flush(kTdA, 1).ok());
  CHECK(auditor.mem_track_finish(kTdA).ok());
  CHECK(auditor.page_remove(kTdA, kGpaPriv0).ok());
}

// Attack 2: alias the same HPA into two different TDs' Secure EPT.
void test_cross_td_alias() {
  TdxAuditor auditor = make_auditor();
  const Hpa hpa = kRamBase + 0x2000ULL;

  CHECK(auditor.page_add(kTdA, hpa).ok());
  CHECK(auditor.page_aug(kTdA, kGpaPriv0, hpa, Granularity::k4K).ok());

  // PAMT ownership blocks both direct reassignment and a second AUG.
  CHECK_VIOLATION(auditor.page_add(kTdB, hpa),
                  Violation::kPageAlreadyOwned);
  CHECK_VIOLATION(
      auditor.page_aug(kTdB, kGpaPriv0, hpa, Granularity::k4K),
      Violation::kPageAlreadyOwned);

  // Metadata pages are protected by the same unique-ownership invariant.
  const Hpa meta_hpa = kRamBase + 0x3000ULL;
  CHECK(auditor.add_metadata_page(kTdA, meta_hpa, PageType::kTdr).ok());
  CHECK_VIOLATION(
      auditor.add_metadata_page(kTdB, meta_hpa, PageType::kEpt),
      Violation::kPageAlreadyOwned);

  // Privately owned HPAs cannot be leaked through a shared EPT either.
  CHECK_VIOLATION(auditor.map_shared(kTdB, kGpaShared0, hpa),
                  Violation::kPageAlreadyOwned);

  // After a full teardown and scrub the frame may be given to another TD.
  CHECK(auditor.page_accept(kTdA, kGpaPriv0, Granularity::k4K).ok());
  CHECK(block_track_remove(auditor, kTdA, 2, kGpaPriv0).ok());
  CHECK_VIOLATION(auditor.page_add(kTdB, hpa),
                  Violation::kPageNotScrubbed);
  CHECK(auditor.scrub_page(hpa).ok());
  CHECK(auditor.page_add(kTdB, hpa).ok());
}

// Attack 3: read/write a SEPT_PENDING page before TDG.MEM.PAGE.ACCEPT.
void test_unaccepted_access() {
  TdxAuditor auditor = make_auditor();
  const Hpa hpa = kRamBase + 0x4000ULL;

  CHECK(auditor.page_add(kTdA, hpa).ok());
  CHECK(auditor.page_aug(kTdA, kGpaPriv0, hpa, Granularity::k4K).ok());

  CHECK_VIOLATION(auditor.guest_private_read(kTdA, kGpaPriv0),
                  Violation::kSeptWrongState);
  CHECK_VIOLATION(auditor.guest_private_write(kTdA, kGpaPriv0),
                  Violation::kSeptWrongState);

  // Double accept and accept with the wrong granularity are rejected.
  CHECK(auditor.page_accept(kTdA, kGpaPriv0, Granularity::k4K).ok());
  CHECK_VIOLATION(
      auditor.page_accept(kTdA, kGpaPriv0, Granularity::k4K),
      Violation::kSeptWrongState);
  CHECK_VIOLATION(
      auditor.page_accept(kTdA, kGpaPriv0, Granularity::k2M),
      Violation::kGranularityMismatch);
  CHECK(auditor.guest_private_read(kTdA, kGpaPriv0).ok());

  // Blocked pages are not accessible either.
  CHECK(auditor.sept_block(kTdA, kGpaPriv0).ok());
  CHECK_VIOLATION(auditor.guest_private_read(kTdA, kGpaPriv0),
                  Violation::kSeptWrongState);
}

// Attack 4: expose private GPA memory to the VMM through TDVMCALL.
void test_tdvmcall_private_leak() {
  TdxAuditor auditor = make_auditor();
  const Hpa shared_hpa = kRamBase + 0x6000ULL;

  CHECK(auditor.map_shared(kTdA, kGpaShared0, shared_hpa).ok());

  // A fully shared buffer over a mapped frame is allowed.
  CHECK(auditor.td_vmcall(kTdA, kGpaShared0, tdx_audit::kPageSize4K).ok());

  // Private start address: direct private-page leak.
  CHECK_VIOLATION(
      auditor.td_vmcall(kTdA, kGpaPriv0, tdx_audit::kPageSize4K),
      Violation::kPrivateGpaLeak);

  // Buffer straddling the Shared Bit boundary (bit 47).
  const Gpa boundary_gpa = tdx_audit::kGpaSharedBit - 0x800ULL;
  CHECK_VIOLATION(auditor.td_vmcall(kTdA, boundary_gpa, 0x1000ULL),
                  Violation::kPrivateGpaLeak);
  CHECK_VIOLATION(auditor.td_vmcall(kTdA, boundary_gpa, 0x2000ULL),
                  Violation::kPrivateGpaLeak);

  // Starts in shared space but crosses bit 47 back into private space.
  const Gpa wrap_gpa =
      tdx_audit::kGpaSharedBit + tdx_audit::kGpaSharedBit - 0x1000ULL;
  CHECK_VIOLATION(auditor.td_vmcall(kTdA, wrap_gpa, 0x2000ULL),
                  Violation::kBoundaryCross);

  // A shared buffer that runs past the GPA space.
  const Gpa near_top = UINT64_MAX - 0x7ULL;
  CHECK_VIOLATION(auditor.td_vmcall(kTdA, near_top, 0x10ULL),
                  Violation::kRegionOverflow);

  // Shared address but no Shared EPT mapping.
  CHECK_VIOLATION(
      auditor.td_vmcall(kTdA, kGpaShared1, tdx_audit::kPageSize4K),
      Violation::kSharedEntryMissing);

  // Zero-length buffers are rejected.
  CHECK_VIOLATION(auditor.td_vmcall(kTdA, kGpaShared0, 0),
                  Violation::kZeroLengthBuffer);
}

// Epoch interlock: an older completed track cannot remove a page blocked in
// a later epoch (TLB residue replay defense).
void test_epoch_interlock() {
  TdxAuditor auditor = make_auditor();
  const Hpa hpa0 = kRamBase + 0x8000ULL;
  const Hpa hpa1 = kRamBase + 0x9000ULL;

  CHECK(auditor.page_add(kTdA, hpa0).ok());
  CHECK(auditor.page_aug(kTdA, kGpaPriv0, hpa0, Granularity::k4K).ok());
  CHECK(auditor.page_accept(kTdA, kGpaPriv0, Granularity::k4K).ok());

  // Complete one full epoch before blocking anything.
  CHECK(auditor.mem_track_start(kTdA).ok());
  CHECK(auditor.vcpu_flush(kTdA, 0).ok());
  CHECK(auditor.vcpu_flush(kTdA, 1).ok());
  CHECK(auditor.mem_track_finish(kTdA).ok());

  // Block now records epoch 2; the stale completed epoch 1 is insufficient.
  CHECK(auditor.sept_block(kTdA, kGpaPriv0).ok());
  CHECK_VIOLATION(auditor.page_remove(kTdA, kGpaPriv0),
                  Violation::kStaleTrackEpoch);

  // A second page mapped while the first is blocked still flows correctly.
  CHECK(auditor.page_add(kTdA, hpa1).ok());
  CHECK(auditor.page_aug(kTdA, kGpaPriv1, hpa1, Granularity::k4K).ok());
  CHECK(auditor.page_accept(kTdA, kGpaPriv1, Granularity::k4K).ok());

  // Opening the covering epoch does not authorize removal mid-window.
  CHECK(auditor.mem_track_start(kTdA).ok());
  CHECK_VIOLATION(auditor.page_remove(kTdA, kGpaPriv0),
                  Violation::kTrackingInProgress);
  CHECK(auditor.vcpu_flush(kTdA, 0).ok());
  CHECK(auditor.vcpu_flush(kTdA, 1).ok());
  CHECK(auditor.mem_track_finish(kTdA).ok());

  // The completed fresh barrier now authorizes removal.
  CHECK(auditor.page_remove(kTdA, kGpaPriv0).ok());
  CHECK(auditor.find_sept(kTdA, kGpaPriv0) == nullptr);
}

// 2M superpage flow, including 512-frame teardown and sub-frame aliasing.
void test_2m_superpage() {
  TdxAuditor auditor = make_auditor();
  const Hpa huge_hpa = kRamBase + tdx_audit::kPageSize2M;  // 2M aligned
  const Gpa huge_gpa = tdx_audit::kPageSize2M;             // 2M aligned

  // Misaligned 2M AUG requests are rejected before touching PAMT state.
  CHECK_VIOLATION(
      auditor.page_aug(kTdA, huge_gpa, huge_hpa + 0x1000ULL,
                       Granularity::k2M),
      Violation::kAlignment);

  // Every backing frame is PAGE.ADD'ed to the TD first, then AUG installs
  // one 2M SEPT entry and coalesces ownership at 2M granularity.
  add_2m_frames(auditor, kTdA, huge_hpa);
  CHECK(auditor.page_aug(kTdA, huge_gpa, huge_hpa, Granularity::k2M).ok());
  const auto* region = auditor.find_pamt_region(huge_hpa);
  CHECK(region != nullptr);
  CHECK(region->granularity == Granularity::k2M);
  CHECK(region->owner_tdr == kTdA);

  // A sub-frame of the 2M page cannot be aliased into another TD.
  const Hpa sub_hpa = huge_hpa + 0x2000ULL;
  CHECK_VIOLATION(auditor.page_add(kTdB, sub_hpa),
                  Violation::kPageAlreadyOwned);

  // Guest accesses anywhere inside the pending huge page are rejected.
  CHECK_VIOLATION(auditor.guest_private_read(kTdA, huge_gpa + 0x1234ULL),
                  Violation::kSeptWrongState);
  CHECK(auditor.page_accept(kTdA, huge_gpa, Granularity::k2M).ok());
  CHECK(auditor.guest_private_write(kTdA, huge_gpa + 0x1234ULL).ok());

  // Full teardown restores 512 unscrubbed NDA 4K frames.
  CHECK(block_track_remove(auditor, kTdA, 2, huge_gpa).ok());
  const auto* tail = auditor.find_pamt_region(huge_hpa + tdx_audit::kPageSize2M
                                                  - tdx_audit::kPageSize4K);
  CHECK(tail != nullptr);
  CHECK(tail->type == PageType::kNda);
  CHECK(!tail->scrubbed);

  // Each frame must be scrubbed before reuse.
  CHECK_VIOLATION(auditor.page_add(kTdB, sub_hpa),
                  Violation::kPageNotScrubbed);
  CHECK(auditor.scrub_page(sub_hpa).ok());
  CHECK(auditor.page_add(kTdB, sub_hpa).ok());
}

// Shared EPT mapping rules and misuse rejection.
void test_shared_mapping_rules() {
  TdxAuditor auditor = make_auditor();
  const Hpa hpa = kRamBase + 0xC000ULL;

  CHECK_VIOLATION(auditor.map_shared(kTdA, kGpaPriv0, hpa),
                  Violation::kPrivateGpaLeak);
  CHECK(auditor.map_shared(kTdA, kGpaShared0, hpa).ok());
  CHECK_VIOLATION(auditor.map_shared(kTdA, kGpaShared0, hpa),
                  Violation::kSharedEntryExists);

  // The shared frame remains NDA (no PAMT ownership taken).
  const auto* region = auditor.find_pamt_region(hpa);
  CHECK(region != nullptr && region->type == PageType::kNda);

  // A multi-frame TDVMCALL needs every frame mapped.
  CHECK_VIOLATION(
      auditor.td_vmcall(kTdA, kGpaShared0, 2ULL * tdx_audit::kPageSize4K),
      Violation::kSharedEntryMissing);
}

}  // namespace

int main() {
  test_happy_path();
  test_trackless_reclaim_race();
  test_cross_td_alias();
  test_unaccepted_access();
  test_tdvmcall_private_leak();
  test_epoch_interlock();
  test_2m_superpage();
  test_shared_mapping_rules();

  std::printf("TDX audit tests: %d checks, %d failures\n", g_checks,
              g_failures);
  return g_failures == 0 ? 0 : 1;
}

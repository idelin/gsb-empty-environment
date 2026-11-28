// SPDX-License-Identifier: Apache-2.0
//
// tdx_audit.hpp
//
// Embeddable, zero-dependency C++17 symbolic verification model of the
// Intel TDX (Trust Domain Extensions) host-side state machine:
//
//   * Physical Address Metadata Table (PAMT) pages:
//       PT_NDA / PT_REG / PT_TDR / PT_TDCS / PT_EPT
//   * Secure EPT (SEPT) page states:
//       SEPT_PENDING / SEPT_MAPPED / SEPT_BLOCKED
//   * Shared EPT mappings for host-shared (Shared Bit = 1) guest memory
//   * Per-TD Epoch / TLB tracking barriers (TDH.MEM.TRACK / VP.ENTER /
//     VP.FLUSH)
//   * Operation flow PAGE.ADD / PAGE.AUG / PAGE.ACCEPT / SEPT.BLOCK /
//     MEM.TRACK / PAGE.REMOVE / TDVMCALL
//
// The model enforces three classes of security invariants:
//
//   1. Unique PAMT ownership.  An HPA is owned by at most one TD.  Cross-TD
//      alias mappings, double assignment and reuse of pages that have not
//      been scrubbed (scrubbed == false) are hard rejected.
//   2. Block/Track/Remove ordering.  A SEPT entry may only be removed after
//      it is blocked and a TLB epoch newer than the blocking epoch has been
//      fully tracked, i.e. every vCPU observed a VP.ENTER/VP.FLUSH exit.
//      Guest access to SEPT_PENDING entries (before TDG.MEM.PAGE.ACCEPT)
//      is rejected.
//   3. TDVMCALL boundary.  Buffers handed to the untrusted VMM must live
//      entirely in shared (Shared Bit = 1) GPA space; private GPAs and
//      ranges straddling the private/shared boundary are rejected.
//
// Every mutating operation returns a Result; on violation the model state
// is left unchanged.  No exceptions, no external dependencies.

#ifndef TDX_AUDIT_TDX_AUDIT_HPP_
#define TDX_AUDIT_TDX_AUDIT_HPP_

#include <cstddef>
#include <cstdint>
#include <map>
#include <vector>

namespace tdx_audit {

// ---------------------------------------------------------------------------
// Fundamental types and constants
// ---------------------------------------------------------------------------

using Hpa = std::uint64_t;   // Host Physical Address
using Gpa = std::uint64_t;   // Guest Physical Address
using TdId = std::uint32_t;

constexpr std::uint64_t kPageSize4K = 4096ULL;
constexpr std::uint64_t kPageSize2M = 2ULL * 1024ULL * 1024ULL;
constexpr std::uint64_t kFramesPer2M = kPageSize2M / kPageSize4K;  // 512

// Bit 47 of a GPA is the TDX Shared Bit: 0 => private, 1 => shared.
constexpr Gpa kGpaSharedBit = 1ULL << 47;
constexpr Gpa kGpaAddressMask = kGpaSharedBit - 1ULL;

constexpr TdId kInvalidTdId = 0xFFFFFFFFULL;
constexpr std::uint32_t kMaxVcpus = 64;

// PAMT page types (PAMT Entry page type field).
enum class PageType : std::uint8_t {
  kNda = 0,   // Non-initialized / Destroyed Address
  kReg = 1,   // Regular private guest page
  kTdr = 2,   // Trust Domain Root metadata page
  kTdcs = 3,  // Trust Domain Control Structure metadata page
  kEpt = 4,   // Secure EPT metadata page
};

// Page mapping granularity.
enum class Granularity : std::uint8_t {
  k4K = 0,
  k2M = 1,
};

// Secure EPT entry state.
enum class SeptState : std::uint8_t {
  kPending = 0,  // Allocated, awaiting TDG.MEM.PAGE.ACCEPT
  kMapped = 1,   // Accepted / present, guest accessible
  kBlocked = 2,  // Blocked, awaiting TLB track + removal
};

// Violation / error codes returned by the verifier.
enum class Violation : std::uint8_t {
  kNone = 0,
  kUnknownTd,
  kDuplicateTd,
  kInvalidVcpuCount,
  kAlignment,
  kRegionUnregistered,
  kRegionOverflow,
  kRegionOverlap,
  kPageNotNda,
  kPageNotScrubbed,
  kPageAlreadyOwned,
  kGranularityMismatch,
  kSeptEntryMissing,
  kSeptEntryExists,
  kSeptWrongState,
  kSharedEntryExists,
  kSharedEntryMissing,
  kTrackingInProgress,
  kTrackingNotStarted,
  kTrackingNotComplete,
  kStaleTrackEpoch,
  kInvalidVcpuId,
  kVcpuAlreadyFlushed,
  kPrivateGpaLeak,
  kBoundaryCross,
  kZeroLengthBuffer,
};

// Result of a verified operation.  ok() == true means the transition was
// legal; otherwise `violation` describes the rejected transition.
struct Result {
  Violation violation = Violation::kNone;
  const char* detail = "";

  bool ok() const noexcept { return violation == Violation::kNone; }
  explicit operator bool() const noexcept { return ok(); }
};

const char* violation_name(Violation violation) noexcept;
const char* page_type_name(PageType type) noexcept;
const char* granularity_name(Granularity granularity) noexcept;
const char* sept_state_name(SeptState state) noexcept;

// ---------------------------------------------------------------------------
// Model records
// ---------------------------------------------------------------------------

// A PAMT entry.  One entry describes either a single 4K frame or, when
// granularity == k2M, a 2M superpage whose base HPA is `base_hpa`.
struct PamtEntry {
  Hpa base_hpa = 0;
  Granularity granularity = Granularity::k4K;
  PageType type = PageType::kNda;
  TdId owner_tdr = kInvalidTdId;
  bool scrubbed = true;  // NDA frames start scrubbed after cold boot
};

// A Secure EPT entry (private GPA mapping) inside one TD.
struct SeptEntry {
  Gpa gpa = 0;  // base GPA (4K or 2M aligned)
  Hpa hpa = 0;
  Granularity granularity = Granularity::k4K;
  SeptState state = SeptState::kPending;
  // TLB epoch at which the entry was blocked.  Removal requires a completed
  // tracking epoch strictly greater than this value.
  std::uint64_t blocked_epoch = 0;
};

// A Shared EPT entry (Shared Bit = 1 GPA mapping) inside one TD.
struct SharedEntry {
  Gpa gpa = 0;  // base GPA, always 4K
  Hpa hpa = 0;
};

// Per-TD Epoch / TLB tracking state.
struct TdState {
  TdId id = kInvalidTdId;
  std::uint32_t vcpu_count = 0;

  // Monotonic TLB epoch counter, bumped when a TDH.MEM.TRACK window opens.
  std::uint64_t epoch = 0;
  // Greatest epoch for which every vCPU has completed a VP.ENTER/VP.FLUSH.
  std::uint64_t last_completed_epoch = 0;

  bool tracking_active = false;
  std::uint64_t track_epoch = 0;
  std::vector<bool> flushed_vcpus;

  std::map<Gpa, SeptEntry> sept;      // keyed by base GPA
  std::map<Gpa, SharedEntry> shared;  // keyed by base GPA (4K frames)
};

// ---------------------------------------------------------------------------
// TdxAuditor: the symbolic host-side verifier
// ---------------------------------------------------------------------------
class TdxAuditor {
 public:
  TdxAuditor() = default;

  // Registers physical RAM as a set of scrubbed PT_NDA 4K PAMT entries.
  Result register_ram(Hpa base_hpa, std::uint64_t size_bytes);

  // Creates a TD with the given number of vCPUs (1..kMaxVcpus).
  Result create_td(TdId td_id, std::uint32_t vcpu_count);

  // Adds a TDR / TDCS / EPT metadata page (PT_TDR / PT_TDCS / PT_EPT).
  Result add_metadata_page(TdId td_id, Hpa hpa, PageType type);

  // TDH.MEM.PAGE.ADD: assigns a 4K NDA frame to the TD as PT_REG.
  Result page_add(TdId td_id, Hpa hpa);

  // TDH.MEM.PAGE.AUG: augments a private GPA mapping (4K or 2M) in the
  // SEPT_PENDING state.
  Result page_aug(TdId td_id, Gpa gpa, Hpa hpa, Granularity granularity);

  // TDG.MEM.PAGE.ACCEPT: guest accepts a pending private page.
  Result page_accept(TdId td_id, Gpa gpa, Granularity granularity);

  // TDH.MEM.SEPT.BLOCK: moves a mapped entry to SEPT_BLOCKED.
  Result sept_block(TdId td_id, Gpa gpa);

  // TDH.MEM.PAGE.REMOVE: removes a blocked entry after a full TLB track.
  Result page_remove(TdId td_id, Gpa gpa);

  // Explicit scrub of an NDA frame, clearing residual data before reuse.
  Result scrub_page(Hpa hpa);

  // TDH.MEM.TRACK: opens a new TLB tracking window / epoch.
  Result mem_track_start(TdId td_id);

  // Records that vcpu `vcpu_id` exited through VP.ENTER / VP.FLUSH and
  // observed the current tracking epoch.
  Result vcpu_flush(TdId td_id, std::uint32_t vcpu_id);

  // TDH.MEM.TRACK completion: succeeds once every vCPU has flushed.
  Result mem_track_finish(TdId td_id);

  // Private (Shared Bit = 0) guest read/write.  Only SEPT_MAPPED entries
  // are accessible; pending or blocked accesses are violations.
  Result guest_private_read(TdId td_id, Gpa gpa);
  Result guest_private_write(TdId td_id, Gpa gpa);

  // Maps a 4K shared (Shared Bit = 1) GPA to an NDA host frame.
  Result map_shared(TdId td_id, Gpa gpa, Hpa hpa);

  // Audits a TDVMCALL buffer: [gpa, gpa + size_bytes) must lie entirely in
  // shared GPA space and every frame must be present in the Shared EPT.
  Result td_vmcall(TdId td_id, Gpa gpa, std::uint64_t size_bytes);

  // Inspection (for tests / symbolic exploration).
  const PamtEntry* find_pamt_region(Hpa hpa) const;
  const SeptEntry* find_sept(TdId td_id, Gpa gpa) const;
  const SharedEntry* find_shared(TdId td_id, Gpa gpa) const;
  const TdState* find_td(TdId td_id) const;

  std::uint64_t pamt_entry_count() const {
    return static_cast<std::uint64_t>(pamt_.size());
  }
  std::uint64_t td_count() const {
    return static_cast<std::uint64_t>(tds_.size());
  }

 private:
  TdState* mutable_td(TdId td_id);
  PamtEntry* mutable_pamt_region(Hpa hpa);
  SeptEntry* mutable_sept(TdState& td, Gpa gpa);

  bool is_aligned(std::uint64_t value, std::uint64_t alignment) const;
  std::uint64_t region_bytes(Granularity granularity) const;

  // Marks an NDA region as owned by `td_id` with `type`.
  void assign_region(Hpa base_hpa, Granularity granularity, PageType type,
                     TdId td_id);

  // Releases a region back to unscrubbed NDA 4K frames.
  void release_region(Hpa base_hpa, Granularity granularity);

  std::map<Hpa, PamtEntry> pamt_;
  std::map<TdId, TdState> tds_;
};

// ---------------------------------------------------------------------------
// Inline implementation: helpers and lookup
// ---------------------------------------------------------------------------

inline const char* page_type_name(PageType type) noexcept {
  switch (type) {
    case PageType::kNda: return "PT_NDA";
    case PageType::kReg: return "PT_REG";
    case PageType::kTdr: return "PT_TDR";
    case PageType::kTdcs: return "PT_TDCS";
    case PageType::kEpt: return "PT_EPT";
  }
  return "PT_UNKNOWN";
}

inline const char* granularity_name(Granularity granularity) noexcept {
  return granularity == Granularity::k2M ? "2M" : "4K";
}

inline const char* sept_state_name(SeptState state) noexcept {
  switch (state) {
    case SeptState::kPending: return "SEPT_PENDING";
    case SeptState::kMapped: return "SEPT_MAPPED";
    case SeptState::kBlocked: return "SEPT_BLOCKED";
  }
  return "SEPT_UNKNOWN";
}

inline const char* violation_name(Violation violation) noexcept {
  switch (violation) {
    case Violation::kNone: return "NONE";
    case Violation::kUnknownTd: return "UNKNOWN_TD";
    case Violation::kDuplicateTd: return "DUPLICATE_TD";
    case Violation::kInvalidVcpuCount: return "INVALID_VCPU_COUNT";
    case Violation::kAlignment: return "ALIGNMENT";
    case Violation::kRegionUnregistered: return "REGION_UNREGISTERED";
    case Violation::kRegionOverflow: return "REGION_OVERFLOW";
    case Violation::kRegionOverlap: return "REGION_OVERLAP";
    case Violation::kPageNotNda: return "PAGE_NOT_NDA";
    case Violation::kPageNotScrubbed: return "PAGE_NOT_SCRUBBED";
    case Violation::kPageAlreadyOwned: return "PAGE_ALREADY_OWNED";
    case Violation::kGranularityMismatch: return "GRANULARITY_MISMATCH";
    case Violation::kSeptEntryMissing: return "SEPT_ENTRY_MISSING";
    case Violation::kSeptEntryExists: return "SEPT_ENTRY_EXISTS";
    case Violation::kSeptWrongState: return "SEPT_WRONG_STATE";
    case Violation::kSharedEntryExists: return "SHARED_ENTRY_EXISTS";
    case Violation::kSharedEntryMissing: return "SHARED_ENTRY_MISSING";
    case Violation::kTrackingInProgress: return "TRACKING_IN_PROGRESS";
    case Violation::kTrackingNotStarted: return "TRACKING_NOT_STARTED";
    case Violation::kTrackingNotComplete: return "TRACKING_NOT_COMPLETE";
    case Violation::kStaleTrackEpoch: return "STALE_TRACK_EPOCH";
    case Violation::kInvalidVcpuId: return "INVALID_VCPU_ID";
    case Violation::kVcpuAlreadyFlushed: return "VCPU_ALREADY_FLUSHED";
    case Violation::kPrivateGpaLeak: return "PRIVATE_GPA_LEAK";
    case Violation::kBoundaryCross: return "BOUNDARY_CROSS";
    case Violation::kZeroLengthBuffer: return "ZERO_LENGTH_BUFFER";
  }
  return "UNKNOWN_VIOLATION";
}

inline bool TdxAuditor::is_aligned(std::uint64_t value,
                                   std::uint64_t alignment) const {
  return (value % alignment) == 0;
}

inline std::uint64_t TdxAuditor::region_bytes(
    Granularity granularity) const {
  return granularity == Granularity::k2M ? kPageSize2M : kPageSize4K;
}

inline const PamtEntry* TdxAuditor::find_pamt_region(Hpa hpa) const {
  auto it = pamt_.upper_bound(hpa);
  if (it == pamt_.begin()) {
    return nullptr;
  }
  --it;
  const PamtEntry& entry = it->second;
  std::uint64_t size = region_bytes(entry.granularity);
  if (hpa < entry.base_hpa || hpa >= entry.base_hpa + size) {
    return nullptr;
  }
  return &entry;
}

inline PamtEntry* TdxAuditor::mutable_pamt_region(Hpa hpa) {
  auto it = pamt_.upper_bound(hpa);
  if (it == pamt_.begin()) {
    return nullptr;
  }
  --it;
  PamtEntry& entry = it->second;
  std::uint64_t size = region_bytes(entry.granularity);
  if (hpa < entry.base_hpa || hpa >= entry.base_hpa + size) {
    return nullptr;
  }
  return &entry;
}

inline TdState* TdxAuditor::mutable_td(TdId td_id) {
  auto it = tds_.find(td_id);
  return it == tds_.end() ? nullptr : &it->second;
}

inline const TdState* TdxAuditor::find_td(TdId td_id) const {
  auto it = tds_.find(td_id);
  return it == tds_.end() ? nullptr : &it->second;
}

inline SeptEntry* TdxAuditor::mutable_sept(TdState& td, Gpa gpa) {
  auto it = td.sept.upper_bound(gpa);
  if (it == td.sept.begin()) {
    return nullptr;
  }
  --it;
  SeptEntry& entry = it->second;
  std::uint64_t size = region_bytes(entry.granularity);
  if (gpa < entry.gpa || gpa >= entry.gpa + size) {
    return nullptr;
  }
  return &entry;
}

inline const SeptEntry* TdxAuditor::find_sept(TdId td_id, Gpa gpa) const {
  const TdState* td = find_td(td_id);
  if (td == nullptr) {
    return nullptr;
  }
  auto it = td->sept.upper_bound(gpa);
  if (it == td->sept.begin()) {
    return nullptr;
  }
  --it;
  const SeptEntry& entry = it->second;
  std::uint64_t size = region_bytes(entry.granularity);
  if (gpa < entry.gpa || gpa >= entry.gpa + size) {
    return nullptr;
  }
  return &entry;
}

inline const SharedEntry* TdxAuditor::find_shared(TdId td_id,
                                                  Gpa gpa) const {
  const TdState* td = find_td(td_id);
  if (td == nullptr) {
    return nullptr;
  }
  auto it = td->shared.find(gpa);
  return it == td->shared.end() ? nullptr : &it->second;
}

inline void TdxAuditor::assign_region(Hpa base_hpa,
                                      Granularity granularity,
                                      PageType type, TdId td_id) {
  PamtEntry& entry = pamt_[base_hpa];
  entry.base_hpa = base_hpa;
  entry.granularity = granularity;
  entry.type = type;
  entry.owner_tdr = td_id;
  entry.scrubbed = false;
}

inline void TdxAuditor::release_region(Hpa base_hpa,
                                       Granularity granularity) {
  pamt_.erase(base_hpa);
  const std::uint64_t frames = granularity == Granularity::k2M
                                   ? kFramesPer2M
                                   : 1ULL;
  for (std::uint64_t index = 0; index < frames; ++index) {
    Hpa frame_hpa = base_hpa + index * kPageSize4K;
    PamtEntry entry;
    entry.base_hpa = frame_hpa;
    entry.granularity = Granularity::k4K;
    entry.type = PageType::kNda;
    entry.owner_tdr = kInvalidTdId;
    entry.scrubbed = false;  // residual data until explicitly scrubbed
    pamt_.emplace(frame_hpa, entry);
  }
}

// ---------------------------------------------------------------------------
// RAM registration and TD lifecycle
// ---------------------------------------------------------------------------

inline Result TdxAuditor::register_ram(Hpa base_hpa,
                                       std::uint64_t size_bytes) {
  if (size_bytes == 0 || !is_aligned(base_hpa, kPageSize4K) ||
      !is_aligned(size_bytes, kPageSize4K)) {
    return Result{Violation::kAlignment,
                  "RAM range must be non-empty and 4K aligned"};
  }
  if (base_hpa > UINT64_MAX - size_bytes) {
    return Result{Violation::kRegionOverflow,
                  "RAM range overflows the physical address space"};
  }
  const std::uint64_t frames = size_bytes / kPageSize4K;
  for (std::uint64_t index = 0; index < frames; ++index) {
    Hpa frame_hpa = base_hpa + index * kPageSize4K;
    if (find_pamt_region(frame_hpa) != nullptr) {
      return Result{Violation::kRegionOverlap,
                    "RAM range overlaps an existing PAMT region"};
    }
  }
  for (std::uint64_t index = 0; index < frames; ++index) {
    Hpa frame_hpa = base_hpa + index * kPageSize4K;
    PamtEntry entry;
    entry.base_hpa = frame_hpa;
    entry.granularity = Granularity::k4K;
    entry.type = PageType::kNda;
    entry.owner_tdr = kInvalidTdId;
    entry.scrubbed = true;  // cold-boot RAM is zero initialized
    pamt_.emplace(frame_hpa, entry);
  }
  return Result{};
}

inline Result TdxAuditor::create_td(TdId td_id,
                                    std::uint32_t vcpu_count) {
  if (tds_.find(td_id) != tds_.end()) {
    return Result{Violation::kDuplicateTd, "TD id already exists"};
  }
  if (vcpu_count == 0 || vcpu_count > kMaxVcpus) {
    return Result{Violation::kInvalidVcpuCount,
                  "vCPU count must be in [1, 64]"};
  }
  TdState td;
  td.id = td_id;
  td.vcpu_count = vcpu_count;
  td.flushed_vcpus.assign(vcpu_count, false);
  tds_.emplace(td_id, td);
  return Result{};
}

inline Result TdxAuditor::add_metadata_page(TdId td_id, Hpa hpa,
                                            PageType type) {
  if (mutable_td(td_id) == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (type != PageType::kTdr && type != PageType::kTdcs &&
      type != PageType::kEpt) {
    return Result{Violation::kPageNotNda,
                  "metadata page type must be PT_TDR/PT_TDCS/PT_EPT"};
  }
  if (!is_aligned(hpa, kPageSize4K)) {
    return Result{Violation::kAlignment, "metadata HPA must be 4K aligned"};
  }
  const PamtEntry* meta_region = find_pamt_region(hpa);
  if (meta_region == nullptr) {
    return Result{Violation::kRegionUnregistered,
                  "metadata HPA is not registered in PAMT"};
  }
  if (meta_region->type != PageType::kNda ||
      meta_region->owner_tdr != kInvalidTdId) {
    return Result{Violation::kPageAlreadyOwned,
                  "metadata HPA is already owned by a TD"};
  }
  if (meta_region->base_hpa != hpa ||
      meta_region->granularity != Granularity::k4K) {
    return Result{Violation::kGranularityMismatch,
                  "metadata page requires a 4K PAMT frame base"};
  }
  if (!meta_region->scrubbed) {
    return Result{Violation::kPageNotScrubbed,
                  "unscrubbed page cannot be reassigned"};
  }
  assign_region(hpa, Granularity::k4K, type, td_id);
  return Result{};
}

// ---------------------------------------------------------------------------
// Private page flow
// ---------------------------------------------------------------------------

inline Result TdxAuditor::page_add(TdId td_id, Hpa hpa) {
  if (mutable_td(td_id) == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (!is_aligned(hpa, kPageSize4K)) {
    return Result{Violation::kAlignment, "PAGE.ADD HPA must be 4K aligned"};
  }
  const PamtEntry* region = find_pamt_region(hpa);
  if (region == nullptr) {
    return Result{Violation::kRegionUnregistered,
                  "PAGE.ADD HPA is not registered in PAMT"};
  }
  if (region->type != PageType::kNda ||
      region->owner_tdr != kInvalidTdId) {
    return Result{Violation::kPageAlreadyOwned,
                  "HPA is already owned by a TD (non-NDA PAMT entry)"};
  }
  if (region->base_hpa != hpa ||
      region->granularity != Granularity::k4K) {
    return Result{Violation::kGranularityMismatch,
                  "PAGE.ADD requires a 4K PAMT frame base"};
  }
  if (!region->scrubbed) {
    return Result{Violation::kPageNotScrubbed,
                  "unscrubbed page cannot be reassigned"};
  }
  assign_region(hpa, Granularity::k4K, PageType::kReg, td_id);
  return Result{};
}

inline Result TdxAuditor::page_aug(TdId td_id, Gpa gpa, Hpa hpa,
                                   Granularity granularity) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  const std::uint64_t size = region_bytes(granularity);
  if (!is_aligned(hpa, size) || !is_aligned(gpa, size)) {
    return Result{Violation::kAlignment,
                  "PAGE.AUG GPA/HPA must match mapping granularity"};
  }
  if ((gpa & kGpaSharedBit) != 0) {
    return Result{Violation::kPrivateGpaLeak,
                  "private AUG requires Shared Bit = 0 GPA"};
  }
  if (mutable_sept(*td, gpa) != nullptr) {
    return Result{Violation::kSeptEntryExists,
                  "SEPT entry already covers the GPA"};
  }
  // PAGE.AUG does not assign PAMT ownership: TDH.MEM.PAGE.ADD already made
  // the region PT_REG.  It must exist at the requested granularity and be
  // owned by this same TD; otherwise the mapping would alias foreign memory.
  const PamtEntry* region = find_pamt_region(hpa);
  if (region == nullptr) {
    return Result{Violation::kRegionUnregistered,
                  "AUG HPA is not registered in PAMT"};
  }
  if (granularity == Granularity::k4K) {
    if (region->base_hpa != hpa ||
        region->granularity != Granularity::k4K) {
      return Result{Violation::kGranularityMismatch,
                    "4K AUG requires a 4K PAMT frame base"};
    }
    if (region->type != PageType::kReg || region->owner_tdr != td_id) {
      return Result{Violation::kPageAlreadyOwned,
                    "AUG requires a PT_REG page owned by this TD"};
    }
  } else {
    // A 2M AUG coalesces 512 consecutive 4K PT_REG frames, each owned by
    // this TD, into one 2M PAMT region.
    for (std::uint64_t index = 0; index < kFramesPer2M; ++index) {
      const Hpa frame_hpa = hpa + index * kPageSize4K;
      const PamtEntry* frame = find_pamt_region(frame_hpa);
      if (frame == nullptr || frame->base_hpa != frame_hpa ||
          frame->granularity != Granularity::k4K) {
        return Result{Violation::kGranularityMismatch,
                      "2M AUG requires 512 distinct 4K PAMT frames"};
      }
      if (frame->type != PageType::kReg || frame->owner_tdr != td_id) {
        return Result{Violation::kPageAlreadyOwned,
                      "2M AUG frames must be PT_REG and owned by this TD"};
      }
    }
    for (std::uint64_t index = 0; index < kFramesPer2M; ++index) {
      pamt_.erase(hpa + index * kPageSize4K);
    }
    PamtEntry huge;
    huge.base_hpa = hpa;
    huge.granularity = Granularity::k2M;
    huge.type = PageType::kReg;
    huge.owner_tdr = td_id;
    huge.scrubbed = false;
    pamt_.emplace(hpa, huge);
  }
  SeptEntry entry;
  entry.gpa = gpa;
  entry.hpa = hpa;
  entry.granularity = granularity;
  entry.state = SeptState::kPending;
  entry.blocked_epoch = 0;
  td->sept.emplace(gpa, entry);
  return Result{};
}

inline Result TdxAuditor::page_accept(TdId td_id, Gpa gpa,
                                      Granularity granularity) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  SeptEntry* entry = mutable_sept(*td, gpa);
  if (entry == nullptr || entry->gpa != gpa) {
    return Result{Violation::kSeptEntryMissing,
                  "no SEPT entry at the requested GPA base"};
  }
  if (entry->granularity != granularity) {
    return Result{Violation::kGranularityMismatch,
                  "accept granularity does not match the SEPT entry"};
  }
  if (entry->state != SeptState::kPending) {
    return Result{Violation::kSeptWrongState,
                  "TDG.MEM.PAGE.ACCEPT requires SEPT_PENDING"};
  }
  entry->state = SeptState::kMapped;
  return Result{};
}

inline Result TdxAuditor::sept_block(TdId td_id, Gpa gpa) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  SeptEntry* entry = mutable_sept(*td, gpa);
  if (entry == nullptr || entry->gpa != gpa) {
    return Result{Violation::kSeptEntryMissing,
                  "no SEPT entry at the requested GPA base"};
  }
  if (entry->state != SeptState::kMapped) {
    return Result{Violation::kSeptWrongState,
                  "SEPT.BLOCK requires SEPT_MAPPED"};
  }
  entry->state = SeptState::kBlocked;
  // The blocking happens under the next epoch; removal must wait for a
  // completed epoch strictly greater than the current one.
  entry->blocked_epoch = td->epoch + 1ULL;
  return Result{};
}

inline Result TdxAuditor::page_remove(TdId td_id, Gpa gpa) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  auto it = td->sept.find(gpa);
  if (it == td->sept.end()) {
    return Result{Violation::kSeptEntryMissing,
                  "no SEPT entry at the requested GPA base"};
  }
  SeptEntry& entry = it->second;
  if (entry.state != SeptState::kBlocked) {
    return Result{Violation::kSeptWrongState,
                  "PAGE.REMOVE requires SEPT_BLOCKED"};
  }
  // Enforce SEPT_BLOCKED -> TDH.MEM.TRACK -> PAGE.REMOVE with a full
  // vCPU-exit TLB barrier on an epoch newer than the blocking epoch.
  if (td->tracking_active) {
    if (td->track_epoch < entry.blocked_epoch) {
      return Result{Violation::kStaleTrackEpoch,
                    "open tracking epoch does not cover the block epoch"};
    }
    return Result{Violation::kTrackingInProgress,
                  "tracking window still open during PAGE.REMOVE"};
  }
  if (td->last_completed_epoch < entry.blocked_epoch) {
    if (td->last_completed_epoch == 0) {
      return Result{Violation::kTrackingNotComplete,
                    "PAGE.REMOVE before any TDH.MEM.TRACK completion"};
    }
    return Result{Violation::kStaleTrackEpoch,
                  "TLB tracking epoch does not cover the block epoch"};
  }
  const Hpa hpa = entry.hpa;
  const Granularity granularity = entry.granularity;
  td->sept.erase(it);
  // Return the frame(s) to unscrubbed NDA state so residual data cannot be
  // re-exposed without an explicit scrub.
  release_region(hpa, granularity);
  return Result{};
}

inline Result TdxAuditor::scrub_page(Hpa hpa) {
  if (!is_aligned(hpa, kPageSize4K)) {
    return Result{Violation::kAlignment, "scrub HPA must be 4K aligned"};
  }
  PamtEntry* entry = mutable_pamt_region(hpa);
  if (entry == nullptr || entry->base_hpa != hpa ||
      entry->granularity != Granularity::k4K) {
    return Result{Violation::kGranularityMismatch,
                  "scrub requires a 4K PAMT frame base"};
  }
  if (entry->type != PageType::kNda ||
      entry->owner_tdr != kInvalidTdId) {
    return Result{Violation::kPageAlreadyOwned,
                  "only NDA frames may be scrubbed"};
  }
  entry->scrubbed = true;
  return Result{};
}

// ---------------------------------------------------------------------------
// Epoch / TLB tracking
// ---------------------------------------------------------------------------

inline Result TdxAuditor::mem_track_start(TdId td_id) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (td->tracking_active) {
    return Result{Violation::kTrackingInProgress,
                  "a TDH.MEM.TRACK window is already open"};
  }
  td->epoch += 1ULL;
  td->track_epoch = td->epoch;
  td->tracking_active = true;
  const std::size_t count = td->flushed_vcpus.size();
  for (std::size_t index = 0; index < count; ++index) {
    td->flushed_vcpus[index] = false;
  }
  return Result{};
}

inline Result TdxAuditor::vcpu_flush(TdId td_id,
                                     std::uint32_t vcpu_id) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (!td->tracking_active) {
    return Result{Violation::kTrackingNotStarted,
                  "VP.FLUSH without an open TDH.MEM.TRACK window"};
  }
  if (vcpu_id >= td->vcpu_count) {
    return Result{Violation::kInvalidVcpuId, "vCPU id out of range"};
  }
  if (td->flushed_vcpus[vcpu_id]) {
    return Result{Violation::kVcpuAlreadyFlushed,
                  "vCPU already recorded for this epoch"};
  }
  td->flushed_vcpus[vcpu_id] = true;
  return Result{};
}

inline Result TdxAuditor::mem_track_finish(TdId td_id) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (!td->tracking_active) {
    return Result{Violation::kTrackingNotStarted,
                  "TDH.MEM.TRACK finish without an open window"};
  }
  const std::size_t count = td->flushed_vcpus.size();
  for (std::size_t index = 0; index < count; ++index) {
    if (!td->flushed_vcpus[index]) {
      return Result{Violation::kTrackingNotComplete,
                    "not every vCPU has flushed the tracking epoch"};
    }
  }
  td->tracking_active = false;
  td->last_completed_epoch = td->track_epoch;
  return Result{};
}

// ---------------------------------------------------------------------------
// Guest private access
// ---------------------------------------------------------------------------

inline Result TdxAuditor::guest_private_read(TdId td_id, Gpa gpa) {
  const TdState* td = find_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if ((gpa & kGpaSharedBit) != 0) {
    return Result{Violation::kPrivateGpaLeak,
                  "private access requires Shared Bit = 0 GPA"};
  }
  const SeptEntry* entry = find_sept(td_id, gpa);
  if (entry == nullptr) {
    return Result{Violation::kSeptEntryMissing,
                  "no private SEPT mapping covers the GPA"};
  }
  if (entry->state == SeptState::kPending) {
    return Result{Violation::kSeptWrongState,
                  "read on SEPT_PENDING before TDG.MEM.PAGE.ACCEPT"};
  }
  if (entry->state == SeptState::kBlocked) {
    return Result{Violation::kSeptWrongState,
                  "read on SEPT_BLOCKED page"};
  }
  return Result{};
}

inline Result TdxAuditor::guest_private_write(TdId td_id, Gpa gpa) {
  const TdState* td = find_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if ((gpa & kGpaSharedBit) != 0) {
    return Result{Violation::kPrivateGpaLeak,
                  "private access requires Shared Bit = 0 GPA"};
  }
  const SeptEntry* entry = find_sept(td_id, gpa);
  if (entry == nullptr) {
    return Result{Violation::kSeptEntryMissing,
                  "no private SEPT mapping covers the GPA"};
  }
  if (entry->state == SeptState::kPending) {
    return Result{Violation::kSeptWrongState,
                  "write on SEPT_PENDING before TDG.MEM.PAGE.ACCEPT"};
  }
  if (entry->state == SeptState::kBlocked) {
    return Result{Violation::kSeptWrongState,
                  "write on SEPT_BLOCKED page"};
  }
  return Result{};
}

// ---------------------------------------------------------------------------
// Shared EPT and TDVMCALL boundary
// ---------------------------------------------------------------------------

inline Result TdxAuditor::map_shared(TdId td_id, Gpa gpa, Hpa hpa) {
  TdState* td = mutable_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (!is_aligned(gpa, kPageSize4K) || !is_aligned(hpa, kPageSize4K)) {
    return Result{Violation::kAlignment,
                  "shared mapping requires 4K aligned GPA/HPA"};
  }
  if ((gpa & kGpaSharedBit) == 0) {
    return Result{Violation::kPrivateGpaLeak,
                  "shared mapping requires Shared Bit = 1 GPA"};
  }
  if (td->shared.find(gpa) != td->shared.end()) {
    return Result{Violation::kSharedEntryExists,
                  "shared EPT entry already exists at GPA"};
  }
  // A privately owned (PAMT non-NDA) frame must never be aliased through a
  // shared EPT, even into the same TD.
  const PamtEntry* region = find_pamt_region(hpa);
  if (region == nullptr) {
    return Result{Violation::kRegionUnregistered,
                  "shared HPA is not registered in PAMT"};
  }
  if (region->base_hpa != hpa ||
      region->granularity != Granularity::k4K) {
    return Result{Violation::kGranularityMismatch,
                  "shared mapping requires a 4K PAMT frame base"};
  }
  if (region->type != PageType::kNda ||
      region->owner_tdr != kInvalidTdId) {
    return Result{Violation::kPageAlreadyOwned,
                  "cannot map a privately owned HPA into shared EPT"};
  }
  if (!region->scrubbed) {
    return Result{Violation::kPageNotScrubbed,
                  "unscrubbed frame cannot back a shared mapping"};
  }
  SharedEntry entry;
  entry.gpa = gpa;
  entry.hpa = hpa;
  td->shared.emplace(gpa, entry);
  return Result{};
}

inline Result TdxAuditor::td_vmcall(TdId td_id, Gpa gpa,
                                    std::uint64_t size_bytes) {
  const TdState* td = find_td(td_id);
  if (td == nullptr) {
    return Result{Violation::kUnknownTd, "unknown TD id"};
  }
  if (size_bytes == 0) {
    return Result{Violation::kZeroLengthBuffer,
                  "TDVMCALL buffer must be non-empty"};
  }
  if (gpa > UINT64_MAX - size_bytes) {
    return Result{Violation::kRegionOverflow,
                  "TDVMCALL buffer overflows the GPA space"};
  }
  const Gpa end = gpa + size_bytes;

  // Every byte of a buffer handed to the untrusted VMM must carry
  // Shared Bit = 1.  A buffer starting in private space is a direct leak;
  // one crossing the bit-47 boundary straddles private/shared regions.
  if ((gpa & kGpaSharedBit) == 0) {
    return Result{Violation::kPrivateGpaLeak,
                  "TDVMCALL buffer starts at a private (Shared Bit=0) GPA"};
  }
  if ((end & kGpaSharedBit) == 0) {
    return Result{Violation::kBoundaryCross,
                  "TDVMCALL buffer crosses into private GPA space"};
  }

  // Audit each covered 4K frame: it must be present in the Shared EPT.
  const Gpa first_frame = gpa & ~(kPageSize4K - 1ULL);
  const Gpa last_frame = (end - 1ULL) & ~(kPageSize4K - 1ULL);
  for (Gpa frame = first_frame;; frame += kPageSize4K) {
    if (td->shared.find(frame) == td->shared.end()) {
      return Result{Violation::kSharedEntryMissing,
                    "TDVMCALL buffer frame is not mapped in Shared EPT"};
    }
    if (frame == last_frame) {
      break;
    }
  }
  return Result{};
}

}  // namespace tdx_audit

#endif  // TDX_AUDIT_TDX_AUDIT_HPP_

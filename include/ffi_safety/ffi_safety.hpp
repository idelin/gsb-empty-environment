#ifndef FFI_SAFETY_FFI_SAFETY_HPP
#define FFI_SAFETY_FFI_SAFETY_HPP

// ffi_safety: an embeddable, zero-dependency FFI boundary lifetime and
// aliasing verification library for C++17 runtime safety hosts.
//
// The library models:
//   * host-owned allocation regions (Owned),
//   * a borrow-handle table with generation counters,
//   * shared/mutable loans enforcing Aliasing XOR Mutability,
//   * an FFI call event stream with deterministic UB verdicts,
//   * multi-hop callback re-entrancy guards around iterated containers,
//   * a bounded adversarial sequence searcher (default depth 6).

#include <cstddef>
#include <cstdint>
#include <deque>
#include <set>
#include <string>
#include <vector>

namespace ffi_safety {

using RegionId = std::uint32_t;
using Generation = std::uint32_t;
using SlotId = std::uint32_t;
using CallbackId = std::uint32_t;

enum class BorrowKind { Shared, Mutable };

// Every cross-boundary operation is represented by one event type.
enum class EventType {
  AcquireOwned,
  BorrowShared,
  BorrowMut,
  Release,
  Deref,
  ReturnBorrow,
  InvokeCallback,
  ReturnCallback,
  BeginIteration,
  EndIteration
};

enum class Verdict { Ok, UB };

// All violation classes recognized by the boundary auditor.
enum class Violation {
  UnknownRegion,          // region id was never acquired or already released
  RegionAlreadyLive,      // duplicate AcquireOwned for a live region
  AliasingViolation,      // XOR mutability: shared+mut or mut+mut overlap
  ActiveBorrowsOnRelease, // Release while loans are still outstanding
  UseAfterRelease,        // Release/access of an already dead region
  DanglingHandle,         // invalid/stale-generation/already-returned handle
  ReentrantMutation,      // callback re-enters and mutates an iterated region
  CallbackStackImbalance, // ReturnCallback without a matching InvokeCallback
  IterationImbalance,     // Begin/End iteration nesting error
  LeakedBorrows,          // session closed with outstanding loans
  TaintedSession          // any event fed after the first UB verdict
};

// Opaque handle handed across the FFI boundary. A handle is only valid while
// its slot is active and its generation matches the region's current one.
struct Handle {
  SlotId slot = 0;
  Generation generation = 0;

  bool valid() const noexcept { return generation != 0; }
};

inline bool operator==(const Handle& lhs, const Handle& rhs) noexcept {
  return lhs.slot == rhs.slot && lhs.generation == rhs.generation;
}

inline bool operator!=(const Handle& lhs, const Handle& rhs) noexcept {
  return !(lhs == rhs);
}

struct Event {
  EventType type = EventType::AcquireOwned;
  RegionId region_id = 0;
  Handle handle{};
  CallbackId callback_id = 0;
};

inline bool operator==(const Event& lhs, const Event& rhs) noexcept {
  return lhs.type == rhs.type && lhs.region_id == rhs.region_id &&
         lhs.handle == rhs.handle && lhs.callback_id == rhs.callback_id;
}

struct ViolationRecord {
  Violation code = Violation::UnknownRegion;
  int step = 0; // zero-based index of the offending event in the stream
  std::string detail;
};

// One adversarial FFI action explored by the bounded searcher.
struct Action {
  enum class Kind {
    Acquire,
    BorrowShared,
    BorrowMut,
    Release,
    ReturnIssuedHandle,
    DerefIssuedHandle,
    BeginIteration,
    EndIteration,
    InvokeCallback,
    ReturnCallback
  };

  Kind kind = Kind::Acquire;
  RegionId region = 0;
};

inline bool operator==(const Action& lhs, const Action& rhs) noexcept {
  return lhs.kind == rhs.kind && lhs.region == rhs.region;
}

const char* to_string(BorrowKind kind);
const char* to_string(Verdict verdict);
const char* to_string(Violation violation);
const char* to_string(EventType type);
const char* to_string(Action::Kind kind);
std::string to_string(const Handle& handle);
std::string to_string(const Event& event);
std::string to_string(const ViolationRecord& record);

// Deterministic, copyable FFI boundary auditor. Feed it the exact event stream
// exchanged with a plugin; every call returns Ok or a blocking UB verdict.
// Once UB is reported the session becomes tainted and every later event is
// rejected, modelling an immediate boundary abort.
class Monitor {
 public:
  Monitor() = default;

  // Owned region lifecycle.
  Verdict acquire_owned(RegionId region_id);
  Verdict release(RegionId region_id);

  // Borrowed loans. On success the returned Handle is valid; on UB the
  // returned Handle is invalid (generation == 0).
  Handle borrow_shared(RegionId region_id);
  Handle borrow_mut(RegionId region_id);
  Verdict return_borrow(const Handle& handle);

  // Plugin-side dereference of a previously issued handle.
  Verdict deref(const Handle& handle);

  // Callback stack management.
  Verdict invoke_callback(CallbackId callback_id);
  Verdict return_callback();

  // Host iteration guards. Mutating an iterated region while a callback is
  // active on the stack is a re-entrancy violation.
  Verdict begin_iteration(RegionId region_id);
  Verdict end_iteration(RegionId region_id);

  // Generic event-stream entry point. Borrow events are executed and the
  // issued handle is recorded in the trace.
  Verdict feed(const Event& event);

  // Final session audit: fails on outstanding loans or unbalanced guards.
  Verdict check_closed();

  bool tainted() const noexcept { return tainted_; }
  bool has_violation() const noexcept { return !violations_.empty(); }
  const ViolationRecord& last_violation() const noexcept {
    return last_violation_;
  }
  const std::vector<ViolationRecord>& violations() const noexcept {
    return violations_;
  }
  const std::vector<Event>& trace() const noexcept { return trace_; }
  const Handle& last_handle() const noexcept { return last_handle_; }

  std::uint32_t callback_depth() const noexcept { return callback_depth_; }
  Generation region_generation(RegionId region_id) const;
  bool region_alive(RegionId region_id) const;
  std::size_t active_borrow_count() const;

  Monitor clone() const { return *this; }

  // Ordered serialization of the full logical state, used by the searcher
  // for deterministic state-space de-duplication.
  std::string state_key() const;

 private:
  struct Region {
    RegionId id = 0;
    bool alive = false;
    Generation generation = 0;
    std::uint32_t shared_count = 0;
    bool mutable_active = false;
    bool iterating = false;
  };

  struct BorrowRec {
    Handle handle{};
    RegionId region_id = 0;
    BorrowKind kind = BorrowKind::Shared;
    bool active = false;
  };

  Region* find_region(RegionId region_id);
  const Region* find_region(RegionId region_id) const;
  BorrowRec* find_borrow(const Handle& handle);
  SlotId alloc_slot();
  Verdict fail(Violation code, const std::string& detail);
  Verdict check_reentrant_mutation(const Region& region);

  std::vector<Region> regions_;
  std::vector<BorrowRec> slots_;
  std::vector<Event> trace_;
  std::vector<ViolationRecord> violations_;
  ViolationRecord last_violation_{};
  Handle last_handle_{};
  std::uint32_t callback_depth_ = 0;
  bool tainted_ = false;
};

struct SearchResult {
  bool found = false;
  Violation violation = Violation::UnknownRegion;
  std::vector<Action> actions;
  std::vector<Event> events;
  std::size_t explored_states = 0;
};

// Bounded breadth-first adversarial searcher. Because exploration is
// breadth-first over a fixed action alphabet, the first hit for a target is
// the shortest sequence, and results are fully deterministic.
class AdversarialSearcher {
 public:
  static constexpr int kDefaultMaxDepth = 6;

  // Single-region alphabet targeting dangling references / aliasing UB.
  static std::vector<Action> single_region_alphabet(RegionId region_id = 1);

  // Alphabet targeting re-entrant mutation of an iterated container.
  static std::vector<Action> reentrancy_alphabet(RegionId region_id = 1);

  // Find the shortest sequence producing any of the target violations.
  SearchResult search(const std::vector<Action>& alphabet,
                      const std::vector<Violation>& targets,
                      int max_depth = kDefaultMaxDepth) const;

  // Find the shortest sequence producing one specific violation.
  SearchResult find_first(Violation target,
                          const std::vector<Action>& alphabet,
                          int max_depth = kDefaultMaxDepth) const;

  // Find the shortest witness for every target in one BFS pass; results are
  // returned in the same order as the requested targets.
  std::vector<SearchResult> find_all(
      const std::vector<Action>& alphabet,
      const std::vector<Violation>& targets,
      int max_depth = kDefaultMaxDepth) const;

  // Convenience searches over the built-in alphabets.
  SearchResult search_dangling_or_alias(
      RegionId region_id = 1, int max_depth = kDefaultMaxDepth) const;
  SearchResult search_reentrancy(
      RegionId region_id = 1, int max_depth = kDefaultMaxDepth) const;
};

namespace detail {

inline void append_int(std::string& out, std::uint64_t value) {
  out += std::to_string(value);
}

inline void append_field(std::string& out, char tag, std::uint64_t value) {
  out.push_back(tag);
  out.push_back(':');
  append_int(out, value);
  out.push_back(';');
}

}  // namespace detail

// ---------------------------------------------------------------------------
// Stringification
// ---------------------------------------------------------------------------

inline const char* to_string(BorrowKind kind) {
  return kind == BorrowKind::Shared ? "Shared" : "Mutable";
}

inline const char* to_string(Verdict verdict) {
  return verdict == Verdict::Ok ? "Ok" : "UB";
}

inline const char* to_string(Violation violation) {
  switch (violation) {
    case Violation::UnknownRegion:
      return "UnknownRegion";
    case Violation::RegionAlreadyLive:
      return "RegionAlreadyLive";
    case Violation::AliasingViolation:
      return "AliasingViolation";
    case Violation::ActiveBorrowsOnRelease:
      return "ActiveBorrowsOnRelease";
    case Violation::UseAfterRelease:
      return "UseAfterRelease";
    case Violation::DanglingHandle:
      return "DanglingHandle";
    case Violation::ReentrantMutation:
      return "ReentrantMutation";
    case Violation::CallbackStackImbalance:
      return "CallbackStackImbalance";
    case Violation::IterationImbalance:
      return "IterationImbalance";
    case Violation::LeakedBorrows:
      return "LeakedBorrows";
    case Violation::TaintedSession:
      return "TaintedSession";
  }
  return "UnknownViolation";
}

inline const char* to_string(EventType type) {
  switch (type) {
    case EventType::AcquireOwned:
      return "AcquireOwned";
    case EventType::BorrowShared:
      return "BorrowShared";
    case EventType::BorrowMut:
      return "BorrowMut";
    case EventType::Release:
      return "Release";
    case EventType::Deref:
      return "Deref";
    case EventType::ReturnBorrow:
      return "ReturnBorrow";
    case EventType::InvokeCallback:
      return "InvokeCallback";
    case EventType::ReturnCallback:
      return "ReturnCallback";
    case EventType::BeginIteration:
      return "BeginIteration";
    case EventType::EndIteration:
      return "EndIteration";
  }
  return "UnknownEvent";
}

inline const char* to_string(Action::Kind kind) {
  switch (kind) {
    case Action::Kind::Acquire:
      return "Acquire";
    case Action::Kind::BorrowShared:
      return "BorrowShared";
    case Action::Kind::BorrowMut:
      return "BorrowMut";
    case Action::Kind::Release:
      return "Release";
    case Action::Kind::ReturnIssuedHandle:
      return "ReturnIssuedHandle";
    case Action::Kind::DerefIssuedHandle:
      return "DerefIssuedHandle";
    case Action::Kind::BeginIteration:
      return "BeginIteration";
    case Action::Kind::EndIteration:
      return "EndIteration";
    case Action::Kind::InvokeCallback:
      return "InvokeCallback";
    case Action::Kind::ReturnCallback:
      return "ReturnCallback";
  }
  return "UnknownAction";
}

inline std::string to_string(const Handle& handle) {
  std::string out = "Handle(slot=";
  detail::append_int(out, static_cast<std::uint64_t>(handle.slot));
  out += ", gen=";
  detail::append_int(out, static_cast<std::uint64_t>(handle.generation));
  out.push_back(')');
  return out;
}

inline std::string to_string(const Event& event) {
  std::string out = to_string(event.type);
  out += "(region=";
  detail::append_int(out, static_cast<std::uint64_t>(event.region_id));
  out += ", ";
  out += to_string(event.handle);
  out += ", cb=";
  detail::append_int(out, static_cast<std::uint64_t>(event.callback_id));
  out.push_back(')');
  return out;
}

inline std::string to_string(const ViolationRecord& record) {
  std::string out = to_string(record.code);
  out += " at step ";
  detail::append_int(out, static_cast<std::uint64_t>(record.step));
  if (!record.detail.empty()) {
    out += ": ";
    out += record.detail;
  }
  return out;
}

// ---------------------------------------------------------------------------
// Monitor internals
// ---------------------------------------------------------------------------

inline Monitor::Region* Monitor::find_region(RegionId region_id) {
  for (Region& region : regions_) {
    if (region.id == region_id) {
      return &region;
    }
  }
  return nullptr;
}

inline const Monitor::Region* Monitor::find_region(RegionId region_id) const {
  for (const Region& region : regions_) {
    if (region.id == region_id) {
      return &region;
    }
  }
  return nullptr;
}

inline Monitor::BorrowRec* Monitor::find_borrow(const Handle& handle) {
  if (handle.slot >= slots_.size()) {
    return nullptr;
  }
  BorrowRec& rec = slots_[static_cast<std::size_t>(handle.slot)];
  if (!rec.active || rec.handle != handle) {
    return nullptr;
  }
  return &rec;
}

inline SlotId Monitor::alloc_slot() {
  for (SlotId idx = 0; idx < slots_.size();
       ++idx) {
    if (!slots_[static_cast<std::size_t>(idx)].active) {
      return idx;
    }
  }
  slots_.emplace_back();
  return static_cast<SlotId>(slots_.size() - 1U);
}

inline Verdict Monitor::fail(Violation code, const std::string& detail) {
  ViolationRecord record;
  record.code = code;
  record.step = static_cast<int>(trace_.size());
  record.detail = detail;
  violations_.push_back(record);
  last_violation_ = record;
  tainted_ = true;
  return Verdict::UB;
}

inline Verdict Monitor::check_reentrant_mutation(const Region& region) {
  if (region.iterating && callback_depth_ > 0U) {
    std::string detail = "region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region.id));
    detail += " mutated while iterating inside callback depth ";
    detail::append_int(detail, static_cast<std::uint64_t>(callback_depth_));
    return fail(Violation::ReentrantMutation, detail);
  }
  return Verdict::Ok;
}

// ---------------------------------------------------------------------------
// Monitor: region lifecycle
// ---------------------------------------------------------------------------

inline Verdict Monitor::acquire_owned(RegionId region_id) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::AcquireOwned;
  event.region_id = region_id;

  Region* existing = find_region(region_id);
  if (existing != nullptr && existing->alive) {
    trace_.push_back(event);
    std::string detail = "region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    detail += " already live";
    return fail(Violation::RegionAlreadyLive, detail);
  }

  if (existing == nullptr) {
    Region region;
    region.id = region_id;
    regions_.push_back(region);
    existing = &regions_.back();
  }
  existing->alive = true;
  existing->generation += 1;
  existing->shared_count = 0;
  existing->mutable_active = false;
  existing->iterating = false;
  trace_.push_back(event);
  return Verdict::Ok;
}

inline Verdict Monitor::release(RegionId region_id) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::Release;
  event.region_id = region_id;
  trace_.push_back(event);

  Region* region = find_region(region_id);
  if (region == nullptr || !region->alive) {
    std::string detail = "release of dead/unknown region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    return fail(Violation::UseAfterRelease, detail);
  }

  if (region->mutable_active || region->shared_count > 0U) {
    std::string detail = "release of region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    detail += " with outstanding borrows";
    return fail(Violation::ActiveBorrowsOnRelease, detail);
  }

  Verdict reentrancy = check_reentrant_mutation(*region);
  if (reentrancy == Verdict::UB) {
    return reentrancy;
  }

  region->alive = false;
  region->generation += 1;
  region->iterating = false;
  return Verdict::Ok;
}

// ---------------------------------------------------------------------------
// Monitor: borrows and handles
// ---------------------------------------------------------------------------

inline Handle Monitor::borrow_shared(RegionId region_id) {
  Handle invalid{};
  last_handle_ = invalid;
  if (tainted_) {
    fail(Violation::TaintedSession, "event after boundary abort");
    return invalid;
  }
  Event event;
  event.type = EventType::BorrowShared;
  event.region_id = region_id;
  trace_.push_back(event);

  Region* region = find_region(region_id);
  if (region == nullptr || !region->alive) {
    std::string detail = "borrow on dead/unknown region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    fail(Violation::UnknownRegion, detail);
    return invalid;
  }
  if (region->mutable_active) {
    fail(Violation::AliasingViolation,
         "shared borrow conflicts with active mutable borrow");
    return invalid;
  }
  Verdict reentrancy = check_reentrant_mutation(*region);
  if (reentrancy == Verdict::UB) {
    return invalid;
  }

  SlotId slot = alloc_slot();
  Handle handle;
  handle.slot = slot;
  handle.generation = region->generation;
  BorrowRec& rec = slots_[static_cast<std::size_t>(slot)];
  rec.handle = handle;
  rec.region_id = region_id;
  rec.kind = BorrowKind::Shared;
  rec.active = true;
  region->shared_count += 1;

  trace_.back().handle = handle;
  last_handle_ = handle;
  return handle;
}

inline Handle Monitor::borrow_mut(RegionId region_id) {
  Handle invalid{};
  last_handle_ = invalid;
  if (tainted_) {
    fail(Violation::TaintedSession, "event after boundary abort");
    return invalid;
  }
  Event event;
  event.type = EventType::BorrowMut;
  event.region_id = region_id;
  trace_.push_back(event);

  Region* region = find_region(region_id);
  if (region == nullptr || !region->alive) {
    std::string detail = "mutable borrow on dead/unknown region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    fail(Violation::UnknownRegion, detail);
    return invalid;
  }
  if (region->mutable_active || region->shared_count > 0U) {
    fail(Violation::AliasingViolation,
         "mutable borrow conflicts with active borrow(s)");
    return invalid;
  }
  Verdict reentrancy = check_reentrant_mutation(*region);
  if (reentrancy == Verdict::UB) {
    return invalid;
  }

  SlotId slot = alloc_slot();
  Handle handle;
  handle.slot = slot;
  handle.generation = region->generation;
  BorrowRec& rec = slots_[static_cast<std::size_t>(slot)];
  rec.handle = handle;
  rec.region_id = region_id;
  rec.kind = BorrowKind::Mutable;
  rec.active = true;
  region->mutable_active = true;

  trace_.back().handle = handle;
  last_handle_ = handle;
  return handle;
}

inline Verdict Monitor::return_borrow(const Handle& handle) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::ReturnBorrow;
  event.handle = handle;
  trace_.push_back(event);

  BorrowRec* rec = find_borrow(handle);
  if (rec == nullptr) {
    return fail(Violation::DanglingHandle,
                "return of invalid/stale/already-returned handle");
  }
  Region* region = find_region(rec->region_id);
  if (region == nullptr || !region->alive ||
      region->generation != handle.generation) {
    rec->active = false;
    return fail(Violation::DanglingHandle,
                "returned handle no longer matches live generation");
  }

  if (rec->kind == BorrowKind::Mutable) {
    region->mutable_active = false;
  } else {
    if (region->shared_count > 0U) {
      region->shared_count -= 1;
    }
  }
  rec->active = false;
  return Verdict::Ok;
}

inline Verdict Monitor::deref(const Handle& handle) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::Deref;
  event.handle = handle;
  trace_.push_back(event);

  BorrowRec* rec = find_borrow(handle);
  if (rec == nullptr) {
    return fail(Violation::DanglingHandle,
                "dereference of invalid/stale/returned handle");
  }
  const Region* region = find_region(rec->region_id);
  if (region == nullptr || !region->alive ||
      region->generation != handle.generation) {
    return fail(Violation::DanglingHandle,
                "dereference across a released generation");
  }
  return Verdict::Ok;
}

// ---------------------------------------------------------------------------
// Monitor: callbacks and iteration guards
// ---------------------------------------------------------------------------

inline Verdict Monitor::invoke_callback(CallbackId callback_id) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::InvokeCallback;
  event.callback_id = callback_id;
  trace_.push_back(event);
  callback_depth_ += 1;
  return Verdict::Ok;
}

inline Verdict Monitor::return_callback() {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::ReturnCallback;
  trace_.push_back(event);
  if (callback_depth_ == 0U) {
    return fail(Violation::CallbackStackImbalance,
                "ReturnCallback without matching InvokeCallback");
  }
  callback_depth_ -= 1;
  return Verdict::Ok;
}

inline Verdict Monitor::begin_iteration(RegionId region_id) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::BeginIteration;
  event.region_id = region_id;
  trace_.push_back(event);

  Region* region = find_region(region_id);
  if (region == nullptr || !region->alive) {
    std::string detail = "iteration on dead/unknown region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    return fail(Violation::UnknownRegion, detail);
  }
  if (region->iterating) {
    std::string detail = "nested iteration on region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    return fail(Violation::IterationImbalance, detail);
  }
  region->iterating = true;
  return Verdict::Ok;
}

inline Verdict Monitor::end_iteration(RegionId region_id) {
  if (tainted_) {
    return fail(Violation::TaintedSession, "event after boundary abort");
  }
  Event event;
  event.type = EventType::EndIteration;
  event.region_id = region_id;
  trace_.push_back(event);

  Region* region = find_region(region_id);
  if (region == nullptr || !region->alive) {
    std::string detail = "end iteration on dead/unknown region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    return fail(Violation::UnknownRegion, detail);
  }
  if (!region->iterating) {
    std::string detail = "EndIteration without BeginIteration on region ";
    detail::append_int(detail, static_cast<std::uint64_t>(region_id));
    return fail(Violation::IterationImbalance, detail);
  }
  region->iterating = false;
  return Verdict::Ok;
}

// ---------------------------------------------------------------------------
// Monitor: generic feed, final audit, queries, state key
// ---------------------------------------------------------------------------

inline Verdict Monitor::feed(const Event& event) {
  switch (event.type) {
    case EventType::AcquireOwned:
      return acquire_owned(event.region_id);
    case EventType::BorrowShared: {
      Handle handle = borrow_shared(event.region_id);
      return handle.valid() ? Verdict::Ok : Verdict::UB;
    }
    case EventType::BorrowMut: {
      Handle handle = borrow_mut(event.region_id);
      return handle.valid() ? Verdict::Ok : Verdict::UB;
    }
    case EventType::Release:
      return release(event.region_id);
    case EventType::Deref:
      return deref(event.handle);
    case EventType::ReturnBorrow:
      return return_borrow(event.handle);
    case EventType::InvokeCallback:
      return invoke_callback(event.callback_id);
    case EventType::ReturnCallback:
      return return_callback();
    case EventType::BeginIteration:
      return begin_iteration(event.region_id);
    case EventType::EndIteration:
      return end_iteration(event.region_id);
  }
  return fail(Violation::UnknownRegion, "unhandled event type");
}

inline Verdict Monitor::check_closed() {
  if (tainted_) {
    return fail(Violation::TaintedSession,
                "check_closed invoked after boundary abort");
  }
  if (active_borrow_count() > 0U) {
    std::string detail = "session closed with ";
    detail::append_int(detail,
                       static_cast<std::uint64_t>(active_borrow_count()));
    detail += " outstanding borrow(s)";
    return fail(Violation::LeakedBorrows, detail);
  }
  if (callback_depth_ != 0U) {
    return fail(Violation::CallbackStackImbalance,
                "session closed with callbacks still active");
  }
  for (const Region& region : regions_) {
    if (region.alive && region.iterating) {
      return fail(Violation::IterationImbalance,
                  "session closed while iteration still active");
    }
  }
  return Verdict::Ok;
}

inline Generation Monitor::region_generation(RegionId region_id) const {
  const Region* region = find_region(region_id);
  return region == nullptr ? 0U : region->generation;
}

inline bool Monitor::region_alive(RegionId region_id) const {
  const Region* region = find_region(region_id);
  return region != nullptr && region->alive;
}

inline std::size_t Monitor::active_borrow_count() const {
  std::size_t count = 0;
  for (const BorrowRec& rec : slots_) {
    if (rec.active) {
      ++count;
    }
  }
  return count;
}

inline std::string Monitor::state_key() const {
  std::string key;
  detail::append_field(key, 'd', static_cast<std::uint64_t>(callback_depth_));
  detail::append_field(key, 't', tainted_ ? 1U : 0U);
  for (const Region& region : regions_) {
    key.push_back('R');
    detail::append_field(key, 'i', static_cast<std::uint64_t>(region.id));
    detail::append_field(key, 'a', region.alive ? 1U : 0U);
    detail::append_field(key, 'g',
                         static_cast<std::uint64_t>(region.generation));
    detail::append_field(key, 's',
                         static_cast<std::uint64_t>(region.shared_count));
    detail::append_field(key, 'm', region.mutable_active ? 1U : 0U);
    detail::append_field(key, 'n', region.iterating ? 1U : 0U);
  }
  for (const BorrowRec& rec : slots_) {
    key.push_back('B');
    detail::append_field(key, 's',
                         static_cast<std::uint64_t>(rec.handle.slot));
    detail::append_field(key, 'g',
                         static_cast<std::uint64_t>(rec.handle.generation));
    detail::append_field(key, 'r',
                         static_cast<std::uint64_t>(rec.region_id));
    detail::append_field(key, 'k',
                         rec.kind == BorrowKind::Mutable ? 1U : 0U);
    detail::append_field(key, 'v', rec.active ? 1U : 0U);
  }
  return key;
}

// ---------------------------------------------------------------------------
// AdversarialSearcher
// ---------------------------------------------------------------------------

namespace detail {

// Returns the first handle ever issued in the session, even if it has since
// been returned or its region released. This lets the searcher exercise true
// stale-generation dangling references rather than only empty handles.
inline Handle first_issued_handle(const Monitor& monitor) {
  for (const Event& event : monitor.trace()) {
    if ((event.type == EventType::BorrowShared ||
         event.type == EventType::BorrowMut) &&
        event.handle.valid()) {
      return event.handle;
    }
  }
  return Handle{};
}

inline Verdict apply_action(Monitor& monitor, const Action& action) {
  switch (action.kind) {
    case Action::Kind::Acquire:
      return monitor.acquire_owned(action.region);
    case Action::Kind::BorrowShared:
      return monitor.borrow_shared(action.region).valid() ? Verdict::Ok
                                                          : Verdict::UB;
    case Action::Kind::BorrowMut:
      return monitor.borrow_mut(action.region).valid() ? Verdict::Ok
                                                       : Verdict::UB;
    case Action::Kind::Release:
      return monitor.release(action.region);
    case Action::Kind::ReturnIssuedHandle:
      if (!first_issued_handle(monitor).valid()) {
        return Verdict::Ok;
      }
      return monitor.return_borrow(first_issued_handle(monitor));
    case Action::Kind::DerefIssuedHandle:
      if (!first_issued_handle(monitor).valid()) {
        return Verdict::Ok;
      }
      return monitor.deref(first_issued_handle(monitor));
    case Action::Kind::BeginIteration:
      return monitor.begin_iteration(action.region);
    case Action::Kind::EndIteration:
      return monitor.end_iteration(action.region);
    case Action::Kind::InvokeCallback:
      return monitor.invoke_callback(1U);
    case Action::Kind::ReturnCallback:
      return monitor.return_callback();
  }
  return Verdict::UB;
}

}  // namespace detail

inline std::vector<Action> AdversarialSearcher::single_region_alphabet(
    RegionId region_id) {
  return {
      Action{Action::Kind::Acquire, region_id},
      Action{Action::Kind::BorrowShared, region_id},
      Action{Action::Kind::BorrowMut, region_id},
      Action{Action::Kind::Release, region_id},
      Action{Action::Kind::ReturnIssuedHandle, region_id},
      Action{Action::Kind::DerefIssuedHandle, region_id},
  };
}

inline std::vector<Action> AdversarialSearcher::reentrancy_alphabet(
    RegionId region_id) {
  return {
      Action{Action::Kind::Acquire, region_id},
      Action{Action::Kind::BorrowMut, region_id},
      Action{Action::Kind::ReturnIssuedHandle, region_id},
      Action{Action::Kind::BeginIteration, region_id},
      Action{Action::Kind::EndIteration, region_id},
      Action{Action::Kind::InvokeCallback, region_id},
      Action{Action::Kind::ReturnCallback, region_id},
  };
}

inline SearchResult AdversarialSearcher::search(
    const std::vector<Action>& alphabet,
    const std::vector<Violation>& targets,
    int max_depth) const {
  struct Node {
    Monitor monitor;
    std::vector<Action> path;
  };

  std::set<std::string> visited;
  std::deque<Node> queue;
  std::size_t explored = 0;

  Node root;
  visited.insert(root.monitor.state_key());
  queue.push_back(std::move(root));

  while (!queue.empty()) {
    Node node = std::move(queue.front());
    queue.pop_front();
    for (const Action& action : alphabet) {
      if (static_cast<int>(node.path.size()) >= max_depth) {
        break;
      }
      Node child = node;
      Verdict verdict = detail::apply_action(child.monitor, action);
      explored += 1;
      child.path.push_back(action);

      if (verdict == Verdict::UB) {
        const ViolationRecord& record = child.monitor.last_violation();
        for (Violation target : targets) {
          bool match = record.code == target;
          // A genuine dangling reference requires the target region to have
          // gone through a release (dead generation), not merely an aborted
          // release while borrows were still outstanding.
          if (match && target == Violation::DanglingHandle) {
            match = action.kind == Action::Kind::DerefIssuedHandle &&
                    !child.monitor.region_alive(action.region);
          }
          if (match) {
            SearchResult result;
            result.found = true;
            result.violation = record.code;
            result.actions = child.path;
            result.events = child.monitor.trace();
            result.explored_states = explored;
            return result;
          }
        }
        continue;
      }

      std::string key = child.monitor.state_key();
      if (visited.insert(key).second) {
        queue.push_back(std::move(child));
      }
    }
  }

  SearchResult miss;
  miss.explored_states = explored;
  return miss;
}

inline SearchResult AdversarialSearcher::find_first(
    Violation target,
    const std::vector<Action>& alphabet,
    int max_depth) const {
  return search(alphabet, std::vector<Violation>{target}, max_depth);
}

inline std::vector<SearchResult> AdversarialSearcher::find_all(
    const std::vector<Action>& alphabet,
    const std::vector<Violation>& targets,
    int max_depth) const {
  std::vector<SearchResult> results;
  results.reserve(targets.size());
  for (Violation target : targets) {
    results.push_back(find_first(target, alphabet, max_depth));
  }
  return results;
}

inline SearchResult AdversarialSearcher::search_dangling_or_alias(
    RegionId region_id, int max_depth) const {
  static const Violation kTargets[] = {
      Violation::AliasingViolation,
      Violation::UseAfterRelease,
      Violation::ActiveBorrowsOnRelease,
      Violation::DanglingHandle,
  };
  return search(single_region_alphabet(region_id),
                std::vector<Violation>(kTargets,
                                       kTargets + sizeof(kTargets) /
                                                       sizeof(kTargets[0])),
                max_depth);
}

inline SearchResult AdversarialSearcher::search_reentrancy(
    RegionId region_id, int max_depth) const {
  return find_first(Violation::ReentrantMutation,
                    reentrancy_alphabet(region_id), max_depth);
}

}  // namespace ffi_safety

#endif  // FFI_SAFETY_FFI_SAFETY_HPP

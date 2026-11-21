#ifndef BORROW_AUTHORITY_HPP
#define BORROW_AUTHORITY_HPP

/*
 * borrow_authority.hpp
 *
 * A small, embeddable C++17 lifetime host.  The component is deliberately
 * abstract: it never dereferences a pointer and only reasons about object
 * identities, generations, half-open intervals, frames, and leases.
 *
 * The important invariants are:
 *
 *   - a frame may enter a child frame before the parent returns;
 *   - suspending a frame keeps all of its leases alive;
 *   - cancelling a frame revokes exactly that frame and its live descendants;
 *   - a restoration ticket is bound to the frame's branch and lineage;
 *   - moving an object bumps its generation and makes old generations stale;
 *   - returning a borrow may cover only part of one or more leases.
 */

#include <algorithm>
#include <atomic>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <optional>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

namespace borrow {

using Offset = std::uint64_t;
using ObjectId = std::uint64_t;
using Generation = std::uint64_t;
using FrameId = std::uint64_t;
using BranchId = std::uint64_t;
using LeaseId = std::uint64_t;
using TicketId = std::uint64_t;

inline constexpr FrameId kRootFrame = 0;
inline constexpr FrameId kNoFrame = std::numeric_limits<FrameId>::max();
inline constexpr ObjectId kNoObject = std::numeric_limits<ObjectId>::max();
inline constexpr LeaseId kNoLease = std::numeric_limits<LeaseId>::max();
inline constexpr BranchId kRootBranch = 1;

// ---------------------------------------------------------------------------
// Half-open intervals
// ---------------------------------------------------------------------------

struct Range {
    Offset begin = 0;
    Offset end = 0;

    constexpr Range() = default;
    constexpr Range(Offset b, Offset e) : begin(b), end(e) {}

    static constexpr Range empty_at(Offset p) { return Range(p, p); }

    constexpr bool empty() const { return begin >= end; }
    constexpr Offset size() const { return empty() ? 0 : end - begin; }
    constexpr bool contains(Offset p) const { return begin <= p && p < end; }

    constexpr bool covers(const Range& other) const {
        return other.empty() || (begin <= other.begin && other.end <= end);
    }

    constexpr bool overlaps(const Range& other) const {
        return !empty() && !other.empty() && begin < other.end && other.begin < end;
    }

    constexpr bool disjoint(const Range& other) const { return !overlaps(other); }

    constexpr Range intersection(const Range& other) const {
        const Offset b = begin > other.begin ? begin : other.begin;
        const Offset e = end < other.end ? end : other.end;
        return b < e ? Range(b, e) : Range::empty_at(b);
    }

    constexpr Range hull(const Range& other) const {
        if (empty()) return other;
        if (other.empty()) return *this;
        return Range(begin < other.begin ? begin : other.begin,
                     end > other.end ? end : other.end);
    }

    friend constexpr bool operator==(const Range& a, const Range& b) {
        return a.begin == b.begin && a.end == b.end;
    }
    friend constexpr bool operator!=(const Range& a, const Range& b) {
        return !(a == b);
    }
    friend constexpr bool operator<(const Range& a, const Range& b) {
        return a.begin != b.begin ? a.begin < b.begin : a.end < b.end;
    }
};

inline std::string json_escape(const std::string& input) {
    std::string out;
    out.reserve(input.size() + 2);
    for (char c : input) {
        switch (c) {
            case '"': out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\n': out += "\\n"; break;
            case '\r': out += "\\r"; break;
            case '\t': out += "\\t"; break;
            default: {
                const unsigned char byte = static_cast<unsigned char>(c);
                if (byte < 0x20) {
                    static const char* digits = "0123456789abcdef";
                    out += "\\u00";
                    out.push_back(digits[byte >> 4]);
                    out.push_back(digits[byte & 0x0f]);
                } else {
                    out.push_back(c);
                }
                break;
            }
        }
    }
    return out;
}

// A normalized union of disjoint, non-touching half-open intervals.
class IntervalSet {
public:
    IntervalSet() = default;
    explicit IntervalSet(Range r) { add(r); }

    static IntervalSet from(Range r) { return IntervalSet(r); }

    void clear() { ranges_.clear(); }

    void add(Range r) {
        if (r.empty()) return;
        ranges_.push_back(r);
        std::sort(ranges_.begin(), ranges_.end());
        std::vector<Range> merged;
        merged.reserve(ranges_.size());
        for (const Range& piece : ranges_) {
            if (merged.empty() || merged.back().end < piece.begin) {
                merged.push_back(piece);
            } else {
                if (piece.end > merged.back().end) merged.back().end = piece.end;
            }
        }
        ranges_.swap(merged);
    }

    void add(const IntervalSet& other) {
        for (const Range& r : other.ranges_) add(r);
    }

    void subtract(Range r) {
        if (r.empty() || ranges_.empty()) return;
        std::vector<Range> out;
        out.reserve(ranges_.size() + 1);
        for (const Range& piece : ranges_) {
            if (!piece.overlaps(r)) {
                out.push_back(piece);
                continue;
            }
            if (piece.begin < r.begin) out.push_back(Range(piece.begin, r.begin));
            if (r.end < piece.end) out.push_back(Range(r.end, piece.end));
        }
        std::sort(out.begin(), out.end());
        ranges_.swap(out);
    }

    void subtract(const IntervalSet& other) {
        for (const Range& r : other.ranges_) subtract(r);
    }

    void intersect(Range r) {
        if (r.empty()) {
            ranges_.clear();
            return;
        }
        std::vector<Range> out;
        for (const Range& piece : ranges_) {
            const Range hit = piece.intersection(r);
            if (!hit.empty()) out.push_back(hit);
        }
        ranges_.swap(out);
    }

    void intersect(const IntervalSet& other) {
        IntervalSet out;
        std::size_t i = 0;
        std::size_t j = 0;
        while (i < ranges_.size() && j < other.ranges_.size()) {
            const Range hit = ranges_[i].intersection(other.ranges_[j]);
            if (!hit.empty()) out.add(hit);
            if (ranges_[i].end < other.ranges_[j].end) {
                ++i;
            } else {
                ++j;
            }
        }
        ranges_.swap(out.ranges_);
    }

    bool empty() const { return ranges_.empty(); }

    bool covers(Range r) const {
        if (r.empty()) return true;
        for (const Range& piece : ranges_) {
            if (piece.covers(r)) return true;
        }
        return false;
    }

    bool covers(const IntervalSet& other) const {
        for (const Range& r : other.ranges_) {
            if (!covers(r)) return false;
        }
        return true;
    }

    bool intersects(Range r) const {
        for (const Range& piece : ranges_) {
            if (piece.overlaps(r)) return true;
        }
        return false;
    }

    bool intersects(const IntervalSet& other) const {
        for (const Range& r : other.ranges_) {
            if (intersects(r)) return true;
        }
        return false;
    }

    const std::vector<Range>& ranges() const { return ranges_; }

    Offset measure() const {
        Offset total = 0;
        for (const Range& r : ranges_) total += r.size();
        return total;
    }

    IntervalSet union_with(const IntervalSet& other) const {
        IntervalSet out = *this;
        out.add(other);
        return out;
    }

    friend bool operator==(const IntervalSet& a, const IntervalSet& b) {
        return a.ranges_ == b.ranges_;
    }
    friend bool operator!=(const IntervalSet& a, const IntervalSet& b) {
        return !(a == b);
    }

    std::string str() const {
        std::string out = "{";
        for (std::size_t i = 0; i < ranges_.size(); ++i) {
            if (i) out += ",";
            out += "[" + std::to_string(ranges_[i].begin) + "," +
                   std::to_string(ranges_[i].end) + ")";
        }
        out += "}";
        return out;
    }

    std::string to_json() const {
        std::string out = "[";
        for (std::size_t i = 0; i < ranges_.size(); ++i) {
            if (i) out += ",";
            out += "[" + std::to_string(ranges_[i].begin) + "," +
                   std::to_string(ranges_[i].end) + "]";
        }
        out += "]";
        return out;
    }

private:
    std::vector<Range> ranges_;
};

// ---------------------------------------------------------------------------
// Basic vocabulary
// ---------------------------------------------------------------------------

enum class BorrowMode { Shared, Exclusive };
enum class AccessKind { Read, Write };
enum class FrameState { Active, Suspended, Cancelled, Returned };
enum class Decision { Allow, Forbid, Undecided };
enum class QueryKind { Read, Write, Move, Return };

struct ObjectRef {
    ObjectId id = kNoObject;
    Generation generation = 0;

    friend constexpr bool operator==(const ObjectRef& a, const ObjectRef& b) {
        return a.id == b.id && a.generation == b.generation;
    }
    friend constexpr bool operator!=(const ObjectRef& a, const ObjectRef& b) {
        return !(a == b);
    }
    friend constexpr bool operator<(const ObjectRef& a, const ObjectRef& b) {
        return a.id != b.id ? a.id < b.id : a.generation < b.generation;
    }
};

struct ObjectInfo {
    ObjectId id = kNoObject;
    Generation generation = 0;
    Offset size = 0;
};

struct FrameInfo {
    FrameId id = kNoFrame;
    FrameId parent = kNoFrame;
    BranchId branch = kRootBranch;
    std::uint64_t depth = 0;
    std::uint64_t lineage_hash = 0;
    FrameState state = FrameState::Active;
    std::uint64_t suspension_epoch = 0;
    std::vector<FrameId> children;
};

struct Lease {
    LeaseId id = kNoLease;
    FrameId owner = kNoFrame;
    ObjectRef object;
    Range range;
    BorrowMode mode = BorrowMode::Shared;
    bool live = true;
    std::string origin;
};

enum class Status {
    Ok,
    NotFound,
    NotActive,
    Suspended,
    Cancelled,
    Returned,
    StaleGeneration,
    OutOfBounds,
    InvalidRange,
    Conflict,
    HasLiveChildren,
    InvalidTicket,
    BranchMismatch,
    TicketUsed,
    TicketExpired,
    TicketHostMismatch,
    NotSuspended,
    AlreadyExists,
    AlreadyCancelled,
    AlreadyReturned,
    NoLease,
    RootFrame
};

inline const char* status_name(Status s) {
    switch (s) {
        case Status::Ok: return "ok";
        case Status::NotFound: return "not-found";
        case Status::NotActive: return "not-active";
        case Status::Suspended: return "suspended";
        case Status::Cancelled: return "cancelled";
        case Status::Returned: return "returned";
        case Status::StaleGeneration: return "stale-generation";
        case Status::OutOfBounds: return "out-of-bounds";
        case Status::InvalidRange: return "invalid-range";
        case Status::Conflict: return "conflict";
        case Status::HasLiveChildren: return "has-live-children";
        case Status::InvalidTicket: return "invalid-ticket";
        case Status::BranchMismatch: return "branch-mismatch";
        case Status::TicketUsed: return "ticket-used";
        case Status::TicketExpired: return "ticket-expired";
        case Status::TicketHostMismatch: return "ticket-host-mismatch";
        case Status::NotSuspended: return "not-suspended";
        case Status::AlreadyExists: return "already-exists";
        case Status::AlreadyCancelled: return "already-cancelled";
        case Status::AlreadyReturned: return "already-returned";
        case Status::NoLease: return "no-lease";
        case Status::RootFrame: return "root-frame";
    }
    return "unknown";
}

inline const char* decision_name(Decision d) {
    switch (d) {
        case Decision::Allow: return "allow";
        case Decision::Forbid: return "forbid";
        case Decision::Undecided: return "undecided";
    }
    return "unknown";
}

inline const char* frame_state_name(FrameState s) {
    switch (s) {
        case FrameState::Active: return "active";
        case FrameState::Suspended: return "suspended";
        case FrameState::Cancelled: return "cancelled";
        case FrameState::Returned: return "returned";
    }
    return "unknown";
}

inline const char* borrow_mode_name(BorrowMode m) {
    return m == BorrowMode::Shared ? "shared" : "exclusive";
}

inline const char* query_kind_name(QueryKind k) {
    switch (k) {
        case QueryKind::Read: return "read";
        case QueryKind::Write: return "write";
        case QueryKind::Move: return "move";
        case QueryKind::Return: return "return";
    }
    return "unknown";
}

// ---------------------------------------------------------------------------
// Hashing helpers (deterministic, endian independent)
// ---------------------------------------------------------------------------

inline std::uint64_t mix_hash(std::uint64_t h, std::uint64_t v) {
    // FNV-1a over the little-endian bytes of v.
    for (int i = 0; i < 8; ++i) {
        h ^= static_cast<std::uint8_t>((v >> (i * 8)) & 0xffu);
        h *= 1099511628211ull;
    }
    return h;
}

inline std::uint64_t hash_bytes(std::uint64_t h, const void* data, std::size_t n) {
    const auto* p = static_cast<const unsigned char*>(data);
    for (std::size_t i = 0; i < n; ++i) {
        h ^= p[i];
        h *= 1099511628211ull;
    }
    return h;
}

inline std::string hex_u64(std::uint64_t v) {
    static const char* digits = "0123456789abcdef";
    std::string out(16, '0');
    for (int i = 15; i >= 0; --i) {
        out[static_cast<std::size_t>(i)] = digits[v & 0xfu];
        v >>= 4;
    }
    return out;
}

// ---------------------------------------------------------------------------
// Suspension tickets
// ---------------------------------------------------------------------------

struct SuspendTicket {
    TicketId id = 0;
    FrameId frame = kNoFrame;
    BranchId branch = 0;
    std::uint64_t lineage_hash = 0;
    std::uint64_t suspension_epoch = 0;
    std::uint64_t host_id = 0;
};

struct SuspendResult {
    Status status = Status::Ok;
    SuspendTicket ticket;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

struct ResumeResult {
    Status status = Status::Ok;
    FrameId frame = kNoFrame;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

struct BorrowResult {
    Status status = Status::Ok;
    LeaseId lease = kNoLease;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

struct AccessCheck {
    bool allowed = false;
    Status status = Status::Conflict;
    BorrowMode effective_mode = BorrowMode::Shared;
    FrameId blocker = kNoFrame;
    LeaseId blocker_lease = kNoLease;
    std::string detail;
    explicit operator bool() const { return allowed; }
};

struct QueryReport {
    QueryKind kind = QueryKind::Read;
    FrameId frame = kNoFrame;
    ObjectRef object;
    Range range;
    Decision decision = Decision::Undecided;
    IntervalSet allowed;
    IntervalSet forbidden;
    IntervalSet undecided;
    FrameId blocker = kNoFrame;
    LeaseId blocker_lease = kNoLease;
    std::string reason;
};

// ---------------------------------------------------------------------------
// Certificates
// ---------------------------------------------------------------------------

struct ObjectView {
    ObjectId id = kNoObject;
    Generation generation = 0;
    Offset size = 0;

    friend bool operator==(const ObjectView& a, const ObjectView& b) {
        return a.id == b.id && a.generation == b.generation && a.size == b.size;
    }
};

struct FrameView {
    FrameId id = kNoFrame;
    FrameId parent = kNoFrame;
    BranchId branch = kRootBranch;
    std::uint64_t depth = 0;
    std::uint64_t lineage_hash = 0;
    FrameState state = FrameState::Active;
    std::vector<FrameId> children;

    friend bool operator==(const FrameView& a, const FrameView& b) {
        return a.id == b.id && a.parent == b.parent && a.branch == b.branch &&
               a.depth == b.depth && a.lineage_hash == b.lineage_hash &&
               a.state == b.state && a.children == b.children;
    }
};

struct LeaseView {
    LeaseId id = kNoLease;
    FrameId owner = kNoFrame;
    ObjectRef object;
    Range range;
    BorrowMode mode = BorrowMode::Shared;
    bool live = true;

    friend bool operator==(const LeaseView& a, const LeaseView& b) {
        return a.id == b.id && a.owner == b.owner && a.object == b.object &&
               a.range == b.range && a.mode == b.mode && a.live == b.live;
    }
};

struct TicketView {
    TicketId id = 0;
    FrameId frame = kNoFrame;
    BranchId branch = 0;
    std::uint64_t lineage_hash = 0;
    std::uint64_t suspension_epoch = 0;
    bool valid = false;

    friend bool operator==(const TicketView& a, const TicketView& b) {
        return a.id == b.id && a.frame == b.frame && a.branch == b.branch &&
               a.lineage_hash == b.lineage_hash &&
               a.suspension_epoch == b.suspension_epoch && a.valid == b.valid;
    }
};

struct StackCertificate {
    std::vector<ObjectView> objects;
    std::vector<FrameView> frames;
    std::vector<LeaseView> leases;
    std::vector<TicketView> tickets;
    std::string summary;
    std::uint64_t digest = 0;

    std::uint64_t recompute_digest() const;
    bool internally_valid() const;
    bool verify(const class Authority& authority) const;
    std::string to_json() const;
};

struct QueryCertificate {
    QueryKind kind = QueryKind::Read;
    FrameId frame = kNoFrame;
    ObjectRef object;
    Range range;
    Decision decision = Decision::Undecided;
    IntervalSet allowed;
    IntervalSet forbidden;
    IntervalSet undecided;
    FrameId blocker = kNoFrame;
    LeaseId blocker_lease = kNoLease;
    std::string reason;
    StackCertificate stack;
    std::uint64_t digest = 0;

    bool verify(const class Authority& authority) const;
    std::string to_json() const;
};

inline std::uint64_t stack_certificate_digest(const StackCertificate& cert) {
    std::uint64_t h = 1469598103934665603ull;
    for (const ObjectView& o : cert.objects) {
        h = mix_hash(h, o.id);
        h = mix_hash(h, o.generation);
        h = mix_hash(h, o.size);
    }
    h = mix_hash(h, 0x6f626a65637473ull);
    for (const FrameView& f : cert.frames) {
        h = mix_hash(h, f.id);
        h = mix_hash(h, f.parent);
        h = mix_hash(h, f.branch);
        h = mix_hash(h, f.depth);
        h = mix_hash(h, f.lineage_hash);
        h = mix_hash(h, static_cast<std::uint64_t>(f.state));
        h = mix_hash(h, f.children.size());
        for (FrameId c : f.children) h = mix_hash(h, c);
    }
    h = mix_hash(h, 0x6672616d6573ull);
    for (const LeaseView& l : cert.leases) {
        h = mix_hash(h, l.id);
        h = mix_hash(h, l.owner);
        h = mix_hash(h, l.object.id);
        h = mix_hash(h, l.object.generation);
        h = mix_hash(h, l.range.begin);
        h = mix_hash(h, l.range.end);
        h = mix_hash(h, static_cast<std::uint64_t>(l.mode));
        h = mix_hash(h, l.live ? 1 : 0);
    }
    h = mix_hash(h, 0x7469636b657473ull);
    for (const TicketView& t : cert.tickets) {
        h = mix_hash(h, t.id);
        h = mix_hash(h, t.frame);
        h = mix_hash(h, t.branch);
        h = mix_hash(h, t.lineage_hash);
        h = mix_hash(h, t.suspension_epoch);
        h = mix_hash(h, t.valid ? 1 : 0);
    }
    return h;
}

// ---------------------------------------------------------------------------
// Authority
// ---------------------------------------------------------------------------

class Authority {
public:
    Authority();
    Authority(const Authority& other);
    Authority& operator=(const Authority& other);
    Authority(Authority&&) noexcept = default;
    Authority& operator=(Authority&&) noexcept = default;

    void reset();

    // Objects -------------------------------------------------------------
    ObjectRef define_object(ObjectId id, Offset size, Generation generation = 0);
    const ObjectInfo* object(ObjectId id) const;
    std::optional<ObjectRef> current_ref(ObjectId id) const;
    bool is_current(ObjectRef ref) const;
    std::optional<ObjectRef> move_object(ObjectId id);
    AccessCheck can_move(FrameId frame, ObjectRef ref, Range range) const;

    // Frames --------------------------------------------------------------
    FrameId enter(FrameId parent = kRootFrame);
    FrameId enter_branch(FrameId parent, BranchId branch);
    Status enter_into(FrameId* out, FrameId parent, std::optional<FrameId> requested_id,
                      std::optional<BranchId> branch);

    const FrameInfo* frame(FrameId id) const;
    bool has_frame(FrameId id) const;
    std::vector<FrameId> call_chain(FrameId id) const;
    std::vector<FrameId> descendants(FrameId id) const;
    bool is_ancestor_or_self(FrameId ancestor, FrameId frame) const;

    SuspendResult suspend(FrameId id);
    bool ticket_valid(const SuspendTicket& ticket) const;
    ResumeResult resume(const SuspendTicket& ticket);
    ResumeResult resume_as(const SuspendTicket& ticket, FrameId target);
    Status resume_chain(const std::vector<SuspendTicket>& tickets,
                        std::vector<FrameId>* resumed = nullptr);

    Status cancel(FrameId id);
    Status leave(FrameId id);
    Status return_frame(FrameId id) { return leave(id); }

    // Borrowing -----------------------------------------------------------
    AccessCheck can_access(FrameId frame, ObjectRef ref, Range range,
                           AccessKind kind) const;
    AccessCheck can_borrow(FrameId frame, ObjectRef ref, Range range,
                           BorrowMode mode) const;
    BorrowResult acquire(FrameId frame, ObjectRef ref, Range range, BorrowMode mode,
                         std::string origin = {});
    Status release(FrameId frame, ObjectRef ref, Range range);
    Status release_all(FrameId frame);

    std::vector<Lease> leases_of(FrameId frame, bool include_dead = false) const;
    std::vector<Lease> all_leases(bool include_dead = false) const;
    IntervalSet borrowed_intervals(FrameId frame, ObjectRef ref) const;
    IntervalSet accessible_intervals(FrameId frame, ObjectRef ref, AccessKind kind,
                                     Range within) const;
    BorrowMode capability_mode(FrameId frame, ObjectRef ref, Range range) const;

    // Reports and certificates --------------------------------------------
    QueryReport evaluate(FrameId frame, ObjectRef ref, Range range, QueryKind kind) const;
    QueryReport evaluate(FrameId frame, ObjectId object, Range range, QueryKind kind,
                         std::optional<Generation> generation = std::nullopt) const;

    StackCertificate stack_certificate() const;
    std::string stack_summary() const { return stack_certificate().summary; }
    QueryCertificate certificate(FrameId frame, ObjectRef ref, Range range,
                                 QueryKind kind) const;
    QueryCertificate certificate(FrameId frame, ObjectId object, Range range,
                                 QueryKind kind,
                                 std::optional<Generation> generation = std::nullopt) const;

    const std::vector<ObjectInfo>& objects() const { return object_list_; }
    const std::vector<FrameInfo>& frames() const { return frame_list_; }

private:
    struct TicketRecord {
        SuspendTicket ticket;
        bool valid = true;
    };

    static std::uint64_t next_host_id();
    void rebuild_lists();
    void invalidate_tickets_for(FrameId frame);
    void collect_subtree(FrameId frame, std::vector<FrameId>* out) const;
    void revoke_leases(FrameId frame);
    AccessCheck check_range(ObjectRef ref, Range range) const;
    std::uint64_t compute_lineage(FrameId parent_hash, FrameId id, BranchId branch,
                                  std::uint64_t birth) const;

    std::unordered_map<ObjectId, ObjectInfo> objects_;
    std::unordered_map<FrameId, FrameInfo> frames_;
    std::unordered_map<FrameId, std::vector<Lease>> leases_;
    std::unordered_map<TicketId, TicketRecord> tickets_;

    // Flattened views kept in sync for stable iteration and certificates.
    std::vector<ObjectInfo> object_list_;
    std::vector<FrameInfo> frame_list_;

    FrameId next_frame_id_ = 1;
    LeaseId next_lease_id_ = 1;
    TicketId next_ticket_id_ = 1;
    std::uint64_t host_id_ = 0;
};

// ------------------------- inline implementation ---------------------------

inline std::uint64_t Authority::next_host_id() {
    static std::atomic<std::uint64_t> counter{1};
    return counter.fetch_add(1, std::memory_order_relaxed);
}

inline Authority::Authority() { reset(); }

inline Authority::Authority(const Authority& other)
    : objects_(other.objects_),
      frames_(other.frames_),
      leases_(other.leases_),
      tickets_(other.tickets_),
      object_list_(other.object_list_),
      frame_list_(other.frame_list_),
      next_frame_id_(other.next_frame_id_),
      next_lease_id_(other.next_lease_id_),
      next_ticket_id_(other.next_ticket_id_),
      host_id_(next_host_id()) {
    // Tickets are capabilities of the original host instance.  A copy is a
    // new host and cannot consume them.
    for (auto& kv : tickets_) kv.second.valid = false;
}

inline Authority& Authority::operator=(const Authority& other) {
    if (this == &other) return *this;
    objects_ = other.objects_;
    frames_ = other.frames_;
    leases_ = other.leases_;
    tickets_ = other.tickets_;
    object_list_ = other.object_list_;
    frame_list_ = other.frame_list_;
    next_frame_id_ = other.next_frame_id_;
    next_lease_id_ = other.next_lease_id_;
    next_ticket_id_ = other.next_ticket_id_;
    host_id_ = next_host_id();
    for (auto& kv : tickets_) kv.second.valid = false;
    return *this;
}

inline void Authority::reset() {
    objects_.clear();
    frames_.clear();
    leases_.clear();
    tickets_.clear();
    object_list_.clear();
    frame_list_.clear();
    next_frame_id_ = 1;
    next_lease_id_ = 1;
    next_ticket_id_ = 1;
    host_id_ = next_host_id();

    FrameInfo root;
    root.id = kRootFrame;
    root.parent = kNoFrame;
    root.branch = kRootBranch;
    root.depth = 0;
    root.lineage_hash = mix_hash(1469598103934665603ull, 0x726f6f74ull);
    root.state = FrameState::Active;
    frames_.emplace(root.id, root);
    leases_[root.id] = {};
    rebuild_lists();
}

inline void Authority::rebuild_lists() {
    object_list_.clear();
    object_list_.reserve(objects_.size());
    for (const auto& kv : objects_) object_list_.push_back(kv.second);
    std::sort(object_list_.begin(), object_list_.end(),
              [](const ObjectInfo& a, const ObjectInfo& b) { return a.id < b.id; });

    frame_list_.clear();
    frame_list_.reserve(frames_.size());
    for (const auto& kv : frames_) frame_list_.push_back(kv.second);
    std::sort(frame_list_.begin(), frame_list_.end(),
              [](const FrameInfo& a, const FrameInfo& b) { return a.id < b.id; });
}

inline ObjectRef Authority::define_object(ObjectId id, Offset size,
                                          Generation generation) {
    auto old = objects_.find(id);
    if (old != objects_.end() &&
        (old->second.generation != generation || old->second.size != size)) {
        for (auto& frame_leases : leases_) {
            for (Lease& lease : frame_leases.second) {
                if (lease.live && lease.object.id == id) lease.live = false;
            }
        }
    }
    ObjectInfo info;
    info.id = id;
    info.size = size;
    info.generation = generation;
    objects_[id] = info;
    rebuild_lists();
    return ObjectRef{id, generation};
}

inline const ObjectInfo* Authority::object(ObjectId id) const {
    auto it = objects_.find(id);
    return it == objects_.end() ? nullptr : &it->second;
}

inline std::optional<ObjectRef> Authority::current_ref(ObjectId id) const {
    auto it = objects_.find(id);
    if (it == objects_.end()) return std::nullopt;
    return ObjectRef{id, it->second.generation};
}

inline bool Authority::is_current(ObjectRef ref) const {
    auto it = objects_.find(ref.id);
    return it != objects_.end() && it->second.generation == ref.generation;
}

inline std::optional<ObjectRef> Authority::move_object(ObjectId id) {
    auto it = objects_.find(id);
    if (it == objects_.end()) return std::nullopt;
    ++it->second.generation;
    const ObjectRef fresh{id, it->second.generation};
    for (auto& frame_leases : leases_) {
        for (Lease& lease : frame_leases.second) {
            if (lease.live && lease.object.id == id) lease.live = false;
        }
    }
    rebuild_lists();
    return fresh;
}

inline AccessCheck Authority::can_move(FrameId frame, ObjectRef ref,
                                       Range range) const {
    AccessCheck write = can_access(frame, ref, range, AccessKind::Write);
    if (!write.allowed) return write;

    for (const auto& kv : leases_) {
        for (const Lease& lease : kv.second) {
            if (!lease.live || lease.object != ref || !lease.range.overlaps(range)) {
                continue;
            }
            if (lease.owner == frame || is_ancestor_or_self(lease.owner, frame)) {
                continue;
            }
            AccessCheck out;
            out.allowed = false;
            out.status = Status::Conflict;
            out.effective_mode = write.effective_mode;
            out.blocker = lease.owner;
            out.blocker_lease = lease.id;
            out.detail = "outstanding child or sibling lease blocks move";
            return out;
        }
    }
    return write;
}

inline std::uint64_t Authority::compute_lineage(FrameId parent_hash, FrameId id,
                                                BranchId branch,
                                                std::uint64_t birth) const {
    std::uint64_t h = parent_hash;
    h = mix_hash(h, id);
    h = mix_hash(h, branch);
    h = mix_hash(h, birth);
    return h;
}

inline Status Authority::enter_into(FrameId* out, FrameId parent,
                                    std::optional<FrameId> requested_id,
                                    std::optional<BranchId> branch) {
    if (out == nullptr) return Status::NotFound;
    auto pit = frames_.find(parent);
    if (pit == frames_.end()) return Status::NotFound;
    if (pit->second.state != FrameState::Active) return Status::NotActive;

    FrameId id = requested_id.value_or(0);
    if (id == 0) {
        id = next_frame_id_++;
    } else {
        if (id == kRootFrame || id == kNoFrame) return Status::AlreadyExists;
        if (frames_.count(id)) return Status::AlreadyExists;
        if (id >= next_frame_id_) next_frame_id_ = id + 1;
    }

    FrameInfo info;
    info.id = id;
    info.parent = parent;
    info.branch = branch.value_or(pit->second.branch);
    info.depth = pit->second.depth + 1;
    info.lineage_hash =
        compute_lineage(pit->second.lineage_hash, id, info.branch,
                        static_cast<std::uint64_t>(next_frame_id_ * 1315423911ull + id));
    info.state = FrameState::Active;
    frames_.emplace(id, info);
    leases_[id] = {};
    auto cit = frames_.find(parent);
    cit->second.children.push_back(id);
    rebuild_lists();
    *out = id;
    return Status::Ok;
}

inline FrameId Authority::enter(FrameId parent) {
    FrameId id = kNoFrame;
    enter_into(&id, parent, std::nullopt, std::nullopt);
    return id;
}

inline FrameId Authority::enter_branch(FrameId parent, BranchId branch) {
    FrameId id = kNoFrame;
    enter_into(&id, parent, std::nullopt, branch);
    return id;
}

inline const FrameInfo* Authority::frame(FrameId id) const {
    auto it = frames_.find(id);
    return it == frames_.end() ? nullptr : &it->second;
}

inline bool Authority::has_frame(FrameId id) const { return frames_.count(id) != 0; }

inline std::vector<FrameId> Authority::call_chain(FrameId id) const {
    std::vector<FrameId> chain;
    FrameId cur = id;
    while (cur != kNoFrame) {
        auto it = frames_.find(cur);
        if (it == frames_.end()) return {};
        chain.push_back(cur);
        if (cur == kRootFrame) break;
        cur = it->second.parent;
        if (chain.size() > frames_.size()) return {};
    }
    std::reverse(chain.begin(), chain.end());
    return chain;
}

inline bool Authority::is_ancestor_or_self(FrameId ancestor, FrameId f) const {
    FrameId cur = f;
    while (cur != kNoFrame) {
        if (cur == ancestor) return true;
        auto it = frames_.find(cur);
        if (it == frames_.end()) return false;
        if (cur == kRootFrame) break;
        cur = it->second.parent;
    }
    return false;
}

inline void Authority::collect_subtree(FrameId id, std::vector<FrameId>* out) const {
    out->push_back(id);
    auto it = frames_.find(id);
    if (it == frames_.end()) return;
    for (FrameId child : it->second.children) collect_subtree(child, out);
}

inline std::vector<FrameId> Authority::descendants(FrameId id) const {
    std::vector<FrameId> all;
    collect_subtree(id, &all);
    if (!all.empty()) all.erase(all.begin());
    return all;
}

inline void Authority::revoke_leases(FrameId id) {
    auto it = leases_.find(id);
    if (it == leases_.end()) return;
    for (Lease& lease : it->second) lease.live = false;
}

inline void Authority::invalidate_tickets_for(FrameId id) {
    for (auto& kv : tickets_) {
        if (kv.second.ticket.frame == id) kv.second.valid = false;
    }
}

inline SuspendResult Authority::suspend(FrameId id) {
    SuspendResult out;
    auto it = frames_.find(id);
    if (it == frames_.end()) {
        out.status = Status::NotFound;
        return out;
    }
    if (id == kRootFrame) {
        out.status = Status::RootFrame;
        return out;
    }
    if (it->second.state == FrameState::Suspended) {
        out.status = Status::Suspended;
        return out;
    }
    if (it->second.state != FrameState::Active) {
        out.status = it->second.state == FrameState::Cancelled ? Status::Cancelled
                                                              : Status::Returned;
        return out;
    }
    for (FrameId child : it->second.children) {
        auto cit = frames_.find(child);
        if (cit != frames_.end() && cit->second.state == FrameState::Active) {
            out.status = Status::HasLiveChildren;
            out.detail = "suspend active children before their caller";
            return out;
        }
    }

    ++it->second.suspension_epoch;
    it->second.state = FrameState::Suspended;

    SuspendTicket ticket;
    ticket.id = next_ticket_id_++;
    ticket.frame = id;
    ticket.branch = it->second.branch;
    ticket.lineage_hash = it->second.lineage_hash;
    ticket.suspension_epoch = it->second.suspension_epoch;
    ticket.host_id = host_id_;
    tickets_[ticket.id] = TicketRecord{ticket, true};
    out.ticket = ticket;
    rebuild_lists();
    return out;
}

inline bool Authority::ticket_valid(const SuspendTicket& ticket) const {
    auto tit = tickets_.find(ticket.id);
    if (tit == tickets_.end() || !tit->second.valid) return false;
    if (ticket.host_id != host_id_) return false;
    auto fit = frames_.find(ticket.frame);
    if (fit == frames_.end()) return false;
    const FrameInfo& f = fit->second;
    return f.state == FrameState::Suspended && f.branch == ticket.branch &&
           f.lineage_hash == ticket.lineage_hash &&
           f.suspension_epoch == ticket.suspension_epoch;
}

inline ResumeResult Authority::resume_as(const SuspendTicket& ticket,
                                         FrameId target) {
    ResumeResult out;
    if (!tickets_.count(ticket.id)) {
        out.status = Status::InvalidTicket;
        out.detail = "unknown ticket";
        return out;
    }
    if (ticket.host_id != host_id_) {
        out.status = Status::TicketHostMismatch;
        out.detail = "ticket belongs to another authority";
        return out;
    }
    if (!tickets_[ticket.id].valid) {
        auto fit = frames_.find(ticket.frame);
        if (fit != frames_.end() && fit->second.state == FrameState::Suspended) {
            out.status = Status::TicketExpired;
        } else {
            out.status = Status::TicketUsed;
        }
        return out;
    }
    if (target != ticket.frame) {
        out.status = Status::BranchMismatch;
        out.detail = "a ticket cannot be transplanted onto another frame";
        return out;
    }
    auto fit = frames_.find(target);
    if (fit == frames_.end()) {
        out.status = Status::NotFound;
        return out;
    }
    FrameInfo& f = fit->second;
    if (f.state != FrameState::Suspended) {
        out.status = f.state == FrameState::Cancelled ? Status::Cancelled
                                                      : Status::NotSuspended;
        return out;
    }
    if (f.branch != ticket.branch || f.lineage_hash != ticket.lineage_hash) {
        out.status = Status::BranchMismatch;
        out.detail = "ticket branch or lineage does not match the suspended frame";
        return out;
    }
    if (f.suspension_epoch != ticket.suspension_epoch) {
        out.status = Status::TicketExpired;
        return out;
    }

    // A continuation can only resume while every caller is running.  A
    // suspended or cancelled ancestor would make the reconstructed chain
    // unreal.
    const std::vector<FrameId> chain = call_chain(f.parent);
    for (FrameId id : chain) {
        auto cit = frames_.find(id);
        if (cit == frames_.end() || cit->second.state != FrameState::Active) {
            out.status = Status::NotActive;
            out.detail = "an ancestor of the restored frame is not active";
            return out;
        }
    }

    f.state = FrameState::Active;
    tickets_[ticket.id].valid = false;
    out.frame = target;
    rebuild_lists();
    return out;
}

inline ResumeResult Authority::resume(const SuspendTicket& ticket) {
    return resume_as(ticket, ticket.frame);
}

inline Status Authority::resume_chain(const std::vector<SuspendTicket>& tickets,
                                      std::vector<FrameId>* resumed) {
    if (tickets.empty()) return Status::InvalidTicket;

    // Validate the whole chain first so a rejected splice leaves the host
    // untouched.
    BranchId branch = tickets.front().branch;
    for (std::size_t i = 0; i < tickets.size(); ++i) {
        const SuspendTicket& t = tickets[i];
        if (t.host_id != host_id_) return Status::TicketHostMismatch;
        if (!ticket_valid(t)) {
            auto fit = frames_.find(t.frame);
            if (fit != frames_.end() && fit->second.state == FrameState::Suspended) {
                return Status::TicketExpired;
            }
            return Status::InvalidTicket;
        }
        if (t.branch != branch) return Status::BranchMismatch;
        auto fit = frames_.find(t.frame);
        if (fit == frames_.end()) return Status::NotFound;
        if (i > 0) {
            if (fit->second.parent != tickets[i - 1].frame) {
                return Status::BranchMismatch;
            }
        }
    }

    std::vector<FrameId> local;
    local.reserve(tickets.size());
    for (const SuspendTicket& t : tickets) {
        ResumeResult rr = resume(t);
        if (!rr) {
            // Roll back only what this call resumed; the validation above
            // makes this path extremely unlikely.
            for (FrameId id : local) {
                auto it = frames_.find(id);
                if (it != frames_.end()) it->second.state = FrameState::Suspended;
            }
            rebuild_lists();
            return rr.status;
        }
        local.push_back(t.frame);
    }
    if (resumed) *resumed = local;
    return Status::Ok;
}

inline Status Authority::cancel(FrameId id) {
    auto it = frames_.find(id);
    if (it == frames_.end()) return Status::NotFound;
    if (id == kRootFrame) return Status::RootFrame;
    if (it->second.state == FrameState::Cancelled) return Status::AlreadyCancelled;
    if (it->second.state == FrameState::Returned) return Status::AlreadyReturned;

    std::vector<FrameId> subtree;
    collect_subtree(id, &subtree);
    for (FrameId node : subtree) {
        auto nit = frames_.find(node);
        if (nit == frames_.end()) continue;
        if (nit->second.state == FrameState::Returned ||
            nit->second.state == FrameState::Cancelled) {
            continue;
        }
        nit->second.state = FrameState::Cancelled;
        revoke_leases(node);
        invalidate_tickets_for(node);
    }
    rebuild_lists();
    return Status::Ok;
}

inline Status Authority::leave(FrameId id) {
    auto it = frames_.find(id);
    if (it == frames_.end()) return Status::NotFound;
    if (id == kRootFrame) return Status::RootFrame;
    if (it->second.state == FrameState::Returned) return Status::AlreadyReturned;
    if (it->second.state == FrameState::Cancelled) return Status::AlreadyCancelled;
    if (it->second.state != FrameState::Active) return Status::NotActive;

    for (FrameId child : it->second.children) {
        auto cit = frames_.find(child);
        if (cit != frames_.end() && (cit->second.state == FrameState::Active ||
                                     cit->second.state == FrameState::Suspended)) {
            return Status::HasLiveChildren;
        }
    }
    revoke_leases(id);
    invalidate_tickets_for(id);
    it->second.state = FrameState::Returned;
    rebuild_lists();
    return Status::Ok;
}

inline AccessCheck Authority::check_range(ObjectRef ref, Range range) const {
    AccessCheck out;
    auto it = objects_.find(ref.id);
    if (it == objects_.end()) {
        out.status = Status::NotFound;
        out.detail = "object is not registered";
        return out;
    }
    if (it->second.generation != ref.generation) {
        out.status = Status::StaleGeneration;
        out.detail = "object generation has been moved past";
        return out;
    }
    if (range.begin > range.end) {
        out.status = Status::InvalidRange;
        out.detail = "range begin is greater than range end";
        return out;
    }
    if (!range.empty() && range.end > it->second.size) {
        out.status = Status::OutOfBounds;
        out.detail = "range exceeds object size";
        return out;
    }
    out.status = Status::Ok;
    return out;
}

inline BorrowMode Authority::capability_mode(FrameId frame, ObjectRef ref,
                                             Range range) const {
    const std::vector<FrameId> chain = call_chain(frame);
    if (chain.empty()) return BorrowMode::Shared;
    BorrowMode cap = BorrowMode::Exclusive;  // the host root owns the object
    for (FrameId id : chain) {
        auto lit = leases_.find(id);
        if (lit == leases_.end()) continue;
        for (const Lease& lease : lit->second) {
            if (!lease.live || lease.object != ref) continue;
            if (!lease.range.covers(range)) continue;
            if (lease.mode == BorrowMode::Shared) cap = BorrowMode::Shared;
        }
    }
    return cap;
}

inline AccessCheck Authority::can_access(FrameId frame, ObjectRef ref, Range range,
                                         AccessKind kind) const {
    AccessCheck range_check = check_range(ref, range);
    if (range_check.status != Status::Ok) return range_check;

    auto fit = frames_.find(frame);
    if (fit == frames_.end()) {
        AccessCheck out;
        out.status = Status::NotFound;
        out.detail = "frame is not registered";
        return out;
    }
    if (fit->second.state == FrameState::Suspended) {
        AccessCheck out;
        out.status = Status::Suspended;
        out.detail = "a suspended frame does not execute";
        return out;
    }
    if (fit->second.state != FrameState::Active) {
        AccessCheck out;
        out.status = fit->second.state == FrameState::Cancelled ? Status::Cancelled
                                                                : Status::Returned;
        out.detail = "only an active frame may access an object";
        return out;
    }
    if (range.empty()) {
        AccessCheck out;
        out.allowed = true;
        out.status = Status::Ok;
        out.effective_mode = BorrowMode::Exclusive;
        return out;
    }

    AccessCheck out;
    out.status = Status::Ok;
    out.effective_mode = capability_mode(frame, ref, range);
    if (kind == AccessKind::Read) {
        out.allowed = true;  // both Shared and Exclusive capabilities permit read
    } else {
        out.allowed = out.effective_mode == BorrowMode::Exclusive;
    }
    if (!out.allowed) {
        out.status = Status::Conflict;
        out.detail = "an enclosing shared lease forbids this write";
        return out;
    }

    if (kind == AccessKind::Write) {
        // capability_mode() answers for ranges fully covered by a lease.  A
        // write that straddles the edge of a shared lease is still partly
        // shared, so reject the conservative whole-range query.
        const std::vector<FrameId> chain = call_chain(frame);
        for (FrameId id : chain) {
            auto lit = leases_.find(id);
            if (lit == leases_.end()) continue;
            for (const Lease& lease : lit->second) {
                if (!lease.live || lease.object != ref) continue;
                if (lease.mode != BorrowMode::Shared) continue;
                if (!lease.range.overlaps(range)) continue;
                out.allowed = false;
                out.status = Status::Conflict;
                out.blocker = id;
                out.blocker_lease = lease.id;
                out.detail = "a shared lease overlaps part of this write";
                return out;
            }
        }
    }

    for (const auto& kv : leases_) {
        const FrameId owner = kv.first;
        if (owner == frame) continue;
        if (is_ancestor_or_self(owner, frame)) continue;
        for (const Lease& lease : kv.second) {
            if (!lease.live || lease.object != ref) continue;
            if (!lease.range.overlaps(range)) continue;
            const bool conflicts =
                lease.mode == BorrowMode::Exclusive || kind == AccessKind::Write;
            if (conflicts) {
                out.allowed = false;
                out.status = Status::Conflict;
                out.blocker = owner;
                out.blocker_lease = lease.id;
                out.detail = lease.mode == BorrowMode::Exclusive
                                 ? "an outstanding exclusive lease conflicts"
                                 : "an outstanding shared lease conflicts with write";
                return out;
            }
        }
    }
    return out;
}

inline AccessCheck Authority::can_borrow(FrameId frame, ObjectRef ref, Range range,
                                         BorrowMode mode) const {
    AccessCheck range_check = check_range(ref, range);
    if (range_check.status != Status::Ok) return range_check;

    auto fit = frames_.find(frame);
    if (fit == frames_.end()) {
        AccessCheck out;
        out.status = Status::NotFound;
        out.detail = "frame is not registered";
        return out;
    }
    if (fit->second.state != FrameState::Active) {
        AccessCheck out;
        out.status = fit->second.state == FrameState::Suspended ? Status::Suspended
                                                                : Status::NotActive;
        out.detail = "only an active frame may acquire a borrow";
        return out;
    }
    if (range.empty()) {
        AccessCheck out;
        out.allowed = true;
        out.status = Status::Ok;
        out.effective_mode = BorrowMode::Exclusive;
        return out;
    }

    AccessCheck out;
    out.status = Status::Ok;
    out.effective_mode = capability_mode(frame, ref, range);
    if (mode == BorrowMode::Exclusive && out.effective_mode != BorrowMode::Exclusive) {
        out.allowed = false;
        out.status = Status::Conflict;
        out.detail = "the enclosing call chain only grants shared access";
        return out;
    }
    if (mode == BorrowMode::Exclusive) {
        const std::vector<FrameId> chain = call_chain(frame);
        for (FrameId id : chain) {
            auto lit = leases_.find(id);
            if (lit == leases_.end()) continue;
            for (const Lease& lease : lit->second) {
                if (!lease.live || lease.object != ref) continue;
                if (lease.mode != BorrowMode::Shared) continue;
                if (!lease.range.overlaps(range)) continue;
                out.allowed = false;
                out.status = Status::Conflict;
                out.blocker = id;
                out.blocker_lease = lease.id;
                out.detail = "a shared lease overlaps part of this exclusive borrow";
                return out;
            }
        }
    }
    out.allowed = true;

    for (const auto& kv : leases_) {
        const FrameId owner = kv.first;
        if (owner == frame) continue;
        if (is_ancestor_or_self(owner, frame)) continue;
        for (const Lease& lease : kv.second) {
            if (!lease.live || lease.object != ref) continue;
            if (!lease.range.overlaps(range)) continue;
            const bool conflicts =
                lease.mode == BorrowMode::Exclusive || mode == BorrowMode::Exclusive;
            if (conflicts) {
                out.allowed = false;
                out.status = Status::Conflict;
                out.blocker = owner;
                out.blocker_lease = lease.id;
                out.detail = lease.mode == BorrowMode::Exclusive
                                 ? "an outstanding exclusive lease conflicts"
                                 : "an outstanding shared lease conflicts with exclusive";
                return out;
            }
        }
    }
    return out;
}

inline BorrowResult Authority::acquire(FrameId frame, ObjectRef ref, Range range,
                                       BorrowMode mode, std::string origin) {
    BorrowResult out;
    AccessCheck check = can_borrow(frame, ref, range, mode);
    if (!check.allowed) {
        out.status = check.status;
        out.detail = check.detail;
        return out;
    }
    if (range.empty()) {
        out.status = Status::InvalidRange;
        out.detail = "an empty borrow cannot be recorded";
        return out;
    }
    Lease lease;
    lease.id = next_lease_id_++;
    lease.owner = frame;
    lease.object = ref;
    lease.range = range;
    lease.mode = mode;
    lease.live = true;
    lease.origin = std::move(origin);
    leases_[frame].push_back(lease);
    out.lease = lease.id;
    return out;
}

inline Status Authority::release(FrameId frame, ObjectRef ref, Range range) {
    auto fit = frames_.find(frame);
    if (fit == frames_.end()) return Status::NotFound;
    if (fit->second.state != FrameState::Active) {
        return fit->second.state == FrameState::Suspended ? Status::Suspended
                                                          : Status::NotActive;
    }
    auto oit = objects_.find(ref.id);
    if (oit == objects_.end()) return Status::NotFound;
    if (oit->second.generation != ref.generation) return Status::StaleGeneration;
    if (range.empty()) return Status::InvalidRange;

    auto lit = leases_.find(frame);
    if (lit == leases_.end()) return Status::NoLease;

    bool changed = false;
    std::vector<Lease> replacement;
    replacement.reserve(lit->second.size() + 2);
    for (Lease lease : lit->second) {
        if (!lease.live || lease.object != ref || !lease.range.overlaps(range)) {
            replacement.push_back(std::move(lease));
            continue;
        }
        changed = true;
        const Range left(lease.range.begin, std::min(lease.range.end, range.begin));
        const Range right(std::max(lease.range.begin, range.end), lease.range.end);
        if (!left.empty()) {
            Lease piece = lease;
            piece.range = left;
            replacement.push_back(std::move(piece));
        }
        if (!right.empty()) {
            Lease piece = lease;
            piece.id = next_lease_id_++;
            piece.range = right;
            replacement.push_back(std::move(piece));
        }
        // The original lease is dropped for the covered part.
    }
    if (!changed) return Status::NoLease;
    lit->second.swap(replacement);
    return Status::Ok;
}

inline Status Authority::release_all(FrameId frame) {
    auto lit = leases_.find(frame);
    if (lit == leases_.end()) return Status::NotFound;
    for (Lease& lease : lit->second) lease.live = false;
    return Status::Ok;
}

inline std::vector<Lease> Authority::leases_of(FrameId frame,
                                               bool include_dead) const {
    std::vector<Lease> out;
    auto it = leases_.find(frame);
    if (it == leases_.end()) return out;
    for (const Lease& lease : it->second) {
        if (include_dead || lease.live) out.push_back(lease);
    }
    std::sort(out.begin(), out.end(),
              [](const Lease& a, const Lease& b) { return a.id < b.id; });
    return out;
}

inline std::vector<Lease> Authority::all_leases(bool include_dead) const {
    std::vector<Lease> out;
    for (const auto& kv : leases_) {
        for (const Lease& lease : kv.second) {
            if (include_dead || lease.live) out.push_back(lease);
        }
    }
    std::sort(out.begin(), out.end(),
              [](const Lease& a, const Lease& b) { return a.id < b.id; });
    return out;
}

inline IntervalSet Authority::borrowed_intervals(FrameId frame,
                                                 ObjectRef ref) const {
    IntervalSet out;
    for (const Lease& lease : leases_of(frame)) {
        if (lease.object == ref) out.add(lease.range);
    }
    return out;
}

inline IntervalSet Authority::accessible_intervals(FrameId frame, ObjectRef ref,
                                                   AccessKind kind,
                                                   Range within) const {
    return evaluate(frame, ref, within,
                    kind == AccessKind::Read ? QueryKind::Read : QueryKind::Write)
        .allowed;
}

inline QueryReport Authority::evaluate(FrameId frame, ObjectRef ref, Range range,
                                       QueryKind kind) const {
    QueryReport report;
    report.kind = kind;
    report.frame = frame;
    report.object = ref;
    report.range = range;

    auto fit = frames_.find(frame);
    if (fit == frames_.end()) {
        report.decision = Decision::Forbid;
        report.forbidden.add(range);
        report.reason = "frame is not registered";
        return report;
    }
    if (fit->second.state != FrameState::Active) {
        report.decision = Decision::Forbid;
        report.forbidden.add(range);
        report.reason = fit->second.state == FrameState::Suspended
                            ? "a suspended frame does not execute"
                            : "only an active frame may query the host";
        return report;
    }

    if (range.begin > range.end) {
        report.decision = Decision::Forbid;
        report.reason = "range begin is greater than range end";
        return report;
    }
    if (range.empty()) {
        report.decision = Decision::Allow;
        return report;
    }

    auto oit = objects_.find(ref.id);
    if (oit == objects_.end() || oit->second.generation != ref.generation ||
        range.end > oit->second.size) {
        report.decision = Decision::Forbid;
        report.forbidden.add(range);
        report.reason = oit == objects_.end()
                            ? "object is not registered"
                            : (oit->second.generation != ref.generation
                                   ? "object generation is stale"
                                   : "range exceeds object size");
        return report;
    }

    if (kind == QueryKind::Return) {
        IntervalSet owned = borrowed_intervals(frame, ref);
        owned.intersect(range);
        report.allowed = owned;
        report.forbidden = IntervalSet(range);
        report.forbidden.subtract(owned);
        if (report.allowed.covers(range)) {
            report.decision = Decision::Allow;
        } else if (report.forbidden.covers(range)) {
            report.decision = Decision::Forbid;
            report.reason = "the frame owns no lease over this range";
        } else {
            report.decision = Decision::Undecided;
            report.reason = "only part of the range is owned by the frame";
        }
        return report;
    }

    std::vector<Offset> cuts;
    cuts.push_back(range.begin);
    cuts.push_back(range.end);
    for (const auto& kv : leases_) {
        for (const Lease& lease : kv.second) {
            if (!lease.live || lease.object != ref) continue;
            if (lease.range.begin > range.begin && lease.range.begin < range.end) {
                cuts.push_back(lease.range.begin);
            }
            if (lease.range.end > range.begin && lease.range.end < range.end) {
                cuts.push_back(lease.range.end);
            }
        }
    }
    std::sort(cuts.begin(), cuts.end());
    cuts.erase(std::unique(cuts.begin(), cuts.end()), cuts.end());

    for (std::size_t i = 0; i + 1 < cuts.size(); ++i) {
        const Range piece(cuts[i], cuts[i + 1]);
        if (piece.empty()) continue;
        AccessCheck check;
        if (kind == QueryKind::Move) {
            check = can_move(frame, ref, piece);
        } else {
            check = can_access(frame, ref, piece,
                               kind == QueryKind::Read ? AccessKind::Read
                                                       : AccessKind::Write);
        }
        if (check.allowed) {
            report.allowed.add(piece);
        } else {
            report.forbidden.add(piece);
            if (report.blocker == kNoFrame && check.blocker != kNoFrame) {
                report.blocker = check.blocker;
                report.blocker_lease = check.blocker_lease;
                report.reason = check.detail;
            }
        }
    }

    if (report.allowed.covers(range)) {
        report.decision = Decision::Allow;
    } else if (report.forbidden.covers(range)) {
        report.decision = Decision::Forbid;
        if (report.reason.empty()) report.reason = "the operation is forbidden";
    } else {
        report.decision = Decision::Undecided;
        if (report.reason.empty()) report.reason = "only part of the range is allowed";
    }
    return report;
}

inline QueryReport Authority::evaluate(FrameId frame, ObjectId object, Range range,
                                       QueryKind kind,
                                       std::optional<Generation> generation) const {
    if (!generation.has_value()) {
        auto ref = current_ref(object);
        if (!ref.has_value()) {
            QueryReport report;
            report.kind = kind;
            report.frame = frame;
            report.range = range;
            report.decision = Decision::Forbid;
            report.forbidden.add(range);
            report.reason = "object is not registered";
            return report;
        }
        return evaluate(frame, *ref, range, kind);
    }
    return evaluate(frame, ObjectRef{object, *generation}, range, kind);
}

inline StackCertificate Authority::stack_certificate() const {
    StackCertificate cert;
    cert.objects.reserve(objects_.size());
    for (const auto& kv : objects_) {
        cert.objects.push_back(ObjectView{kv.second.id, kv.second.generation,
                                          kv.second.size});
    }
    std::sort(cert.objects.begin(), cert.objects.end(),
              [](const ObjectView& a, const ObjectView& b) { return a.id < b.id; });

    cert.frames.reserve(frames_.size());
    for (const auto& kv : frames_) {
        const FrameInfo& f = kv.second;
        FrameView v;
        v.id = f.id;
        v.parent = f.parent;
        v.branch = f.branch;
        v.depth = f.depth;
        v.lineage_hash = f.lineage_hash;
        v.state = f.state;
        v.children = f.children;
        std::sort(v.children.begin(), v.children.end());
        cert.frames.push_back(std::move(v));
    }
    std::sort(cert.frames.begin(), cert.frames.end(),
              [](const FrameView& a, const FrameView& b) { return a.id < b.id; });

    for (const auto& kv : leases_) {
        for (const Lease& lease : kv.second) {
            LeaseView v;
            v.id = lease.id;
            v.owner = lease.owner;
            v.object = lease.object;
            v.range = lease.range;
            v.mode = lease.mode;
            v.live = lease.live;
            cert.leases.push_back(v);
        }
    }
    std::sort(cert.leases.begin(), cert.leases.end(),
              [](const LeaseView& a, const LeaseView& b) { return a.id < b.id; });

    cert.tickets.reserve(tickets_.size());
    for (const auto& kv : tickets_) {
        TicketView v;
        v.id = kv.second.ticket.id;
        v.frame = kv.second.ticket.frame;
        v.branch = kv.second.ticket.branch;
        v.lineage_hash = kv.second.ticket.lineage_hash;
        v.suspension_epoch = kv.second.ticket.suspension_epoch;
        v.valid = kv.second.valid;
        cert.tickets.push_back(v);
    }
    std::sort(cert.tickets.begin(), cert.tickets.end(),
              [](const TicketView& a, const TicketView& b) {
                  return a.id < b.id;
              });

    cert.digest = stack_certificate_digest(cert);
    cert.summary = hex_u64(cert.digest);
    return cert;
}

inline std::uint64_t StackCertificate::recompute_digest() const {
    return stack_certificate_digest(*this);
}

inline bool StackCertificate::internally_valid() const {
    return recompute_digest() == digest && summary == hex_u64(digest);
}

inline bool StackCertificate::verify(const Authority& authority) const {
    StackCertificate fresh = authority.stack_certificate();
    return internally_valid() && fresh.digest == digest &&
           fresh.summary == summary &&
           fresh.objects == objects && fresh.frames == frames &&
           fresh.leases == leases && fresh.tickets == tickets;
}

inline std::string StackCertificate::to_json() const {
    std::string out = "{";
    out += "\"objects\":[";
    for (std::size_t i = 0; i < objects.size(); ++i) {
        if (i) out += ",";
        out += "{\"id\":" + std::to_string(objects[i].id) +
               ",\"generation\":" + std::to_string(objects[i].generation) +
               ",\"size\":" + std::to_string(objects[i].size) + "}";
    }
    out += "],\"frames\":[";
    for (std::size_t i = 0; i < frames.size(); ++i) {
        const FrameView& f = frames[i];
        if (i) out += ",";
        out += "{\"id\":" + std::to_string(f.id) +
               ",\"parent\":" + std::to_string(f.parent) +
               ",\"branch\":" + std::to_string(f.branch) +
               ",\"depth\":" + std::to_string(f.depth) +
               ",\"lineage\":\"" + hex_u64(f.lineage_hash) +
               "\",\"state\":\"" + frame_state_name(f.state) +
               "\",\"children\":[";
        for (std::size_t j = 0; j < f.children.size(); ++j) {
            if (j) out += ",";
            out += std::to_string(f.children[j]);
        }
        out += "]}";
    }
    out += "],\"leases\":[";
    for (std::size_t i = 0; i < leases.size(); ++i) {
        const LeaseView& l = leases[i];
        if (i) out += ",";
        out += "{\"id\":" + std::to_string(l.id) +
               ",\"owner\":" + std::to_string(l.owner) +
               ",\"object\":" + std::to_string(l.object.id) +
               ",\"generation\":" + std::to_string(l.object.generation) +
               ",\"range\":[" + std::to_string(l.range.begin) + "," +
               std::to_string(l.range.end) + "],\"mode\":\"" +
               borrow_mode_name(l.mode) + "\",\"live\":" +
               (l.live ? "true" : "false") + "}";
    }
    out += "],\"tickets\":[";
    for (std::size_t i = 0; i < tickets.size(); ++i) {
        const TicketView& t = tickets[i];
        if (i) out += ",";
        out += "{\"id\":" + std::to_string(t.id) +
               ",\"frame\":" + std::to_string(t.frame) +
               ",\"branch\":" + std::to_string(t.branch) +
               ",\"lineage\":\"" + hex_u64(t.lineage_hash) +
               "\",\"epoch\":" + std::to_string(t.suspension_epoch) +
               ",\"valid\":" + (t.valid ? "true" : "false") + "}";
    }
    out += "],\"summary\":\"" + json_escape(summary) +
           "\",\"digest\":\"" + hex_u64(digest) + "\"}";
    return out;
}

inline QueryCertificate Authority::certificate(FrameId frame, ObjectRef ref,
                                               Range range, QueryKind kind) const {
    QueryCertificate cert;
    const QueryReport report = evaluate(frame, ref, range, kind);
    cert.kind = kind;
    cert.frame = frame;
    cert.object = ref;
    cert.range = range;
    cert.decision = report.decision;
    cert.allowed = report.allowed;
    cert.forbidden = report.forbidden;
    cert.undecided = report.undecided;
    cert.blocker = report.blocker;
    cert.blocker_lease = report.blocker_lease;
    cert.reason = report.reason;
    cert.stack = stack_certificate();

    std::uint64_t h = 1469598103934665603ull;
    h = mix_hash(h, static_cast<std::uint64_t>(kind));
    h = mix_hash(h, frame);
    h = mix_hash(h, ref.id);
    h = mix_hash(h, ref.generation);
    h = mix_hash(h, range.begin);
    h = mix_hash(h, range.end);
    h = mix_hash(h, static_cast<std::uint64_t>(report.decision));
    h = mix_hash(h, cert.stack.digest);
    cert.digest = h;
    return cert;
}

inline QueryCertificate Authority::certificate(FrameId frame, ObjectId object,
                                               Range range, QueryKind kind,
                                               std::optional<Generation> generation) const {
    if (generation.has_value()) {
        return certificate(frame, ObjectRef{object, *generation}, range, kind);
    }
    auto ref = current_ref(object);
    if (!ref.has_value()) {
        return certificate(frame, ObjectRef{object, 0}, range, kind);
    }
    return certificate(frame, *ref, range, kind);
}

inline bool QueryCertificate::verify(const Authority& authority) const {
    QueryCertificate fresh = authority.certificate(frame, object, range, kind);
    return fresh.digest == digest && fresh.decision == decision &&
           fresh.allowed == allowed && fresh.forbidden == forbidden &&
           fresh.undecided == undecided && fresh.stack.digest == stack.digest &&
           fresh.stack.verify(authority);
}

inline std::string QueryCertificate::to_json() const {
    std::string out = "{";
    out += "\"kind\":\"" + std::string(query_kind_name(kind)) + "\"";
    out += ",\"frame\":" + std::to_string(frame);
    out += ",\"object\":{\"id\":" + std::to_string(object.id) +
           ",\"generation\":" + std::to_string(object.generation) + "}";
    out += ",\"range\":[" + std::to_string(range.begin) + "," +
           std::to_string(range.end) + "]";
    out += ",\"decision\":\"" + std::string(decision_name(decision)) + "\"";
    out += ",\"allowed\":" + allowed.to_json();
    out += ",\"forbidden\":" + forbidden.to_json();
    out += ",\"undecided\":" + undecided.to_json();
    out += ",\"blocker\":" + std::to_string(blocker);
    out += ",\"blocker_lease\":" + std::to_string(blocker_lease);
    out += ",\"reason\":\"" + json_escape(reason) + "\"";
    out += ",\"stack\":" + stack.to_json();
    out += ",\"digest\":\"" + hex_u64(digest) + "\"}";
    return out;
}

}  // namespace borrow

#endif  // BORROW_AUTHORITY_HPP

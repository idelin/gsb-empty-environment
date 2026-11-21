#ifndef BORROW_HISTORY_HPP
#define BORROW_HISTORY_HPP

/*
 * history.hpp
 *
 * Replays a set of observed event segments and partially specified gaps.
 *
 * A segment with missing=true records only a stack summary and the operation
 * categories that the host permits there.  Every gap sharing one summary must
 * be interpreted by the same category.  The solver enumerates all compatible
 * interpretations and reports what every surviving history agrees on:
 *
 *   Allow      every compatible history allows the whole query range
 *   Forbid     every compatible history forbids the whole query range
 *   Undecided  compatible histories disagree, or the range is only partial
 *
 * Gaps can be fixed later with fix_summary(); re-solving then turns a formerly
 * undecided query into a definite one.
 */

#include "borrow_authority.hpp"

#include <functional>
#include <map>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

namespace borrow {

using SegmentId = std::uint64_t;
using SummaryId = std::string;
using CategoryId = std::string;

enum class EventKind {
    Noop,
    Enter,
    AcquireShared,
    AcquireExclusive,
    Read,
    Write,
    Move,
    Release,
    Suspend,
    Resume,
    Cancel,
    Leave
};

inline const char* event_kind_name(EventKind k) {
    switch (k) {
        case EventKind::Noop: return "noop";
        case EventKind::Enter: return "enter";
        case EventKind::AcquireShared: return "acquire-shared";
        case EventKind::AcquireExclusive: return "acquire-exclusive";
        case EventKind::Read: return "read";
        case EventKind::Write: return "write";
        case EventKind::Move: return "move";
        case EventKind::Release: return "release";
        case EventKind::Suspend: return "suspend";
        case EventKind::Resume: return "resume";
        case EventKind::Cancel: return "cancel";
        case EventKind::Leave: return "leave";
    }
    return "unknown";
}

struct Event {
    EventKind kind = EventKind::Noop;
    FrameId frame = kRootFrame;
    FrameId parent = kRootFrame;
    ObjectId object = kNoObject;
    std::optional<Generation> generation;
    Range range;
    BorrowMode mode = BorrowMode::Shared;
    BranchId branch = 0;
    std::string ticket_ref;
    std::string note;

    static Event noop() { return Event{}; }

    static Event enter(FrameId parent = kRootFrame, FrameId id = 0,
                       BranchId branch = 0) {
        Event e;
        e.kind = EventKind::Enter;
        e.parent = parent;
        e.frame = id;
        e.branch = branch;
        return e;
    }

    static Event shared(FrameId frame, ObjectId object, Range range,
                        std::optional<Generation> generation = std::nullopt) {
        Event e;
        e.kind = EventKind::AcquireShared;
        e.frame = frame;
        e.object = object;
        e.range = range;
        e.mode = BorrowMode::Shared;
        e.generation = generation;
        return e;
    }

    static Event exclusive(FrameId frame, ObjectId object, Range range,
                           std::optional<Generation> generation = std::nullopt) {
        Event e;
        e.kind = EventKind::AcquireExclusive;
        e.frame = frame;
        e.object = object;
        e.range = range;
        e.mode = BorrowMode::Exclusive;
        e.generation = generation;
        return e;
    }

    static Event read(FrameId frame, ObjectId object, Range range,
                      std::optional<Generation> generation = std::nullopt) {
        Event e;
        e.kind = EventKind::Read;
        e.frame = frame;
        e.object = object;
        e.range = range;
        e.generation = generation;
        return e;
    }

    static Event write(FrameId frame, ObjectId object, Range range,
                       std::optional<Generation> generation = std::nullopt) {
        Event e;
        e.kind = EventKind::Write;
        e.frame = frame;
        e.object = object;
        e.range = range;
        e.generation = generation;
        return e;
    }

    static Event move(FrameId frame, ObjectId object, Range range,
                      std::optional<Generation> generation = std::nullopt) {
        Event e;
        e.kind = EventKind::Move;
        e.frame = frame;
        e.object = object;
        e.range = range;
        e.generation = generation;
        return e;
    }

    static Event release(FrameId frame, ObjectId object, Range range,
                         std::optional<Generation> generation = std::nullopt) {
        Event e;
        e.kind = EventKind::Release;
        e.frame = frame;
        e.object = object;
        e.range = range;
        e.generation = generation;
        return e;
    }

    static Event suspend(FrameId frame, std::string ticket_ref = {}) {
        Event e;
        e.kind = EventKind::Suspend;
        e.frame = frame;
        e.ticket_ref = std::move(ticket_ref);
        return e;
    }

    static Event resume(std::string ticket_ref, FrameId frame = kNoFrame) {
        Event e;
        e.kind = EventKind::Resume;
        e.frame = frame;
        e.ticket_ref = std::move(ticket_ref);
        return e;
    }

    static Event cancel(FrameId frame) {
        Event e;
        e.kind = EventKind::Cancel;
        e.frame = frame;
        return e;
    }

    static Event leave(FrameId frame) {
        Event e;
        e.kind = EventKind::Leave;
        e.frame = frame;
        return e;
    }

    std::string text() const {
        std::ostringstream os;
        os << event_kind_name(kind) << "(f=" << frame << ",p=" << parent
           << ",o=" << object << ",r=[" << range.begin << "," << range.end
           << "),m=" << (mode == BorrowMode::Shared ? "s" : "x")
           << ",b=" << branch << ",t=" << ticket_ref;
        if (generation.has_value()) os << ",g=" << *generation;
        if (!note.empty()) os << ",note=" << note;
        os << ")";
        return os.str();
    }
};

struct ObjectVersion {
    ObjectId id = kNoObject;
    Generation generation = 0;
};

struct Segment {
    SegmentId id = 0;
    bool missing = false;
    SummaryId summary;
    std::vector<Event> events;
    std::vector<ObjectVersion> objects;
    std::string expected_stack_digest;

    // Context used when a missing segment is filled from an operation
    // category.  These fields are ignored for observed segments.
    FrameId frame = kRootFrame;
    ObjectId object = kNoObject;
    std::optional<Generation> generation;
    Range range;
    std::vector<CategoryId> categories;
    std::string label;
};

struct Category {
    CategoryId id;
    std::string description;
    std::function<std::vector<Event>(const Segment&)> make;
};

class CategoryRegistry {
public:
    CategoryRegistry() { add_standard(); }

    void add(Category category) {
        const CategoryId id = category.id;
        categories_[id] = std::move(category);
    }

    const Category* find(const CategoryId& id) const {
        auto it = categories_.find(id);
        return it == categories_.end() ? nullptr : &it->second;
    }

    bool has(const CategoryId& id) const { return categories_.count(id) != 0; }

    std::vector<CategoryId> ids() const {
        std::vector<CategoryId> out;
        out.reserve(categories_.size());
        for (const auto& kv : categories_) out.push_back(kv.first);
        return out;
    }

    void add_standard() {
        add(Category{"noop", "do nothing", [](const Segment&) {
                 return std::vector<Event>{};
             }});
        add(Category{"read", "read the gap range",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::read(s.frame, s.object, s.range, s.generation)};
                     }});
        add(Category{"write", "write the gap range",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::write(s.frame, s.object, s.range, s.generation)};
                     }});
        add(Category{"shared", "acquire a shared gap lease",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::shared(s.frame, s.object, s.range, s.generation)};
                     }});
        add(Category{"exclusive", "acquire an exclusive gap lease",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::exclusive(s.frame, s.object, s.range,
                                              s.generation)};
                     }});
        add(Category{"release", "release the gap range",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::release(s.frame, s.object, s.range,
                                            s.generation)};
                     }});
        add(Category{"move", "move the gap object",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::move(s.frame, s.object, s.range,
                                         s.generation)};
                     }});
        add(Category{"suspend", "suspend the gap frame",
                     [](const Segment& s) {
                         std::string ref = s.label.empty()
                                               ? "gap:" + std::to_string(s.id)
                                               : s.label;
                         return std::vector<Event>{
                             Event::suspend(s.frame, std::move(ref))};
                     }});
        add(Category{"resume", "resume the ticket named by the gap label",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::resume(s.label, s.frame)};
                     }});
        add(Category{"cancel", "cancel the gap frame and its descendants",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::cancel(s.frame)};
                     }});
        add(Category{"enter", "enter a child of the gap frame",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::enter(s.frame, 0, 0)};
                     }});
        add(Category{"leave", "return the gap frame",
                     [](const Segment& s) {
                         return std::vector<Event>{
                             Event::leave(s.frame)};
                     }});
    }

private:
    std::map<CategoryId, Category> categories_;
};

struct HistoryOptions {
    bool check_object_versions = true;
    bool check_expected_stack_digest = true;
    bool deduplicate_histories = true;
    std::size_t max_interpretations = 1000000;
};

struct HistoryQuery {
    FrameId frame = kRootFrame;
    ObjectId object = kNoObject;
    Range range;
    QueryKind kind = QueryKind::Read;
    std::optional<Generation> generation;
    std::optional<SegmentId> after_segment;
};

struct HistoryChoice {
    SummaryId summary;
    CategoryId category;

    friend bool operator==(const HistoryChoice& a, const HistoryChoice& b) {
        return a.summary == b.summary && a.category == b.category;
    }
    friend bool operator!=(const HistoryChoice& a, const HistoryChoice& b) {
        return !(a == b);
    }
};

struct HistoryWitness {
    std::vector<HistoryChoice> choices;
    bool valid = false;
    Status status = Status::Ok;
    std::string error;
    Decision outcome = Decision::Undecided;
    IntervalSet allowed;
    IntervalSet forbidden;
    std::string stack_summary;
    std::string trace_summary;
    std::vector<std::string> trace;
    StackCertificate stack;
};

struct MaterializeResult {
    Status status = Status::Ok;
    std::string error;
    bool valid = false;
    Authority authority;
    explicit operator bool() const { return valid; }
};

class HistoryProblem;

struct HistoryCertificate {
    HistoryQuery query;
    Decision decision = Decision::Undecided;
    IntervalSet common_allowed;
    IntervalSet common_forbidden;
    IntervalSet undecided;
    std::size_t interpretations = 0;
    std::size_t compatible_histories = 0;
    bool truncated = false;
    bool inconsistent = false;
    std::string reason;
    StackCertificate representative_stack;
    std::vector<HistoryWitness> witnesses;
    std::uint64_t digest = 0;

    bool verify(const HistoryProblem& problem) const;
    std::string to_json() const;
};

class HistoryProblem {
public:
    void add_object(ObjectId id, Offset size, Generation generation = 0) {
        ObjectInfo info;
        info.id = id;
        info.size = size;
        info.generation = generation;
        objects_[id] = info;
        rebuild_object_list();
    }

    void add_object(const ObjectInfo& info) {
        objects_[info.id] = info;
        rebuild_object_list();
    }

    void add_segment(Segment segment) {
        if (segment.id == 0) segment.id = next_segment_id_++;
        else if (segment.id >= next_segment_id_) next_segment_id_ = segment.id + 1;
        segments_.push_back(std::move(segment));
    }

    void add_gap(Segment gap) {
        gap.missing = true;
        add_segment(std::move(gap));
    }

    void register_category(Category category) { registry_.add(std::move(category)); }

    const CategoryRegistry& registry() const { return registry_; }
    const std::vector<Segment>& segments() const { return segments_; }
    const std::vector<ObjectInfo>& objects() const { return object_list_; }
    const HistoryOptions& options() const { return options_; }
    HistoryOptions& options() { return options_; }

    void fix_summary(SummaryId summary, CategoryId category) {
        fixed_[std::move(summary)] = std::move(category);
    }

    void clear_fix(const SummaryId& summary) { fixed_.erase(summary); }
    void clear_fixes() { fixed_.clear(); }

    // Turn a missing segment into an observed one.  This is the direct form
    // of "the gap was filled later"; category-based refinement can be done
    // with fix_summary().
    bool fill_gap(SegmentId id, std::vector<Event> events) {
        for (Segment& seg : segments_) {
            if (seg.id != id) continue;
            seg.missing = false;
            seg.events = std::move(events);
            seg.categories.clear();
            return true;
        }
        return false;
    }

    HistoryCertificate solve(FrameId frame, ObjectId object, Range range,
                             QueryKind kind,
                             std::optional<Generation> generation = std::nullopt) const;
    HistoryCertificate solve(const HistoryQuery& query) const;

    // Independently replay one interpretation.  Useful to verify a witness
    // without trusting the aggregate certificate.
    HistoryWitness replay(const HistoryQuery& query,
                          const std::vector<HistoryChoice>& choices) const;

    // Replay an interpretation without a query and hand back the resulting
    // authority state.  This is what lets a byte-level checker inspect the
    // exact small-object consequences of a compatible history.
    MaterializeResult materialize(
        const std::vector<HistoryChoice>& choices,
        std::optional<SegmentId> after_segment = std::nullopt) const;

private:
    struct GapGroup {
        SummaryId key;
        std::vector<std::size_t> segment_indices;
        std::vector<CategoryId> candidates;
    };

    std::vector<GapGroup> build_groups(
        bool* feasible, std::optional<SegmentId> stop = std::nullopt) const;
    HistoryWitness replay_impl(const HistoryQuery& query,
                               const std::vector<HistoryChoice>& choices,
                               bool keep_trace,
                               Authority* final_state = nullptr) const;
    void rebuild_object_list();

    std::map<ObjectId, ObjectInfo> objects_;
    std::vector<ObjectInfo> object_list_;
    std::vector<Segment> segments_;
    CategoryRegistry registry_;
    std::map<SummaryId, CategoryId> fixed_;
    HistoryOptions options_;
    SegmentId next_segment_id_ = 1;
};

// ---------------------------------------------------------------------------
// Replay helpers
// ---------------------------------------------------------------------------

namespace detail {

struct ReplayContext {
    Authority authority;
    std::map<std::string, SuspendTicket> tickets;
    std::size_t auto_ticket = 0;
};

inline Status resolve_ref(const Authority& authority, const Event& event,
                          ObjectRef* ref, std::string* error) {
    if (event.object == kNoObject) {
        if (error) *error = "event has no object";
        return Status::NotFound;
    }
    if (event.generation.has_value()) {
        *ref = ObjectRef{event.object, *event.generation};
        if (!authority.is_current(*ref)) {
            if (error) *error = "event refers to a stale generation";
            return Status::StaleGeneration;
        }
        return Status::Ok;
    }
    auto current = authority.current_ref(event.object);
    if (!current.has_value()) {
        if (error) *error = "event refers to an unknown object";
        return Status::NotFound;
    }
    *ref = *current;
    return Status::Ok;
}

inline Status apply_event(ReplayContext& ctx, const Event& event,
                          std::string* error) {
    Authority& auth = ctx.authority;
    switch (event.kind) {
        case EventKind::Noop:
            return Status::Ok;

        case EventKind::Enter: {
            FrameId out = kNoFrame;
            std::optional<FrameId> requested;
            if (event.frame != kRootFrame) requested = event.frame;
            std::optional<BranchId> branch;
            if (event.branch != 0) branch = event.branch;
            Status s = auth.enter_into(&out, event.parent, requested, branch);
            return s;
        }

        case EventKind::AcquireShared:
        case EventKind::AcquireExclusive: {
            ObjectRef ref;
            Status s = resolve_ref(auth, event, &ref, error);
            if (s != Status::Ok) return s;
            BorrowMode mode = event.kind == EventKind::AcquireShared
                                  ? BorrowMode::Shared
                                  : BorrowMode::Exclusive;
            BorrowResult br = auth.acquire(event.frame, ref, event.range, mode,
                                           event.note);
            if (!br) {
                if (error) *error = br.detail;
                return br.status;
            }
            return Status::Ok;
        }

        case EventKind::Read:
        case EventKind::Write: {
            ObjectRef ref;
            Status s = resolve_ref(auth, event, &ref, error);
            if (s != Status::Ok) return s;
            AccessKind kind = event.kind == EventKind::Read ? AccessKind::Read
                                                            : AccessKind::Write;
            AccessCheck check = auth.can_access(event.frame, ref, event.range, kind);
            if (!check.allowed) {
                if (error) *error = check.detail;
                return check.status;
            }
            return Status::Ok;
        }

        case EventKind::Move: {
            auto info = auth.object(event.object);
            if (info == nullptr) {
                if (error) *error = "move refers to an unknown object";
                return Status::NotFound;
            }
            if (event.generation.has_value() &&
                info->generation != *event.generation) {
                if (error) *error = "move expected a different generation";
                return Status::StaleGeneration;
            }
            const ObjectRef ref{event.object, info->generation};
            const Range moved = event.range.empty()
                                    ? Range(0, info->size)
                                    : event.range;
            AccessCheck check = auth.can_move(event.frame, ref, moved);
            if (!check.allowed) {
                if (error) *error = check.detail;
                return check.status;
            }
            if (!auth.move_object(event.object).has_value()) {
                if (error) *error = "move failed";
                return Status::NotFound;
            }
            return Status::Ok;
        }

        case EventKind::Release: {
            ObjectRef ref;
            Status s = resolve_ref(auth, event, &ref, error);
            if (s != Status::Ok) return s;
            Status r = auth.release(event.frame, ref, event.range);
            if (r != Status::Ok && error) *error = status_name(r);
            return r;
        }

        case EventKind::Suspend: {
            SuspendResult sr = auth.suspend(event.frame);
            if (!sr) {
                if (error) *error = sr.detail;
                return sr.status;
            }
            std::string key = event.ticket_ref;
            if (key.empty()) {
                key = "auto:" + std::to_string(event.frame) + ":" +
                      std::to_string(ctx.auto_ticket++);
            }
            ctx.tickets[key] = sr.ticket;
            return Status::Ok;
        }

        case EventKind::Resume: {
            auto it = ctx.tickets.find(event.ticket_ref);
            if (it == ctx.tickets.end()) {
                if (error) *error = "resume refers to an unknown ticket";
                return Status::InvalidTicket;
            }
            if (event.frame != kNoFrame && event.frame != it->second.frame) {
                if (error) *error = "resume targets a different frame";
                return Status::BranchMismatch;
            }
            ResumeResult rr = auth.resume(it->second);
            if (!rr) {
                if (error) *error = rr.detail;
                return rr.status;
            }
            return Status::Ok;
        }

        case EventKind::Cancel: {
            Status s = auth.cancel(event.frame);
            if (s != Status::Ok && error) *error = status_name(s);
            return s;
        }

        case EventKind::Leave: {
            Status s = auth.leave(event.frame);
            if (s != Status::Ok && error) *error = status_name(s);
            return s;
        }
    }
    if (error) *error = "unknown event kind";
    return Status::InvalidRange;
}

inline std::uint64_t trace_hash(std::uint64_t h, const Event& event) {
    const std::string text = event.text();
    h = mix_hash(h, text.size());
    return hash_bytes(h, text.data(), text.size());
}

}  // namespace detail

inline void HistoryProblem::rebuild_object_list() {
    object_list_.clear();
    object_list_.reserve(objects_.size());
    for (const auto& kv : objects_) object_list_.push_back(kv.second);
    std::sort(object_list_.begin(), object_list_.end(),
              [](const ObjectInfo& a, const ObjectInfo& b) { return a.id < b.id; });
}

inline std::vector<HistoryProblem::GapGroup> HistoryProblem::build_groups(
    bool* feasible, std::optional<SegmentId> stop) const {
    std::vector<GapGroup> groups;
    std::map<SummaryId, std::size_t> index;
    *feasible = true;

    std::size_t limit = segments_.size();
    if (stop.has_value()) {
        for (std::size_t i = 0; i < segments_.size(); ++i) {
            if (segments_[i].id == *stop) {
                limit = i + 1;
                break;
            }
        }
    }

    for (std::size_t i = 0; i < limit; ++i) {
        const Segment& seg = segments_[i];
        if (!seg.missing) continue;

        SummaryId key = seg.summary;
        if (key.empty()) key = "\x01gap:" + std::to_string(seg.id);

        std::vector<CategoryId> candidates;
        auto fixed_it = fixed_.find(key);
        if (fixed_it != fixed_.end()) {
            if (!registry_.has(fixed_it->second)) {
                *feasible = false;
                return groups;
            }
            candidates.push_back(fixed_it->second);
        } else if (seg.categories.empty()) {
            candidates = registry_.ids();
        } else {
            for (const CategoryId& id : seg.categories) {
                if (!registry_.has(id)) {
                    *feasible = false;
                    return groups;
                }
                candidates.push_back(id);
            }
        }

        auto found = index.find(key);
        if (found == index.end()) {
            GapGroup group;
            group.key = key;
            group.segment_indices.push_back(i);
            group.candidates = std::move(candidates);
            index[key] = groups.size();
            groups.push_back(std::move(group));
        } else {
            GapGroup& group = groups[found->second];
            group.segment_indices.push_back(i);
            std::vector<CategoryId> common;
            for (const CategoryId& id : group.candidates) {
                if (std::find(candidates.begin(), candidates.end(), id) !=
                    candidates.end()) {
                    common.push_back(id);
                }
            }
            group.candidates.swap(common);
            if (group.candidates.empty()) *feasible = false;
        }
    }
    return groups;
}

inline std::string ref_key(ObjectRef ref, bool valid);

inline HistoryWitness HistoryProblem::replay_impl(
    const HistoryQuery& query, const std::vector<HistoryChoice>& choices,
    bool keep_trace, Authority* final_state) const {
    HistoryWitness witness;
    witness.choices = choices;

    detail::ReplayContext ctx;
    for (const auto& kv : objects_) {
        ctx.authority.define_object(kv.second.id, kv.second.size,
                                    kv.second.generation);
    }

    std::map<SummaryId, CategoryId> chosen;
    for (const HistoryChoice& c : choices) chosen[c.summary] = c.category;

    bool ok = true;
    Status status = Status::Ok;
    std::string error;
    std::uint64_t trace_h = 1469598103934665603ull;
    bool reached_after = !query.after_segment.has_value();

    for (const Segment& seg : segments_) {
        std::vector<Event> events;
        if (seg.missing) {
            SummaryId key = seg.summary;
            if (key.empty()) key = "\x01gap:" + std::to_string(seg.id);
            auto cit = chosen.find(key);
            if (cit == chosen.end()) {
                ok = false;
                status = Status::InvalidTicket;
                error = "no interpretation chosen for gap " + key;
                break;
            }
            const Category* category = registry_.find(cit->second);
            if (category == nullptr) {
                ok = false;
                status = Status::NotFound;
                error = "unknown category " + cit->second;
                break;
            }
            events = category->make(seg);
        } else {
            events = seg.events;
        }

        if (keep_trace) {
            std::string header = "segment " + std::to_string(seg.id);
            if (seg.missing) {
                SummaryId key = seg.summary;
                if (key.empty()) key = "\x01gap:" + std::to_string(seg.id);
                auto cit = chosen.find(key);
                header += " [gap " + key + " -> " +
                          (cit == chosen.end() ? std::string("<none>")
                                               : cit->second) +
                          "]";
            }
            witness.trace.push_back(std::move(header));
        }
        trace_h = mix_hash(trace_h, seg.id);
        trace_h = mix_hash(trace_h, seg.missing ? 1 : 0);
        trace_h = mix_hash(trace_h, seg.events.size());

        for (const Event& event : events) {
            if (keep_trace) witness.trace.push_back("  " + event.text());
            trace_h = detail::trace_hash(trace_h, event);
            Status s = detail::apply_event(ctx, event, &error);
            if (s != Status::Ok) {
                ok = false;
                status = s;
                trace_h = mix_hash(trace_h, static_cast<std::uint64_t>(s));
                break;
            }
        }
        if (!ok) break;

        if (options_.check_object_versions) {
            for (const ObjectVersion& expected : seg.objects) {
                auto actual = ctx.authority.current_ref(expected.id);
                if (!actual.has_value() ||
                    actual->generation != expected.generation) {
                    ok = false;
                    status = Status::StaleGeneration;
                    error = "segment object version does not match replay";
                    break;
                }
            }
        }
        if (!ok) break;

        if (options_.check_expected_stack_digest &&
            !seg.expected_stack_digest.empty()) {
            const std::string actual =
                ctx.authority.stack_certificate().summary;
            if (actual != seg.expected_stack_digest) {
                ok = false;
                status = Status::InvalidRange;
                error = "segment stack digest does not match replay";
                break;
            }
        }
        if (!ok) break;

        if (query.after_segment.has_value() &&
            seg.id == *query.after_segment) {
            reached_after = true;
            break;
        }
    }

    if (ok && !reached_after) {
        ok = false;
        status = Status::NotFound;
        error = "query point segment was not found";
    }

    if (!ok) {
        witness.valid = false;
        witness.status = status;
        witness.error = error;
        witness.trace_summary = hex_u64(trace_h);
        return witness;
    }

    ObjectRef ref;
    bool have_ref = false;
    if (query.object != kNoObject) {
        if (query.generation.has_value()) {
            ref = ObjectRef{query.object, *query.generation};
            have_ref = true;
        } else {
            auto current = ctx.authority.current_ref(query.object);
            if (current.has_value()) {
                ref = *current;
                have_ref = true;
            }
        }
    }

    if (!have_ref) {
        witness.valid = true;
        witness.status = Status::NotFound;
        witness.outcome = Decision::Forbid;
        witness.forbidden.add(query.range);
        witness.error = "query object is not registered";
    } else {
        QueryReport report =
            ctx.authority.evaluate(query.frame, ref, query.range, query.kind);
        witness.valid = true;
        witness.status = Status::Ok;
        witness.outcome = report.decision;
        witness.allowed = report.allowed;
        witness.forbidden = report.forbidden;
        witness.error = report.reason;
    }
    witness.stack_summary = ctx.authority.stack_certificate().summary;
    witness.stack = ctx.authority.stack_certificate();
    if (final_state != nullptr) *final_state = ctx.authority;
    if (keep_trace) witness.trace.push_back("query " + ref_key(ref, have_ref));
    trace_h = mix_hash(trace_h, have_ref ? 1 : 0);
    trace_h = mix_hash(trace_h, ref.id);
    trace_h = mix_hash(trace_h, ref.generation);
    trace_h = mix_hash(trace_h, static_cast<std::uint64_t>(witness.outcome));
    witness.trace_summary = hex_u64(trace_h);
    return witness;
}

inline std::string ref_key(ObjectRef ref, bool valid) {
    if (!valid) return "<none>";
    return std::to_string(ref.id) + "@" + std::to_string(ref.generation);
}

inline HistoryWitness HistoryProblem::replay(
    const HistoryQuery& query, const std::vector<HistoryChoice>& choices) const {
    return replay_impl(query, choices, true);
}

inline MaterializeResult HistoryProblem::materialize(
    const std::vector<HistoryChoice>& choices,
    std::optional<SegmentId> after_segment) const {
    MaterializeResult out;
    HistoryQuery query;
    query.object = kNoObject;
    query.after_segment = after_segment;
    HistoryWitness witness =
        replay_impl(query, choices, false, &out.authority);
    out.valid = witness.valid;
    out.status = witness.status;
    out.error = witness.error;
    return out;
}

inline HistoryCertificate HistoryProblem::solve(FrameId frame, ObjectId object,
                                                Range range, QueryKind kind,
                                                std::optional<Generation> generation) const {
    HistoryQuery query;
    query.frame = frame;
    query.object = object;
    query.range = range;
    query.kind = kind;
    query.generation = generation;
    return solve(query);
}

inline HistoryCertificate HistoryProblem::solve(const HistoryQuery& query) const {
    HistoryCertificate cert;
    cert.query = query;

    if (query.range.begin > query.range.end) {
        cert.decision = Decision::Forbid;
        cert.reason = "range begin is greater than range end";
        return cert;
    }

    bool feasible = true;
    std::vector<GapGroup> groups = build_groups(&feasible, query.after_segment);
    if (!feasible) {
        cert.inconsistent = true;
        cert.decision = Decision::Undecided;
        cert.undecided = IntervalSet::from(query.range);
        cert.reason = "the gap interpretations have no common category";
        return cert;
    }

    for (const GapGroup& group : groups) {
        if (group.candidates.empty()) {
            cert.inconsistent = true;
            cert.decision = Decision::Undecided;
            cert.undecided = IntervalSet::from(query.range);
            cert.reason = "a gap has no available category";
            return cert;
        }
    }

    std::vector<std::size_t> cursor(groups.size(), 0);
    std::set<std::string> seen_history;
    std::size_t interpretations = 0;
    bool first_valid = true;
    bool have_valid = false;

    IntervalSet common_allowed = IntervalSet::from(query.range);
    IntervalSet common_forbidden = IntervalSet::from(query.range);

    while (true) {
        if (interpretations >= options_.max_interpretations) {
            cert.truncated = true;
            break;
        }
        ++interpretations;

        std::vector<HistoryChoice> choices;
        choices.reserve(groups.size());
        for (std::size_t i = 0; i < groups.size(); ++i) {
            choices.push_back(HistoryChoice{groups[i].key,
                                            groups[i].candidates[cursor[i]]});
        }

        HistoryWitness witness = replay_impl(query, choices, false);
        if (witness.valid) {
            bool already = false;
            if (options_.deduplicate_histories) {
                const std::string key =
                    witness.trace_summary + "|" + witness.stack_summary + "|" +
                    witness.allowed.str() + "|" + witness.forbidden.str();
                already = !seen_history.insert(key).second;
            }
            if (!already) {
                if (first_valid) {
                    cert.representative_stack = witness.stack;
                    first_valid = false;
                }
                have_valid = true;
                ++cert.compatible_histories;
                common_allowed.intersect(witness.allowed);
                common_forbidden.intersect(witness.forbidden);
                cert.witnesses.push_back(std::move(witness));
            }
        } else {
            cert.witnesses.push_back(std::move(witness));
        }

        // Advance the mixed-radix cursor.
        std::size_t pos = groups.size();
        while (pos > 0) {
            --pos;
            ++cursor[pos];
            if (cursor[pos] < groups[pos].candidates.size()) break;
            cursor[pos] = 0;
            if (pos == 0) {
                pos = groups.size();
                break;
            }
        }
        if (groups.empty()) break;
        if (pos == groups.size()) break;
    }

    cert.interpretations = interpretations;
    if (!have_valid) {
        cert.inconsistent = true;
        cert.decision = Decision::Undecided;
        cert.undecided = IntervalSet::from(query.range);
        if (cert.reason.empty()) {
            cert.reason = "no compatible history survived replay";
        }
    } else {
        cert.common_allowed = common_allowed;
        cert.common_forbidden = common_forbidden;
        cert.decision = common_allowed.covers(query.range)
                            ? Decision::Allow
                            : (common_forbidden.covers(query.range)
                                   ? Decision::Forbid
                                   : Decision::Undecided);
        if (cert.decision == Decision::Undecided) {
            cert.reason = "compatible histories disagree or the range is partial";
        } else if (cert.decision == Decision::Allow) {
            cert.reason = "every compatible history allows the range";
        } else {
            cert.reason = "every compatible history forbids the range";
        }
    }

    cert.undecided = IntervalSet::from(query.range);
    if (have_valid) {
        cert.undecided.subtract(cert.common_allowed);
        cert.undecided.subtract(cert.common_forbidden);
    }

    if (cert.truncated) {
        // A partial enumeration is not a proof over all compatible histories.
        cert.decision = Decision::Undecided;
        cert.common_allowed.clear();
        cert.common_forbidden.clear();
        cert.undecided = IntervalSet::from(query.range);
        cert.reason = "interpretation enumeration was truncated";
    }

    std::uint64_t h = 1469598103934665603ull;
    h = mix_hash(h, static_cast<std::uint64_t>(query.kind));
    h = mix_hash(h, query.frame);
    h = mix_hash(h, query.object);
    h = mix_hash(h, query.range.begin);
    h = mix_hash(h, query.range.end);
    h = mix_hash(h, query.generation.value_or(0));
    h = mix_hash(h, query.after_segment.has_value() ? 1 : 0);
    h = mix_hash(h, query.after_segment.value_or(0));
    h = mix_hash(h, static_cast<std::uint64_t>(cert.decision));
    h = mix_hash(h, cert.interpretations);
    h = mix_hash(h, cert.compatible_histories);
    h = mix_hash(h, cert.truncated ? 1 : 0);
    h = mix_hash(h, cert.inconsistent ? 1 : 0);
    h = mix_hash(h, cert.representative_stack.digest);
    for (const HistoryWitness& w : cert.witnesses) {
        h = mix_hash(h, w.valid ? 1 : 0);
        h = mix_hash(h, static_cast<std::uint64_t>(w.outcome));
        h = mix_hash(h, w.stack.digest);
        for (const HistoryChoice& c : w.choices) {
            h = mix_hash(h, c.summary.size());
            h = hash_bytes(h, c.summary.data(), c.summary.size());
            h = mix_hash(h, c.category.size());
            h = hash_bytes(h, c.category.data(), c.category.size());
        }
        h = mix_hash(h, w.trace_summary.size());
        h = hash_bytes(h, w.trace_summary.data(), w.trace_summary.size());
    }
    cert.digest = h;
    return cert;
}

inline bool HistoryCertificate::verify(const HistoryProblem& problem) const {
    HistoryCertificate fresh = problem.solve(query);
    if (fresh.digest != digest) return false;
    if (fresh.decision != decision) return false;
    if (fresh.common_allowed != common_allowed) return false;
    if (fresh.common_forbidden != common_forbidden) return false;
    if (fresh.undecided != undecided) return false;
    if (fresh.interpretations != interpretations) return false;
    if (fresh.compatible_histories != compatible_histories) return false;
    if (fresh.truncated != truncated || fresh.inconsistent != inconsistent) {
        return false;
    }
    if (fresh.representative_stack.digest != representative_stack.digest) {
        return false;
    }
    if (compatible_histories > 0 && !representative_stack.internally_valid()) {
        return false;
    }
    if (fresh.witnesses.size() != witnesses.size()) return false;
    for (std::size_t i = 0; i < witnesses.size(); ++i) {
        const HistoryWitness& a = witnesses[i];
        const HistoryWitness& b = fresh.witnesses[i];
        if (a.valid != b.valid || a.outcome != b.outcome ||
            a.choices != b.choices || a.allowed != b.allowed ||
            a.forbidden != b.forbidden || a.trace_summary != b.trace_summary ||
            a.stack_summary != b.stack_summary ||
            a.stack.digest != b.stack.digest) {
            return false;
        }
        if (a.valid && !a.stack.internally_valid()) return false;
        HistoryWitness replayed = problem.replay(query, a.choices);
        if (replayed.valid != a.valid || replayed.outcome != a.outcome ||
            replayed.allowed != a.allowed || replayed.forbidden != a.forbidden ||
            replayed.trace_summary != a.trace_summary) {
            return false;
        }
    }
    return true;
}

inline std::string HistoryCertificate::to_json() const {
    std::string out = "{";
    out += "\"query\":{\"frame\":" + std::to_string(query.frame) +
           ",\"object\":" + std::to_string(query.object) +
           ",\"generation\":";
    if (query.generation.has_value()) {
        out += std::to_string(*query.generation);
    } else {
        out += "null";
    }
    out += ",\"range\":[" + std::to_string(query.range.begin) + "," +
           std::to_string(query.range.end) + "],\"kind\":\"" +
           query_kind_name(query.kind) + "\",\"after_segment\":";
    if (query.after_segment.has_value()) {
        out += std::to_string(*query.after_segment);
    } else {
        out += "null";
    }
    out += "}";
    out += ",\"decision\":\"" + std::string(decision_name(decision)) + "\"";
    out += ",\"common_allowed\":" + common_allowed.to_json();
    out += ",\"common_forbidden\":" + common_forbidden.to_json();
    out += ",\"undecided\":" + undecided.to_json();
    out += ",\"interpretations\":" + std::to_string(interpretations);
    out += ",\"compatible_histories\":" +
           std::to_string(compatible_histories);
    out += ",\"truncated\":";
    out += truncated ? "true" : "false";
    out += ",\"inconsistent\":";
    out += inconsistent ? "true" : "false";
    out += ",\"reason\":\"" + json_escape(reason) + "\"";
    out += ",\"representative_stack\":" + representative_stack.to_json();
    out += ",\"witnesses\":[";
    for (std::size_t i = 0; i < witnesses.size(); ++i) {
        const HistoryWitness& w = witnesses[i];
        if (i) out += ",";
        out += "{\"valid\":";
        out += w.valid ? "true" : "false";
        out += ",\"status\":\"" + std::string(status_name(w.status)) + "\"";
        out += ",\"outcome\":\"" + std::string(decision_name(w.outcome)) + "\"";
        out += ",\"choices\":[";
        for (std::size_t j = 0; j < w.choices.size(); ++j) {
            if (j) out += ",";
            out += "{\"summary\":\"" + json_escape(w.choices[j].summary) +
                   "\",\"category\":\"" + json_escape(w.choices[j].category) +
                   "\"}";
        }
        out += "],\"allowed\":" + w.allowed.to_json();
        out += ",\"forbidden\":" + w.forbidden.to_json();
        out += ",\"stack_summary\":\"" + json_escape(w.stack_summary) + "\"";
        out += ",\"stack_digest\":\"" + hex_u64(w.stack.digest) + "\"";
        out += ",\"trace_summary\":\"" + json_escape(w.trace_summary) + "\"";
        out += ",\"error\":\"" + json_escape(w.error) + "\"}";
    }
    out += "],\"digest\":\"" + hex_u64(digest) + "\"}";
    return out;
}

}  // namespace borrow

#endif  // BORROW_HISTORY_HPP

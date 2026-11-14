#include "netsec.hpp"

#include <algorithm>
#include <deque>
#include <limits>
#include <map>
#include <mutex>
#include <sstream>
#include <stdexcept>
#include <utility>

namespace netsec {

std::string_view to_string(ConflictKind kind) noexcept {
    switch (kind) {
        case ConflictKind::none:
            return "none";
        case ConflictKind::session_aborted:
            return "session-aborted";
        case ConflictKind::session_restarted:
            return "session-restarted";
        case ConflictKind::unknown_node:
            return "unknown-node";
        case ConflictKind::nodeid_reused:
            return "nodeid-reused";
        case ConflictKind::generation_mismatch:
            return "generation-mismatch";
        case ConflictKind::stable_object_mismatch:
            return "stable-object-mismatch";
        case ConflictKind::policy_generation_changed:
            return "policy-generation-changed";
        case ConflictKind::credential_rejected:
            return "credential-rejected";
        case ConflictKind::request_interrupted:
            return "request-interrupted";
        case ConflictKind::request_aborted:
            return "request-aborted";
        case ConflictKind::request_already_settled:
            return "request-already-settled";
        case ConflictKind::unique_reused:
            return "unique-reused";
        case ConflictKind::notification_stale:
            return "notification-stale";
        case ConflictKind::name_changed:
            return "name-changed";
        case ConflictKind::cache_invalidated:
            return "cache-invalidated";
        case ConflictKind::nodeid_busy:
            return "nodeid-busy";
    }
    return "unknown";
}

std::string IdentityWitness::to_string() const {
    std::ostringstream out;
    out << "sequence=" << sequence << " kind=" << netsec::to_string(kind)
        << " op=" << operation;
    if (expected_session.valid() || actual_session.valid()) {
        out << " session=" << expected_session.mount << ':'
            << expected_session.epoch << "->" << actual_session.mount << ':'
            << actual_session.epoch;
    }
    if (expected_nodeid != 0 || actual_nodeid != 0) {
        out << " nodeid=" << expected_nodeid << "->" << actual_nodeid;
    }
    if (expected_generation != 0 || actual_generation != 0) {
        out << " generation=" << expected_generation << "->"
            << actual_generation;
    }
    if (!expected_object_id.empty() || !actual_object_id.empty()) {
        out << " object=" << expected_object_id << "->" << actual_object_id;
    }
    if (expected_policy_generation != 0 ||
        actual_policy_generation != 0) {
        out << " policy=" << expected_policy_generation << "->"
            << actual_policy_generation;
    }
    if (unique != 0) {
        out << " unique=" << unique;
    }
    if (ticket != 0) {
        out << " ticket=" << ticket;
    }
    if (credentials.uid != 0 || credentials.gid != 0 ||
        credentials.pid != 0 || !credentials.security_context.empty()) {
        out << " cred=" << credentials.uid << ':' << credentials.gid << ':'
            << credentials.pid << ':' << credentials.security_context;
    }
    if (!detail.empty()) {
        out << " detail=" << detail;
    }
    return out.str();
}

namespace detail {

using Clock = std::chrono::steady_clock;
using TimePoint = Clock::time_point;

struct NodeRecord {
    NodeKey key;
    Generation generation{};
    StableObjectId object_id;
    PolicyGeneration policy_generation{};

    std::uint64_t kernel_lookups{};
    std::uint64_t request_refs{};
    std::uint64_t notification_refs{};
    std::uint64_t open_handles{};

    NameKey name;
    bool name_live{};
    std::uint64_t dentry_revision{};
    std::uint64_t cache_epoch{};
    bool cache_valid{true};
    std::uint32_t nlink{1};

    bool retired{};
    NodePhase phase{NodePhase::active};
    TimePoint retired_at{};
};

struct RequestState {
    RequestSnapshot snapshot;
    RequestPhase phase{RequestPhase::received};
    std::weak_ptr<NodeRecord> record;
    bool ref_released{};
};

struct HandleState {
    std::weak_ptr<NodeRecord> record;
    NodeRef node;
    bool open{true};
};

struct NotificationState {
    std::weak_ptr<NodeRecord> record;
    NodeRef node;
    bool active{true};
};

struct SessionSlot {
    SessionEpoch current{};
    bool active{};
};

struct UniqueKey {
    SessionKey session;
    Unique unique{};

    auto operator<=>(const UniqueKey&) const = default;
};

struct NameIndexKey {
    SessionKey session;
    NameKey name;

    auto operator<=>(const NameIndexKey&) const = default;
};

struct Core : std::enable_shared_from_this<Core> {
    explicit Core(LifecycleOptions opts) : options(std::move(opts)) {}

    mutable std::mutex mutex;
    LifecycleOptions options;
    std::chrono::nanoseconds clock_skew{};

    std::map<MountId, SessionSlot> sessions;
    std::map<NodeKey, std::shared_ptr<NodeRecord>> nodes;
    std::map<NameIndexKey, std::shared_ptr<NodeRecord>> names;
    std::map<NodeKey, Generation> generation_high_water;
    std::vector<std::shared_ptr<NodeRecord>> records;

    std::map<TicketId, std::shared_ptr<RequestState>> requests;
    std::map<UniqueKey, std::deque<TicketId>> unique_tickets;
    TicketId next_ticket{1};

    std::vector<IdentityWitness> conflicts;
    std::uint64_t next_event{};

    [[nodiscard]] TimePoint now_locked() const {
        return Clock::now() + clock_skew;
    }

    [[nodiscard]] bool session_current_locked(const SessionKey& key) const {
        auto it = sessions.find(key.mount);
        return it != sessions.end() && it->second.active &&
               it->second.current == key.epoch;
    }

    [[nodiscard]] std::shared_ptr<NodeRecord> current_locked(
        const NodeKey& key) const {
        auto it = nodes.find(key);
        if (it == nodes.end()) {
            return {};
        }
        return it->second;
    }

    [[nodiscard]] std::shared_ptr<NodeRecord> current_locked(
        const NodeRef& ref) const {
        return current_locked(NodeKey{ref.session, ref.nodeid});
    }

    [[nodiscard]] std::shared_ptr<NodeRecord> history_locked(
        const NodeRef& ref) const {
        for (auto it = records.rbegin(); it != records.rend(); ++it) {
            const auto& record = *it;
            if (record->key.session == ref.session &&
                record->key.nodeid == ref.nodeid &&
                record->generation == ref.generation) {
                return record;
            }
        }
        return {};
    }

    [[nodiscard]] bool reusable_locked(const NodeRecord& record) const {
        if (!record.retired || record.kernel_lookups != 0 ||
            record.request_refs != 0 || record.notification_refs != 0 ||
            record.open_handles != 0) {
            return false;
        }
        return now_locked() >= record.retired_at + options.quarantine;
    }

    void maybe_retire_locked(const std::shared_ptr<NodeRecord>& record) {
        if (!record) {
            return;
        }
        if (record->kernel_lookups != 0) {
            if (!record->retired) {
                record->phase = NodePhase::active;
            }
            return;
        }
        if (!record->retired) {
            record->retired = true;
            record->phase = NodePhase::quarantined;
            record->retired_at = now_locked();
        }
    }

    [[nodiscard]] std::chrono::nanoseconds quarantine_remaining_locked(
        const NodeRecord& record) const {
        if (!record.retired) {
            return std::chrono::nanoseconds::zero();
        }
        const TimePoint release_at = record.retired_at + options.quarantine;
        const TimePoint now = now_locked();
        if (now >= release_at) {
            return std::chrono::nanoseconds::zero();
        }
        return std::chrono::duration_cast<std::chrono::nanoseconds>(
            release_at - now);
    }

    [[nodiscard]] NodeView view_locked(const NodeRecord& record) const {
        NodeView view;
        view.session = record.key.session;
        view.nodeid = record.key.nodeid;
        view.generation = record.generation;
        view.kernel_lookup_refs = record.kernel_lookups;
        view.in_flight_requests = record.request_refs;
        view.notification_refs = record.notification_refs;
        view.open_handle_refs = record.open_handles;
        view.stable_object_id = record.object_id;
        view.policy_generation = record.policy_generation;
        view.name = record.name;
        view.dentry_revision = record.dentry_revision;
        view.cache_epoch = record.cache_epoch;
        view.cache_valid = record.cache_valid;
        view.name_live = record.name_live;
        view.retired = record.retired;
        view.reusable = reusable_locked(record);
        view.nlink = record.nlink;
        view.phase = record.retired ? NodePhase::quarantined
                                    : NodePhase::active;
        view.quarantine_remaining =
            quarantine_remaining_locked(record);
        return view;
    }

    [[nodiscard]] LookupResult entry_locked(const NodeRecord& record,
                                             bool reused) const {
        LookupResult result;
        result.session = record.key.session;
        result.nodeid = record.key.nodeid;
        result.generation = record.generation;
        result.kernel_lookup_refs = record.kernel_lookups;
        result.open_handle_refs = record.open_handles;
        result.stable_object_id = record.object_id;
        result.policy_generation = record.policy_generation;
        result.name = record.name;
        result.dentry_revision = record.dentry_revision;
        result.cache_epoch = record.cache_epoch;
        result.nodeid_reused = reused;
        return result;
    }

    IdentityWitness witness_locked(std::string operation, ConflictKind kind,
                                   SessionKey expected_session,
                                   SessionKey actual_session,
                                   NodeId expected_nodeid,
                                   NodeId actual_nodeid,
                                   Generation expected_generation,
                                   Generation actual_generation,
                                   StableObjectId expected_object,
                                   StableObjectId actual_object,
                                   PolicyGeneration expected_policy,
                                   PolicyGeneration actual_policy,
                                   Unique unique = 0, TicketId ticket = 0,
                                   Credentials credentials = {},
                                   std::string detail = {}) {
        IdentityWitness witness;
        witness.sequence = ++next_event;
        witness.kind = kind;
        witness.operation = std::move(operation);
        witness.expected_session = expected_session;
        witness.actual_session = actual_session;
        witness.expected_nodeid = expected_nodeid;
        witness.actual_nodeid = actual_nodeid;
        witness.expected_generation = expected_generation;
        witness.actual_generation = actual_generation;
        witness.expected_object_id = std::move(expected_object);
        witness.actual_object_id = std::move(actual_object);
        witness.expected_policy_generation = expected_policy;
        witness.actual_policy_generation = actual_policy;
        witness.unique = unique;
        witness.ticket = ticket;
        witness.credentials = std::move(credentials);
        witness.detail = std::move(detail);
        conflicts.push_back(witness);
        return witness;
    }

    IdentityWitness node_witness_locked(std::string operation,
                                        ConflictKind kind,
                                        const NodeRef& expected,
                                        const std::shared_ptr<NodeRecord>& actual,
                                        StableObjectId expected_object = {},
                                        PolicyGeneration expected_policy = 0,
                                        Unique unique = 0, TicketId ticket = 0,
                                        Credentials credentials = {},
                                        std::string detail = {}) {
        if (actual) {
            return witness_locked(
                std::move(operation), kind, expected.session,
                actual->key.session, expected.nodeid, actual->key.nodeid,
                expected.generation, actual->generation,
                std::move(expected_object), actual->object_id, expected_policy,
                actual->policy_generation, unique, ticket,
                std::move(credentials), std::move(detail));
        }
        return witness_locked(
            std::move(operation), kind, expected.session, {}, expected.nodeid,
            0, expected.generation, 0, std::move(expected_object), {},
            expected_policy, 0, unique, ticket, std::move(credentials),
            std::move(detail));
    }

    [[nodiscard]] std::optional<SessionKey> active_session_locked(
        MountId mount) const {
        auto it = sessions.find(mount);
        if (it == sessions.end() || !it->second.active) {
            return std::nullopt;
        }
        return SessionKey{mount, it->second.current};
    }

    SessionKey open_session_locked(MountId mount) {
        auto& slot = sessions[mount];
        if (!slot.active) {
            if (slot.current == std::numeric_limits<SessionEpoch>::max()) {
                throw std::overflow_error(
                    "mount session epoch exhausted");
            }
            ++slot.current;
            slot.active = true;
        }
        return SessionKey{mount, slot.current};
    }

    void detach_name_locked(const std::shared_ptr<NodeRecord>& record) {
        if (!record || !record->name_live) {
            return;
        }
        const NameIndexKey key{record->key.session, record->name};
        auto it = names.find(key);
        if (it != names.end() && it->second == record) {
            names.erase(it);
        }
        record->name_live = false;
        ++record->dentry_revision;
    }

    [[nodiscard]] bool name_available_locked(
        const SessionKey& session, const NameKey& name) {
        const NameIndexKey key{session, name};
        auto it = names.find(key);
        if (it == names.end()) {
            return true;
        }
        if (it->second && it->second->retired) {
            detach_name_locked(it->second);
            return true;
        }
        return false;
    }

    void bind_name_locked(const std::shared_ptr<NodeRecord>& record,
                          const NameKey& name) {
        record->name = name;
        if (name.valid()) {
            names[NameIndexKey{record->key.session, name}] = record;
            record->name_live = true;
        } else {
            record->name_live = false;
        }
    }

    [[nodiscard]] std::shared_ptr<NodeRecord> allocate_record_locked(
        const SessionKey& session, NodeId nodeid,
        const StableObjectId& object_id, PolicyGeneration policy_generation,
        const NameKey& name, std::uint64_t nlookup, bool reused) {
        const NodeKey key{session, nodeid};
        auto old = current_locked(key);
        if (old) {
            detach_name_locked(old);
        }

        auto record = std::make_shared<NodeRecord>();
        record->key = key;
        Generation& generation = generation_high_water[key];
        if (generation == std::numeric_limits<Generation>::max()) {
            throw std::overflow_error(
                "node generation exhausted for this node id");
        }
        ++generation;
        record->generation = generation;
        record->object_id = object_id;
        record->policy_generation = policy_generation;
        record->kernel_lookups = nlookup;
        record->cache_valid = true;
        record->cache_epoch = 0;
        record->nlink = 1;

        nodes[key] = record;
        records.push_back(record);
        bind_name_locked(record, name);
        (void)reused;
        return record;
    }

    [[nodiscard]] NodeId allocate_nodeid_locked(const SessionKey& session) {
        NodeId candidate = options.first_dynamic_nodeid == 0
                               ? NodeId{2}
                               : options.first_dynamic_nodeid;
        for (std::size_t attempts = 0; attempts <= nodes.size() + 1;
             ++attempts) {
            const NodeKey key{session, candidate};
            auto it = nodes.find(key);
            if (it == nodes.end() || reusable_locked(*it->second)) {
                return candidate;
            }
            if (candidate == std::numeric_limits<NodeId>::max()) {
                break;
            }
            ++candidate;
        }
        return 0;
    }

    [[nodiscard]] LookupOutcome lookup_locked(const LookupRequest& request) {
        LookupOutcome outcome;
        if (!session_current_locked(request.session)) {
            auto slot = sessions.find(request.session.mount);
            const ConflictKind kind =
                slot != sessions.end() && slot->second.active
                    ? ConflictKind::session_restarted
                    : ConflictKind::session_aborted;
            outcome.status = LookupStatus::invalid_session;
            outcome.conflict = witness_locked(
                "lookup", kind, request.session,
                slot != sessions.end()
                    ? SessionKey{request.session.mount, slot->second.current}
                    : SessionKey{},
                0, 0, 0, 0, {}, {}, 0, 0, 0, 0, {});
            return outcome;
        }
        if (request.nlookup == 0) {
            outcome.status = LookupStatus::invalid_lookup_count;
            return outcome;
        }

        NodeId nodeid = request.nodeid;
        if (nodeid == 0) {
            nodeid = allocate_nodeid_locked(request.session);
            if (nodeid == 0) {
                outcome.status = LookupStatus::no_free_nodeid;
                return outcome;
            }
        }

        const NodeKey key{request.session, nodeid};
        auto existing = current_locked(key);
        if (existing && !existing->retired) {
            if (existing->object_id != request.stable_object_id) {
                outcome.status = LookupStatus::nodeid_busy;
                outcome.conflict = node_witness_locked(
                    "lookup", ConflictKind::nodeid_reused,
                    NodeRef{request.session, nodeid, existing->generation},
                    existing, request.stable_object_id,
                    request.policy_generation);
                return outcome;
            }
            const bool owns_requested_name =
                existing->name_live && existing->name == request.name;
            if (request.name.valid() && !owns_requested_name &&
                !name_available_locked(request.session, request.name)) {
                outcome.status = LookupStatus::name_busy;
                outcome.conflict = node_witness_locked(
                    "lookup", ConflictKind::name_changed,
                    NodeRef{request.session, nodeid, existing->generation},
                    existing, request.stable_object_id,
                    request.policy_generation);
                return outcome;
            }

            existing->policy_generation = request.policy_generation;
            existing->kernel_lookups =
                existing->kernel_lookups >
                        std::numeric_limits<std::uint64_t>::max() -
                            request.nlookup
                    ? std::numeric_limits<std::uint64_t>::max()
                    : existing->kernel_lookups + request.nlookup;
            if (request.name.valid()) {
                if (existing->name_live && existing->name != request.name) {
                    detach_name_locked(existing);
                }
                bind_name_locked(existing, request.name);
            }
            outcome.status = LookupStatus::ok;
            outcome.entry =
                entry_locked(*existing, existing->generation > 1);
            return outcome;
        }

        if (existing && existing->retired && !reusable_locked(*existing)) {
            outcome.status = LookupStatus::nodeid_busy;
            outcome.conflict = node_witness_locked(
                "lookup", ConflictKind::nodeid_busy,
                NodeRef{request.session, nodeid,
                        generation_high_water[key]},
                existing, request.stable_object_id,
                request.policy_generation, 0, 0, {},
                "forgotten node id is still quarantined or referenced");
            return outcome;
        }

        if (request.name.valid() &&
            !name_available_locked(request.session, request.name)) {
            outcome.status = LookupStatus::name_busy;
            outcome.conflict = node_witness_locked(
                "lookup", ConflictKind::name_changed,
                NodeRef{request.session, nodeid, 0}, existing,
                request.stable_object_id, request.policy_generation);
            return outcome;
        }

        const bool reused = generation_high_water[key] != 0;
        auto record = allocate_record_locked(
            request.session, nodeid, request.stable_object_id,
            request.policy_generation, request.name, request.nlookup, reused);
        outcome.status = LookupStatus::ok;
        outcome.entry = entry_locked(*record, reused);
        return outcome;
    }

    [[nodiscard]] bool session_failure_locked(
        std::string operation, const SessionKey& session,
        std::optional<IdentityWitness>& conflict) {
        if (session_current_locked(session)) {
            return false;
        }
        auto slot = sessions.find(session.mount);
        const ConflictKind kind =
            slot != sessions.end() && slot->second.active
                ? ConflictKind::session_restarted
                : ConflictKind::session_aborted;
        conflict = witness_locked(
            std::move(operation), kind, session,
            slot != sessions.end()
                ? SessionKey{session.mount, slot->second.current}
                : SessionKey{},
            0, 0, 0, 0, {}, {}, 0, 0, 0, 0, {});
        return true;
    }

    [[nodiscard]] ForgetResult forget_locked(const SessionKey& session,
                                              const ForgetEntry& entry) {
        ForgetResult result;
        if (!session_current_locked(session) || entry.nodeid == 0 ||
            entry.nlookup == 0) {
            result.ignored = 1;
            return result;
        }
        auto record =
            current_locked(NodeKey{session, entry.nodeid});
        if (!record || record->kernel_lookups == 0) {
            result.ignored = 1;
            return result;
        }
        if (entry.nlookup > record->kernel_lookups) {
            result.ignored = 1;
            return result;
        }
        record->kernel_lookups -= entry.nlookup;
        result.applied = 1;
        maybe_retire_locked(record);
        return result;
    }

    [[nodiscard]] bool unique_has_active_locked(const UniqueKey& key) const {
        auto it = unique_tickets.find(key);
        if (it == unique_tickets.end()) {
            return false;
        }
        for (TicketId ticket : it->second) {
            auto request = requests.find(ticket);
            if (request == requests.end()) {
                continue;
            }
            const RequestPhase phase = request->second->phase;
            if (phase == RequestPhase::received ||
                phase == RequestPhase::side_effect) {
                return true;
            }
        }
        return false;
    }

    void remove_unique_ticket_locked(const UniqueKey& key, TicketId ticket) {
        auto it = unique_tickets.find(key);
        if (it == unique_tickets.end()) {
            return;
        }
        auto& queue = it->second;
        queue.erase(std::remove(queue.begin(), queue.end(), ticket),
                    queue.end());
        if (queue.empty()) {
            unique_tickets.erase(it);
        }
    }

    [[nodiscard]] ValidationResult validate_locked(
        const std::shared_ptr<RequestState>& state) const {
        ValidationResult result;
        if (!state) {
            result.conflict = ConflictKind::request_already_settled;
            return result;
        }
        switch (state->phase) {
            case RequestPhase::interrupted:
                result.conflict = ConflictKind::request_interrupted;
                return result;
            case RequestPhase::aborted:
                result.conflict = ConflictKind::request_aborted;
                return result;
            case RequestPhase::completed:
                result.conflict = ConflictKind::request_already_settled;
                return result;
            case RequestPhase::received:
            case RequestPhase::side_effect:
                break;
        }

        const RequestSnapshot& snapshot = state->snapshot;
        auto record = current_locked(snapshot.node);
        if (!record) {
            result.conflict = ConflictKind::unknown_node;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate", ConflictKind::unknown_node,
                    snapshot.node, {}, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials);
            }
            return result;
        }
        if (record->generation != snapshot.node.generation) {
            result.conflict = ConflictKind::nodeid_reused;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate", ConflictKind::nodeid_reused,
                    snapshot.node, record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials,
                    "node id now names a different generation");
            }
            return result;
        }
        if (record->object_id != snapshot.stable_object_id) {
            result.conflict = ConflictKind::stable_object_mismatch;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate",
                    ConflictKind::stable_object_mismatch, snapshot.node,
                    record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials);
            }
            return result;
        }
        if (record->policy_generation != snapshot.policy_generation) {
            result.conflict = ConflictKind::policy_generation_changed;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate",
                    ConflictKind::policy_generation_changed, snapshot.node,
                    record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials);
            }
            return result;
        }
        if (snapshot.options.require_name_unchanged &&
            (record->name != snapshot.name ||
             record->dentry_revision != snapshot.dentry_revision ||
             !record->name_live)) {
            result.conflict = ConflictKind::name_changed;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate", ConflictKind::name_changed,
                    snapshot.node, record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials,
                    "name binding changed while request was queued");
            }
            return result;
        }
        if (snapshot.options.require_cache_unchanged &&
            (!record->cache_valid ||
             record->cache_epoch != snapshot.cache_epoch)) {
            result.conflict = ConflictKind::cache_invalidated;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate", ConflictKind::cache_invalidated,
                    snapshot.node, record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials);
            }
            return result;
        }

        const UniqueKey unique_key{snapshot.node.session, snapshot.unique};
        auto queue = unique_tickets.find(unique_key);
        if (queue == unique_tickets.end() ||
            std::find(queue->second.begin(), queue->second.end(),
                      snapshot.ticket) == queue->second.end()) {
            result.conflict = ConflictKind::unique_reused;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate", ConflictKind::unique_reused,
                    snapshot.node, record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials,
                    "ticket is no longer registered for this unique");
            }
            return result;
        }

        if (options.credential_validator &&
            !options.credential_validator(snapshot.credentials)) {
            result.conflict = ConflictKind::credential_rejected;
            if (auto core = const_cast<Core*>(this)) {
                result.witness = core->node_witness_locked(
                    "request-validate", ConflictKind::credential_rejected,
                    snapshot.node, record, snapshot.stable_object_id,
                    snapshot.policy_generation, snapshot.unique,
                    snapshot.ticket, snapshot.credentials);
            }
            return result;
        }

        result.ok = true;
        return result;
    }

    void release_request_locked(const std::shared_ptr<RequestState>& state) {
        if (!state || state->ref_released) {
            return;
        }
        state->ref_released = true;
        auto record = state->record.lock();
        if (record) {
            if (record->request_refs != 0) {
                --record->request_refs;
            }
            maybe_retire_locked(record);
        }
        remove_unique_ticket_locked(
            UniqueKey{state->snapshot.node.session, state->snapshot.unique},
            state->snapshot.ticket);
    }

    [[nodiscard]] RequestOutcome receive_locked(const RequestInput& input) {
        RequestOutcome outcome;
        if (!input.node.valid()) {
            outcome.status = RequestStatus::invalid_node;
            return outcome;
        }
        if (input.unique == 0) {
            outcome.status = RequestStatus::invalid_node;
            return outcome;
        }
        if (!session_current_locked(input.node.session)) {
            outcome.status = RequestStatus::invalid_session;
            (void)session_failure_locked("request-receive",
                                         input.node.session,
                                         outcome.conflict);
            return outcome;
        }

        auto record = current_locked(input.node);
        if (!record) {
            outcome.status = RequestStatus::invalid_node;
            outcome.conflict = node_witness_locked(
                "request-receive", ConflictKind::unknown_node, input.node, {},
                {}, {}, input.unique);
            return outcome;
        }
        if (record->generation != input.node.generation) {
            outcome.status = RequestStatus::stale_generation;
            const auto historical = history_locked(input.node);
            outcome.conflict = node_witness_locked(
                "request-receive", ConflictKind::nodeid_reused, input.node,
                record,
                historical ? historical->object_id : StableObjectId{},
                historical ? historical->policy_generation : 0,
                input.unique, 0, input.credentials,
                "request carries a generation that was forgotten and reused");
            return outcome;
        }
        if (record->retired && record->open_handles == 0) {
            outcome.status = RequestStatus::stale_generation;
            outcome.conflict = node_witness_locked(
                "request-receive", ConflictKind::generation_mismatch,
                input.node, record, record->object_id,
                record->policy_generation, input.unique, 0,
                input.credentials,
                "lookup was forgotten before request side effect");
            return outcome;
        }
        if (input.options.via_open_handle && record->open_handles == 0) {
            outcome.status = RequestStatus::invalid_node;
            outcome.conflict = node_witness_locked(
                "request-receive", ConflictKind::unknown_node, input.node,
                record, record->object_id, record->policy_generation,
                input.unique, 0, input.credentials,
                "no open handle owns this identity");
            return outcome;
        }
        if (options.credential_validator &&
            !options.credential_validator(input.credentials)) {
            outcome.status = RequestStatus::credentials_rejected;
            outcome.conflict = node_witness_locked(
                "request-receive", ConflictKind::credential_rejected,
                input.node, record, record->object_id,
                record->policy_generation, input.unique, 0,
                input.credentials);
            return outcome;
        }

        const UniqueKey unique_key{input.node.session, input.unique};
        if (unique_has_active_locked(unique_key)) {
            outcome.status = RequestStatus::duplicate_unique;
            outcome.conflict = node_witness_locked(
                "request-receive", ConflictKind::unique_reused, input.node,
                record, record->object_id, record->policy_generation,
                input.unique, 0, input.credentials,
                "unique already has an unsettled active ticket");
            return outcome;
        }

        if (next_ticket == std::numeric_limits<TicketId>::max()) {
            throw std::overflow_error("request ticket space exhausted");
        }
        const TicketId ticket = next_ticket++;
        auto state = std::make_shared<RequestState>();
        state->snapshot.node = input.node;
        state->snapshot.stable_object_id = record->object_id;
        state->snapshot.policy_generation = record->policy_generation;
        state->snapshot.kernel_lookup_refs = record->kernel_lookups;
        state->snapshot.open_handle_refs = record->open_handles;
        state->snapshot.notification_refs = record->notification_refs;
        state->snapshot.name = record->name;
        state->snapshot.dentry_revision = record->dentry_revision;
        state->snapshot.cache_epoch = record->cache_epoch;
        state->snapshot.cache_valid = record->cache_valid;
        state->snapshot.credentials = input.credentials;
        state->snapshot.unique = input.unique;
        state->snapshot.ticket = ticket;
        state->snapshot.options = input.options;
        state->record = record;
        state->phase = RequestPhase::received;

        ++record->request_refs;
        requests[ticket] = state;
        unique_tickets[unique_key].push_back(ticket);

        outcome.status = RequestStatus::ok;
        outcome.request = Request(shared_from_this(), state);
        return outcome;
    }

    [[nodiscard]] CompletionOutcome complete_locked(
        const std::shared_ptr<RequestState>& state) {
        CompletionOutcome outcome;
        if (!state) {
            outcome.status = CompletionStatus::ignored_already_settled;
            return outcome;
        }

        switch (state->phase) {
            case RequestPhase::completed:
                outcome.status = CompletionStatus::ignored_already_settled;
                return outcome;
            case RequestPhase::interrupted:
                release_request_locked(state);
                state->phase = RequestPhase::completed;
                requests.erase(state->snapshot.ticket);
                outcome.status = CompletionStatus::ignored_interrupted;
                outcome.may_reply = false;
                return outcome;
            case RequestPhase::aborted:
                release_request_locked(state);
                state->phase = RequestPhase::completed;
                requests.erase(state->snapshot.ticket);
                outcome.status = CompletionStatus::ignored_aborted;
                outcome.may_reply = false;
                return outcome;
            case RequestPhase::received:
            case RequestPhase::side_effect:
                break;
        }

        release_request_locked(state);
        state->phase = RequestPhase::completed;
        requests.erase(state->snapshot.ticket);
        outcome.status = CompletionStatus::settled;
        outcome.may_reply = true;
        return outcome;
    }

    void abandon_locked(const std::shared_ptr<RequestState>& state) {
        if (!state) {
            return;
        }
        if (state->phase == RequestPhase::completed ||
            state->phase == RequestPhase::aborted) {
            return;
        }
        state->phase = RequestPhase::aborted;
        release_request_locked(state);
        requests.erase(state->snapshot.ticket);
    }

    [[nodiscard]] bool interrupt_locked(const SessionKey& session,
                                        Unique unique,
                                        std::optional<TicketId> ticket) {
        if (!session_current_locked(session) || unique == 0) {
            return false;
        }
        const UniqueKey key{session, unique};
        auto it = unique_tickets.find(key);
        if (it == unique_tickets.end()) {
            return false;
        }
        for (TicketId candidate : it->second) {
            if (ticket && candidate != *ticket) {
                continue;
            }
            auto request = requests.find(candidate);
            if (request == requests.end()) {
                continue;
            }
            RequestPhase& phase = request->second->phase;
            if (phase == RequestPhase::received ||
                phase == RequestPhase::side_effect) {
                phase = RequestPhase::interrupted;
                return true;
            }
            // Without an explicit ticket, only the oldest incarnation of this
            // unique is eligible.  A delayed INTERRUPT for an older
            // incarnation must not fall through and cancel a newer ticket.
            if (!ticket) {
                return false;
            }
        }
        return false;
    }

    [[nodiscard]] OpenOutcome open_locked(const NodeRef& node) {
        OpenOutcome outcome;
        if (!session_current_locked(node.session)) {
            outcome.status = OpenStatus::invalid_session;
            (void)session_failure_locked("open", node.session,
                                         outcome.conflict);
            return outcome;
        }
        auto record = current_locked(node);
        if (!record || record->generation != node.generation ||
            record->retired) {
            outcome.status = OpenStatus::invalid_identity;
            const auto historical = history_locked(node);
            outcome.conflict = node_witness_locked(
                "open",
                record && record->generation != node.generation
                    ? ConflictKind::nodeid_reused
                    : ConflictKind::unknown_node,
                node, record,
                historical ? historical->object_id : StableObjectId{},
                historical ? historical->policy_generation : 0);
            return outcome;
        }
        auto state = std::make_shared<HandleState>();
        state->record = record;
        state->node = node;
        ++record->open_handles;
        outcome.status = OpenStatus::ok;
        outcome.handle = OpenHandle(shared_from_this(), state);
        return outcome;
    }

    bool close_handle_locked(const std::shared_ptr<HandleState>& state) {
        if (!state || !state->open) {
            return false;
        }
        state->open = false;
        auto record = state->record.lock();
        if (record && record->open_handles != 0) {
            --record->open_handles;
            maybe_retire_locked(record);
        }
        return true;
    }

    [[nodiscard]] NotificationOutcome notify_inode_locked(
        const NodeRef& node) {
        NotificationOutcome outcome;
        if (!session_current_locked(node.session)) {
            outcome.status = NotifyStatus::invalid_session;
            (void)session_failure_locked("notify-inval-inode", node.session,
                                         outcome.conflict);
            return outcome;
        }
        auto record = current_locked(node);
        if (!record || record->generation != node.generation) {
            outcome.status = NotifyStatus::invalid_identity;
            const auto historical = history_locked(node);
            outcome.conflict = node_witness_locked(
                "notify-inval-inode", ConflictKind::notification_stale, node,
                record,
                historical ? historical->object_id : StableObjectId{},
                historical ? historical->policy_generation : 0);
            return outcome;
        }
        auto state = std::make_shared<NotificationState>();
        state->record = record;
        state->node = node;
        ++record->notification_refs;
        ++record->cache_epoch;
        record->cache_valid = false;
        outcome.status = NotifyStatus::ok;
        outcome.notification = InodeNotification(
            shared_from_this(), state, record->cache_epoch);
        return outcome;
    }

    bool complete_notification_locked(
        const std::shared_ptr<NotificationState>& state) {
        if (!state || !state->active) {
            return false;
        }
        state->active = false;
        auto record = state->record.lock();
        if (record && record->notification_refs != 0) {
            --record->notification_refs;
            maybe_retire_locked(record);
        }
        return true;
    }

    [[nodiscard]] RenameOutcome rename_locked(const NodeRef& first,
                                              const NodeRef& second) {
        RenameOutcome outcome;
        if (!session_current_locked(first.session)) {
            outcome.status = RenameStatus::invalid_session;
            (void)session_failure_locked("rename-exchange", first.session,
                                         outcome.conflict);
            return outcome;
        }
        if (first.session != second.session) {
            outcome.status = RenameStatus::invalid_session;
            outcome.conflict = witness_locked(
                "rename-exchange", ConflictKind::session_restarted,
                first.session, second.session, first.nodeid, second.nodeid,
                first.generation, second.generation, {}, {}, 0, 0, 0, 0, {});
            return outcome;
        }
        auto left = current_locked(first);
        auto right = current_locked(second);
        if (!left || left->generation != first.generation) {
            outcome.status = RenameStatus::invalid_identity;
            outcome.conflict = node_witness_locked(
                "rename-exchange", ConflictKind::nodeid_reused, first, left);
            return outcome;
        }
        if (!right || right->generation != second.generation) {
            outcome.status = RenameStatus::invalid_identity;
            outcome.conflict = node_witness_locked(
                "rename-exchange", ConflictKind::nodeid_reused, second, right);
            return outcome;
        }
        if (left == right) {
            outcome.status = RenameStatus::same_node;
            return outcome;
        }
        if (!left->name_live || !right->name_live) {
            outcome.status = RenameStatus::invalid_identity;
            outcome.conflict = node_witness_locked(
                "rename-exchange", ConflictKind::name_changed, first, left);
            return outcome;
        }

        const NameKey left_name = left->name;
        const NameKey right_name = right->name;
        detach_name_locked(left);
        detach_name_locked(right);

        // Identity fields deliberately remain untouched.  Only directory-entry
        // bindings move, and each reuse of a name gets a new dentry revision.
        bind_name_locked(left, right_name);
        bind_name_locked(right, left_name);

        outcome.status = RenameStatus::ok;
        outcome.first = view_locked(*left);
        outcome.second = view_locked(*right);
        return outcome;
    }

    [[nodiscard]] RenameOutcome rename_by_name_locked(
        const SessionKey& session, const NameKey& first,
        const NameKey& second) {
        RenameOutcome outcome;
        if (!session_current_locked(session)) {
            outcome.status = RenameStatus::invalid_session;
            (void)session_failure_locked("rename-exchange", session,
                                         outcome.conflict);
            return outcome;
        }
        auto left_it =
            names.find(NameIndexKey{session, first});
        auto right_it =
            names.find(NameIndexKey{session, second});
        if (left_it == names.end() || right_it == names.end()) {
            outcome.status = RenameStatus::invalid_identity;
            auto actual = left_it != names.end() ? left_it->second
                                                 : right_it->second;
            outcome.conflict = node_witness_locked(
                "rename-exchange", ConflictKind::name_changed,
                NodeRef{session,
                        left_it != names.end() ? left_it->second->key.nodeid
                                              : 0,
                        left_it != names.end()
                            ? left_it->second->generation
                            : 0},
                actual, {}, {}, 0, 0, {},
                "one side of RENAME_EXCHANGE is not a live name");
            return outcome;
        }
        return rename_locked(
            NodeRef{session, left_it->second->key.nodeid,
                    left_it->second->generation},
            NodeRef{session, right_it->second->key.nodeid,
                    right_it->second->generation});
    }

    [[nodiscard]] UnlinkOutcome unlink_locked(
        const SessionKey& session, const NameKey& name,
        std::optional<NodeRef> expected, std::optional<NodeRef> node_ref) {
        UnlinkOutcome outcome;
        if (!session_current_locked(session)) {
            outcome.status = UnlinkStatus::invalid_session;
            (void)session_failure_locked("unlink", session,
                                         outcome.conflict);
            return outcome;
        }

        std::shared_ptr<NodeRecord> record;
        if (node_ref) {
            record = current_locked(*node_ref);
            if (!record || record->generation != node_ref->generation) {
                outcome.status = UnlinkStatus::invalid_identity;
                outcome.conflict = node_witness_locked(
                    "unlink", ConflictKind::nodeid_reused, *node_ref, record);
                return outcome;
            }
            if (!record->name_live || record->name != name) {
                outcome.status = UnlinkStatus::invalid_identity;
                outcome.conflict = node_witness_locked(
                    "unlink", ConflictKind::name_changed, *node_ref, record);
                return outcome;
            }
        } else {
            auto it = names.find(NameIndexKey{session, name});
            if (it == names.end()) {
                outcome.status = UnlinkStatus::name_not_found;
                return outcome;
            }
            record = it->second;
        }

        if (expected &&
            (record->key.session != expected->session ||
             record->key.nodeid != expected->nodeid ||
             record->generation != expected->generation)) {
            outcome.status = UnlinkStatus::invalid_identity;
            outcome.conflict = node_witness_locked(
                "unlink", ConflictKind::name_changed, *expected, record);
            return outcome;
        }

        detach_name_locked(record);
        if (record->nlink != 0) {
            --record->nlink;
        }
        maybe_retire_locked(record);
        outcome.status = UnlinkStatus::ok;
        outcome.node = view_locked(*record);
        return outcome;
    }

    [[nodiscard]] bool notify_entry_locked(
        const SessionKey& session, const NameKey& name,
        std::optional<NodeRef> expected) {
        if (!session_current_locked(session)) {
            return false;
        }
        auto it = names.find(NameIndexKey{session, name});
        if (it == names.end()) {
            return false;
        }
        auto record = it->second;
        if (expected && (record->generation != expected->generation ||
                         record->key.nodeid != expected->nodeid ||
                         record->key.session != expected->session)) {
            return false;
        }
        // The dentry is invalidated, not unlinked.  The inode identity and the
        // underlying object reference remain unchanged.
        ++record->dentry_revision;
        return true;
    }

    [[nodiscard]] CacheHit cache_hit_locked(const NodeRef& node,
                                             std::uint64_t cached_epoch) const {
        CacheHit hit;
        if (!session_current_locked(node.session)) {
            return hit;
        }
        auto record = current_locked(node);
        if (!record || record->generation != node.generation ||
            record->retired) {
            return hit;
        }
        hit.cache_epoch = record->cache_epoch;
        hit.cache_valid = record->cache_valid;
        hit.hit = record->cache_valid && record->cache_epoch == cached_epoch;
        return hit;
    }
};

}  // namespace detail

namespace {

using detail::Core;
using detail::HandleState;
using detail::NodeRecord;
using detail::NotificationState;
using detail::RequestState;

std::shared_ptr<Core> core_of(const std::shared_ptr<void>& pointer) {
    return std::static_pointer_cast<Core>(pointer);
}

}  // namespace

Request::Request(std::shared_ptr<void> impl, std::shared_ptr<void> state)
    : impl_(std::move(impl)), state_(std::move(state)) {}

Request::Request(Request&& other) noexcept
    : impl_(std::move(other.impl_)), state_(std::move(other.state_)) {}

Request& Request::operator=(Request&& other) noexcept {
    if (this != &other) {
        if (state_) {
            auto core = core_of(impl_);
            std::lock_guard lock(core->mutex);
            core->abandon_locked(
                std::static_pointer_cast<RequestState>(state_));
        }
        impl_ = std::move(other.impl_);
        state_ = std::move(other.state_);
    }
    return *this;
}

Request::~Request() {
    if (!state_) {
        return;
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    core->abandon_locked(std::static_pointer_cast<RequestState>(state_));
}

bool Request::valid() const noexcept {
    return impl_ && state_;
}

TicketId Request::ticket() const noexcept {
    if (!state_) {
        return 0;
    }
    return std::static_pointer_cast<RequestState>(state_)->snapshot.ticket;
}

Unique Request::unique() const noexcept {
    if (!state_) {
        return 0;
    }
    return std::static_pointer_cast<RequestState>(state_)->snapshot.unique;
}

RequestPhase Request::phase() const {
    if (!state_) {
        return RequestPhase::completed;
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    return std::static_pointer_cast<RequestState>(state_)->phase;
}

RequestSnapshot Request::snapshot() const {
    if (!state_) {
        return {};
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    return std::static_pointer_cast<RequestState>(state_)->snapshot;
}

ValidationResult Request::validate() const {
    if (!state_ || !impl_) {
        ValidationResult result;
        result.conflict = ConflictKind::request_already_settled;
        return result;
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    return core->validate_locked(
        std::static_pointer_cast<RequestState>(state_));
}

ValidationResult Request::before_side_effect() {
    if (!state_ || !impl_) {
        ValidationResult result;
        result.conflict = ConflictKind::request_already_settled;
        return result;
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    auto state = std::static_pointer_cast<RequestState>(state_);
    if (state->phase == RequestPhase::side_effect) {
        ValidationResult result;
        result.ok = true;
        return result;
    }
    ValidationResult result = core->validate_locked(state);
    if (!result.ok) {
        return result;
    }
    if (state->phase != RequestPhase::received) {
        result.ok = false;
        result.conflict = ConflictKind::request_already_settled;
        return result;
    }
    state->phase = RequestPhase::side_effect;
    return result;
}

CompletionOutcome Request::complete() {
    if (!state_ || !impl_) {
        CompletionOutcome outcome;
        outcome.status = CompletionStatus::ignored_already_settled;
        return outcome;
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    auto state = std::static_pointer_cast<RequestState>(state_);
    CompletionOutcome outcome = core->complete_locked(state);
    state_.reset();
    impl_.reset();
    return outcome;
}

void Request::abandon() {
    if (!state_ || !impl_) {
        return;
    }
    auto core = core_of(impl_);
    {
        std::lock_guard lock(core->mutex);
        core->abandon_locked(std::static_pointer_cast<RequestState>(state_));
    }
    state_.reset();
    impl_.reset();
}

OpenHandle::OpenHandle(std::shared_ptr<void> impl,
                       std::shared_ptr<void> state)
    : impl_(std::move(impl)), state_(std::move(state)) {}

OpenHandle::OpenHandle(OpenHandle&& other) noexcept
    : impl_(std::move(other.impl_)), state_(std::move(other.state_)) {}

OpenHandle& OpenHandle::operator=(OpenHandle&& other) noexcept {
    if (this != &other) {
        (void)close();
        impl_ = std::move(other.impl_);
        state_ = std::move(other.state_);
    }
    return *this;
}

OpenHandle::~OpenHandle() {
    (void)close();
}

bool OpenHandle::valid() const noexcept {
    return impl_ && state_;
}

NodeRef OpenHandle::node() const {
    if (!state_) {
        return {};
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    return std::static_pointer_cast<HandleState>(state_)->node;
}

bool OpenHandle::close() {
    if (!state_ || !impl_) {
        return false;
    }
    auto core = core_of(impl_);
    bool closed = false;
    {
        std::lock_guard lock(core->mutex);
        closed = core->close_handle_locked(
            std::static_pointer_cast<HandleState>(state_));
    }
    state_.reset();
    impl_.reset();
    return closed;
}

InodeNotification::InodeNotification(std::shared_ptr<void> impl,
                                     std::shared_ptr<void> state,
                                     std::uint64_t cache_epoch)
    : impl_(std::move(impl)),
      state_(std::move(state)),
      cache_epoch_(cache_epoch) {}

InodeNotification::InodeNotification(InodeNotification&& other) noexcept
    : impl_(std::move(other.impl_)),
      state_(std::move(other.state_)),
      cache_epoch_(other.cache_epoch_) {
    other.cache_epoch_ = 0;
}

InodeNotification& InodeNotification::operator=(
    InodeNotification&& other) noexcept {
    if (this != &other) {
        (void)complete();
        impl_ = std::move(other.impl_);
        state_ = std::move(other.state_);
        cache_epoch_ = other.cache_epoch_;
        other.cache_epoch_ = 0;
    }
    return *this;
}

InodeNotification::~InodeNotification() {
    (void)complete();
}

bool InodeNotification::valid() const noexcept {
    return impl_ && state_;
}

NodeRef InodeNotification::node() const {
    if (!state_) {
        return {};
    }
    auto core = core_of(impl_);
    std::lock_guard lock(core->mutex);
    return std::static_pointer_cast<NotificationState>(state_)->node;
}

std::uint64_t InodeNotification::cache_epoch() const noexcept {
    return cache_epoch_;
}

bool InodeNotification::complete() {
    if (!state_ || !impl_) {
        return false;
    }
    auto core = core_of(impl_);
    bool completed = false;
    {
        std::lock_guard lock(core->mutex);
        completed = core->complete_notification_locked(
            std::static_pointer_cast<NotificationState>(state_));
    }
    state_.reset();
    impl_.reset();
    cache_epoch_ = 0;
    return completed;
}

NodeLifecycle::NodeLifecycle(LifecycleOptions options)
    : impl_(std::make_shared<Impl>(std::move(options))) {}

NodeLifecycle::NodeLifecycle(const NodeLifecycle&) noexcept = default;

NodeLifecycle& NodeLifecycle::operator=(const NodeLifecycle&) noexcept =
    default;

SessionKey NodeLifecycle::open_session(MountId mount) {
    std::lock_guard lock(impl_->mutex);
    return impl_->open_session_locked(mount);
}

std::optional<SessionKey> NodeLifecycle::current_session(MountId mount) const {
    std::lock_guard lock(impl_->mutex);
    return impl_->active_session_locked(mount);
}

bool NodeLifecycle::session_active(const SessionKey& session) const {
    std::lock_guard lock(impl_->mutex);
    return impl_->session_current_locked(session);
}

std::size_t NodeLifecycle::abort_session(const SessionKey& session) {
    std::lock_guard lock(impl_->mutex);
    auto slot = impl_->sessions.find(session.mount);
    if (slot == impl_->sessions.end() || !slot->second.active ||
        slot->second.current != session.epoch) {
        return 0;
    }
    slot->second.active = false;

    std::size_t affected = 0;
    for (auto& [ticket, state] : impl_->requests) {
        (void)ticket;
        if (!state || state->snapshot.node.session != session) {
            continue;
        }
        if (state->phase == RequestPhase::completed ||
            state->phase == RequestPhase::aborted) {
            continue;
        }
        state->phase = RequestPhase::aborted;
        impl_->release_request_locked(state);
        ++affected;
    }
    return affected;
}

SessionKey NodeLifecycle::restart_session(const SessionKey& session) {
    std::lock_guard lock(impl_->mutex);
    auto slot = impl_->sessions.find(session.mount);
    if (slot != impl_->sessions.end() && slot->second.active &&
        slot->second.current == session.epoch) {
        slot->second.active = false;
        for (auto& [ticket, state] : impl_->requests) {
            (void)ticket;
            if (!state || state->snapshot.node.session != session) {
                continue;
            }
            if (state->phase == RequestPhase::completed ||
                state->phase == RequestPhase::aborted) {
                continue;
            }
            state->phase = RequestPhase::aborted;
            impl_->release_request_locked(state);
        }
    }
    return impl_->open_session_locked(session.mount);
}

LookupOutcome NodeLifecycle::lookup(const LookupRequest& request) {
    std::lock_guard lock(impl_->mutex);
    return impl_->lookup_locked(request);
}

LookupOutcome NodeLifecycle::lookup(SessionKey session, NodeId nodeid,
                                    StableObjectId stable_object_id,
                                    PolicyGeneration policy_generation,
                                    NameKey name, std::uint64_t nlookup) {
    return lookup(LookupRequest{std::move(session), nodeid,
                                std::move(stable_object_id),
                                policy_generation, std::move(name), nlookup});
}

ForgetResult NodeLifecycle::forget(const SessionKey& session, NodeId nodeid,
                                   std::uint64_t nlookup) {
    std::lock_guard lock(impl_->mutex);
    return impl_->forget_locked(session, ForgetEntry{nodeid, nlookup});
}

ForgetResult NodeLifecycle::batch_forget(
    const SessionKey& session, const std::vector<ForgetEntry>& entries) {
    std::lock_guard lock(impl_->mutex);
    ForgetResult total;
    for (const ForgetEntry& entry : entries) {
        const ForgetResult one = impl_->forget_locked(session, entry);
        total.applied += one.applied;
        total.ignored += one.ignored;
    }
    return total;
}

RequestOutcome NodeLifecycle::receive_request(const RequestInput& input) {
    std::lock_guard lock(impl_->mutex);
    return impl_->receive_locked(input);
}

RequestOutcome NodeLifecycle::receive_request(NodeRef node, Unique unique,
                                              Credentials credentials,
                                              RequestOptions options) {
    return receive_request(RequestInput{std::move(node), unique,
                                        std::move(credentials), options});
}

RequestOutcome NodeLifecycle::receive_request(const OpenHandle& handle,
                                              Unique unique,
                                              Credentials credentials,
                                              RequestOptions options) {
    if (!handle.valid()) {
        RequestOutcome outcome;
        outcome.status = RequestStatus::invalid_node;
        return outcome;
    }
    NodeRef node = handle.node();
    if (!options.via_open_handle) {
        options.via_open_handle = true;
    }
    return receive_request(node, unique, std::move(credentials), options);
}

bool NodeLifecycle::interrupt(const SessionKey& session, Unique unique) {
    std::lock_guard lock(impl_->mutex);
    return impl_->interrupt_locked(session, unique, std::nullopt);
}

bool NodeLifecycle::interrupt_ticket(const SessionKey& session, Unique unique,
                                     TicketId ticket) {
    std::lock_guard lock(impl_->mutex);
    return impl_->interrupt_locked(session, unique, ticket);
}

OpenOutcome NodeLifecycle::open_handle(NodeRef node) {
    std::lock_guard lock(impl_->mutex);
    return impl_->open_locked(node);
}

OpenOutcome NodeLifecycle::open_handle(SessionKey session, NodeId nodeid,
                                       Generation generation) {
    return open_handle(NodeRef{std::move(session), nodeid, generation});
}

NotificationOutcome NodeLifecycle::notify_inval_inode(
    NodeRef node, std::int64_t offset, std::int64_t length) {
    (void)offset;
    (void)length;
    std::lock_guard lock(impl_->mutex);
    return impl_->notify_inode_locked(node);
}

bool NodeLifecycle::notify_inval_entry(
    const SessionKey& session, const NameKey& name,
    std::optional<NodeRef> expected) {
    std::lock_guard lock(impl_->mutex);
    return impl_->notify_entry_locked(session, name, std::move(expected));
}

RenameOutcome NodeLifecycle::rename_exchange(NodeRef first, NodeRef second) {
    std::lock_guard lock(impl_->mutex);
    return impl_->rename_locked(first, second);
}

RenameOutcome NodeLifecycle::rename_exchange(SessionKey session,
                                             const NameKey& first,
                                             const NameKey& second) {
    std::lock_guard lock(impl_->mutex);
    return impl_->rename_by_name_locked(session, first, second);
}

UnlinkOutcome NodeLifecycle::unlink(SessionKey session, const NameKey& name,
                                    std::optional<NodeRef> expected) {
    std::lock_guard lock(impl_->mutex);
    return impl_->unlink_locked(std::move(session), name, std::move(expected),
                                std::nullopt);
}

UnlinkOutcome NodeLifecycle::unlink(NodeRef node,
                                    const NameKey& expected_name) {
    std::lock_guard lock(impl_->mutex);
    return impl_->unlink_locked(node.session, expected_name, node, node);
}

CacheHit NodeLifecycle::cache_hit(NodeRef node,
                                  std::uint64_t cached_epoch) const {
    std::lock_guard lock(impl_->mutex);
    return impl_->cache_hit_locked(node, cached_epoch);
}

bool NodeLifecycle::mark_cache_valid(NodeRef node) {
    std::lock_guard lock(impl_->mutex);
    auto record = impl_->current_locked(node);
    if (!record || record->generation != node.generation ||
        record->retired) {
        return false;
    }
    if (!record->cache_valid) {
        ++record->cache_epoch;
        record->cache_valid = true;
    }
    return true;
}

std::optional<NodeView> NodeLifecycle::node(NodeRef ref) const {
    std::lock_guard lock(impl_->mutex);
    auto record = impl_->current_locked(ref);
    if (!record || record->generation != ref.generation) {
        return std::nullopt;
    }
    return impl_->view_locked(*record);
}

std::optional<LookupResult> NodeLifecycle::resolve_name(
    const SessionKey& session, const NameKey& name) const {
    std::lock_guard lock(impl_->mutex);
    if (!impl_->session_current_locked(session)) {
        return std::nullopt;
    }
    auto it = impl_->names.find(detail::NameIndexKey{session, name});
    if (it == impl_->names.end()) {
        return std::nullopt;
    }
    return impl_->entry_locked(*it->second, it->second->generation > 1);
}

std::optional<QuarantineInfo> NodeLifecycle::quarantine(NodeRef ref) const {
    std::lock_guard lock(impl_->mutex);
    auto record = impl_->current_locked(ref);
    if (!record || record->generation != ref.generation || !record->retired) {
        return std::nullopt;
    }
    QuarantineInfo info;
    info.node = ref;
    info.kernel_lookup_refs = record->kernel_lookups;
    info.in_flight_requests = record->request_refs;
    info.notification_refs = record->notification_refs;
    info.open_handle_refs = record->open_handles;
    info.reusable = impl_->reusable_locked(*record);
    info.remaining = impl_->quarantine_remaining_locked(*record);
    return info;
}

bool NodeLifecycle::reusable(NodeRef ref) const {
    std::lock_guard lock(impl_->mutex);
    auto record = impl_->current_locked(ref);
    return record && record->generation == ref.generation &&
           impl_->reusable_locked(*record);
}

void NodeLifecycle::advance(std::chrono::nanoseconds delta) {
    std::lock_guard lock(impl_->mutex);
    impl_->clock_skew += delta;
}

std::optional<IdentityWitness> NodeLifecycle::first_identity_conflict() const {
    std::lock_guard lock(impl_->mutex);
    if (impl_->conflicts.empty()) {
        return std::nullopt;
    }
    return impl_->conflicts.front();
}

std::vector<IdentityWitness> NodeLifecycle::identity_conflicts() const {
    std::lock_guard lock(impl_->mutex);
    return impl_->conflicts;
}

void NodeLifecycle::clear_identity_conflicts() {
    std::lock_guard lock(impl_->mutex);
    impl_->conflicts.clear();
    impl_->next_event = 0;
}

LifecycleStats NodeLifecycle::stats() const {
    std::lock_guard lock(impl_->mutex);
    LifecycleStats result;
    for (const auto& [mount, slot] : impl_->sessions) {
        (void)mount;
        if (slot.active) {
            ++result.sessions;
        }
    }
    for (const auto& [key, record] : impl_->nodes) {
        (void)key;
        if (record->retired) {
            ++result.retired_nodes;
        } else {
            ++result.live_nodes;
        }
        result.in_flight_requests += record->request_refs;
        result.open_handles += record->open_handles;
        result.notifications += record->notification_refs;
    }
    return result;
}

}  // namespace netsec

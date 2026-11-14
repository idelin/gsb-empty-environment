#pragma once

#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using MountId = std::uint64_t;
using SessionEpoch = std::uint64_t;
using NodeId = std::uint64_t;
using Generation = std::uint64_t;
using PolicyGeneration = std::uint64_t;
using Unique = std::uint64_t;
using TicketId = std::uint64_t;
using StableObjectId = std::string;

namespace detail {
struct Core;
}

// A mount session is deliberately more than a mount id.  A reconnect begins a
// new epoch, so a request, notification or generation from the old connection
// can never become valid again by accident.
struct SessionKey {
    MountId mount{};
    SessionEpoch epoch{};

    [[nodiscard]] bool valid() const noexcept { return epoch != 0; }
    auto operator<=>(const SessionKey&) const = default;
};

struct NodeKey {
    SessionKey session;
    NodeId nodeid{};

    auto operator<=>(const NodeKey&) const = default;
};

struct Credentials {
    std::uint32_t uid{};
    std::uint32_t gid{};
    std::int32_t pid{};
    std::string security_context;

    auto operator<=>(const Credentials&) const = default;
};

// RENAME operates on directory entries, not inode identities.
struct NameKey {
    NodeId parent{};
    std::string name;

    [[nodiscard]] bool valid() const noexcept { return !name.empty(); }
    auto operator<=>(const NameKey&) const = default;
};

struct NodeRef {
    SessionKey session;
    NodeId nodeid{};
    Generation generation{};

    [[nodiscard]] bool valid() const noexcept {
        return session.valid() && nodeid != 0 && generation != 0;
    }
    auto operator<=>(const NodeRef&) const = default;
};

enum class NodePhase : std::uint8_t {
    active,
    quarantined,
};

enum class ConflictKind : std::uint8_t {
    none,
    session_aborted,
    session_restarted,
    unknown_node,
    nodeid_reused,
    generation_mismatch,
    stable_object_mismatch,
    policy_generation_changed,
    credential_rejected,
    request_interrupted,
    request_aborted,
    request_already_settled,
    unique_reused,
    notification_stale,
    name_changed,
    cache_invalidated,
    nodeid_busy,
};

[[nodiscard]] std::string_view to_string(ConflictKind kind) noexcept;

// The first mismatch observed by the component.  This is intentionally a
// value type: callers may log it, return it from a test, or compare it without
// retaining any node internals.
struct IdentityWitness {
    std::uint64_t sequence{};
    ConflictKind kind{ConflictKind::none};
    std::string operation;

    SessionKey expected_session;
    SessionKey actual_session;
    NodeId expected_nodeid{};
    NodeId actual_nodeid{};
    Generation expected_generation{};
    Generation actual_generation{};
    StableObjectId expected_object_id;
    StableObjectId actual_object_id;
    PolicyGeneration expected_policy_generation{};
    PolicyGeneration actual_policy_generation{};
    Unique unique{};
    TicketId ticket{};
    Credentials credentials;
    std::string detail;

    [[nodiscard]] std::string to_string() const;
};

struct LookupResult {
    SessionKey session;
    NodeId nodeid{};
    Generation generation{};
    std::uint64_t kernel_lookup_refs{};
    std::uint64_t open_handle_refs{};
    StableObjectId stable_object_id;
    PolicyGeneration policy_generation{};
    NameKey name;
    std::uint64_t dentry_revision{};
    std::uint64_t cache_epoch{};
    bool nodeid_reused{};
};

struct NodeView {
    SessionKey session;
    NodeId nodeid{};
    Generation generation{};
    std::uint64_t kernel_lookup_refs{};
    std::uint64_t in_flight_requests{};
    std::uint64_t notification_refs{};
    std::uint64_t open_handle_refs{};
    StableObjectId stable_object_id;
    PolicyGeneration policy_generation{};
    NameKey name;
    std::uint64_t dentry_revision{};
    std::uint64_t cache_epoch{};
    bool cache_valid{};
    bool name_live{};
    bool retired{};
    bool reusable{};
    std::uint32_t nlink{};
    NodePhase phase{NodePhase::active};
    std::chrono::nanoseconds quarantine_remaining{};
};

struct QuarantineInfo {
    NodeRef node;
    std::uint64_t kernel_lookup_refs{};
    std::uint64_t in_flight_requests{};
    std::uint64_t notification_refs{};
    std::uint64_t open_handle_refs{};
    bool reusable{};
    std::chrono::nanoseconds remaining{};
};

enum class LookupStatus : std::uint8_t {
    ok,
    invalid_session,
    invalid_lookup_count,
    nodeid_busy,
    name_busy,
    no_free_nodeid,
};

struct LookupRequest {
    SessionKey session;
    // Zero means "allocate a node id".  A non-zero hint is never silently
    // retargeted: if it is not reusable/identical the lookup fails.
    NodeId nodeid{};
    StableObjectId stable_object_id;
    PolicyGeneration policy_generation{};
    NameKey name;
    std::uint64_t nlookup{1};
};

struct LookupOutcome {
    LookupStatus status{LookupStatus::invalid_session};
    std::optional<LookupResult> entry;
    std::optional<IdentityWitness> conflict;

    [[nodiscard]] bool ok() const noexcept { return status == LookupStatus::ok; }
};

struct ForgetEntry {
    NodeId nodeid{};
    std::uint64_t nlookup{1};
};

struct ForgetResult {
    std::size_t applied{};
    std::size_t ignored{};
};

struct RequestOptions {
    // Set this when the operation arrived through a still-open file handle and
    // the kernel has already forgotten the lookup.  Such a request may run
    // against the retained object identity, but still cannot make the node id
    // reusable while the handle is open.
    bool via_open_handle{};
    bool require_name_unchanged{};
    bool require_cache_unchanged{};
};

struct RequestInput {
    NodeRef node;
    Unique unique{};
    Credentials credentials;
    RequestOptions options;
};

enum class RequestPhase : std::uint8_t {
    received,
    side_effect,
    interrupted,
    aborted,
    completed,
};

struct RequestSnapshot {
    NodeRef node;
    StableObjectId stable_object_id;
    PolicyGeneration policy_generation{};
    std::uint64_t kernel_lookup_refs{};
    std::uint64_t open_handle_refs{};
    std::uint64_t notification_refs{};
    NameKey name;
    std::uint64_t dentry_revision{};
    std::uint64_t cache_epoch{};
    bool cache_valid{};
    Credentials credentials;
    Unique unique{};
    TicketId ticket{};
    RequestOptions options;
};

struct ValidationResult {
    bool ok{};
    ConflictKind conflict{ConflictKind::none};
    std::optional<IdentityWitness> witness;

    explicit operator bool() const noexcept { return ok; }
};

enum class RequestStatus : std::uint8_t {
    ok,
    invalid_node,
    invalid_session,
    stale_generation,
    duplicate_unique,
    credentials_rejected,
};

struct RequestOutcome;
struct CompletionOutcome;

class Request {
public:
    Request() = default;
    Request(Request&& other) noexcept;
    Request& operator=(Request&& other) noexcept;
    Request(const Request&) = delete;
    Request& operator=(const Request&) = delete;
    ~Request();

    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] TicketId ticket() const noexcept;
    [[nodiscard]] Unique unique() const noexcept;
    [[nodiscard]] RequestPhase phase() const;
    [[nodiscard]] RequestSnapshot snapshot() const;

    // Re-check all snapshotted identity and authorization data.  This does not
    // change the request phase and can be called by a queue probe.
    [[nodiscard]] ValidationResult validate() const;

    // Atomically re-check and enter the side-effect phase.  A backend must call
    // this immediately before its first externally visible mutation.
    [[nodiscard]] ValidationResult before_side_effect();

    // Settle this exact ticket.  Interrupted/aborted tickets are consumed but
    // never produce a reply.  A late duplicate completion is a no-op.
    [[nodiscard]] CompletionOutcome complete();

    // Cancel the request and release its node reference.  Call only after the
    // backend confirms that it cannot complete the operation later.
    void abandon();

private:
    friend class NodeLifecycle;
    friend struct detail::Core;
    friend struct RequestOutcome;

    Request(std::shared_ptr<void> impl, std::shared_ptr<void> state);
    std::shared_ptr<void> impl_;
    std::shared_ptr<void> state_;
};

struct RequestOutcome {
    RequestStatus status{RequestStatus::invalid_session};
    std::optional<Request> request;
    std::optional<IdentityWitness> conflict;

    [[nodiscard]] bool ok() const noexcept { return status == RequestStatus::ok; }
};

enum class CompletionStatus : std::uint8_t {
    settled,
    ignored_interrupted,
    ignored_aborted,
    ignored_already_settled,
};

struct CompletionOutcome {
    CompletionStatus status{CompletionStatus::ignored_already_settled};
    bool may_reply{};
    std::optional<IdentityWitness> conflict;
};

enum class OpenStatus : std::uint8_t {
    ok,
    invalid_identity,
    invalid_session,
};

struct OpenOutcome;

class OpenHandle {
public:
    OpenHandle() = default;
    OpenHandle(OpenHandle&& other) noexcept;
    OpenHandle& operator=(OpenHandle&& other) noexcept;
    OpenHandle(const OpenHandle&) = delete;
    OpenHandle& operator=(const OpenHandle&) = delete;
    ~OpenHandle();

    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] NodeRef node() const;
    [[nodiscard]] bool close();

private:
    friend class NodeLifecycle;
    friend struct detail::Core;
    friend struct OpenOutcome;

    OpenHandle(std::shared_ptr<void> impl, std::shared_ptr<void> state);
    std::shared_ptr<void> impl_;
    std::shared_ptr<void> state_;
};

struct OpenOutcome {
    OpenStatus status{OpenStatus::invalid_identity};
    std::optional<OpenHandle> handle;
    std::optional<IdentityWitness> conflict;

    [[nodiscard]] bool ok() const noexcept { return status == OpenStatus::ok; }
};

enum class NotifyStatus : std::uint8_t {
    ok,
    invalid_identity,
    invalid_session,
};

struct NotificationOutcome;

class InodeNotification {
public:
    InodeNotification() = default;
    InodeNotification(InodeNotification&& other) noexcept;
    InodeNotification& operator=(InodeNotification&& other) noexcept;
    InodeNotification(const InodeNotification&) = delete;
    InodeNotification& operator=(const InodeNotification&) = delete;
    ~InodeNotification();

    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] NodeRef node() const;
    [[nodiscard]] std::uint64_t cache_epoch() const noexcept;

    // The notification is "in flight" until this is called.  A late completion
    // cannot touch a node id that has since been retargeted.
    [[nodiscard]] bool complete();

private:
    friend class NodeLifecycle;
    friend struct detail::Core;
    friend struct NotificationOutcome;

    InodeNotification(std::shared_ptr<void> impl,
                      std::shared_ptr<void> state,
                      std::uint64_t cache_epoch);
    std::shared_ptr<void> impl_;
    std::shared_ptr<void> state_;
    std::uint64_t cache_epoch_{};
};

struct NotificationOutcome {
    NotifyStatus status{NotifyStatus::invalid_identity};
    std::optional<InodeNotification> notification;
    std::optional<IdentityWitness> conflict;

    [[nodiscard]] bool ok() const noexcept { return status == NotifyStatus::ok; }
};

enum class RenameStatus : std::uint8_t {
    ok,
    invalid_identity,
    invalid_session,
    same_node,
};

struct RenameOutcome {
    RenameStatus status{RenameStatus::invalid_identity};
    std::optional<NodeView> first;
    std::optional<NodeView> second;
    std::optional<IdentityWitness> conflict;

    [[nodiscard]] bool ok() const noexcept { return status == RenameStatus::ok; }
};

enum class UnlinkStatus : std::uint8_t {
    ok,
    name_not_found,
    invalid_identity,
    invalid_session,
};

struct UnlinkOutcome {
    UnlinkStatus status{UnlinkStatus::name_not_found};
    std::optional<NodeView> node;
    std::optional<IdentityWitness> conflict;

    [[nodiscard]] bool ok() const noexcept { return status == UnlinkStatus::ok; }
};

struct CacheHit {
    bool hit{};
    std::uint64_t cache_epoch{};
    bool cache_valid{};
};

struct LifecycleStats {
    std::size_t sessions{};
    std::size_t live_nodes{};
    std::size_t retired_nodes{};
    std::size_t in_flight_requests{};
    std::size_t open_handles{};
    std::size_t notifications{};
};

struct LifecycleOptions {
    // The node id remains quarantined for at least this long after all kernel
    // lookup references reach zero.  In-flight requests, open handles and
    // notifications are independent additional blockers.
    std::chrono::nanoseconds quarantine{std::chrono::milliseconds(100)};
    NodeId first_dynamic_nodeid{2};
    // The validator is evaluated both at receive time and immediately before a
    // side effect.  It must be safe to call while the lifecycle lock is held.
    std::function<bool(const Credentials&)> credential_validator;
};

class NodeLifecycle {
public:
    explicit NodeLifecycle(LifecycleOptions options = {});
    NodeLifecycle(const NodeLifecycle&) noexcept;
    NodeLifecycle& operator=(const NodeLifecycle&) noexcept;
    NodeLifecycle(NodeLifecycle&&) noexcept = default;
    NodeLifecycle& operator=(NodeLifecycle&&) noexcept = default;
    ~NodeLifecycle() = default;

    // Opening an already-active mount is idempotent.  Opening after an abort
    // starts a fresh epoch.
    [[nodiscard]] SessionKey open_session(MountId mount);
    [[nodiscard]] std::optional<SessionKey> current_session(MountId mount) const;
    [[nodiscard]] bool session_active(const SessionKey& session) const;
    [[nodiscard]] std::size_t abort_session(const SessionKey& session);
    [[nodiscard]] SessionKey restart_session(const SessionKey& session);

    [[nodiscard]] LookupOutcome lookup(const LookupRequest& request);
    [[nodiscard]] LookupOutcome lookup(SessionKey session,
                                       NodeId nodeid,
                                       StableObjectId stable_object_id,
                                       PolicyGeneration policy_generation,
                                       NameKey name = {},
                                       std::uint64_t nlookup = 1);

    // FORGET/BATCH_FORGET never enqueue a reply.  They only account for kernel
    // lookup references and may put an otherwise idle record into quarantine.
    ForgetResult forget(const SessionKey& session, NodeId nodeid,
                        std::uint64_t nlookup = 1);
    ForgetResult batch_forget(const SessionKey& session,
                              const std::vector<ForgetEntry>& entries);

    [[nodiscard]] RequestOutcome receive_request(const RequestInput& input);
    [[nodiscard]] RequestOutcome receive_request(NodeRef node, Unique unique,
                                                 Credentials credentials,
                                                 RequestOptions options = {});
    [[nodiscard]] RequestOutcome receive_request(const OpenHandle& handle,
                                                 Unique unique,
                                                 Credentials credentials,
                                                 RequestOptions options = {});

    // Targets the oldest still-unsettled ticket for this unique.  The unique
    // number may be reused by a later request only as a distinct ticket.
    bool interrupt(const SessionKey& session, Unique unique);
    bool interrupt_ticket(const SessionKey& session, Unique unique,
                          TicketId ticket);

    [[nodiscard]] OpenOutcome open_handle(NodeRef node);
    [[nodiscard]] OpenOutcome open_handle(SessionKey session, NodeId nodeid,
                                          Generation generation);

    [[nodiscard]] NotificationOutcome notify_inval_inode(
        NodeRef node, std::int64_t offset = 0, std::int64_t length = -1);
    bool notify_inval_entry(const SessionKey& session, const NameKey& name,
                            std::optional<NodeRef> expected = std::nullopt);

    [[nodiscard]] RenameOutcome rename_exchange(NodeRef first, NodeRef second);
    [[nodiscard]] RenameOutcome rename_exchange(SessionKey session,
                                                const NameKey& first,
                                                const NameKey& second);

    [[nodiscard]] UnlinkOutcome unlink(SessionKey session,
                                       const NameKey& name,
                                       std::optional<NodeRef> expected =
                                           std::nullopt);
    [[nodiscard]] UnlinkOutcome unlink(NodeRef node,
                                       const NameKey& expected_name);

    [[nodiscard]] CacheHit cache_hit(NodeRef node,
                                     std::uint64_t cached_epoch) const;
    [[nodiscard]] bool mark_cache_valid(NodeRef node);

    [[nodiscard]] std::optional<NodeView> node(NodeRef node) const;
    [[nodiscard]] std::optional<LookupResult> resolve_name(
        const SessionKey& session, const NameKey& name) const;
    [[nodiscard]] std::optional<QuarantineInfo> quarantine(NodeRef node) const;
    [[nodiscard]] bool reusable(NodeRef node) const;

    // Deterministic clock aid for replay tests and maintenance loops.
    void advance(std::chrono::nanoseconds delta);

    [[nodiscard]] std::optional<IdentityWitness> first_identity_conflict()
        const;
    [[nodiscard]] std::vector<IdentityWitness> identity_conflicts() const;
    void clear_identity_conflicts();

    [[nodiscard]] LifecycleStats stats() const;

private:
    using Impl = detail::Core;
    std::shared_ptr<Impl> impl_;
};

using FuseNodeLifecycle = NodeLifecycle;

}  // namespace netsec

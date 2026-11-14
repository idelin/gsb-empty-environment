#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <vector>

namespace netsec {

using UserNsId = std::uint64_t;
using Pid = std::int64_t;
using Uid = std::uint32_t;

enum Capability : std::uint32_t {
    kCapSysPtrace = 1u << 0,
};

// Owns the user-namespace tree. Callers reference namespaces strictly by id;
// parent links live only inside the registry, so a caller cannot forge an
// ancestor relationship by handing the evaluator a crafted pointer chain.
class UserNamespaceRegistry {
public:
    UserNsId create_root(Uid creator);
    // Fails (returns std::nullopt) when the parent id is unknown.
    std::optional<UserNsId> create_child(UserNsId parent, Uid creator);

    bool exists(UserNsId id) const;
    // True when `ancestor` is `node` itself or a strict ancestor of it.
    bool is_ancestor(UserNsId ancestor, UserNsId node) const;
    // Chain from `node` up to and including the root; empty if unknown.
    std::vector<UserNsId> ancestor_chain(UserNsId node) const;

    // Authoritative pid -> namespace membership, maintained by the runtime.
    void assign(Pid pid, UserNsId ns);
    std::optional<UserNsId> owner_ns(Pid pid) const;

private:
    struct Node {
        UserNsId parent; // 0 for root
        Uid creator;
    };
    UserNsId next_id_ = 1;
    std::map<UserNsId, Node> nodes_;
    std::map<Pid, UserNsId> membership_;
};

struct ProcessDescriptor {
    Pid pid = 0;
    Uid real_uid = 0;
    Uid effective_uid = 0;
    UserNsId user_ns = 0;
    bool dumpable = true;
    bool no_new_privs = false;
    // Task-tree ancestors (parent, grandparent, ...), kernel-maintained.
    std::vector<Pid> task_ancestors;
    // Effective capability sets this process holds per user namespace.
    std::map<UserNsId, std::uint32_t> caps_by_ns;
};

struct TracePolicy {
    bool enforce_dumpable = true;
    // Tightening knob: uid-based tracing of no_new_privs targets is refused
    // unless the capability path succeeds.
    bool require_capability_for_no_new_privs = false;
    // Yama-style scope applied across the whole task tree, independent of
    // namespace boundaries: 0 = classic, 1 = descendants only.
    int yama_scope = 0;
};

enum class TraceVerdict {
    kAllowSelf,
    kAllowCapability,
    kAllowUidMatch,
    kDenyUnknownNamespace,
    kDenyMembershipForgery,
    kDenyNamespaceIsolation,
    kDenyNotDumpable,
    kDenyNoNewPrivs,
    kDenyYamaScope,
    kDenyUidMismatch,
};

struct TraceDecision {
    bool allowed = false;
    TraceVerdict verdict = TraceVerdict::kDenyUidMismatch;
    // Namespace ancestor chain consulted while deciding (the audited call chain).
    std::vector<UserNsId> ns_chain;
    std::string reason;
};

struct AuditRecord {
    std::uint64_t seq = 0;
    Pid tracer = 0;
    Pid target = 0;
    std::vector<UserNsId> ns_chain;
    bool allowed = false;
    TraceVerdict verdict = TraceVerdict::kDenyUidMismatch;
    std::string reason;
    std::uint64_t prev_hash = 0;
    std::uint64_t hash = 0;
};

// Append-only, hash-chained audit log; verify_chain() detects tampering.
class AuditLog {
public:
    void append(const ProcessDescriptor& tracer, const ProcessDescriptor& target,
                const TraceDecision& decision);
    bool verify_chain() const;
    const std::vector<AuditRecord>& records() const { return records_; }

private:
    std::vector<AuditRecord> records_;
};

class TracePermissionEvaluator {
public:
    TracePermissionEvaluator(const UserNamespaceRegistry& registry, TracePolicy policy,
                             AuditLog* audit);

    TraceDecision evaluate(const ProcessDescriptor& tracer,
                           const ProcessDescriptor& target) const;

private:
    bool has_ptrace_cap_in(const ProcessDescriptor& tracer,
                           const std::vector<UserNsId>& target_chain) const;

    const UserNamespaceRegistry& registry_;
    TracePolicy policy_;
    AuditLog* audit_; // may be nullptr
};

} // namespace netsec

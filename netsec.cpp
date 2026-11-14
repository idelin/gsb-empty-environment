#include "netsec.hpp"

#include <algorithm>

namespace netsec {

namespace {

std::uint64_t fnv1a_mix(std::uint64_t h, std::uint64_t v) {
    for (int i = 0; i < 8; ++i) {
        h ^= (v >> (i * 8)) & 0xffu;
        h *= 1099511628211ull;
    }
    return h;
}

std::uint64_t fnv1a_mix_str(std::uint64_t h, const std::string& s) {
    for (unsigned char c : s) {
        h ^= c;
        h *= 1099511628211ull;
    }
    return h;
}

std::uint64_t record_hash(const AuditRecord& r) {
    std::uint64_t h = 1469598103934665603ull;
    h = fnv1a_mix(h, r.seq);
    h = fnv1a_mix(h, static_cast<std::uint64_t>(r.tracer));
    h = fnv1a_mix(h, static_cast<std::uint64_t>(r.target));
    for (UserNsId id : r.ns_chain) h = fnv1a_mix(h, id);
    h = fnv1a_mix(h, r.allowed ? 1 : 0);
    h = fnv1a_mix(h, static_cast<std::uint64_t>(r.verdict));
    h = fnv1a_mix_str(h, r.reason);
    h = fnv1a_mix(h, r.prev_hash);
    return h;
}

} // namespace

UserNsId UserNamespaceRegistry::create_root(Uid creator) {
    const UserNsId id = next_id_++;
    nodes_.emplace(id, Node{0, creator});
    return id;
}

std::optional<UserNsId> UserNamespaceRegistry::create_child(UserNsId parent, Uid creator) {
    if (!exists(parent)) return std::nullopt;
    const UserNsId id = next_id_++;
    nodes_.emplace(id, Node{parent, creator});
    return id;
}

bool UserNamespaceRegistry::exists(UserNsId id) const {
    return nodes_.find(id) != nodes_.end();
}

bool UserNamespaceRegistry::is_ancestor(UserNsId ancestor, UserNsId node) const {
    const auto chain = ancestor_chain(node);
    return std::find(chain.begin(), chain.end(), ancestor) != chain.end();
}

std::vector<UserNsId> UserNamespaceRegistry::ancestor_chain(UserNsId node) const {
    std::vector<UserNsId> chain;
    UserNsId cur = node;
    while (cur != 0) {
        auto it = nodes_.find(cur);
        if (it == nodes_.end()) return {};
        chain.push_back(cur);
        cur = it->second.parent;
    }
    return chain;
}

void UserNamespaceRegistry::assign(Pid pid, UserNsId ns) {
    membership_[pid] = ns;
}

std::optional<UserNsId> UserNamespaceRegistry::owner_ns(Pid pid) const {
    auto it = membership_.find(pid);
    if (it == membership_.end()) return std::nullopt;
    return it->second;
}

void AuditLog::append(const ProcessDescriptor& tracer, const ProcessDescriptor& target,
                      const TraceDecision& decision) {
    AuditRecord rec;
    rec.seq = records_.empty() ? 1 : records_.back().seq + 1;
    rec.tracer = tracer.pid;
    rec.target = target.pid;
    rec.ns_chain = decision.ns_chain;
    rec.allowed = decision.allowed;
    rec.verdict = decision.verdict;
    rec.reason = decision.reason;
    rec.prev_hash = records_.empty() ? 0 : records_.back().hash;
    rec.hash = record_hash(rec);
    records_.push_back(std::move(rec));
}

bool AuditLog::verify_chain() const {
    std::uint64_t prev = 0;
    for (const auto& rec : records_) {
        if (rec.prev_hash != prev) return false;
        if (rec.hash != record_hash(rec)) return false;
        prev = rec.hash;
    }
    return true;
}

TracePermissionEvaluator::TracePermissionEvaluator(const UserNamespaceRegistry& registry,
                                                   TracePolicy policy, AuditLog* audit)
    : registry_(registry), policy_(policy), audit_(audit) {}

bool TracePermissionEvaluator::has_ptrace_cap_in(
    const ProcessDescriptor& tracer, const std::vector<UserNsId>& target_chain) const {
    // A capability only counts when held in the target's own namespace or one
    // of its ancestors. Capabilities acquired inside a descendant namespace
    // (e.g. a sandbox-created userns) never apply to ancestor-ns processes.
    for (UserNsId ns : target_chain) {
        auto it = tracer.caps_by_ns.find(ns);
        if (it != tracer.caps_by_ns.end() && (it->second & kCapSysPtrace)) return true;
    }
    return false;
}

TraceDecision TracePermissionEvaluator::evaluate(const ProcessDescriptor& tracer,
                                                 const ProcessDescriptor& target) const {
    TraceDecision d;
    d.ns_chain = registry_.ancestor_chain(target.user_ns);

    auto finish = [&](bool allowed, TraceVerdict v, std::string reason) {
        d.allowed = allowed;
        d.verdict = v;
        d.reason = std::move(reason);
        if (audit_) audit_->append(tracer, target, d);
        return d;
    };

    // Forgery guards: both namespaces must exist, and the descriptor's
    // namespace claim must match the registry's authoritative membership.
    if (!registry_.exists(target.user_ns) || !registry_.exists(tracer.user_ns))
        return finish(false, TraceVerdict::kDenyUnknownNamespace,
                      "namespace id not present in registry");
    const auto tracer_owner = registry_.owner_ns(tracer.pid);
    const auto target_owner = registry_.owner_ns(target.pid);
    if ((tracer_owner && *tracer_owner != tracer.user_ns) ||
        (target_owner && *target_owner != target.user_ns))
        return finish(false, TraceVerdict::kDenyMembershipForgery,
                      "descriptor namespace claim contradicts registry membership");

    if (tracer.pid == target.pid)
        return finish(true, TraceVerdict::kAllowSelf, "self trace");

    // Capability path: valid along the target's ancestor chain only.
    if (has_ptrace_cap_in(tracer, d.ns_chain))
        return finish(true, TraceVerdict::kAllowCapability,
                      "CAP_SYS_PTRACE in target namespace or its ancestor");

    // Isolation: a process in a descendant (or unrelated) namespace gains no
    // uid-based rights over a process in an ancestor/sibling namespace.
    if (tracer.user_ns != target.user_ns)
        return finish(false, TraceVerdict::kDenyNamespaceIsolation,
                      "tracer namespace is outside target namespace; "
                      "descendant-namespace privileges do not propagate upward");

    // Yama-style scope, evaluated on the global task tree so a namespace
    // boundary cannot reset the ancestry computation.
    if (policy_.yama_scope >= 1 &&
        std::find(target.task_ancestors.begin(), target.task_ancestors.end(),
                  tracer.pid) == target.task_ancestors.end())
        return finish(false, TraceVerdict::kDenyYamaScope,
                      "restricted scope: tracer is not a task-tree ancestor of target");

    // Uid path (same namespace): real uids must match.
    if (tracer.real_uid != target.real_uid)
        return finish(false, TraceVerdict::kDenyUidMismatch, "real uid mismatch");

    if (policy_.enforce_dumpable && !target.dumpable)
        return finish(false, TraceVerdict::kDenyNotDumpable,
                      "target is not dumpable and tracer lacks capability");

    if (policy_.require_capability_for_no_new_privs && target.no_new_privs)
        return finish(false, TraceVerdict::kDenyNoNewPrivs,
                      "policy requires capability for no_new_privs targets");

    return finish(true, TraceVerdict::kAllowUidMatch,
                  "same namespace, matching real uid, dumpable");
}

} // namespace netsec

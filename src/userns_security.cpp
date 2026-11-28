// SPDX-License-Identifier: MIT

#include "userns_security.hpp"

#include <algorithm>
#include <stdexcept>
#include <utility>

namespace userns_sec {

namespace {

constexpr std::uint64_t kIdSpan = 0x100000000ULL;  // UINT32_MAX + 1
constexpr CapMask kValidCapMask =
    (kLastCap >= 63) ? ~static_cast<CapMask>(0)
                     : (static_cast<CapMask>(1) << (kLastCap + 1)) - 1U;

const char* map_kind(bool is_uid) { return is_uid ? "uid_map" : "gid_map"; }

void add_violation(AuditResult& out, ViolationCode code, int node_id,
                   std::string detail) {
    out.violations.push_back(Violation{code, node_id, std::move(detail)});
}

IdRange entry_upper(const IdMapEntry& e) {
    return IdRange(static_cast<std::uint64_t>(e.ns_id),
                   static_cast<std::uint64_t>(e.ns_id) +
                       static_cast<std::uint64_t>(e.count));
}

IdRange entry_lower(const IdMapEntry& e) {
    return IdRange(static_cast<std::uint64_t>(e.lower_id),
                   static_cast<std::uint64_t>(e.lower_id) +
                       static_cast<std::uint64_t>(e.count));
}

// Kernel-set relationship invariants shared by every credential audit.
AuditResult check_cap_invariants(int node_id, const ProcessCredentials& c) {
    AuditResult r;
    const CapMask all = c.cap_prm | c.cap_eff | c.cap_inh | c.cap_bnd |
                        c.cap_amb | c.cap_eff_in_init;
    if ((all & ~kValidCapMask) != 0U) {
        add_violation(r, ViolationCode::CapInvalidBit, node_id,
                      "capability bit above CAP_CHECKPOINT_RESTORE is set");
    }
    if (!cap_subset(c.cap_eff, c.cap_prm)) {
        add_violation(r, ViolationCode::CapEffNotSubsetPrm, node_id,
                      "CapEff must be a subset of CapPrm");
    }
    if (!cap_subset(c.cap_prm, c.cap_bnd)) {
        add_violation(r, ViolationCode::CapPrmNotSubsetBnd, node_id,
                      "CapPrm must be a subset of CapBnd");
    }
    if (!cap_subset(c.cap_amb, c.cap_prm)) {
        add_violation(r, ViolationCode::CapAmbNotSubsetPrm, node_id,
                      "CapAmb must be a subset of CapPrm");
    }
    if (!cap_subset(c.cap_amb, c.cap_inh)) {
        add_violation(r, ViolationCode::CapAmbNotSubsetInh, node_id,
                      "CapAmb must be a subset of CapInh");
    }
    return r;
}

}  // namespace

bool ranges_intersect(const IdRange& a, const IdRange& b) {
    return a.first < b.last && b.first < a.last;
}

bool range_contains(const IdRange& outer, const IdRange& inner) {
    return outer.first <= inner.first && inner.last <= outer.last;
}

bool cap_subset(CapMask subset, CapMask superset) {
    return (subset & ~superset) == 0U;
}

const char* violation_name(ViolationCode code) {
    switch (code) {
        case ViolationCode::MapTooManyEntries:       return "MapTooManyEntries";
        case ViolationCode::MapZeroCount:            return "MapZeroCount";
        case ViolationCode::Map32BitWrapAround:      return "Map32BitWrapAround";
        case ViolationCode::MapUpperOverlap:         return "MapUpperOverlap";
        case ViolationCode::MapLowerOverlap:         return "MapLowerOverlap";
        case ViolationCode::MapLowerOutsideParent:   return "MapLowerOutsideParent";
        case ViolationCode::MapProtectedHostId:      return "MapProtectedHostId";
        case ViolationCode::SetgroupsRequired:       return "SetgroupsRequired";
        case ViolationCode::CapInvalidBit:           return "CapInvalidBit";
        case ViolationCode::CapEffNotSubsetPrm:      return "CapEffNotSubsetPrm";
        case ViolationCode::CapPrmNotSubsetBnd:      return "CapPrmNotSubsetBnd";
        case ViolationCode::CapAmbNotSubsetPrm:      return "CapAmbNotSubsetPrm";
        case ViolationCode::CapAmbNotSubsetInh:      return "CapAmbNotSubsetInh";
        case ViolationCode::CapSysAdminInInitNs:     return "CapSysAdminInInitNs";
        case ViolationCode::MissingNoNewPrivs:       return "MissingNoNewPrivs";
        case ViolationCode::ExecAmbNotSubsetBnd:     return "ExecAmbNotSubsetBnd";
        case ViolationCode::ExecInhNotSubsetBnd:     return "ExecInhNotSubsetBnd";
        case ViolationCode::ExecFileCapNotSubsetBnd: return "ExecFileCapNotSubsetBnd";
        case ViolationCode::ExecResultMismatch:      return "ExecResultMismatch";
    }
    return "Unknown";
}

UserNsAuditor::UserNsAuditor() : UserNsAuditor(SecurityPolicy{}) {}

UserNsAuditor::UserNsAuditor(SecurityPolicy policy)
    : policy_(std::move(policy)) {
    // Node 0: init_user_ns. Its full 32-bit ID space is implicitly mapped
    // onto itself, so containment treats any parent range as covered.
    UserNsNode init_node;
    init_node.id = 0;
    init_node.parent = -1;
    init_node.created_unprivileged = false;
    init_node.setgroups = SetgroupsState::Allow;
    nodes_.push_back(std::move(init_node));
}

int UserNsAuditor::add_namespace(int parent_id, bool created_unprivileged,
                                 SetgroupsState setgroups,
                                 std::vector<IdMapEntry> uid_map,
                                 std::vector<IdMapEntry> gid_map) {
    if (parent_id < 0 ||
        static_cast<std::size_t>(parent_id) >= nodes_.size()) {
        throw std::invalid_argument(
            "add_namespace: unknown parent namespace id");
    }
    UserNsNode node;
    node.id = static_cast<int>(nodes_.size());
    node.parent = parent_id;
    node.created_unprivileged = created_unprivileged;
    node.setgroups = setgroups;
    node.uid_map = std::move(uid_map);
    node.gid_map = std::move(gid_map);
    nodes_.push_back(std::move(node));
    return nodes_.back().id;
}

const UserNsNode& UserNsAuditor::node(int node_id) const {
    if (node_id < 0 ||
        static_cast<std::size_t>(node_id) >= nodes_.size()) {
        throw std::invalid_argument("node: unknown namespace id");
    }
    return nodes_[static_cast<std::size_t>(node_id)];
}

bool UserNsAuditor::translate_to_parent(int node_id, bool is_uid,
                                        Id in, Id& out) const {
    const UserNsNode& n = node(node_id);
    const std::vector<IdMapEntry>& map = is_uid ? n.uid_map : n.gid_map;
    for (const IdMapEntry& e : map) {
        const std::uint64_t upper_first =
            static_cast<std::uint64_t>(e.ns_id);
        const std::uint64_t upper_last =
            upper_first + static_cast<std::uint64_t>(e.count);
        const std::uint64_t v = static_cast<std::uint64_t>(in);
        if (v >= upper_first && v < upper_last) {
            out = static_cast<Id>(
                static_cast<std::uint64_t>(e.lower_id) +
                (v - upper_first));
            return true;
        }
    }
    return false;
}

std::vector<int> UserNsAuditor::ancestor_chain(int node_id) const {
    std::vector<int> chain;
    int cur = node_id;
    while (cur >= 0) {
        chain.push_back(cur);
        cur = node(cur).parent;
    }
    std::reverse(chain.begin(), chain.end());  // init_user_ns first
    return chain;
}

bool UserNsAuditor::translate_to_init(int node_id, bool is_uid,
                                      Id in, Id& out) const {
    Id current = in;
    const std::vector<int> chain = ancestor_chain(node_id);
    // Walk from the deepest node up to (but not including) init_user_ns;
    // the init node itself is the translation target.
    for (auto it = chain.rbegin(); it != chain.rend(); ++it) {
        if (*it == 0) {
            break;
        }
        Id parent_id = 0;
        if (!translate_to_parent(*it, is_uid, current, parent_id)) {
            return false;
        }
        current = parent_id;
    }
    out = current;
    return true;
}

bool UserNsAuditor::is_unprivileged_tenant(int node_id) const {
    // A tenant is unprivileged when the target node itself was created by an
    // unprivileged process or any ancestor namespace in the chain was. A
    // privileged creation nested inside an unprivileged branch still shares
    // the unprivileged ancestor's reduced host capabilities.
    int cur = node_id;
    while (cur > 0) {
        if (node(cur).created_unprivileged) {
            return true;
        }
        cur = node(cur).parent;
    }
    return false;
}

bool UserNsAuditor::parent_covers(int parent_id, bool is_uid,
                                  const IdRange& r) const {
    // init_user_ns covers the full 32-bit ID space.
    if (parent_id == 0) {
        return r.last <= kIdSpan;
    }
    const UserNsNode& p = node(parent_id);
    const std::vector<IdMapEntry>& map = is_uid ? p.uid_map : p.gid_map;
    for (const IdMapEntry& e : map) {
        if (range_contains(entry_upper(e), r)) {
            return true;
        }
    }
    return false;
}

AuditResult UserNsAuditor::analyze_map(int node_id, bool is_uid) const {
    AuditResult out;
    const UserNsNode& n = node(node_id);
    const std::vector<IdMapEntry>& map = is_uid ? n.uid_map : n.gid_map;
    const std::vector<IdRange>& protected_ranges =
        is_uid ? policy_.protected_uid_ranges
               : policy_.protected_gid_ranges;

    if (map.size() > kMaxMapEntries) {
        add_violation(out, ViolationCode::MapTooManyEntries, node_id,
                      std::string(map_kind(is_uid)) + " exceeds 340 entries");
        // Entries present are still validated below.
    }

    std::vector<IdRange> uppers;
    std::vector<IdRange> lowers;
    uppers.reserve(map.size());
    lowers.reserve(map.size());

    for (std::size_t i = 0; i < map.size(); ++i) {
        const IdMapEntry& e = map[i];
        if (e.count == 0U) {
            add_violation(out, ViolationCode::MapZeroCount, node_id,
                          std::string(map_kind(is_uid)) + " entry " +
                          std::to_string(i) + " has count 0");
            continue;
        }
        const IdRange upper = entry_upper(e);
        const IdRange lower = entry_lower(e);
        if (upper.last > kIdSpan || lower.last > kIdSpan) {
            add_violation(out, ViolationCode::Map32BitWrapAround, node_id,
                          std::string(map_kind(is_uid)) + " entry " +
                          std::to_string(i) +
                          " overflows the 32-bit ID space");
            // Overflowing ranges never participate in an accepted map.
            continue;
        }
        uppers.push_back(upper);
        lowers.push_back(lower);
    }

    // Overlap checks on both sides of the map.
    for (std::size_t i = 0; i < uppers.size(); ++i) {
        for (std::size_t j = i + 1; j < uppers.size(); ++j) {
            if (ranges_intersect(uppers[i], uppers[j])) {
                add_violation(out, ViolationCode::MapUpperOverlap, node_id,
                              std::string(map_kind(is_uid)) +
                                  " in-namespace ranges overlap");
            }
            if (ranges_intersect(lowers[i], lowers[j])) {
                add_violation(out, ViolationCode::MapLowerOverlap, node_id,
                              std::string(map_kind(is_uid)) +
                                  " parent-namespace ranges overlap");
            }
        }
    }

    // Containment of every lower range within the parent's mapped ranges.
    if (n.parent >= 0) {
        for (const IdRange& lower : lowers) {
            if (!parent_covers(n.parent, is_uid, lower)) {
                add_violation(out, ViolationCode::MapLowerOutsideParent,
                              node_id,
                              std::string(map_kind(is_uid)) +
                                  " lower range is outside the parent map");
            }
        }
    }

    // Translate every mapped in-namespace ID along the full ancestor chain
    // and reject translations landing on host 0 / a protected system range.
    for (const IdRange& upper : uppers) {
        for (std::uint64_t v = upper.first; v < upper.last; ++v) {
            Id host_id = 0;
            if (!translate_to_init(node_id, is_uid,
                                   static_cast<Id>(v), host_id)) {
                // Unmapped at some ancestor level: already reported by the
                // containment rule, no need for another violation.
                break;
            }
            const IdRange host_range(
                static_cast<std::uint64_t>(host_id),
                static_cast<std::uint64_t>(host_id) + 1U);
            for (const IdRange& prot : protected_ranges) {
                if (ranges_intersect(host_range, prot)) {
                    add_violation(out, ViolationCode::MapProtectedHostId,
                                  node_id,
                                  std::string(map_kind(is_uid)) +
                                      " translates in-namespace id " +
                                      std::to_string(v) +
                                      " to protected host id " +
                                      std::to_string(host_id));
                    break;
                }
            }
        }
    }

    return out;
}

AuditResult UserNsAuditor::audit_one_map(int node_id, bool is_uid) const {
    return analyze_map(node_id, is_uid);
}

AuditResult UserNsAuditor::audit_maps(int node_id) const {
    AuditResult r = audit_one_map(node_id, true);
    AuditResult g = audit_one_map(node_id, false);
    r.violations.insert(r.violations.end(), g.violations.begin(),
                        g.violations.end());
    return r;
}

AuditResult UserNsAuditor::audit_setgroups(int node_id) const {
    AuditResult r;
    const UserNsNode& n = node(node_id);
    // The kernel only applies the setgroups gate to non-init namespaces
    // created without privilege; init_user_ns always manages real groups.
    if (node_id == 0 || !n.created_unprivileged) {
        return r;
    }
    if (!n.gid_map.empty() && n.setgroups != SetgroupsState::Deny) {
        add_violation(r, ViolationCode::SetgroupsRequired, node_id,
                      "CVE-2014-8989: unprivileged gid_map write requires "
                      "setgroups=deny first, otherwise supplementary groups "
                      "can be stripped to bypass deny rules");
    }
    return r;
}

AuditResult UserNsAuditor::audit_credentials(
    int node_id, const ProcessCredentials& cred) const {
    AuditResult r = check_cap_invariants(node_id, cred);

    if (is_unprivileged_tenant(node_id)) {
        if (policy_.forbid_sys_admin_in_init_for_unprivileged &&
            (cred.cap_eff_in_init & cap_bit(CAP_SYS_ADMIN)) != 0U) {
            add_violation(r, ViolationCode::CapSysAdminInInitNs, node_id,
                          "unprivileged tenant retains CAP_SYS_ADMIN in "
                          "init_user_ns");
        }
        if (policy_.require_no_new_privs_for_unprivileged &&
            !cred.no_new_privs) {
            add_violation(r, ViolationCode::MissingNoNewPrivs, node_id,
                          "unprivileged tenant must set PR_SET_NO_NEW_PRIVS "
                          "to block SUID/file-capability escalation");
        }
    }
    return r;
}

AuditResult UserNsAuditor::audit_enter_namespace(
    int node_id, const ProcessCredentials& cred) const {
    // Entering the namespace is governed by the same credential invariants
    // and unprivileged-tenant policy as holding credentials inside it.
    return audit_credentials(node_id, cred);
}

AuditResult UserNsAuditor::audit_execve(
    int node_id, const ProcessCredentials& parent_cred,
    const ProcessCredentials& result, const FileCapabilities* file_caps) const {
    AuditResult r;

    // Bounding set never grows on execve and constrains every other set.
    if (!cap_subset(result.cap_bnd, parent_cred.cap_bnd)) {
        add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                      "CapBnd grew across execve");
    }
    if (!cap_subset(result.cap_inh, result.cap_bnd)) {
        add_violation(r, ViolationCode::ExecInhNotSubsetBnd, node_id,
                      "post-exec CapInh is not a subset of CapBnd");
    }
    if (!cap_subset(result.cap_amb, result.cap_bnd)) {
        add_violation(r, ViolationCode::ExecAmbNotSubsetBnd, node_id,
                      "post-exec CapAmb is not a subset of CapBnd");
    }

    // Inheritable/ambient caps cannot appear on execve unless the running
    // process already held them inside the bounding set.
    if (!cap_subset(result.cap_inh, parent_cred.cap_inh)) {
        add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                      "CapInh grew across execve");
    }
    if (!cap_subset(result.cap_amb, parent_cred.cap_amb)) {
        add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                      "CapAmb grew across execve");
    }

    if (file_caps != nullptr) {
        if (!cap_subset(file_caps->permitted, result.cap_bnd)) {
            add_violation(r, ViolationCode::ExecFileCapNotSubsetBnd, node_id,
                          "file permitted capabilities are outside CapBnd");
        }
        // Expected new permitted set per capabilities(7):
        //   P' = (P_inh & X_inh) | (X_perm & P_bnd) | P_amb
        const CapMask expected_prm =
            (parent_cred.cap_inh & file_caps->inheritable) |
            (file_caps->permitted & result.cap_bnd) |
            result.cap_amb;
        if (!cap_subset(result.cap_prm, expected_prm) ||
            !cap_subset(expected_prm, result.cap_prm)) {
            add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                          "post-exec CapPrm does not match the file/"
                          "inheritable/ambient transition rule");
        }
        const CapMask expected_eff =
            file_caps->effective ? result.cap_prm
                                 : result.cap_amb;
        if (!cap_subset(result.cap_eff, expected_eff) ||
            !cap_subset(expected_eff, result.cap_eff)) {
            add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                          "post-exec CapEff does not match the file "
                          "effective-bit / ambient transition rule");
        }
    } else {
        // Without file capabilities the only surviving permitted/effective
        // caps on execve are the ambient set.
        if (!cap_subset(result.cap_prm, result.cap_amb) ||
            !cap_subset(result.cap_amb, result.cap_prm)) {
            add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                          "without file capabilities post-exec CapPrm must "
                          "equal CapAmb");
        }
        if (!cap_subset(result.cap_eff, result.cap_amb) ||
            !cap_subset(result.cap_amb, result.cap_eff)) {
            add_violation(r, ViolationCode::ExecResultMismatch, node_id,
                          "without file capabilities post-exec CapEff must "
                          "equal CapAmb");
        }
    }

    // The resulting state must also satisfy the static invariants and the
    // unprivileged-tenant policy (CAP_SYS_ADMIN in init / no_new_privs).
    AuditResult inner = audit_credentials(node_id, result);
    r.violations.insert(r.violations.end(), inner.violations.begin(),
                        inner.violations.end());
    return r;
}

AuditResult UserNsAuditor::audit_all() const {
    AuditResult r;
    for (const UserNsNode& n : nodes_) {
        AuditResult maps = audit_maps(n.id);
        r.violations.insert(r.violations.end(), maps.violations.begin(),
                            maps.violations.end());
        AuditResult sg = audit_setgroups(n.id);
        r.violations.insert(r.violations.end(), sg.violations.begin(),
                            sg.violations.end());
    }
    return r;
}

}  // namespace userns_sec

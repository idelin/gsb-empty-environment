// SPDX-License-Identifier: MIT
//
// userns_security.hpp
//
// Embeddable, zero-dependency C++17 security audit library for nested Linux
// user-namespace ID maps (/proc/<pid>/uid_map, gid_map, setgroups) and the
// five process capability sets (CapPrm/CapEff/CapInh/CapBnd/CapAmb).
//
// The library models an arbitrary tree of nested user namespaces and
// validates:
//
//   1. uid_map/gid_map correctness: 32-bit wrap-around, overlap of the
//      in-namespace (upper) ranges, overlap of the parent (lower) ranges,
//      containment of every lower range inside the parent namespace's
//      already-mapped ranges, and prohibition of any tenant ID (including
//      IDs reached transitively through multiple nested namespaces) from
//      mapping onto host UID/GID 0 or any configured protected system ID
//      range in init_user_ns.
//   2. The CVE-2014-8989 rule: an unprivileged creator may only write
//      gid_map after "deny" has been written to setgroups.
//   3. Process credential and capability transition rules on entering a
//      child namespace and on execve: CapEff subset of CapPrm,
//      CapAmb subset of CapPrm intersect CapInh, bounding-set containment,
//      no CAP_SYS_ADMIN retained in init_user_ns by an unprivileged tenant,
//      and mandatory PR_SET_NO_NEW_PRIVS for unprivileged containers.
//
// No dynamic allocation is required by callers; all storage is owned by the
// auditor and grows as nodes are registered.

#ifndef USERNS_SECURITY_HPP
#define USERNS_SECURITY_HPP

#include <cstdint>
#include <string>
#include <vector>

namespace userns_sec {

// 32-bit kernel ID type (uid_t/gid_t).
using Id = std::uint32_t;

// 64-bit Linux capability bitmask (bit n == CAP_n, see cap_bit()).
using CapMask = std::uint64_t;

// Maximum number of entries accepted in a single uid_map/gid_map file.
// Matches the kernel's 340-line per-map write limit.
constexpr std::size_t kMaxMapEntries = 340;

// Highest valid Linux capability bit modelled by this library
// (CAP_CHECKPOINT_RESTORE, Linux 5.9).
constexpr int kLastCap = 40;

// Linux capability numbers modelled (the full bit layout up to kLastCap is
// understood; the named constants cover the bits relevant to containers).
enum Capability : int {
    CAP_CHOWN            = 0,
    CAP_DAC_OVERRIDE     = 1,
    CAP_DAC_READ_SEARCH  = 2,
    CAP_FOWNER           = 3,
    CAP_FSETID           = 4,
    CAP_KILL             = 5,
    CAP_SETGID           = 6,
    CAP_SETUID           = 7,
    CAP_SETPCAP          = 8,
    CAP_LINUX_IMMUTABLE  = 9,
    CAP_NET_BIND_SERVICE = 10,
    CAP_NET_BROADCAST    = 11,
    CAP_NET_ADMIN        = 12,
    CAP_NET_RAW          = 13,
    CAP_IPC_LOCK         = 14,
    CAP_IPC_OWNER        = 15,
    CAP_SYS_MODULE       = 16,
    CAP_SYS_RAWIO        = 17,
    CAP_SYS_CHROOT       = 18,
    CAP_SYS_PTRACE       = 19,
    CAP_SYS_PACCT        = 20,
    CAP_SYS_ADMIN        = 21,
    CAP_SYS_BOOT         = 22,
    CAP_SYS_NICE         = 23,
    CAP_SYS_RESOURCE     = 24,
    CAP_SYS_TIME         = 25,
    CAP_SYS_TTY_CONFIG   = 26,
    CAP_MKNOD            = 27,
    CAP_LEASE            = 28,
    CAP_AUDIT_WRITE      = 29,
    CAP_AUDIT_CONTROL    = 30,
    CAP_SETFCAP          = 31,
    CAP_MAC_OVERRIDE     = 32,
    CAP_MAC_ADMIN        = 33,
    CAP_SYSLOG           = 34,
    CAP_WAKE_ALARM       = 35,
    CAP_BLOCK_SUSPEND    = 36,
    CAP_AUDIT_READ       = 37,
    CAP_PERFMON          = 38,
    CAP_BPF              = 39,
    CAP_CHECKPOINT_RESTORE = 40,
};

// Returns the 64-bit bit corresponding to a Linux capability number.
constexpr CapMask cap_bit(int cap_number) {
    return static_cast<CapMask>(1) << cap_number;
}

// One line of /proc/<pid>/uid_map or gid_map:
//   "<ns_id>  <lower_id>  <count>"
// ns_id    = first ID inside the (child) namespace owning the map.
// lower_id = first ID in the parent namespace that ns_id maps to.
// count    = number of consecutive IDs mapped.
struct IdMapEntry {
    Id ns_id = 0;
    Id lower_id = 0;
    Id count = 0;
};

// Half-open ID interval [first, last) using 64-bit arithmetic so that
// ranges ending exactly at UINT32_MAX + 1 are representable without
// wrap-around.
struct IdRange {
    std::uint64_t first = 0;
    std::uint64_t last = 0;

    IdRange() = default;
    IdRange(std::uint64_t first_id, std::uint64_t one_past_last)
        : first(first_id), last(one_past_last) {}
};

// Content of /proc/<pid>/setgroups.
enum class SetgroupsState {
    Deny,
    Allow,
};

// A registered user-namespace node. Node 0 is always init_user_ns, created
// implicitly by the auditor.
struct UserNsNode {
    int id = -1;                                  // auditor-assigned node id
    int parent = -1;                              // -1 for init_user_ns
    bool created_unprivileged = false;            // clone(CLONE_NEWUSER)
                                                  // by an unprivileged process
    SetgroupsState setgroups = SetgroupsState::Allow;
    std::vector<IdMapEntry> uid_map;
    std::vector<IdMapEntry> gid_map;
};

// Snapshot of a process's credential/capability state, mirroring
// /proc/<pid>/status CapPrm/CapEff/CapInh/CapBnd/CapAmb.
struct ProcessCredentials {
    CapMask cap_prm = 0;   // CapPrm: permitted
    CapMask cap_eff = 0;   // CapEff: effective
    CapMask cap_inh = 0;   // CapInh: inheritable
    CapMask cap_bnd = 0;   // CapBnd: bounding
    CapMask cap_amb = 0;   // CapAmb: ambient

    // Effective capabilities as they appear in init_user_ns, obtained by
    // translating the credentials up the ancestor chain. An unprivileged
    // tenant must never retain CAP_SYS_ADMIN here.
    CapMask cap_eff_in_init = 0;

    // Whether PR_SET_NO_NEW_PRIVS has been applied (no_new_privs bit).
    bool no_new_privs = false;
};

// File capabilities attached to an executable, as parsed from the
// security.capability xattr (only the bits audited on execve).
struct FileCapabilities {
    CapMask permitted = 0;
    CapMask inheritable = 0;
    bool effective = false;  // legacy "effective" single-bit
};

// Identifies which rule produced a violation.
enum class ViolationCode {
    MapTooManyEntries,        // more than 340 lines
    MapZeroCount,             // an entry has count == 0
    Map32BitWrapAround,       // ns_id + count or lower_id + count overflows u32
    MapUpperOverlap,          // in-namespace (ns_id) ranges overlap
    MapLowerOverlap,          // parent (lower_id) ranges overlap
    MapLowerOutsideParent,    // lower range is not inside the parent map
    MapProtectedHostId,       // translation reaches host UID/GID 0 / protected
    SetgroupsRequired,        // CVE-2014-8989: gid_map without setgroups=deny
    CapInvalidBit,            // bit above kLastCap is set
    CapEffNotSubsetPrm,       // CapEff is not a subset of CapPrm
    CapPrmNotSubsetBnd,       // CapPrm is not a subset of CapBnd
    CapAmbNotSubsetPrm,       // CapAmb is not a subset of CapPrm
    CapAmbNotSubsetInh,       // CapAmb is not a subset of CapInh
    CapSysAdminInInitNs,      // unprivileged tenant holds CAP_SYS_ADMIN in init
    MissingNoNewPrivs,        // unprivileged tenant without no_new_privs
    ExecAmbNotSubsetBnd,      // ambient caps survive execve outside CapBnd
    ExecInhNotSubsetBnd,      // inheritable caps outside CapBnd
    ExecFileCapNotSubsetBnd,  // file permitted caps outside CapBnd
    ExecResultMismatch,       // observed post-exec sets violate transition rules
};

struct Violation {
    ViolationCode code;
    int node_id;       // namespace node, -1 when not namespace-specific
    std::string detail;
};

struct AuditResult {
    std::vector<Violation> violations;

    bool ok() const { return violations.empty(); }
    explicit operator bool() const { return ok(); }
};

// Configurable policy. Defaults follow common container-host hardening:
// host IDs [0, 1000) (root plus the system/systemd UID/GID ranges) are
// protected for both UIDs and GIDs.
struct SecurityPolicy {
    std::vector<IdRange> protected_uid_ranges = {IdRange(0, 1000)};
    std::vector<IdRange> protected_gid_ranges = {IdRange(0, 1000)};

    // When true, an unprivileged tenant must have no_new_privs set before
    // entering the namespace and on every audited execve.
    bool require_no_new_privs_for_unprivileged = true;

    // When true, an unprivileged tenant must not retain CAP_SYS_ADMIN in
    // init_user_ns.
    bool forbid_sys_admin_in_init_for_unprivileged = true;
};

class UserNsAuditor {
public:
    UserNsAuditor();
    explicit UserNsAuditor(SecurityPolicy policy);

    // Registers a namespace node. parent_id must reference a previously
    // registered node (use 0 for init_user_ns). Returns the assigned node
    // id; throws std::invalid_argument on malformed topology.
    int add_namespace(int parent_id,
                      bool created_unprivileged,
                      SetgroupsState setgroups,
                      std::vector<IdMapEntry> uid_map,
                      std::vector<IdMapEntry> gid_map);

    const UserNsNode& node(int node_id) const;
    const SecurityPolicy& policy() const { return policy_; }
    std::size_t node_count() const { return nodes_.size(); }

    // Translates an ID inside node_id up one level to the parent namespace.
    // Returns false (and leaves out untouched) when the ID is not covered by
    // the node's map. kind selects uid_map (true) or gid_map (false).
    bool translate_to_parent(int node_id, bool is_uid, Id in, Id& out) const;

    // Translates an ID inside node_id along the full ancestor chain into
    // init_user_ns. Returns false if any level does not cover the ID.
    bool translate_to_init(int node_id, bool is_uid, Id in, Id& out) const;

    // (1) Map validation for a single node (wrap/overlap/containment/
    // protected host IDs), both uid_map and gid_map.
    AuditResult audit_maps(int node_id) const;

    // (2) CVE-2014-8989 setgroups gate for gid_map writes.
    AuditResult audit_setgroups(int node_id) const;

    // (3a) Static credential invariant checks plus unprivileged-tenant
    // CAP_SYS_ADMIN / no_new_privs policy checks. node_id is the namespace
    // in which the credentials are interpreted.
    AuditResult audit_credentials(int node_id,
                                  const ProcessCredentials& cred) const;

    // (3b) Audit of a process entering (already inside) child node_id.
    // Credentials are interpreted in the child namespace; the unprivileged
    // policy of the target namespace and every ancestor is enforced.
    AuditResult audit_enter_namespace(int node_id,
                                      const ProcessCredentials& cred) const;

    // (3c) Audit of an execve transition. parent_cred is the pre-exec
    // credential state in node_id; result is the observed post-exec state.
    // When file_caps is null the executable carries no file capabilities.
    AuditResult audit_execve(int node_id,
                             const ProcessCredentials& parent_cred,
                             const ProcessCredentials& result,
                             const FileCapabilities* file_caps) const;

    // Runs every applicable check across the whole registered tree.
    AuditResult audit_all() const;

private:
    AuditResult audit_one_map(int node_id, bool is_uid) const;
    AuditResult analyze_map(int node_id, bool is_uid) const;
    bool parent_covers(int parent_id, bool is_uid, const IdRange& r) const;
    bool is_unprivileged_tenant(int node_id) const;
    std::vector<int> ancestor_chain(int node_id) const;

    SecurityPolicy policy_;
    std::vector<UserNsNode> nodes_;
};

// True when [a.first, a.last) and [b.first, b.last) share any ID.
bool ranges_intersect(const IdRange& a, const IdRange& b);

// True when inner is fully contained in (or equal to) outer.
bool range_contains(const IdRange& outer, const IdRange& inner);

// True when every bit set in subset is also set in superset.
bool cap_subset(CapMask subset, CapMask superset);

// Human-readable name of a violation code (for logging/tests).
const char* violation_name(ViolationCode code);

}  // namespace userns_sec

#endif  // USERNS_SECURITY_HPP

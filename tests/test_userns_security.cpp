// SPDX-License-Identifier: MIT
//
// Test suite for the user-namespace / capability security audit library.
// Zero-dependency: a tiny assertion harness lives in this file.

#include "userns_security.hpp"

#include <cstdio>
#include <string>
#include <vector>

using namespace userns_sec;

namespace {

int g_checks = 0;
int g_failures = 0;
std::string g_current_test;

void check(bool condition, const char* expr, const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::printf("  FAIL [%s] %s (%s:%d)\n", g_current_test.c_str(),
                    expr, file, line);
    }
}

#define CHECK(cond) check((cond), #cond, __FILE__, __LINE__)

bool has_code(const AuditResult& r, ViolationCode code) {
    for (const Violation& v : r.violations) {
        if (v.code == code) {
            return true;
        }
    }
    return false;
}

void run(const char* name, void (*fn)()) {
    g_current_test = name;
    std::printf("[ RUN  ] %s\n", name);
    fn();
    std::printf("[ DONE ] %s\n", name);
}

IdMapEntry me(Id ns_id, Id lower_id, Id count) {
    return IdMapEntry{ns_id, lower_id, count};
}

// A clean, policy-compliant unprivileged three-level namespace tree:
//   init(0) -> A(1, unpriv) -> B(2, unpriv)
// Every tenant ID lands on host IDs >= 10000, setgroups is denied.
struct CleanTree {
    UserNsAuditor auditor;
    int a = -1;
    int b = -1;

    CleanTree() {
        // A maps its ns ids 0..999 onto host 10000..10999; B maps its ns
        // ids 0..499 onto A ids 500..999 (host 10500..10999).
        a = auditor.add_namespace(
            0, true, SetgroupsState::Deny,
            {me(0, 10000, 1000)}, {me(0, 10000, 1000)});
        b = auditor.add_namespace(
            a, true, SetgroupsState::Deny,
            {me(0, 500, 500)}, {me(0, 500, 500)});
    }
};

ProcessCredentials safe_creds() {
    ProcessCredentials c;
    c.cap_bnd = cap_bit(CAP_NET_RAW) | cap_bit(CAP_SETUID);
    c.cap_prm = cap_bit(CAP_NET_RAW);
    c.cap_eff = cap_bit(CAP_NET_RAW);
    c.cap_inh = cap_bit(CAP_NET_RAW);
    c.cap_amb = cap_bit(CAP_NET_RAW);
    c.cap_eff_in_init = 0;
    c.no_new_privs = true;
    return c;
}

void test_clean_tree_passes() {
    CleanTree t;
    CHECK(t.auditor.audit_all().ok());
    CHECK(t.auditor.audit_maps(t.a).ok());
    CHECK(t.auditor.audit_maps(t.b).ok());
    CHECK(t.auditor.audit_setgroups(t.a).ok());
    CHECK(t.auditor.audit_credentials(t.b, safe_creds()).ok());
    CHECK(t.auditor.audit_enter_namespace(t.b, safe_creds()).ok());
}

void test_translation_along_ancestor_chain() {
    CleanTree t;
    Id out = 0;
    // B ns id 0 -> A id 500 -> host id 10500.
    CHECK(t.auditor.translate_to_parent(t.b, true, 0, out));
    CHECK(out == 500);
    CHECK(t.auditor.translate_to_init(t.b, true, 0, out));
    CHECK(out == 10500);
    // B ns id 499 -> A 999 -> host 10999.
    CHECK(t.auditor.translate_to_init(t.b, true, 499, out));
    CHECK(out == 10999);
    // Outside B's mapped window.
    CHECK(!t.auditor.translate_to_init(t.b, true, 500, out));
    // init node translation is identity.
    CHECK(t.auditor.translate_to_init(0, true, 42, out));
    CHECK(out == 42);
}

void test_32bit_wraparound_rejected() {
    UserNsAuditor auditor;
    // Upper range wraps past UINT32_MAX.
    int n1 = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0xFFFFFFF0U, 10000, 32)}, {});
    AuditResult r1 = auditor.audit_maps(n1);
    CHECK(has_code(r1, ViolationCode::Map32BitWrapAround));

    // Lower range wraps past UINT32_MAX.
    int n2 = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0, 0xFFFFFFF0U, 32)}, {});
    AuditResult r2 = auditor.audit_maps(n2);
    CHECK(has_code(r2, ViolationCode::Map32BitWrapAround));

    // Range ending exactly at UINT32_MAX+1 is representable and must not
    // be flagged as wrap-around (host 0 protection still applies elsewhere).
    int n3 = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0xFFFFFFF0U, 20000, 16)}, {});
    AuditResult r3 = auditor.audit_maps(n3);
    CHECK(!has_code(r3, ViolationCode::Map32BitWrapAround));
}

void test_zero_count_rejected() {
    UserNsAuditor auditor;
    int n = auditor.add_namespace(0, true, SetgroupsState::Deny,
                                  {me(0, 10000, 0)}, {});
    CHECK(has_code(auditor.audit_maps(n), ViolationCode::MapZeroCount));
}

void test_upper_range_overlap_rejected() {
    UserNsAuditor auditor;
    int n = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0, 10000, 100), me(50, 20000, 100)}, {});
    CHECK(has_code(auditor.audit_maps(n),
                   ViolationCode::MapUpperOverlap));
}

void test_lower_range_overlap_rejected() {
    UserNsAuditor auditor;
    int n = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0, 10000, 100), me(200, 10050, 100)}, {});
    AuditResult r = auditor.audit_maps(n);
    CHECK(has_code(r, ViolationCode::MapLowerOverlap));
    CHECK(!has_code(r, ViolationCode::MapUpperOverlap));
}

void test_lower_range_outside_parent_rejected() {
    CleanTree t;
    // A only maps its own ns ids 0..999; lower_id 5000 is unmapped in A.
    int c = t.auditor.add_namespace(
        t.a, true, SetgroupsState::Deny,
        {me(0, 5000, 10)}, {});
    CHECK(has_code(t.auditor.audit_maps(c),
                   ViolationCode::MapLowerOutsideParent));
    // A valid sibling mapping for contrast.
    int d = t.auditor.add_namespace(
        t.a, true, SetgroupsState::Deny,
        {me(0, 900, 10)}, {});
    CHECK(t.auditor.audit_maps(d).ok());
}

void test_direct_host_root_mapping_rejected() {
    UserNsAuditor auditor;
    int n = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0, 0, 1)}, {me(0, 0, 1)});
    AuditResult r = auditor.audit_maps(n);
    CHECK(has_code(r, ViolationCode::MapProtectedHostId));
}

void test_protected_system_range_rejected() {
    UserNsAuditor auditor;
    // Host UID 500 is inside the default protected [0,1000) range.
    int n = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0, 500, 10)}, {});
    CHECK(has_code(auditor.audit_maps(n),
                   ViolationCode::MapProtectedHostId));
}

void test_indirect_host_root_mapping_rejected() {
    // Policy protects only host ID 0, so A's mapping below is locally
    // legal; the two-hop translation through A is what exposes host 0.
    SecurityPolicy policy;
    policy.protected_uid_ranges = {IdRange(0, 1)};
    policy.protected_gid_ranges = {IdRange(0, 1)};
    UserNsAuditor auditor(policy);

    // A: its ns id 7000 maps onto host 0. B never mentions host 0 itself.
    int a = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(7000, 0, 5)}, {});
    int b = auditor.add_namespace(
        a, true, SetgroupsState::Deny,
        {me(0, 7000, 5)}, {});

    // B's local lower_id 7000 looks harmless, but translating B ns 0
    // through A reaches host UID 0.
    Id host = 999999;
    CHECK(auditor.translate_to_init(b, true, 0, host));
    CHECK(host == 0);
    AuditResult rb = auditor.audit_maps(b);
    CHECK(has_code(rb, ViolationCode::MapProtectedHostId));
    // The stepping-stone node A must be flagged as well.
    CHECK(has_code(auditor.audit_maps(a),
                   ViolationCode::MapProtectedHostId));
}

void test_setgroups_cve_2014_8989() {
    UserNsAuditor auditor;

    // Unprivileged creator + gid_map + setgroups=allow => vulnerability.
    int bad = auditor.add_namespace(
        0, true, SetgroupsState::Allow,
        {me(0, 10000, 10)}, {me(0, 10000, 10)});
    CHECK(has_code(auditor.audit_setgroups(bad),
                   ViolationCode::SetgroupsRequired));

    // Same configuration with setgroups=deny is the kernel-blessed fix.
    int good = auditor.add_namespace(
        0, true, SetgroupsState::Deny,
        {me(0, 10000, 10)}, {me(0, 10000, 10)});
    CHECK(auditor.audit_setgroups(good).ok());

    // A privileged creator may keep setgroups=allow.
    int priv = auditor.add_namespace(
        0, false, SetgroupsState::Allow,
        {me(0, 10000, 10)}, {me(0, 10000, 10)});
    CHECK(auditor.audit_setgroups(priv).ok());

    // Empty gid_map means no write happened yet: nothing to gate.
    int empty = auditor.add_namespace(
        0, true, SetgroupsState::Allow, {}, {});
    CHECK(auditor.audit_setgroups(empty).ok());
}

void test_capability_invariants() {
    CleanTree t;

    ProcessCredentials c = safe_creds();
    CHECK(t.auditor.audit_credentials(t.b, c).ok());

    ProcessCredentials eff = c;
    eff.cap_eff = cap_bit(CAP_SYS_ADMIN);  // not in CapPrm
    CHECK(has_code(t.auditor.audit_credentials(t.b, eff),
                   ViolationCode::CapEffNotSubsetPrm));

    ProcessCredentials prm = c;
    prm.cap_prm = cap_bit(CAP_SYS_ADMIN);  // outside CapBnd
    CHECK(has_code(t.auditor.audit_credentials(t.b, prm),
                   ViolationCode::CapPrmNotSubsetBnd));

    ProcessCredentials amb1 = c;
    amb1.cap_amb = cap_bit(CAP_NET_ADMIN);  // not in CapPrm
    CHECK(has_code(t.auditor.audit_credentials(t.b, amb1),
                   ViolationCode::CapAmbNotSubsetPrm));

    ProcessCredentials amb2 = c;
    amb2.cap_inh = 0;  // ambient no longer subset of inheritable
    CHECK(has_code(t.auditor.audit_credentials(t.b, amb2),
                   ViolationCode::CapAmbNotSubsetInh));

    ProcessCredentials badbit = c;
    badbit.cap_bnd |= static_cast<CapMask>(1) << 63;
    CHECK(has_code(t.auditor.audit_credentials(t.b, badbit),
                   ViolationCode::CapInvalidBit));
}

void test_unprivileged_tenant_policy() {
    CleanTree t;

    // CAP_SYS_ADMIN retained in init_user_ns by an unprivileged tenant.
    ProcessCredentials admin = safe_creds();
    admin.cap_eff_in_init = cap_bit(CAP_SYS_ADMIN);
    CHECK(has_code(t.auditor.audit_credentials(t.b, admin),
                   ViolationCode::CapSysAdminInInitNs));

    // Missing PR_SET_NO_NEW_PRIVS leaves SUID escalation open.
    ProcessCredentials nonnp = safe_creds();
    nonnp.no_new_privs = false;
    CHECK(has_code(t.auditor.audit_credentials(t.b, nonnp),
                   ViolationCode::MissingNoNewPrivs));

    // An unprivileged ancestor taints a "privileged" nested creation too.
    int c = t.auditor.add_namespace(
        t.b, false, SetgroupsState::Deny,
        {me(0, 100, 10)}, {me(0, 100, 10)});
    CHECK(has_code(t.auditor.audit_credentials(c, nonnp),
                   ViolationCode::MissingNoNewPrivs));

    // A genuinely privileged branch under init is not tented.
    UserNsAuditor a2;
    int priv = a2.add_namespace(
        0, false, SetgroupsState::Allow,
        {me(0, 10000, 100)}, {me(0, 10000, 100)});
    ProcessCredentials host_cred = safe_creds();
    host_cred.no_new_privs = false;
    host_cred.cap_eff_in_init = cap_bit(CAP_SYS_ADMIN);
    CHECK(a2.audit_credentials(priv, host_cred).ok());
}

void test_execve_without_file_caps() {
    CleanTree t;
    ProcessCredentials parent = safe_creds();

    // Legit execve: ambient NET_RAW survives as prm/eff, bnd/inh stay.
    ProcessCredentials good = parent;
    good.cap_prm = cap_bit(CAP_NET_RAW);
    good.cap_eff = cap_bit(CAP_NET_RAW);
    CHECK(t.auditor.audit_execve(t.b, parent, good, nullptr).ok());

    // Executed binary gains a non-ambient permitted cap: privilege
    // escalation (exactly what no_new_privs blocks for SUID too).
    ProcessCredentials gained = parent;
    gained.cap_prm = cap_bit(CAP_NET_RAW) | cap_bit(CAP_SETUID);
    gained.cap_eff = gained.cap_prm;
    AuditResult rg =
        t.auditor.audit_execve(t.b, parent, gained, nullptr);
    CHECK(has_code(rg, ViolationCode::ExecResultMismatch));

    // Ambient cap outside the bounding set cannot survive execve.
    ProcessCredentials bad_amb = parent;
    bad_amb.cap_bnd = 0;
    bad_amb.cap_amb = cap_bit(CAP_NET_RAW);
    bad_amb.cap_prm = cap_bit(CAP_NET_RAW);
    bad_amb.cap_eff = cap_bit(CAP_NET_RAW);
    AuditResult ra =
        t.auditor.audit_execve(t.b, parent, bad_amb, nullptr);
    CHECK(has_code(ra, ViolationCode::ExecAmbNotSubsetBnd));
}

void test_execve_with_file_caps() {
    CleanTree t;
    ProcessCredentials parent = safe_creds();
    // The bounding set already contains the capability the file may raise.
    parent.cap_bnd = cap_bit(CAP_NET_RAW) | cap_bit(CAP_SETUID) |
                     cap_bit(CAP_NET_BIND_SERVICE);

    // Binary carries permitted NET_BIND_SERVICE inside CapBnd; no
    // inheritable/ambient contribution.
    FileCapabilities fc;
    fc.permitted = cap_bit(CAP_NET_BIND_SERVICE);
    fc.inheritable = 0;
    fc.effective = true;

    ProcessCredentials ok = parent;
    ok.cap_prm = cap_bit(CAP_NET_BIND_SERVICE) | cap_bit(CAP_NET_RAW);
    ok.cap_eff = ok.cap_prm;
    CHECK(t.auditor.audit_execve(t.b, parent, ok, &fc).ok());

    // File cap outside the process bounding set must be dropped/refused.
    FileCapabilities fc_bad = fc;
    fc_bad.permitted = cap_bit(CAP_SYS_ADMIN);
    ProcessCredentials bad = parent;
    bad.cap_prm = cap_bit(CAP_SYS_ADMIN);
    bad.cap_eff = cap_bit(CAP_SYS_ADMIN);
    AuditResult r =
        t.auditor.audit_execve(t.b, parent, bad, &fc_bad);
    CHECK(has_code(r, ViolationCode::ExecFileCapNotSubsetBnd));
}

void test_map_entry_limit() {
    UserNsAuditor auditor;
    std::vector<IdMapEntry> entries;
    // 341 non-overlapping entries mapping host IDs 10000+.
    for (std::uint32_t i = 0; i < 341U; ++i) {
        entries.push_back(me(i * 2U, 10000U + i * 2U, 1));
    }
    int n = auditor.add_namespace(0, true, SetgroupsState::Deny,
                                  entries, {});
    AuditResult r = auditor.audit_maps(n);
    CHECK(has_code(r, ViolationCode::MapTooManyEntries));

    // Exactly 340 entries is the accepted ceiling.
    entries.pop_back();
    UserNsAuditor auditor2;
    int n2 = auditor2.add_namespace(0, true, SetgroupsState::Deny,
                                    entries, {});
    CHECK(auditor2.audit_maps(n2).ok());
}

void test_gid_map_protected_and_aggregation() {
    UserNsAuditor auditor;
    int bad = auditor.add_namespace(
        0, true, SetgroupsState::Allow,
        {me(0, 10000, 10)}, {me(0, 0, 10)});
    (void)bad;
    AuditResult all = auditor.audit_all();
    CHECK(has_code(all, ViolationCode::MapProtectedHostId));
    CHECK(has_code(all, ViolationCode::SetgroupsRequired));
    CHECK(all.violations.size() >= 2U);
}

}  // namespace

int main() {
    run("clean_tree_passes", test_clean_tree_passes);
    run("translation_along_ancestor_chain",
        test_translation_along_ancestor_chain);
    run("32bit_wraparound_rejected", test_32bit_wraparound_rejected);
    run("zero_count_rejected", test_zero_count_rejected);
    run("upper_range_overlap_rejected", test_upper_range_overlap_rejected);
    run("lower_range_overlap_rejected", test_lower_range_overlap_rejected);
    run("lower_range_outside_parent_rejected",
        test_lower_range_outside_parent_rejected);
    run("direct_host_root_mapping_rejected",
        test_direct_host_root_mapping_rejected);
    run("protected_system_range_rejected",
        test_protected_system_range_rejected);
    run("indirect_host_root_mapping_rejected",
        test_indirect_host_root_mapping_rejected);
    run("setgroups_cve_2014_8989", test_setgroups_cve_2014_8989);
    run("capability_invariants", test_capability_invariants);
    run("unprivileged_tenant_policy", test_unprivileged_tenant_policy);
    run("execve_without_file_caps", test_execve_without_file_caps);
    run("execve_with_file_caps", test_execve_with_file_caps);
    run("map_entry_limit", test_map_entry_limit);
    run("gid_map_protected_and_aggregation",
        test_gid_map_protected_and_aggregation);

    std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}

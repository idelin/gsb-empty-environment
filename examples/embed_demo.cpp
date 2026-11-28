// Minimal embedding example: audit one unprivileged container namespace and
// its process credentials. Build with the library sources on the include path.

#include <cstdio>

#include "userns_security.hpp"

int main() {
    using namespace userns_sec;

    UserNsAuditor auditor;

    // Unprivileged tenant: container root (ns uid 0) maps to host uid 10000,
    // setgroups denied before writing gid_map (CVE-2014-8989 mitigation).
    const int ns = auditor.add_namespace(
        /*parent=*/0,
        /*created_unprivileged=*/true,
        SetgroupsState::Deny,
        /*uid_map=*/{IdMapEntry{0, 10000, 1000}},
        /*gid_map=*/{IdMapEntry{0, 10000, 1000}});

    AuditResult map_result = auditor.audit_maps(ns);
    if (!map_result) {
        for (const Violation& v : map_result.violations) {
            std::printf("map violation: %s: %s\n",
                        violation_name(v.code), v.detail.c_str());
        }
        return 1;
    }

    // Process inside the namespace: bounded ambient NET_RAW only, no host
    // CAP_SYS_ADMIN, no_new_privs set.
    ProcessCredentials cred;
    cred.cap_bnd = cap_bit(CAP_NET_RAW);
    cred.cap_prm = cap_bit(CAP_NET_RAW);
    cred.cap_eff = cap_bit(CAP_NET_RAW);
    cred.cap_inh = cap_bit(CAP_NET_RAW);
    cred.cap_amb = cap_bit(CAP_NET_RAW);
    cred.cap_eff_in_init = 0;
    cred.no_new_privs = true;

    AuditResult cred_result = auditor.audit_enter_namespace(ns, cred);
    if (cred_result) {
        std::printf("namespace %d accepted: no host-root mapping, "
                    "capabilities and no_new_privs valid\n", ns);
        return 0;
    }
    for (const Violation& v : cred_result.violations) {
        std::printf("credential violation: %s: %s\n",
                    violation_name(v.code), v.detail.c_str());
    }
    return 1;
}

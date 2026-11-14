#include "netsec.hpp"

#include <cstdio>
#include <string>

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool cond, const std::string& name) {
    ++g_checks;
    if (!cond) {
        ++g_failures;
        std::fprintf(stderr, "FAIL: %s\n", name.c_str());
    }
}

using namespace netsec;

struct Fixture {
    UserNamespaceRegistry registry;
    AuditLog audit;
    UserNsId root;
    UserNsId child;
    UserNsId grandchild;
    UserNsId sibling;

    Fixture() {
        root = registry.create_root(0);
        child = *registry.create_child(root, 1000);
        grandchild = *registry.create_child(child, 1000);
        sibling = *registry.create_child(root, 1000);
    }

    ProcessDescriptor make_proc(Pid pid, Uid uid, UserNsId ns) {
        ProcessDescriptor p;
        p.pid = pid;
        p.real_uid = uid;
        p.effective_uid = uid;
        p.user_ns = ns;
        registry.assign(pid, ns);
        return p;
    }
};

// 1. Cross-namespace tracing: privileges created inside a sandboxed user
//    namespace must not reach ancestor or sibling namespace processes.
void test_cross_namespace() {
    Fixture f;
    TracePermissionEvaluator ev(f.registry, TracePolicy{}, &f.audit);

    auto host = f.make_proc(100, 1000, f.root);
    auto attacker = f.make_proc(200, 1000, f.child);
    // Full caps inside the child namespace (the sandbox "root").
    attacker.caps_by_ns[f.child] = kCapSysPtrace;
    attacker.caps_by_ns[f.grandchild] = kCapSysPtrace;

    auto d1 = ev.evaluate(attacker, host);
    check(!d1.allowed && d1.verdict == TraceVerdict::kDenyNamespaceIsolation,
          "cross-ns: child-ns caps must not trace ancestor-ns process");

    auto host_sibling = f.make_proc(300, 1000, f.sibling);
    auto d2 = ev.evaluate(attacker, host_sibling);
    check(!d2.allowed && d2.verdict == TraceVerdict::kDenyNamespaceIsolation,
          "cross-ns: child-ns caps must not trace sibling-ns process");

    // Same uid in both namespaces still denied: uid path is same-ns only.
    ProcessDescriptor bare = f.make_proc(201, 1000, f.child);
    auto d3 = ev.evaluate(bare, host);
    check(!d3.allowed && d3.verdict == TraceVerdict::kDenyNamespaceIsolation,
          "cross-ns: matching uid without capability still denied");

    // Downward direction stays legal: a capability held in the ancestor
    // namespace applies to descendant-namespace processes.
    auto privileged_host = f.make_proc(101, 0, f.root);
    privileged_host.caps_by_ns[f.root] = kCapSysPtrace;
    auto sandbox_proc = f.make_proc(202, 1000, f.grandchild);
    auto d4 = ev.evaluate(privileged_host, sandbox_proc);
    check(d4.allowed && d4.verdict == TraceVerdict::kAllowCapability,
          "cross-ns: ancestor-ns capability may trace descendant-ns process");
}

// 2. Forged relationships: unknown namespace ids and membership claims that
//    contradict the registry must be rejected, never fail-open.
void test_relationship_forgery() {
    Fixture f;
    TracePermissionEvaluator ev(f.registry, TracePolicy{}, &f.audit);

    check(!f.registry.create_child(9999, 0).has_value(),
          "forgery: cannot create child of unknown namespace");

    auto host = f.make_proc(100, 1000, f.root);

    ProcessDescriptor ghost;
    ghost.pid = 400;
    ghost.real_uid = 1000;
    ghost.user_ns = 4242; // not in registry
    ghost.caps_by_ns[4242] = kCapSysPtrace;
    auto d1 = ev.evaluate(ghost, host);
    check(!d1.allowed && d1.verdict == TraceVerdict::kDenyUnknownNamespace,
          "forgery: unknown namespace id denied");

    // Attacker is registered in the child namespace but claims the root
    // namespace (and a root-ns capability) in its descriptor.
    ProcessDescriptor liar;
    liar.pid = 401;
    liar.real_uid = 1000;
    liar.user_ns = f.root;
    liar.caps_by_ns[f.root] = kCapSysPtrace;
    f.registry.assign(401, f.child);
    auto d2 = ev.evaluate(liar, host);
    check(!d2.allowed && d2.verdict == TraceVerdict::kDenyMembershipForgery,
          "forgery: descriptor contradicting registry membership denied");
}

// 3. Dumpable transitions are re-evaluated on every check.
void test_dumpable_change() {
    Fixture f;
    TracePermissionEvaluator ev(f.registry, TracePolicy{}, &f.audit);

    auto tracer = f.make_proc(500, 1000, f.child);
    auto target = f.make_proc(501, 1000, f.child);

    check(ev.evaluate(tracer, target).allowed,
          "dumpable: same-ns same-uid dumpable target allowed");

    target.dumpable = false; // e.g. target exec'd a setuid binary
    auto d = ev.evaluate(tracer, target);
    check(!d.allowed && d.verdict == TraceVerdict::kDenyNotDumpable,
          "dumpable: loss of dumpable revokes uid-based tracing");

    tracer.caps_by_ns[f.child] = kCapSysPtrace;
    check(ev.evaluate(tracer, target).allowed,
          "dumpable: same-ns capability overrides non-dumpable target");
}

// 4. Legitimate same-namespace tracing keeps working.
void test_same_namespace_legit() {
    Fixture f;
    TracePermissionEvaluator ev(f.registry, TracePolicy{}, &f.audit);

    auto tracer = f.make_proc(600, 1000, f.child);
    auto target = f.make_proc(601, 1000, f.child);

    auto d1 = ev.evaluate(tracer, target);
    check(d1.allowed && d1.verdict == TraceVerdict::kAllowUidMatch,
          "same-ns: uid match on dumpable target allowed");

    auto d2 = ev.evaluate(tracer, tracer);
    check(d2.allowed && d2.verdict == TraceVerdict::kAllowSelf,
          "same-ns: self trace allowed");

    auto other_uid = f.make_proc(602, 1001, f.child);
    auto d3 = ev.evaluate(tracer, other_uid);
    check(!d3.allowed && d3.verdict == TraceVerdict::kDenyUidMismatch,
          "same-ns: different uid without capability denied");
}

// 5. Policy tightening: no_new_privs re-check and Yama-style scope evaluated
//    on the global task tree; audit chain stays verifiable and tamper-evident.
void test_policy_tightening() {
    Fixture f;

    TracePolicy strict;
    strict.require_capability_for_no_new_privs = true;
    strict.yama_scope = 1;
    TracePermissionEvaluator ev(f.registry, strict, &f.audit);

    auto tracer = f.make_proc(700, 1000, f.child);
    auto target = f.make_proc(701, 1000, f.child);
    target.no_new_privs = true;
    target.task_ancestors = {900, 901}; // tracer is not an ancestor

    auto d1 = ev.evaluate(tracer, target);
    check(!d1.allowed && d1.verdict == TraceVerdict::kDenyYamaScope,
          "tightening: non-ancestor tracer denied under restricted scope");

    target.task_ancestors = {700, 900}; // tracer is the parent
    auto d2 = ev.evaluate(tracer, target);
    check(!d2.allowed && d2.verdict == TraceVerdict::kDenyNoNewPrivs,
          "tightening: no_new_privs target requires capability");

    tracer.caps_by_ns[f.child] = kCapSysPtrace;
    check(ev.evaluate(tracer, target).allowed,
          "tightening: capability path still satisfies strict policy");

    // Tightened policy must not open cross-namespace holes either.
    auto host = f.make_proc(100, 1000, f.root);
    ProcessDescriptor escapee = f.make_proc(702, 1000, f.grandchild);
    escapee.caps_by_ns[f.grandchild] = kCapSysPtrace;
    escapee.task_ancestors = {100};
    auto d4 = ev.evaluate(escapee, host);
    check(!d4.allowed && d4.verdict == TraceVerdict::kDenyNamespaceIsolation,
          "tightening: strict policy still blocks descendant-to-ancestor trace");

    check(f.audit.verify_chain(), "audit: hash chain verifies");
    check(!f.audit.records().empty(), "audit: decisions were recorded");
    check(!f.audit.records().back().ns_chain.empty(),
          "audit: namespace call chain recorded on each decision");
    check(f.audit.records().back().ns_chain.front() == f.root,
          "audit: call chain starts at target namespace");
}

// Audit tamper evidence, using the log produced by legitimate traffic.
void test_audit_tamper_detection() {
    Fixture f;
    TracePermissionEvaluator ev(f.registry, TracePolicy{}, &f.audit);
    auto a = f.make_proc(800, 1000, f.child);
    auto b = f.make_proc(801, 1000, f.child);
    ev.evaluate(a, b);
    ev.evaluate(b, a);
    check(f.audit.verify_chain(), "audit: clean log verifies");

    AuditLog tampered = f.audit;
    const_cast<AuditRecord&>(tampered.records().front()).allowed = false;
    check(!tampered.verify_chain(), "audit: tampered record detected");
}

} // namespace

int main() {
    test_cross_namespace();
    test_relationship_forgery();
    test_dumpable_change();
    test_same_namespace_legit();
    test_policy_tightening();
    test_audit_tamper_detection();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}

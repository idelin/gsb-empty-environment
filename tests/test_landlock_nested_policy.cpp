#include "landlock_nested_policy.hpp"

#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char *expression, int line) {
    if (condition) {
        return;
    }
    std::cerr << "check failed at line " << line << ": " << expression
              << '\n';
    ++failures;
}

#define CHECK(expression) check((expression), #expression, __LINE__)

const landlock::ReparentIssue *
find_issue(const landlock::ReparentDecision &decision,
           landlock::ReparentIssueKind kind) {
    for (const landlock::ReparentIssue &issue : decision.issues) {
        if (issue.kind == kind) {
            return &issue;
        }
    }
    return nullptr;
}

void test_paths_and_tree() {
    landlock::DirectoryTree tree;
    tree.add_file("/data/file.txt");
    CHECK(tree.is_directory("/data"));
    CHECK(tree.exists("/data/file.txt"));
    CHECK(tree.descendants("/data", false).size() == 1u);
    CHECK(landlock::normalize_path("//data///file.txt/") ==
          "/data/file.txt");
    CHECK(landlock::path_parent("/data/file.txt") == "/data");
    CHECK(landlock::path_basename("/data/file.txt") == "file.txt");
    CHECK(landlock::path_is_ancestor("/data", "/data/file.txt"));
    CHECK(!landlock::path_is_ancestor("/data", "/database"));
    CHECK(landlock::path_is_within("/data/file.txt", "/data"));

    bool threw = false;
    try {
        static_cast<void>(landlock::normalize_path("relative/path"));
    } catch (const landlock::PolicyError &) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        static_cast<void>(landlock::normalize_path("/a/../b"));
    } catch (const landlock::PolicyError &) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        tree.add_file("/data/file.txt/child");
    } catch (const landlock::PolicyError &) {
        threw = true;
    }
    CHECK(threw);
}

void test_multilayer_intersection() {
    landlock::DirectoryTree tree;
    tree.add_file("/work/inside/file.txt");
    tree.add_file("/work/outside/file.txt");
    tree.add_file("/readonly/file.txt");
    tree.add_file("/secret/file.txt");

    landlock::PolicyLayer parent;
    parent.name = "parent";
    parent.abi_version = 4u;
    parent.handled_access_fs = landlock::all_fs_access();
    parent.allow_fs("/", landlock::FsAccess::Execute |
                            landlock::FsAccess::ReadFile |
                            landlock::FsAccess::ReadDir);
    parent.allow_fs("/work", landlock::FsAccess::Execute |
                                  landlock::FsAccess::ReadFile |
                                  landlock::FsAccess::ReadDir |
                                  landlock::FsAccess::WriteFile |
                                  landlock::FsAccess::MakeRegular |
                                  landlock::FsAccess::RemoveFile);
    parent.allow_fs("/readonly", landlock::FsAccess::ReadFile);
    parent.allow_fs("/secret", landlock::FsAccess::ReadFile);

    landlock::PolicyLayer child;
    child.name = "child";
    child.abi_version = 4u;
    child.handled_access_fs =
        landlock::FsAccess::ReadFile | landlock::FsAccess::WriteFile |
        landlock::FsAccess::ReadDir | landlock::FsAccess::MakeRegular |
        landlock::FsAccess::RemoveFile;
    child.allow_fs("/work/inside",
                   landlock::FsAccess::Execute | landlock::FsAccess::ReadFile |
                       landlock::FsAccess::ReadDir |
                       landlock::FsAccess::WriteFile |
                       landlock::FsAccess::MakeRegular |
                       landlock::FsAccess::RemoveFile);
    child.allow_fs("/readonly", landlock::FsAccess::ReadFile |
                                    landlock::FsAccess::WriteFile);

    landlock::PolicyStack policy;
    policy.add(parent);
    policy.add(child);
    landlock::PolicyEvaluator evaluator(policy, tree);

    const landlock::AccessDecision inside_read = evaluator.evaluate_fs(
        "/work/inside/file.txt", landlock::FsAccess::ReadFile);
    CHECK(inside_read.allowed);

    const landlock::AccessDecision inside_write = evaluator.evaluate_fs(
        "/work/inside/file.txt", landlock::FsAccess::WriteFile);
    CHECK(inside_write.allowed);

    const landlock::AccessDecision outside_write = evaluator.evaluate_fs(
        "/work/outside/file.txt", landlock::FsAccess::WriteFile);
    CHECK(!outside_write.allowed);
    CHECK(landlock::has_fs(outside_write.denied,
                           landlock::FsAccess::WriteFile));
    CHECK(outside_write.layers.size() == 2u);
    CHECK(!outside_write.layers[1].permits);

    const landlock::AccessDecision readonly_write = evaluator.evaluate_fs(
        "/readonly/file.txt", landlock::FsAccess::WriteFile);
    CHECK(!readonly_write.allowed);
    CHECK(!readonly_write.layers[0].permits);
    CHECK(readonly_write.layers[1].permits);

    CHECK(!evaluator.allows("/secret/file.txt",
                            landlock::FsAccess::ReadFile));
    CHECK(!evaluator.evaluate_fs("/secret/file.txt",
                                 landlock::FsAccess::ReadFile)
               .layers[1]
               .permits);
    CHECK(!evaluator.allows("/secret/file.txt",
                            landlock::FsAccess::WriteFile));
}

void test_unhandled_permissions_default_allow() {
    landlock::DirectoryTree tree;
    tree.add_file("/sandbox/file.txt");
    tree.add_file("/secret/file.txt");

    landlock::PolicyLayer read_layer;
    read_layer.name = "read-only-layer";
    read_layer.handled_access_fs = landlock::FsAccess::ReadFile;
    read_layer.allow_fs("/", landlock::FsAccess::ReadFile);

    landlock::PolicyLayer write_layer;
    write_layer.name = "sandbox-write-layer";
    write_layer.handled_access_fs = landlock::FsAccess::WriteFile;
    write_layer.allow_fs("/sandbox", landlock::FsAccess::WriteFile);

    landlock::PolicyStack policy;
    policy.add(read_layer);
    policy.add(write_layer);
    landlock::PolicyEvaluator evaluator(policy, tree);

    const landlock::AccessDecision sandbox_write = evaluator.evaluate_fs(
        "/sandbox/file.txt", landlock::FsAccess::WriteFile);
    CHECK(sandbox_write.allowed);
    CHECK(!sandbox_write.layers[0].handles_request);
    CHECK(sandbox_write.layers[1].handles_request);

    CHECK(!evaluator.allows("/secret/file.txt",
                            landlock::FsAccess::WriteFile));
    CHECK(evaluator.allows("/secret/file.txt",
                           landlock::FsAccess::ReadFile));
    CHECK(evaluator.allows("/sandbox/file.txt",
                           landlock::FsAccess::ReadFile));

    CHECK(landlock::has_fs(
        evaluator.effective_fs("/sandbox/file.txt"),
        landlock::FsAccess::WriteFile));
    CHECK(!landlock::has_fs(
        evaluator.effective_fs("/secret/file.txt"),
        landlock::FsAccess::WriteFile));
}

void test_tcp_port_narrowing() {
    landlock::DirectoryTree tree;

    landlock::PolicyLayer parent;
    parent.name = "tcp-parent";
    parent.abi_version = 4u;
    parent.handled_access_net =
        landlock::NetAccess::BindTcp | landlock::NetAccess::ConnectTcp;
    parent.add_tcp_rule(4000u, 5000u, landlock::NetAccess::BindTcp);
    parent.add_tcp_rule(8000u, 8080u, landlock::NetAccess::ConnectTcp);

    landlock::PolicyLayer child;
    child.name = "tcp-connect-child";
    child.abi_version = 4u;
    child.handled_access_net = landlock::NetAccess::ConnectTcp;
    child.add_tcp_rule(8000u, 8010u, landlock::NetAccess::ConnectTcp);

    landlock::PolicyLayer widener;
    widener.name = "tcp-widener";
    widener.abi_version = 4u;
    widener.handled_access_net = landlock::NetAccess::BindTcp;
    widener.add_tcp_rule(4000u, 5001u, landlock::NetAccess::BindTcp);

    landlock::PolicyStack policy;
    policy.add(parent);
    policy.add(child);
    policy.add(widener);
    landlock::PolicyEvaluator evaluator(policy, tree);

    CHECK(evaluator.can_connect(8005u));
    CHECK(!evaluator.can_connect(8050u));
    CHECK(!evaluator.can_connect(9000u));
    CHECK(evaluator.can_bind(4500u));
    CHECK(!evaluator.can_bind(5001u));
    CHECK(!evaluator.can_bind(9000u));

    const landlock::TcpDecision narrowed =
        evaluator.check_tcp(8050u, landlock::NetAccess::ConnectTcp);
    CHECK(!narrowed.allowed);
    CHECK(!narrowed.layers[1].permits);
    CHECK(narrowed.layers[2].permits);

    landlock::PolicyLayer old_abi;
    old_abi.name = "abi-v3-network";
    old_abi.abi_version = 3u;
    old_abi.handled_access_net =
        landlock::NetAccess::BindTcp | landlock::NetAccess::ConnectTcp;
    old_abi.add_tcp_rule(443u, landlock::NetAccess::ConnectTcp);
    landlock::PolicyStack old_policy;
    old_policy.add(old_abi);
    landlock::PolicyEvaluator old_evaluator(old_policy, tree);
    CHECK(old_evaluator.can_connect(443u));
    CHECK(old_evaluator.handled_net(0u) == landlock::NetAccess::None);
}

landlock::PolicyLayer make_dangerous_reparent_layer() {
    landlock::PolicyLayer layer;
    layer.name = "dangerous-reparent";
    layer.abi_version = 4u;
    layer.handled_access_fs = landlock::all_fs_access();
    layer.handled_access_net =
        landlock::NetAccess::BindTcp | landlock::NetAccess::ConnectTcp;
    layer.allow_fs("/", landlock::FsAccess::Execute |
                            landlock::FsAccess::ReadFile |
                            landlock::FsAccess::ReadDir);
    layer.allow_fs("/secret", landlock::FsAccess::Execute |
                                  landlock::FsAccess::ReadFile |
                                  landlock::FsAccess::ReadDir |
                                  landlock::FsAccess::RemoveFile |
                                  landlock::FsAccess::Refer);
    layer.allow_fs("/sandbox", landlock::FsAccess::Execute |
                                   landlock::FsAccess::ReadFile |
                                   landlock::FsAccess::ReadDir |
                                   landlock::FsAccess::WriteFile |
                                   landlock::FsAccess::MakeRegular |
                                   landlock::FsAccess::RemoveFile |
                                   landlock::FsAccess::Refer);
    return layer;
}

landlock::DirectoryTree make_dangerous_tree() {
    landlock::DirectoryTree tree;
    tree.add_file("/secret/report.txt");
    tree.add_directory("/sandbox");
    return tree;
}

landlock::PolicyLayer make_safe_reparent_layer() {
    landlock::PolicyLayer layer;
    layer.name = "safe-reparent";
    layer.abi_version = 4u;
    layer.handled_access_fs = landlock::all_fs_access();
    layer.allow_fs("/", landlock::FsAccess::Execute |
                            landlock::FsAccess::ReadFile |
                            landlock::FsAccess::ReadDir);
    layer.allow_fs("/from", landlock::FsAccess::Execute |
                                  landlock::FsAccess::ReadFile |
                                  landlock::FsAccess::ReadDir |
                                  landlock::FsAccess::WriteFile |
                                  landlock::FsAccess::MakeRegular |
                                  landlock::FsAccess::RemoveFile |
                                  landlock::FsAccess::Refer);
    layer.allow_fs("/to", landlock::FsAccess::Execute |
                                landlock::FsAccess::ReadFile |
                                landlock::FsAccess::ReadDir |
                                landlock::FsAccess::MakeRegular |
                                landlock::FsAccess::RemoveFile |
                                landlock::FsAccess::Refer);
    return layer;
}

void test_refer_reparent_checks() {
    const landlock::DirectoryTree dangerous_tree = make_dangerous_tree();
    landlock::PolicyStack dangerous_policy;
    dangerous_policy.add(make_dangerous_reparent_layer());
    landlock::PolicyEvaluator dangerous(dangerous_policy, dangerous_tree);

    const landlock::ReparentDecision rename_escape =
        dangerous.check_rename("/secret/report.txt",
                               "/sandbox/report.txt");
    CHECK(!rename_escape.allowed);
    const landlock::ReparentIssue *subset_issue = find_issue(
        rename_escape, landlock::ReparentIssueKind::DestinationHierarchyNotSubset);
    CHECK(subset_issue != nullptr);
    if (subset_issue != nullptr) {
        CHECK(subset_issue->source_path == "/secret/report.txt");
        CHECK(subset_issue->destination_path == "/sandbox/report.txt");
        CHECK(landlock::has_fs(subset_issue->privilege_gain,
                               landlock::FsAccess::WriteFile));
    }

    const landlock::ReparentDecision hard_link_escape =
        dangerous.check_hard_link("/secret/report.txt",
                                  "/sandbox/report-link.txt");
    CHECK(!hard_link_escape.allowed);
    CHECK(find_issue(hard_link_escape,
                     landlock::ReparentIssueKind::
                         DestinationHierarchyNotSubset) != nullptr);

    landlock::DirectoryTree safe_tree;
    safe_tree.add_file("/from/file.txt");
    safe_tree.add_directory("/to");
    landlock::PolicyStack safe_policy;
    safe_policy.add(make_safe_reparent_layer());
    landlock::PolicyEvaluator safe(safe_policy, safe_tree);

    const landlock::ReparentDecision safe_rename =
        safe.check_rename("/from/file.txt", "/to/file.txt");
    CHECK(safe_rename.allowed);

    const landlock::ReparentDecision safe_link =
        safe.check_hard_link("/from/file.txt", "/to/link.txt");
    CHECK(safe_link.allowed);

    landlock::PolicyLayer missing_refer = make_safe_reparent_layer();
    missing_refer.handled_access_fs =
        landlock::fs_without(missing_refer.handled_access_fs,
                             landlock::FsAccess::Refer);
    for (landlock::FsRule &rule : missing_refer.fs_rules) {
        rule.allowed_access =
            landlock::fs_without(rule.allowed_access, landlock::FsAccess::Refer);
    }
    landlock::PolicyStack missing_refer_policy;
    missing_refer_policy.add(missing_refer);
    landlock::PolicyEvaluator missing_refer_evaluator(missing_refer_policy,
                                                      safe_tree);
    const landlock::ReparentDecision missing_refer_decision =
        missing_refer_evaluator.check_rename("/from/file.txt",
                                             "/to/file.txt");
    CHECK(!missing_refer_decision.allowed);
    CHECK(find_issue(missing_refer_decision,
                     landlock::ReparentIssueKind::ReferNotHandled) != nullptr);

    landlock::PolicyStack layered;
    layered.add(make_safe_reparent_layer());
    landlock::PolicyLayer refer_gap_child;
    refer_gap_child.name = "refer-gap-child";
    refer_gap_child.handled_access_fs = landlock::FsAccess::ReadFile;
    layered.add(refer_gap_child);
    landlock::PolicyEvaluator layered_evaluator(layered, safe_tree);
    const landlock::ReparentDecision layered_decision =
        layered_evaluator.check_rename("/from/file.txt", "/to/file.txt");
    CHECK(!layered_decision.allowed);
    CHECK(find_issue(layered_decision,
                     landlock::ReparentIssueKind::ReferNotHandled) != nullptr);

    landlock::PolicyLayer missing_destination_refer =
        make_safe_reparent_layer();
    for (landlock::FsRule &rule : missing_destination_refer.fs_rules) {
        if (rule.path == "/to") {
            rule.allowed_access = landlock::fs_without(
                rule.allowed_access, landlock::FsAccess::Refer);
        }
    }
    landlock::PolicyStack missing_destination_refer_policy;
    missing_destination_refer_policy.add(missing_destination_refer);
    landlock::PolicyEvaluator missing_destination_refer_evaluator(
        missing_destination_refer_policy, safe_tree);
    const landlock::ReparentDecision missing_destination_refer_decision =
        missing_destination_refer_evaluator.check_rename("/from/file.txt",
                                                         "/to/file.txt");
    CHECK(!missing_destination_refer_decision.allowed);
    CHECK(find_issue(missing_destination_refer_decision,
                     landlock::ReparentIssueKind::ReferDenied) != nullptr);

    landlock::DirectoryTree removed_tree;
    removed_tree.add_file("/readonly/file.txt");
    removed_tree.add_directory("/to");
    landlock::PolicyLayer no_removal = make_safe_reparent_layer();
    for (landlock::FsRule &rule : no_removal.fs_rules) {
        if (rule.path == "/from") {
            rule.path = "/readonly";
            rule.allowed_access = landlock::FsAccess::ReadFile |
                                  landlock::FsAccess::Refer;
        }
    }
    landlock::PolicyStack no_removal_policy;
    no_removal_policy.add(no_removal);
    landlock::PolicyEvaluator no_removal_evaluator(no_removal_policy,
                                                   removed_tree);
    const landlock::ReparentDecision no_removal_decision =
        no_removal_evaluator.check_rename("/readonly/file.txt",
                                          "/to/file.txt");
    CHECK(!no_removal_decision.allowed);
    CHECK(find_issue(no_removal_decision,
                     landlock::ReparentIssueKind::RemoveDenied) != nullptr);

    landlock::PolicyLayer abi1_layer = make_safe_reparent_layer();
    abi1_layer.abi_version = 1u;
    landlock::PolicyStack abi1_policy;
    abi1_policy.add(abi1_layer);
    landlock::PolicyEvaluator abi1_evaluator(abi1_policy, safe_tree);
    const landlock::ReparentDecision abi1_decision =
        abi1_evaluator.check_rename("/from/file.txt", "/to/file.txt");
    CHECK(!abi1_decision.allowed);
    CHECK(find_issue(abi1_decision,
                     landlock::ReparentIssueKind::AbiDoesNotSupportRefer) !=
          nullptr);

    landlock::PolicyLayer abi2_layer = make_safe_reparent_layer();
    abi2_layer.abi_version = 2u;
    landlock::PolicyStack abi2_policy;
    abi2_policy.add(abi2_layer);
    landlock::PolicyEvaluator abi2_evaluator(abi2_policy, safe_tree);
    CHECK(abi2_evaluator.check_rename("/from/file.txt", "/to/file.txt")
              .allowed);
    CHECK(abi1_evaluator.check_hard_link("/from/file.txt",
                                         "/from/link.txt")
              .allowed);
}

void test_directory_subtree_refer_check() {
    landlock::DirectoryTree tree;
    tree.add_file("/source/child.txt");
    tree.add_directory("/dest");

    landlock::PolicyLayer layer;
    layer.name = "directory-move";
    layer.abi_version = 4u;
    layer.handled_access_fs = landlock::all_fs_access();
    layer.allow_fs("/", landlock::FsAccess::Execute |
                            landlock::FsAccess::ReadDir |
                            landlock::FsAccess::RemoveDir |
                            landlock::FsAccess::Refer);
    layer.allow_fs("/source", landlock::FsAccess::Execute |
                                  landlock::FsAccess::ReadDir |
                                  landlock::FsAccess::ReadFile);
    layer.allow_fs("/dest", landlock::FsAccess::Execute |
                                landlock::FsAccess::ReadDir |
                                landlock::FsAccess::ReadFile |
                                landlock::FsAccess::WriteFile |
                                landlock::FsAccess::MakeDir |
                                landlock::FsAccess::MakeRegular |
                                landlock::FsAccess::RemoveDir |
                                landlock::FsAccess::Refer);

    landlock::PolicyStack policy;
    policy.add(layer);
    landlock::PolicyEvaluator evaluator(policy, tree);
    const landlock::ReparentDecision decision =
        evaluator.check_rename("/source", "/dest/moved");
    CHECK(!decision.allowed);

    std::size_t subset_issues = 0u;
    for (const landlock::ReparentIssue &issue : decision.issues) {
        if (issue.kind ==
            landlock::ReparentIssueKind::DestinationHierarchyNotSubset) {
            ++subset_issues;
        }
    }
    CHECK(subset_issues >= 2u);
}

void test_per_layer_refer_subset_check() {
    landlock::DirectoryTree tree;
    tree.add_file("/src/file.txt");
    tree.add_directory("/dst");

    landlock::PolicyLayer destination_reader;
    destination_reader.name = "layer-0-denies-destination-read";
    destination_reader.abi_version = 4u;
    destination_reader.handled_access_fs =
        landlock::FsAccess::ReadFile | landlock::FsAccess::Refer;
    destination_reader.allow_fs(
        "/src", landlock::FsAccess::ReadFile | landlock::FsAccess::Refer);
    destination_reader.allow_fs("/dst", landlock::FsAccess::Refer);

    landlock::PolicyLayer source_reader;
    source_reader.name = "layer-1-denies-source-read";
    source_reader.abi_version = 4u;
    source_reader.handled_access_fs =
        landlock::FsAccess::ReadFile | landlock::FsAccess::Refer |
        landlock::FsAccess::RemoveFile | landlock::FsAccess::MakeRegular;
    source_reader.allow_fs(
        "/src", landlock::FsAccess::RemoveFile |
                    landlock::FsAccess::MakeRegular |
                    landlock::FsAccess::Refer);
    source_reader.allow_fs(
        "/dst", landlock::FsAccess::ReadFile |
                    landlock::FsAccess::RemoveFile |
                    landlock::FsAccess::MakeRegular |
                    landlock::FsAccess::Refer);

    landlock::PolicyStack policy;
    policy.add(destination_reader);
    policy.add(source_reader);
    landlock::PolicyEvaluator evaluator(policy, tree);

    const landlock::ReparentDecision decision =
        evaluator.check_rename("/src/file.txt", "/dst/file.txt");
    CHECK(!decision.allowed);
    CHECK(find_issue(decision,
                     landlock::ReparentIssueKind::DestinationLayerNotSubset) !=
          nullptr);
    CHECK(find_issue(decision,
                     landlock::ReparentIssueKind::
                         DestinationHierarchyNotSubset) == nullptr);
    CHECK(evaluator.effective_fs("/src/file.txt") ==
          evaluator.effective_fs("/dst/file.txt"));

    const landlock::AuditReport report = landlock::audit_policy(policy, tree);
    CHECK(!report.has_reparent_escape());
}

void test_audit_missing_handled_access() {
    landlock::DirectoryTree tree;
    tree.add_file("/data/file.txt");

    landlock::PolicyLayer layer;
    layer.name = "read-only-policy";
    layer.abi_version = 4u;
    layer.handled_access_fs = landlock::FsAccess::ReadFile;
    layer.allow_fs("/", landlock::FsAccess::ReadFile);

    landlock::PolicyStack policy;
    policy.add(layer);
    landlock::AuditOptions options;
    options.high_risk_net = landlock::NetAccess::None;
    options.detect_reparent_escapes = false;
    const landlock::AuditReport report =
        landlock::audit_policy(policy, tree, options);

    CHECK(report.has_missing_handled_access());
    CHECK(report.has_uncontrolled_access());
    CHECK(report.has_critical());
    const landlock::AuditFinding *missing =
        report.first_of_kind(landlock::AuditFindingKind::MissingHandledAccess);
    CHECK(missing != nullptr);
    if (missing != nullptr) {
        CHECK(missing->layer_index.has_value());
    }

    bool found_write = false;
    bool found_uncontrolled_write = false;
    for (const landlock::AuditFinding &finding : report.findings) {
        if (finding.kind ==
                landlock::AuditFindingKind::UncontrolledHighRiskAccess &&
            landlock::has_fs(finding.access, landlock::FsAccess::WriteFile)) {
            found_uncontrolled_write = true;
        }
        if (finding.kind ==
                landlock::AuditFindingKind::MissingHandledAccess &&
            landlock::has_fs(finding.access, landlock::FsAccess::WriteFile)) {
            found_write = true;
        }
    }
    CHECK(found_write);
    CHECK(found_uncontrolled_write);
}

void test_audit_rule_consistency() {
    landlock::DirectoryTree tree;

    landlock::PolicyLayer layer;
    layer.name = "bad-rule";
    layer.abi_version = 4u;
    layer.handled_access_fs = landlock::FsAccess::ReadFile;
    layer.allow_fs("/", landlock::FsAccess::ReadFile |
                            landlock::FsAccess::WriteFile);

    landlock::PolicyStack policy;
    policy.add(layer);

    landlock::AuditOptions options;
    options.high_risk_net = landlock::NetAccess::None;
    options.detect_reparent_escapes = false;
    const landlock::AuditReport report =
        landlock::audit_policy(policy, tree, options);
    CHECK(report.first_of_kind(
              landlock::AuditFindingKind::RuleExceedsHandledAccess) != nullptr);
}

void test_audit_unsupported_and_network_gaps() {
    landlock::DirectoryTree tree;

    landlock::PolicyLayer supported_layer;
    supported_layer.name = "abi3";
    supported_layer.abi_version = 3u;
    supported_layer.handled_access_fs = landlock::FsAccess::ReadFile;
    supported_layer.allow_fs("/", landlock::FsAccess::ReadFile);

    landlock::PolicyLayer unsupported_layer;
    unsupported_layer.name = "abi2-declares-truncate";
    unsupported_layer.abi_version = 2u;
    unsupported_layer.handled_access_fs =
        landlock::FsAccess::ReadFile | landlock::FsAccess::Truncate;
    unsupported_layer.allow_fs("/", landlock::FsAccess::ReadFile);

    landlock::PolicyStack policy;
    policy.add(supported_layer);
    policy.add(unsupported_layer);
    landlock::AuditOptions options;
    options.high_risk_net = landlock::NetAccess::None;
    options.detect_reparent_escapes = false;
    const landlock::AuditReport report =
        landlock::audit_policy(policy, tree, options);

    CHECK(report.first_of_kind(
              landlock::AuditFindingKind::UnsupportedHandledAccess) != nullptr);
    bool uncontrolled_truncate = false;
    for (const landlock::AuditFinding &finding : report.findings) {
        if (finding.kind ==
                landlock::AuditFindingKind::UncontrolledHighRiskAccess &&
            landlock::has_fs(finding.access,
                             landlock::FsAccess::Truncate)) {
            uncontrolled_truncate = true;
        }
    }
    CHECK(uncontrolled_truncate);

    landlock::PolicyLayer net_gap;
    net_gap.name = "abi4-no-network-policy";
    net_gap.abi_version = 4u;
    net_gap.handled_access_fs = landlock::FsAccess::ReadFile;
    net_gap.allow_fs("/", landlock::FsAccess::ReadFile);
    landlock::PolicyStack net_gap_policy;
    net_gap_policy.add(net_gap);

    landlock::AuditOptions net_options;
    net_options.detect_reparent_escapes = false;
    const landlock::AuditReport net_report =
        landlock::audit_policy(net_gap_policy, tree, net_options);
    bool missing_net_bind = false;
    bool uncontrolled_net_bind = false;
    for (const landlock::AuditFinding &finding : net_report.findings) {
        if (finding.kind ==
                landlock::AuditFindingKind::MissingHandledAccess &&
            landlock::has_net(finding.net_access,
                              landlock::NetAccess::BindTcp)) {
            missing_net_bind = true;
        }
        if (finding.kind ==
                landlock::AuditFindingKind::UncontrolledHighRiskAccess &&
            landlock::has_net(finding.net_access,
                              landlock::NetAccess::BindTcp)) {
            uncontrolled_net_bind = true;
        }
    }
    CHECK(missing_net_bind);
    CHECK(uncontrolled_net_bind);
}

void test_audit_reparent_escape_witness() {
    const landlock::DirectoryTree tree = make_dangerous_tree();
    landlock::PolicyStack policy;
    policy.add(make_dangerous_reparent_layer());
    landlock::PolicyEvaluator evaluator(policy, tree);

    const landlock::AuditReport report = landlock::audit_policy(policy, tree);
    CHECK(report.has_reparent_escape());
    CHECK(report.has_critical());

    const landlock::AuditFinding *witness_finding = nullptr;
    for (const landlock::AuditFinding &finding : report.findings) {
        if (!finding.reparent.has_value()) {
            continue;
        }
        const landlock::ReparentWitness &witness = *finding.reparent;
        if (witness.source == "/secret/report.txt" &&
            witness.destination_parent == "/sandbox") {
            witness_finding = &finding;
            break;
        }
    }
    CHECK(witness_finding != nullptr);
    if (witness_finding != nullptr) {
        CHECK(witness_finding->reparent.has_value());
        const landlock::ReparentWitness &witness =
            *witness_finding->reparent;
        CHECK(landlock::has_fs(witness.privilege_gain,
                               landlock::FsAccess::WriteFile));
        CHECK(witness.blocked_by_refer_hierarchy);
        CHECK(!witness.describe().empty());
    }

    const landlock::ReparentDecision guarded =
        evaluator.check_rename("/secret/report.txt",
                               "/sandbox/report.txt");
    CHECK(!guarded.allowed);
}

void test_audit_clean_policy() {
    landlock::DirectoryTree tree;
    tree.add_file("/secret/report.txt");
    tree.add_file("/sandbox/work.txt");

    landlock::PolicyLayer layer;
    layer.name = "secure";
    layer.abi_version = 4u;
    layer.handled_access_fs = landlock::all_fs_access();
    layer.handled_access_net =
        landlock::NetAccess::BindTcp | landlock::NetAccess::ConnectTcp;
    layer.allow_fs("/", landlock::FsAccess::Execute |
                            landlock::FsAccess::ReadFile |
                            landlock::FsAccess::ReadDir);
    layer.allow_fs("/secret", landlock::FsAccess::ReadFile);
    layer.allow_fs("/sandbox", landlock::FsAccess::Execute |
                                   landlock::FsAccess::ReadFile |
                                   landlock::FsAccess::ReadDir |
                                   landlock::FsAccess::WriteFile |
                                   landlock::FsAccess::MakeRegular |
                                   landlock::FsAccess::RemoveFile);

    landlock::PolicyStack policy;
    policy.add(layer);
    const landlock::AuditReport report = landlock::audit_policy(policy, tree);
    CHECK(report.clean());
    CHECK(!report.has_reparent_escape());
}

} // namespace

int main() {
    test_paths_and_tree();
    test_multilayer_intersection();
    test_unhandled_permissions_default_allow();
    test_tcp_port_narrowing();
    test_refer_reparent_checks();
    test_directory_subtree_refer_check();
    test_per_layer_refer_subset_check();
    test_audit_missing_handled_access();
    test_audit_rule_consistency();
    test_audit_unsupported_and_network_gaps();
    test_audit_reparent_escape_witness();
    test_audit_clean_policy();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all landlock nested-policy tests passed\n";
    return 0;
}

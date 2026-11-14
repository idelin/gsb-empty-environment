#include "netsec.hpp"

#include <chrono>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace {

using namespace std::chrono_literals;
using netsec::AuditEvent;
using netsec::AuditToken;
using netsec::BrokerPolicy;
using netsec::CallChainEntry;
using netsec::Decision;
using netsec::DenialReason;
using netsec::Duration;
using netsec::ExecutionMode;
using netsec::Generation;
using netsec::Grant;
using netsec::Permission;
using netsec::PermissionBroker;
using netsec::PermissionRequest;
using netsec::PermissionSet;
using netsec::ProcessId;
using netsec::ProcessKind;
using netsec::RequestContext;
using netsec::SignedSubject;
using netsec::TimePoint;

const TimePoint kNow{std::chrono::seconds(1'700'000'000)};

void check(bool condition, const char* expression, const char* file, int line) {
    if (!condition) {
        throw std::runtime_error(std::string(file) + ":" + std::to_string(line) +
                                 ": check failed: " + expression);
    }
}

#define CHECK(expression) check((expression), #expression, __FILE__, __LINE__)

SignedSubject host_subject() {
    return netsec::make_signed_subject("com.example.host", "Team-A", "sig:host");
}

SignedSubject plugin_subject() {
    return netsec::make_signed_subject("com.example.media-plugin", "Team-B",
                                       "sig:media-plugin");
}

RequestContext context_for(const SignedSubject& subject, const AuditToken& token,
                           ProcessId pid, Generation generation, ProcessKind kind,
                           ExecutionMode mode = ExecutionMode::Foreground) {
    RequestContext context;
    context.subject = subject;
    context.audit_token = token;
    context.process_id = pid;
    context.generation = generation;
    context.process_kind = kind;
    context.execution_mode = mode;
    return context;
}

PermissionRequest request_for(const RequestContext& context, PermissionSet permissions,
                              std::string purpose,
                              std::optional<Duration> ttl = std::nullopt) {
    PermissionRequest request;
    static_cast<RequestContext&>(request) = context;
    request.permissions = permissions;
    request.purpose = std::move(purpose);
    request.ttl = ttl;
    return request;
}

CallChainEntry chain_entry(const RequestContext& context, std::string operation,
                           std::string grant_id = {}) {
    CallChainEntry entry;
    entry.subject_id = context.subject.id;
    entry.signer = context.subject.signer;
    entry.operation = std::move(operation);
    entry.process_id = context.process_id;
    entry.generation = context.generation;
    entry.process_kind = context.process_kind;
    entry.execution_mode =
        context.process_kind == ProcessKind::BackgroundTask
            ? ExecutionMode::Background
            : context.execution_mode;
    entry.grant_id = std::move(grant_id);
    entry.purpose = "test";
    return entry;
}

Grant issue_host_grant(PermissionBroker& broker, const RequestContext& host_context,
                       PermissionSet permissions, std::string purpose,
                       std::optional<Duration> ttl = std::nullopt) {
    PermissionRequest request = request_for(host_context, permissions, std::move(purpose), ttl);
    Decision decision = broker.request(request, kNow);
    CHECK(decision.allowed);
    CHECK(decision.grant.has_value());
    return *decision.grant;
}

void host_grant_is_bound_to_context() {
    PermissionBroker broker;
    const auto subject = host_subject();
    const AuditToken token = broker.issue_audit_token(subject, 1000, 1);
    CHECK(!token.empty());
    CHECK(broker.is_audit_token_valid(token));

    const auto context =
        context_for(subject, token, 1000, 1, ProcessKind::HostApplication);
    const Grant grant = issue_host_grant(
        broker, context, PermissionSet{Permission::Camera, Permission::FileRead},
        "camera-preview-and-file-import");

    CHECK(grant.audit_token_id == token.value);
    CHECK(grant.process_id == 1000);
    CHECK(grant.generation == 1);
    CHECK(grant.purpose == "camera-preview-and-file-import");
    CHECK(grant.call_chain.size() == 1);

    Decision camera =
        broker.check(grant.id, context, Permission::Camera, grant.purpose, kNow + 1s);
    CHECK(camera.allowed);

    Decision files =
        broker.check(grant.id, context, PermissionSet{Permission::FileRead}, grant.purpose,
                     kNow + 1s);
    CHECK(files.allowed);

    Decision write =
        broker.check(grant.id, context, Permission::FileWrite, grant.purpose, kNow + 1s);
    CHECK(!write.allowed);
    CHECK(write.reason == DenialReason::PermissionNotGranted);

    Decision wrong_purpose =
        broker.check(grant.id, context, Permission::Camera, "screen-recording", kNow + 1s);
    CHECK(!wrong_purpose.allowed);
    CHECK(wrong_purpose.reason == DenialReason::PurposeMismatch);

    Decision looked_up =
        broker.authorize(context, Permission::Camera, grant.purpose, kNow + 1s);
    CHECK(looked_up.allowed);
    CHECK(looked_up.grant.has_value());
    CHECK(looked_up.grant->id == grant.id);

    PermissionBroker empty_broker;
    Decision no_grants = empty_broker.authorize(
        context_for(subject, empty_broker.issue_audit_token(subject, 1002, 1), 1002, 1,
                    ProcessKind::HostApplication),
        Permission::Camera, grant.purpose, kNow + 1s);
    CHECK(!no_grants.allowed);
    CHECK(no_grants.reason == DenialReason::GrantNotFound);
}

void plugin_inheritance_is_denied_and_reauthorization_works() {
    PermissionBroker broker;
    const auto host = host_subject();
    const auto plugin = plugin_subject();

    const AuditToken host_token = broker.issue_audit_token(host, 1000, 1);
    const auto host_context =
        context_for(host, host_token, 1000, 1, ProcessKind::HostApplication);
    const Grant host_grant = issue_host_grant(
        broker, host_context, PermissionSet{Permission::Camera, Permission::FileRead},
        "host-media");

    const AuditToken plugin_token = broker.issue_audit_token(plugin, 2000, 1);
    const auto plugin_context = context_for(plugin, plugin_token, 2000, 1, ProcessKind::Plugin);

    Decision inherited =
        broker.check(host_grant.id, plugin_context, Permission::FileRead, "host-media",
                     kNow + 1s);
    CHECK(!inherited.allowed);
    CHECK(inherited.reason == DenialReason::PluginInheritanceDenied);

    PermissionRequest implicit =
        request_for(plugin_context, PermissionSet{Permission::FileRead}, "plugin-import");
    Decision missing_chain = broker.request(implicit, kNow + 1s);
    CHECK(!missing_chain.allowed);
    CHECK(missing_chain.reason == DenialReason::CallChainRequired);

    PermissionRequest explicit_request =
        request_for(plugin_context, PermissionSet{Permission::FileRead}, "plugin-import");
    explicit_request.parent_grant_id = host_grant.id;
    explicit_request.call_chain = {
        chain_entry(host_context, "host-load-plugin", host_grant.id),
        chain_entry(plugin_context, "plugin-request"),
    };

    Decision reauthorized = broker.request(explicit_request, kNow + 1s);
    CHECK(reauthorized.allowed);
    CHECK(reauthorized.grant.has_value());
    CHECK(reauthorized.grant->subject.same_principal(plugin));
    CHECK(reauthorized.grant->call_chain.size() == 2);
    CHECK(reauthorized.call_chain.size() == 2);
    CHECK(reauthorized.grant->process_kind == ProcessKind::Plugin);
    CHECK(reauthorized.grant->parent_grant_id == host_grant.id);

    PermissionRequest plugin_escalation =
        request_for(plugin_context,
                    PermissionSet{Permission::FileRead, Permission::FileWrite},
                    "plugin-write");
    plugin_escalation.parent_grant_id = host_grant.id;
    plugin_escalation.call_chain = {
        chain_entry(host_context, "host-load-plugin", host_grant.id),
        chain_entry(plugin_context, "plugin-request"),
    };
    Decision blocked_escalation = broker.request(plugin_escalation, kNow + 1s);
    CHECK(!blocked_escalation.allowed);
    CHECK(blocked_escalation.reason == DenialReason::PermissionEscalation);

    Decision own_grant =
        broker.check(reauthorized.grant->id, plugin_context, Permission::FileRead,
                     "plugin-import", kNow + 2s);
    CHECK(own_grant.allowed);

    Decision host_grant_from_plugin =
        broker.check(host_grant.id, plugin_context, Permission::Camera, "host-media",
                     kNow + 2s);
    CHECK(!host_grant_from_plugin.allowed);
    CHECK(host_grant_from_plugin.reason == DenialReason::PluginInheritanceDenied);

    CHECK(PermissionBroker::format_call_chain(reauthorized.grant->call_chain).find(
              "com.example.host") != std::string::npos);
    CHECK(PermissionBroker::format_call_chain(reauthorized.grant->call_chain).find(
              "com.example.media-plugin") != std::string::npos);
}

void child_process_reauthorization_cannot_escalate() {
    PermissionBroker broker;
    const auto host = host_subject();
    const AuditToken host_token = broker.issue_audit_token(host, 1000, 1);
    const auto host_context =
        context_for(host, host_token, 1000, 1, ProcessKind::HostApplication);
    const Grant host_grant =
        issue_host_grant(broker, host_context, Permission::Camera, "camera-preview");

    const AuditToken child_token = broker.issue_audit_token(host, 1001, 1);
    const auto child_context =
        context_for(host, child_token, 1001, 1, ProcessKind::ChildProcess);

    Decision inherited = broker.check(host_grant.id, child_context, Permission::Camera,
                                      "camera-preview", kNow + 1s);
    CHECK(!inherited.allowed);
    CHECK(inherited.reason == DenialReason::ChildInheritanceDenied);

    PermissionRequest escalation =
        request_for(child_context,
                    PermissionSet{Permission::Camera, Permission::FileRead},
                    "child-import");
    escalation.parent_grant_id = host_grant.id;
    escalation.call_chain = {
        chain_entry(host_context, "host-spawn-child", host_grant.id),
        chain_entry(child_context, "child-request"),
    };
    Decision escalated = broker.request(escalation, kNow + 1s);
    CHECK(!escalated.allowed);
    CHECK(escalated.reason == DenialReason::PermissionEscalation);

    PermissionRequest chain_only_escalation =
        request_for(child_context,
                    PermissionSet{Permission::Camera, Permission::FileRead},
                    "child-import");
    chain_only_escalation.call_chain = {
        chain_entry(host_context, "host-spawn-child"),
        chain_entry(child_context, "child-request"),
    };
    Decision implicit_escalation = broker.request(chain_only_escalation, kNow + 1s);
    CHECK(!implicit_escalation.allowed);
    CHECK(implicit_escalation.reason == DenialReason::PermissionEscalation);

    PermissionRequest narrowed = request_for(child_context, Permission::Camera,
                                             "child-camera");
    narrowed.parent_grant_id = host_grant.id;
    narrowed.call_chain = {
        chain_entry(host_context, "host-spawn-child", host_grant.id),
        chain_entry(child_context, "child-request"),
    };
    Decision child_grant = broker.request(narrowed, kNow + 1s);
    CHECK(child_grant.allowed);
    CHECK(child_grant.grant.has_value());
    CHECK(child_grant.grant->process_id == 1001);
    CHECK(child_grant.grant->process_kind == ProcessKind::ChildProcess);

    Decision live = broker.check(child_grant.grant->id, child_context, Permission::Camera,
                                 "child-camera", kNow + 2s);
    CHECK(live.allowed);

    Decision still_no_host_grant =
        broker.check(host_grant.id, child_context, Permission::Camera, "camera-preview",
                     kNow + 2s);
    CHECK(!still_no_host_grant.allowed);
    CHECK(still_no_host_grant.reason == DenialReason::ChildInheritanceDenied);
}

void generation_change_revokes_old_capability() {
    PermissionBroker broker;
    const auto subject = host_subject();
    const AuditToken old_token = broker.issue_audit_token(subject, 1000, 1);
    const auto old_context =
        context_for(subject, old_token, 1000, 1, ProcessKind::HostApplication);
    const Grant old_grant =
        issue_host_grant(broker, old_context, Permission::FileRead, "file-import");

    CHECK(broker.advance_generation(1000, 2, kNow + 1s) == 2);
    CHECK(broker.current_generation(1000) == 2);
    CHECK(!broker.is_audit_token_valid(old_token));

    Decision old_grant_old_context =
        broker.check(old_grant.id, old_context, Permission::FileRead, "file-import",
                     kNow + 2s);
    CHECK(!old_grant_old_context.allowed);
    CHECK(old_grant_old_context.reason == DenialReason::GenerationMismatch);

    const AuditToken new_token = broker.issue_audit_token(subject, 1000, 2);
    CHECK(!new_token.empty());
    const auto new_context =
        context_for(subject, new_token, 1000, 2, ProcessKind::HostApplication);

    Decision stale_with_new_token =
        broker.check(old_grant.id, new_context, Permission::FileRead, "file-import",
                     kNow + 2s);
    CHECK(!stale_with_new_token.allowed);

    const Grant new_grant =
        issue_host_grant(broker, new_context, Permission::FileRead, "file-import");
    CHECK(new_grant.generation == 2);
    Decision fresh = broker.check(new_grant.id, new_context, Permission::FileRead,
                                  "file-import", kNow + 2s);
    CHECK(fresh.allowed);

    CHECK(broker.issue_audit_token(subject, 1000, 1).empty());
}

void temporary_grants_require_purpose_and_expire() {
    PermissionBroker broker;
    const auto subject = host_subject();
    const AuditToken token = broker.issue_audit_token(subject, 1000, 1);
    const auto context =
        context_for(subject, token, 1000, 1, ProcessKind::HostApplication);

    const Grant grant =
        issue_host_grant(broker, context, Permission::FileRead, "temporary-import",
                         Duration(5000));
    CHECK(grant.temporary);
    CHECK(grant.expires_at.has_value());
    CHECK(*grant.expires_at == kNow + 5s);

    Decision before =
        broker.check(grant.id, context, Permission::FileRead, "temporary-import",
                     kNow + 4s);
    CHECK(before.allowed);

    Decision at_boundary =
        broker.check(grant.id, context, Permission::FileRead, "temporary-import",
                     kNow + 5s);
    CHECK(!at_boundary.allowed);
    CHECK(at_boundary.reason == DenialReason::Expired);

    Decision wrong_purpose =
        broker.check(grant.id, context, Permission::FileRead, "background-sync",
                     kNow + 1s);
    CHECK(!wrong_purpose.allowed);
    CHECK(wrong_purpose.reason == DenialReason::PurposeMismatch);

    PermissionRequest expired_parent =
        request_for(context, Permission::FileRead, "child-import");
    expired_parent.parent_grant_id = grant.id;
    Decision parent_denied = broker.request(expired_parent, kNow + 6s);
    CHECK(!parent_denied.allowed);
    CHECK(parent_denied.reason == DenialReason::ParentGrantInvalid);

    Grant missing_deadline = grant;
    missing_deadline.expires_at.reset();
    Decision malformed =
        broker.inspect_grant(missing_deadline, context, Permission::FileRead,
                             "temporary-import", kNow + 1s);
    CHECK(!malformed.allowed);
    CHECK(malformed.reason == DenialReason::ExpiryRequired);
}

void background_work_is_evaluated_separately() {
    PermissionBroker broker;
    const auto subject = host_subject();
    const AuditToken token = broker.issue_audit_token(subject, 1000, 1);
    const auto foreground =
        context_for(subject, token, 1000, 1, ProcessKind::HostApplication);
    const Grant foreground_grant =
        issue_host_grant(broker, foreground, Permission::FileRead, "foreground-import");

    const auto background =
        context_for(subject, token, 1000, 1, ProcessKind::HostApplication,
                    ExecutionMode::Background);
    Decision cross_mode =
        broker.check(foreground_grant.id, background, Permission::FileRead,
                     "foreground-import", kNow + 1s);
    CHECK(!cross_mode.allowed);
    CHECK(cross_mode.reason == DenialReason::BackgroundMismatch);

    PermissionRequest background_request =
        request_for(background, Permission::FileRead, "background-sync");
    Decision background_grant = broker.request(background_request, kNow + 1s);
    CHECK(background_grant.allowed);
    CHECK(background_grant.grant.has_value());
    CHECK(background_grant.grant->execution_mode == ExecutionMode::Background);

    Decision background_ok =
        broker.check(background_grant.grant->id, background, Permission::FileRead,
                     "background-sync", kNow + 2s);
    CHECK(background_ok.allowed);

    Decision background_grant_foreground =
        broker.check(background_grant.grant->id, foreground, Permission::FileRead,
                     "background-sync", kNow + 2s);
    CHECK(!background_grant_foreground.allowed);
    CHECK(background_grant_foreground.reason == DenialReason::BackgroundMismatch);
}

void subject_and_token_crossing_are_rejected() {
    PermissionBroker broker;
    const auto subject_a = host_subject();
    const auto subject_b =
        netsec::make_signed_subject("com.example.other", "Team-C", "sig:other");

    const AuditToken token_a = broker.issue_audit_token(subject_a, 1000, 1);
    const auto context_a =
        context_for(subject_a, token_a, 1000, 1, ProcessKind::HostApplication);
    const Grant grant_a = issue_host_grant(broker, context_a, Permission::Camera,
                                           "camera-preview");

    const AuditToken token_b = broker.issue_audit_token(subject_b, 1000, 1);
    const auto context_b =
        context_for(subject_b, token_b, 1000, 1, ProcessKind::HostApplication);
    Decision crossed = broker.check(grant_a.id, context_b, Permission::Camera,
                                    "camera-preview", kNow + 1s);
    CHECK(!crossed.allowed);
    CHECK(crossed.reason == DenialReason::SubjectMismatch);

    AuditToken forged;
    forged.value = "audit-forged-token";
    forged.subject_id = subject_a.id;
    forged.signer = subject_a.signer;
    forged.process_id = 1000;
    forged.generation = 1;
    const auto forged_context =
        context_for(subject_a, forged, 1000, 1, ProcessKind::HostApplication);
    PermissionRequest forged_request =
        request_for(forged_context, Permission::FileRead, "forged");
    Decision forged_decision = broker.request(forged_request, kNow + 1s);
    CHECK(!forged_decision.allowed);
    CHECK(forged_decision.reason == DenialReason::InvalidAuditToken);

    AuditToken changed_token = token_a;
    changed_token.subject_id = subject_b.id;
    changed_token.signer = subject_b.signer;
    const auto changed_context =
        context_for(subject_a, changed_token, 1000, 1, ProcessKind::HostApplication);
    Decision changed =
        broker.check(grant_a.id, changed_context, Permission::Camera, "camera-preview",
                     kNow + 1s);
    CHECK(changed.allowed);
}

void invalid_chain_and_revocation_are_denied() {
    PermissionBroker broker;
    const auto host = host_subject();
    const auto plugin = plugin_subject();
    const AuditToken host_token = broker.issue_audit_token(host, 1000, 1);
    const auto host_context =
        context_for(host, host_token, 1000, 1, ProcessKind::HostApplication);
    const Grant host_grant =
        issue_host_grant(broker, host_context, Permission::FileRead, "host-import");

    const AuditToken plugin_token = broker.issue_audit_token(plugin, 2000, 1);
    const auto plugin_context = context_for(plugin, plugin_token, 2000, 1, ProcessKind::Plugin);

    PermissionRequest invalid_chain =
        request_for(plugin_context, Permission::FileRead, "plugin-import");
    invalid_chain.call_chain = {
        chain_entry(host_context, "host-load-plugin", "grant-does-not-exist"),
        chain_entry(plugin_context, "plugin-request"),
    };
    Decision invalid = broker.request(invalid_chain, kNow + 1s);
    CHECK(!invalid.allowed);
    CHECK(invalid.reason == DenialReason::CallChainInvalid);

    PermissionRequest too_short =
        request_for(plugin_context, Permission::FileRead, "plugin-import");
    too_short.call_chain = {chain_entry(plugin_context, "plugin-request")};
    Decision short_chain = broker.request(too_short, kNow + 1s);
    CHECK(!short_chain.allowed);
    CHECK(short_chain.reason == DenialReason::CallChainRequired);

    CHECK(broker.revoke(host_grant.id, kNow + 2s));
    Decision revoked = broker.check(host_grant.id, host_context, Permission::FileRead,
                                    "host-import", kNow + 2s);
    CHECK(!revoked.allowed);
    CHECK(revoked.reason == DenialReason::GrantRevoked);
}

void malformed_and_untrusted_requests_are_rejected() {
    PermissionBroker broker;
    const auto subject = host_subject();
    const AuditToken token = broker.issue_audit_token(subject, 1000, 1);
    const auto context =
        context_for(subject, token, 1000, 1, ProcessKind::HostApplication);

    PermissionRequest no_permissions = request_for(context, PermissionSet{}, "empty");
    Decision empty = broker.request(no_permissions, kNow);
    CHECK(!empty.allowed);
    CHECK(empty.reason == DenialReason::EmptyPermissionSet);

    PermissionRequest no_purpose = request_for(context, Permission::Camera, "");
    Decision purpose = broker.request(no_purpose, kNow);
    CHECK(!purpose.allowed);
    CHECK(purpose.reason == DenialReason::PurposeRequired);

    SignedSubject untrusted = subject;
    untrusted.signature.clear();
    CHECK(broker.issue_audit_token(untrusted, 1001, 1).empty());

    PermissionBroker strict(
        BrokerPolicy{},
        [](const SignedSubject& candidate) { return candidate.signature == "sig:good"; });
    SignedSubject bad_signature = subject;
    bad_signature.signature = "sig:bad";
    const AuditToken bad_token = strict.issue_audit_token(bad_signature, 1000, 1);
    CHECK(bad_token.empty());
}

void audit_trail_records_responsibility() {
    PermissionBroker broker;
    const auto host = host_subject();
    const auto plugin = plugin_subject();
    const AuditToken host_token = broker.issue_audit_token(host, 1000, 1);
    const auto host_context =
        context_for(host, host_token, 1000, 1, ProcessKind::HostApplication);
    const Grant host_grant =
        issue_host_grant(broker, host_context, Permission::FileRead, "host-import");

    const AuditToken plugin_token = broker.issue_audit_token(plugin, 2000, 1);
    const auto plugin_context = context_for(plugin, plugin_token, 2000, 1, ProcessKind::Plugin);
    (void)broker.check(host_grant.id, plugin_context, Permission::FileRead, "host-import",
                       kNow + 1s);

    const auto trail = broker.audit_trail();
    CHECK(!trail.empty());
    CHECK(trail.front().event == AuditEvent::TokenIssued);
    CHECK(trail.back().event == AuditEvent::AccessDenied);
    CHECK(trail.back().reason == DenialReason::PluginInheritanceDenied);
    CHECK(!trail.back().call_chain.empty() ||
          trail.back().subject_id == "com.example.media-plugin");

    for (std::size_t index = 1; index < trail.size(); ++index) {
        CHECK(trail[index].sequence > trail[index - 1].sequence);
    }
}

using Test = std::pair<const char*, void (*)()>;

}  // namespace

int main() {
    const std::vector<Test> tests = {
        {"host grant is bound to context", host_grant_is_bound_to_context},
        {"plugin inheritance and reauthorization", plugin_inheritance_is_denied_and_reauthorization_works},
        {"child cannot escalate", child_process_reauthorization_cannot_escalate},
        {"generation change", generation_change_revokes_old_capability},
        {"temporary grant expiry", temporary_grants_require_purpose_and_expire},
        {"background evaluation", background_work_is_evaluated_separately},
        {"subject and token crossing", subject_and_token_crossing_are_rejected},
        {"invalid chain and revocation", invalid_chain_and_revocation_are_denied},
        {"malformed and untrusted", malformed_and_untrusted_requests_are_rejected},
        {"audit trail", audit_trail_records_responsibility},
    };

    std::size_t failures = 0;
    for (const auto& [name, test] : tests) {
        try {
            test();
            std::cout << "[pass] " << name << '\n';
        } catch (const std::exception& error) {
            ++failures;
            std::cerr << "[fail] " << name << ": " << error.what() << '\n';
        }
    }

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << tests.size() << " test(s) passed\n";
    return 0;
}

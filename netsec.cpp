#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <atomic>
#include <chrono>
#include <iomanip>
#include <random>
#include <sstream>
#include <utility>

namespace netsec {
namespace {

std::atomic<std::uint64_t> g_grant_sequence{1};
std::atomic<std::uint64_t> g_token_sequence{1};

std::string random_hex(std::size_t bytes) {
    std::array<unsigned char, 32> buffer{};
    if (bytes > buffer.size()) {
        bytes = buffer.size();
    }

    try {
        std::random_device device;
        for (std::size_t index = 0; index < bytes; ++index) {
            buffer[index] = static_cast<unsigned char>(device());
        }
    } catch (...) {
        const auto fallback =
            static_cast<std::uint64_t>(std::chrono::steady_clock::now()
                                           .time_since_epoch()
                                           .count()) ^
            g_token_sequence.fetch_add(1, std::memory_order_relaxed);
        for (std::size_t index = 0; index < bytes; ++index) {
            buffer[index] =
                static_cast<unsigned char>((fallback >> ((index % 8) * 8)) & 0xffu);
        }
    }

    std::ostringstream stream;
    stream << std::hex << std::setfill('0');
    for (std::size_t index = 0; index < bytes; ++index) {
        stream << std::setw(2) << static_cast<unsigned int>(buffer[index]);
    }
    return stream.str();
}

std::string make_grant_id() {
    const auto sequence = g_grant_sequence.fetch_add(1, std::memory_order_relaxed);
    return "grant-" + std::to_string(sequence) + "-" + random_hex(12);
}

ExecutionMode effective_mode(const RequestContext& context) noexcept {
    if (context.execution_mode == ExecutionMode::Background ||
        context.process_kind == ProcessKind::BackgroundTask) {
        return ExecutionMode::Background;
    }
    return ExecutionMode::Foreground;
}

bool is_background(const RequestContext& context) noexcept {
    return effective_mode(context) == ExecutionMode::Background;
}

std::string principal_text(const std::string& id, const std::string& signer) {
    if (id.empty()) {
        return "<unknown>";
    }
    if (signer.empty()) {
        return id;
    }
    return id + "@" + signer;
}

CallChainEntry make_chain_entry(const RequestContext& context, std::string_view purpose,
                                std::string_view operation, std::string grant_id = {}) {
    CallChainEntry entry;
    entry.subject_id = context.subject.id;
    entry.signer = context.subject.signer;
    entry.operation = std::string(operation);
    entry.process_id = context.process_id;
    entry.generation = context.generation;
    entry.process_kind = context.process_kind;
    entry.execution_mode = effective_mode(context);
    entry.grant_id = std::move(grant_id);
    entry.purpose = std::string(purpose);
    return entry;
}

bool entry_matches_context(const CallChainEntry& entry, const RequestContext& context) {
    return entry.subject_id == context.subject.id && entry.signer == context.subject.signer &&
           entry.process_id == context.process_id &&
           entry.generation == context.generation &&
           entry.process_kind == context.process_kind &&
           entry.execution_mode == effective_mode(context);
}

bool is_valid_chain_entry(const CallChainEntry& entry) {
    return !entry.subject_id.empty() && !entry.signer.empty();
}

int grant_match_score(const Grant& grant, const RequestContext& context) {
    int score = 0;
    if (grant.subject.same_principal(context.subject)) {
        score += 4;
    }
    if (grant.process_id == context.process_id) {
        score += 2;
    }
    if (grant.audit_token_id == context.audit_token.value) {
        score += 1;
    }
    return score;
}

}  // namespace

std::string_view to_string(Permission permission) noexcept {
    switch (permission) {
        case Permission::Camera:
            return "camera";
        case Permission::Microphone:
            return "microphone";
        case Permission::FileRead:
            return "file-read";
        case Permission::FileWrite:
            return "file-write";
        case Permission::Location:
            return "location";
        case Permission::Network:
            return "network";
        case Permission::Clipboard:
            return "clipboard";
        case Permission::Contacts:
            return "contacts";
        case Permission::Calendar:
            return "calendar";
        case Permission::ProcessControl:
            return "process-control";
    }
    return "unknown";
}

std::string to_string(PermissionSet permissions) {
    static constexpr std::array<Permission, 10> values = {
        Permission::Camera,    Permission::Microphone, Permission::FileRead,
        Permission::FileWrite, Permission::Location,   Permission::Network,
        Permission::Clipboard, Permission::Contacts,   Permission::Calendar,
        Permission::ProcessControl,
    };

    std::string result;
    for (Permission permission : values) {
        if (!permissions.contains(permission)) {
            continue;
        }
        if (!result.empty()) {
            result += ",";
        }
        result += to_string(permission);
    }
    return result.empty() ? "<none>" : result;
}

SignedSubject make_signed_subject(std::string id, std::string signer,
                                  std::string signature, std::string display_name) {
    SignedSubject subject;
    subject.id = std::move(id);
    subject.signer = std::move(signer);
    subject.signature = std::move(signature);
    subject.display_name = std::move(display_name);
    return subject;
}

std::string_view to_string(ProcessKind kind) noexcept {
    switch (kind) {
        case ProcessKind::HostApplication:
            return "host";
        case ProcessKind::ChildProcess:
            return "child";
        case ProcessKind::Plugin:
            return "plugin";
        case ProcessKind::BackgroundTask:
            return "background-task";
    }
    return "unknown";
}

std::string_view to_string(ExecutionMode mode) noexcept {
    switch (mode) {
        case ExecutionMode::Foreground:
            return "foreground";
        case ExecutionMode::Background:
            return "background";
    }
    return "unknown";
}

std::string_view to_string(DenialReason reason) noexcept {
    switch (reason) {
        case DenialReason::Allowed:
            return "allowed";
        case DenialReason::NotEvaluated:
            return "not-evaluated";
        case DenialReason::InvalidSubject:
            return "invalid-signed-subject";
        case DenialReason::InvalidAuditToken:
            return "invalid-audit-token";
        case DenialReason::AuditTokenSubjectMismatch:
            return "audit-token-subject-mismatch";
        case DenialReason::AuditTokenProcessMismatch:
            return "audit-token-process-mismatch";
        case DenialReason::AuditTokenGenerationMismatch:
            return "audit-token-generation-mismatch";
        case DenialReason::AuditTokenGrantMismatch:
            return "audit-token-grant-mismatch";
        case DenialReason::GrantNotFound:
            return "grant-not-found";
        case DenialReason::GrantRevoked:
            return "grant-revoked";
        case DenialReason::SubjectMismatch:
            return "subject-mismatch";
        case DenialReason::ProcessMismatch:
            return "process-mismatch";
        case DenialReason::GenerationMismatch:
            return "generation-mismatch";
        case DenialReason::ProcessKindMismatch:
            return "process-kind-mismatch";
        case DenialReason::InheritanceDenied:
            return "inheritance-denied";
        case DenialReason::ChildInheritanceDenied:
            return "child-inheritance-denied";
        case DenialReason::PluginInheritanceDenied:
            return "plugin-inheritance-denied";
        case DenialReason::BackgroundMismatch:
            return "background-mismatch";
        case DenialReason::BackgroundGrantDisabled:
            return "background-grant-disabled";
        case DenialReason::BackgroundPermissionDenied:
            return "background-permission-denied";
        case DenialReason::PurposeRequired:
            return "purpose-required";
        case DenialReason::PurposeMismatch:
            return "purpose-mismatch";
        case DenialReason::PermissionNotGranted:
            return "permission-not-granted";
        case DenialReason::ExpiryRequired:
            return "expiry-required";
        case DenialReason::Expired:
            return "expired";
        case DenialReason::TemporaryGrantDisabled:
            return "temporary-grant-disabled";
        case DenialReason::InvalidTtl:
            return "invalid-ttl";
        case DenialReason::CallChainRequired:
            return "call-chain-required";
        case DenialReason::CallChainInvalid:
            return "call-chain-invalid";
        case DenialReason::ParentGrantNotFound:
            return "parent-grant-not-found";
        case DenialReason::ParentGrantInvalid:
            return "parent-grant-invalid";
        case DenialReason::CrossSubjectDelegation:
            return "cross-subject-delegation";
        case DenialReason::PermissionEscalation:
            return "permission-escalation";
        case DenialReason::ChildGrantDisabled:
            return "child-grant-disabled";
        case DenialReason::PluginGrantDisabled:
            return "plugin-grant-disabled";
        case DenialReason::EmptyPermissionSet:
            return "empty-permission-set";
    }
    return "unknown";
}

std::string_view to_string(AuditEvent event) noexcept {
    switch (event) {
        case AuditEvent::TokenIssued:
            return "token-issued";
        case AuditEvent::GrantIssued:
            return "grant-issued";
        case AuditEvent::GrantDenied:
            return "grant-denied";
        case AuditEvent::AccessGranted:
            return "access-granted";
        case AuditEvent::AccessDenied:
            return "access-denied";
        case AuditEvent::GrantRevoked:
            return "grant-revoked";
        case AuditEvent::GenerationAdvanced:
            return "generation-advanced";
    }
    return "unknown";
}

Decision Decision::allow(Grant grant, std::vector<CallChainEntry> chain, TimePoint now) {
    Decision decision;
    decision.allowed = true;
    decision.reason = DenialReason::Allowed;
    decision.detail = "authorized";
    decision.call_chain = std::move(chain);
    decision.grant = std::move(grant);
    decision.evaluated_at = now;
    return decision;
}

Decision Decision::deny(DenialReason reason, std::string detail, TimePoint now,
                        std::optional<Grant> grant, std::vector<CallChainEntry> chain) {
    Decision decision;
    decision.allowed = false;
    decision.reason = reason;
    decision.detail = std::move(detail);
    decision.grant = std::move(grant);
    decision.call_chain = std::move(chain);
    decision.evaluated_at = now;
    return decision;
}

PermissionBroker::PermissionBroker() : PermissionBroker(BrokerPolicy{}) {}

PermissionBroker::PermissionBroker(BrokerPolicy policy)
    : policy_(std::move(policy)),
      clock_([] { return std::chrono::system_clock::now(); }) {}

PermissionBroker::PermissionBroker(BrokerPolicy policy, SubjectVerifier verifier)
    : policy_(std::move(policy)),
      verifier_(std::move(verifier)),
      clock_([] { return std::chrono::system_clock::now(); }) {}

void PermissionBroker::set_clock(std::function<TimePoint()> clock) {
    std::lock_guard<std::mutex> lock(mutex_);
    clock_ = std::move(clock);
}

void PermissionBroker::set_subject_verifier(SubjectVerifier verifier) {
    std::lock_guard<std::mutex> lock(mutex_);
    verifier_ = std::move(verifier);
}

TimePoint PermissionBroker::resolve_now(std::optional<TimePoint> now) const {
    if (now.has_value()) {
        return *now;
    }
    return clock_ ? clock_() : std::chrono::system_clock::now();
}

bool PermissionBroker::verify_subject(const SignedSubject& subject) const {
    if (!policy_.trusted_signers.empty() &&
        !policy_.trusted_signers.contains(subject.signer)) {
        return false;
    }
    if (verifier_) {
        return verifier_(subject);
    }
    if (!policy_.require_signed_subject) {
        return !subject.id.empty();
    }
    if (subject.empty()) {
        return false;
    }
    return true;
}

AuditToken PermissionBroker::issue_audit_token(const SignedSubject& subject,
                                               ProcessId process_id,
                                               Generation generation) {
    std::lock_guard<std::mutex> lock(mutex_);

    if (!verify_subject(subject)) {
        return {};
    }

    const auto floor = generation_floor_.find(process_id);
    if (floor != generation_floor_.end() && generation < floor->second) {
        return {};
    }

    AuditToken token;
    do {
        token.value = "audit-" + random_hex(24);
    } while (tokens_.contains(token.value));
    token.subject_id = subject.id;
    token.signer = subject.signer;
    token.process_id = process_id;
    token.generation = generation;
    tokens_.emplace(token.value, token);

    AuditRecord record;
    record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
    record.at = resolve_now(std::nullopt);
    record.event = AuditEvent::TokenIssued;
    record.subject_id = subject.id;
    record.signer = subject.signer;
    record.audit_token_id = token.value;
    record.process_id = process_id;
    record.generation = generation;
    record.reason = DenialReason::Allowed;
    record.detail = "audit token issued";
    audit_.push_back(std::move(record));

    return token;
}

bool PermissionBroker::is_audit_token_valid(const AuditToken& token) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto iterator = tokens_.find(token.value);
    if (iterator == tokens_.end()) {
        return false;
    }
    const auto floor = generation_floor_.find(iterator->second.process_id);
    return floor == generation_floor_.end() || iterator->second.generation >= floor->second;
}

DenialReason PermissionBroker::validate_context_token(
    const RequestContext& context) const {
    if (context.audit_token.value.empty()) {
        return DenialReason::InvalidAuditToken;
    }

    const auto iterator = tokens_.find(context.audit_token.value);
    if (iterator == tokens_.end()) {
        return DenialReason::InvalidAuditToken;
    }

    const AuditToken& stored = iterator->second;
    if (stored.subject_id != context.subject.id || stored.signer != context.subject.signer) {
        return DenialReason::AuditTokenSubjectMismatch;
    }
    if (stored.process_id != context.process_id) {
        return DenialReason::AuditTokenProcessMismatch;
    }
    if (stored.generation != context.generation) {
        return DenialReason::AuditTokenGenerationMismatch;
    }

    const auto floor = generation_floor_.find(context.process_id);
    if (floor != generation_floor_.end() && context.generation < floor->second) {
        return DenialReason::GenerationMismatch;
    }
    return DenialReason::Allowed;
}

Decision PermissionBroker::request(const PermissionRequest& request,
                                   std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    const RequestContext& context = request;
    const ExecutionMode mode = effective_mode(context);
    std::vector<CallChainEntry> chain = request.call_chain;

    const auto reject = [&](DenialReason reason, std::string detail,
                            std::optional<Grant> grant = std::nullopt) {
        Decision decision =
            Decision::deny(reason, std::move(detail), evaluated_at, std::move(grant), chain);

        AuditRecord record;
        record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
        record.at = evaluated_at;
        record.event = AuditEvent::GrantDenied;
        record.subject_id = context.subject.id;
        record.signer = context.subject.signer;
        if (decision.grant.has_value()) {
            record.grant_id = decision.grant->id;
        }
        record.audit_token_id = context.audit_token.value;
        record.process_id = context.process_id;
        record.generation = context.generation;
        record.process_kind = context.process_kind;
        record.execution_mode = mode;
        record.permissions = request.permissions;
        record.purpose = request.purpose;
        record.reason = reason;
        record.detail = decision.detail;
        record.call_chain = chain;
        audit_.push_back(std::move(record));
        return decision;
    };

    if (!verify_subject(context.subject)) {
        return reject(DenialReason::InvalidSubject,
                      "request does not carry a valid signed subject");
    }

    const DenialReason token_status = validate_context_token(context);
    if (token_status != DenialReason::Allowed) {
        return reject(token_status, "audit token binding does not match the request");
    }

    if (request.permissions.empty()) {
        return reject(DenialReason::EmptyPermissionSet,
                      "a grant must contain at least one permission");
    }
    if (request.purpose.empty()) {
        return reject(DenialReason::PurposeRequired, "a grant must carry a purpose");
    }
    if (!policy_.allowed_purposes.empty() &&
        !policy_.allowed_purposes.contains(request.purpose)) {
        return reject(DenialReason::PurposeMismatch,
                      "purpose is not registered with this broker");
    }

    if (context.process_kind == ProcessKind::ChildProcess && !policy_.allow_child_grants) {
        return reject(DenialReason::ChildGrantDisabled,
                      "this broker does not issue grants to child processes");
    }
    if (context.process_kind == ProcessKind::Plugin && !policy_.allow_plugin_grants) {
        return reject(DenialReason::PluginGrantDisabled,
                      "this broker does not issue grants to plugins");
    }
    if (mode == ExecutionMode::Background) {
        if (!policy_.allow_background_grants) {
            return reject(DenialReason::BackgroundGrantDisabled,
                          "background grants are disabled by policy");
        }
        if (policy_.background_allowed_permissions.has_value() &&
            !policy_.background_allowed_permissions->contains(request.permissions)) {
            return reject(DenialReason::BackgroundPermissionDenied,
                          "requested permissions are not eligible for background work");
        }
    }

    if (chain.size() > policy_.max_call_chain_depth) {
        return reject(DenialReason::CallChainInvalid, "call chain is too deep");
    }
    for (const CallChainEntry& entry : chain) {
        if (!is_valid_chain_entry(entry)) {
            return reject(DenialReason::CallChainInvalid,
                          "call chain contains an unsigned or unidentified subject");
        }
    }

    const bool requires_chain =
        (context.process_kind != ProcessKind::HostApplication &&
         policy_.require_call_chain_for_descendants) ||
        (mode == ExecutionMode::Background && policy_.require_call_chain_for_background);

    if (chain.empty()) {
        if (requires_chain) {
            return reject(DenialReason::CallChainRequired,
                          "descendant and background grants require a call chain");
        }
        chain.push_back(make_chain_entry(context, request.purpose, "grant-request"));
    } else {
        const bool requester_in_chain =
            std::any_of(chain.begin(), chain.end(), [&](const CallChainEntry& entry) {
                return entry_matches_context(entry, context);
            });
        if (!requester_in_chain) {
            chain.push_back(make_chain_entry(context, request.purpose, "grant-request"));
        }
    }

    if (chain.size() > policy_.max_call_chain_depth) {
        return reject(DenialReason::CallChainInvalid, "call chain is too deep");
    }
    if (requires_chain && chain.size() < 2) {
        return reject(DenialReason::CallChainRequired,
                      "descendant grant request does not show its caller");
    }

    // References in the chain are checked against the same broker state. This
    // prevents a plugin from inventing a grant id or from citing an expired
    // ancestor as its sponsor.
    for (const CallChainEntry& entry : chain) {
        if (entry.grant_id.empty()) {
            continue;
        }
        const auto referenced = grants_.find(entry.grant_id);
        if (referenced == grants_.end() || referenced->second.revoked ||
            referenced->second.is_expired(evaluated_at)) {
            return reject(DenialReason::CallChainInvalid,
                          "call chain cites an unknown, revoked, or expired grant");
        }
        if (referenced->second.subject.id != entry.subject_id ||
            referenced->second.subject.signer != entry.signer) {
            return reject(DenialReason::CallChainInvalid,
                          "call chain grant does not belong to the cited subject");
        }
    }

    if (!request.parent_grant_id.empty()) {
        const auto parent = grants_.find(request.parent_grant_id);
        if (parent == grants_.end()) {
            return reject(DenialReason::ParentGrantNotFound,
                          "named parent grant does not exist");
        }
        if (parent->second.revoked || parent->second.is_expired(evaluated_at)) {
            return reject(DenialReason::ParentGrantInvalid,
                          "named parent grant is revoked or expired");
        }
        if (!parent->second.subject.same_principal(context.subject)) {
            if (!policy_.allow_cross_subject_sponsorship) {
                return reject(DenialReason::CrossSubjectDelegation,
                              "cross-subject sponsorship is disabled by policy");
            }
            // The parent is provenance, not a capability. The child's fresh
            // grant remains bound to the child subject, but it still may not
            // acquire more permissions than the sponsor already held.
        }
        if (parent->second.execution_mode != mode) {
            return reject(DenialReason::ParentGrantInvalid,
                          "parent grant belongs to a different execution mode");
        }
        if (!policy_.allow_descendant_escalation &&
            !parent->second.permissions.contains(request.permissions)) {
            return reject(DenialReason::PermissionEscalation,
                          "descendant request exceeds its parent grant");
        }
    }

    // If a same-principal ancestor grant is named in the chain, it is also a
    // narrowing boundary even when parent_grant_id was omitted.
    if (!policy_.allow_descendant_escalation) {
        for (const CallChainEntry& entry : chain) {
            if (entry.grant_id.empty() || entry.subject_id != context.subject.id ||
                entry.signer != context.subject.signer) {
                continue;
            }
            const auto ancestor = grants_.find(entry.grant_id);
            if (ancestor != grants_.end() &&
                !ancestor->second.permissions.contains(request.permissions)) {
                return reject(DenialReason::PermissionEscalation,
                              "request exceeds a same-subject ancestor grant");
            }
        }

        // A call chain is not required to carry grant ids. For a same-subject
        // ancestor, use every live grant bound to that ancestor hop as the
        // permission envelope. This prevents an unannotated chain from
        // smuggling a broader child request past the narrowing rule.
        for (const CallChainEntry& entry : chain) {
            if (entry.subject_id != context.subject.id ||
                entry.signer != context.subject.signer) {
                continue;
            }
            PermissionSet ancestor_permissions;
            bool found_ancestor_grant = false;
            for (const auto& [id, candidate] : grants_) {
                (void)id;
                if (candidate.revoked || candidate.is_expired(evaluated_at) ||
                    candidate.subject.id != entry.subject_id ||
                    candidate.subject.signer != entry.signer ||
                    candidate.process_id != entry.process_id ||
                    candidate.generation != entry.generation ||
                    candidate.process_kind != entry.process_kind ||
                    candidate.execution_mode != entry.execution_mode) {
                    continue;
                }
                ancestor_permissions |= candidate.permissions;
                found_ancestor_grant = true;
            }
            if (found_ancestor_grant &&
                !ancestor_permissions.contains(request.permissions)) {
                return reject(DenialReason::PermissionEscalation,
                              "request exceeds the same-subject ancestor permission envelope");
            }
        }
    }

    Grant new_grant;
    new_grant.id = make_grant_id();
    new_grant.subject = context.subject;
    new_grant.audit_token_id = context.audit_token.value;
    new_grant.process_id = context.process_id;
    new_grant.generation = context.generation;
    new_grant.process_kind = context.process_kind;
    new_grant.execution_mode = mode;
    new_grant.permissions = request.permissions;
    new_grant.purpose = request.purpose;
    new_grant.issued_at = evaluated_at;
    new_grant.call_chain = chain;
    new_grant.parent_grant_id = request.parent_grant_id;

    if (request.ttl.has_value()) {
        if (!policy_.allow_temporary_grants) {
            return reject(DenialReason::TemporaryGrantDisabled,
                          "temporary grants are disabled by policy");
        }
        if (*request.ttl < policy_.min_temporary_ttl ||
            *request.ttl > policy_.max_temporary_ttl) {
            return reject(DenialReason::InvalidTtl,
                          "temporary grant lifetime is outside policy bounds");
        }
        new_grant.temporary = true;
        new_grant.expires_at = evaluated_at + *request.ttl;
    }

    grants_.emplace(new_grant.id, new_grant);

    AuditRecord record;
    record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
    record.at = evaluated_at;
    record.event = AuditEvent::GrantIssued;
    record.subject_id = context.subject.id;
    record.signer = context.subject.signer;
    record.grant_id = new_grant.id;
    record.audit_token_id = context.audit_token.value;
    record.process_id = context.process_id;
    record.generation = context.generation;
    record.process_kind = context.process_kind;
    record.execution_mode = mode;
    record.permissions = new_grant.permissions;
    record.purpose = new_grant.purpose;
    record.reason = DenialReason::Allowed;
    record.detail = new_grant.temporary ? "temporary grant issued" : "grant issued";
    record.call_chain = new_grant.call_chain;
    audit_.push_back(std::move(record));

    return Decision::allow(new_grant, new_grant.call_chain, evaluated_at);
}

Decision PermissionBroker::evaluate_bound_grant(
    const Grant& grant, const RequestContext& context, PermissionSet permissions,
    std::string_view purpose, TimePoint now, bool check_token_store,
    bool check_revocation) const {
    if (policy_.verify_subject_on_access && !verify_subject(context.subject)) {
        return Decision::deny(DenialReason::InvalidSubject,
                              "access context does not carry a valid signed subject", now,
                              grant, grant.call_chain);
    }

    if (check_token_store) {
        const DenialReason token_status = validate_context_token(context);
        if (token_status != DenialReason::Allowed) {
            return Decision::deny(token_status,
                                  "audit token does not bind this access context", now,
                                  grant, grant.call_chain);
        }
    } else {
        // inspect_grant() does not have the broker's token store, but a grant
        // obtained from a trusted store must still be internally consistent.
        if (context.audit_token.subject_id != context.subject.id ||
            context.audit_token.signer != context.subject.signer) {
            return Decision::deny(DenialReason::AuditTokenSubjectMismatch,
                                  "token fields do not bind the claimed subject", now,
                                  grant, grant.call_chain);
        }
        if (context.audit_token.process_id != context.process_id) {
            return Decision::deny(DenialReason::AuditTokenProcessMismatch,
                                  "token fields do not bind the claimed process", now,
                                  grant, grant.call_chain);
        }
        if (context.audit_token.generation != context.generation) {
            return Decision::deny(DenialReason::AuditTokenGenerationMismatch,
                                  "token fields do not bind the claimed generation", now,
                                  grant, grant.call_chain);
        }
    }

    if (grant.revoked) {
        return Decision::deny(DenialReason::GrantRevoked, "grant has been revoked", now,
                              grant, grant.call_chain);
    }
    if (check_revocation) {
        const auto stored = grants_.find(grant.id);
        if (stored != grants_.end() && stored->second.revoked) {
            return Decision::deny(DenialReason::GrantRevoked, "grant has been revoked", now,
                                  grant, grant.call_chain);
        }
    }

    if (policy_.verify_grant_subject_on_access && !verify_subject(grant.subject)) {
        return Decision::deny(DenialReason::InvalidSubject,
                              "grant is bound to an invalid signed subject", now, grant,
                              grant.call_chain);
    }

    if (!grant.subject.same_principal(context.subject)) {
        DenialReason reason = DenialReason::SubjectMismatch;
        if (grant.process_kind == ProcessKind::HostApplication &&
            context.process_kind == ProcessKind::Plugin) {
            reason = DenialReason::PluginInheritanceDenied;
        } else if (grant.process_kind == ProcessKind::HostApplication &&
                   context.process_kind == ProcessKind::ChildProcess) {
            reason = DenialReason::ChildInheritanceDenied;
        } else if (grant.process_kind != context.process_kind) {
            reason = DenialReason::InheritanceDenied;
        }
        return Decision::deny(reason,
                              "grant is bound to a different signed subject", now, grant,
                              grant.call_chain);
    }

    if (grant.process_kind != context.process_kind) {
        DenialReason reason = DenialReason::ProcessKindMismatch;
        if (grant.process_kind == ProcessKind::HostApplication &&
            context.process_kind == ProcessKind::Plugin) {
            reason = DenialReason::PluginInheritanceDenied;
        } else if (grant.process_kind == ProcessKind::HostApplication &&
                   context.process_kind == ProcessKind::ChildProcess) {
            reason = DenialReason::ChildInheritanceDenied;
        } else {
            reason = DenialReason::InheritanceDenied;
        }
        return Decision::deny(reason, "process kind must obtain its own grant", now, grant,
                              grant.call_chain);
    }

    if (grant.audit_token_id != context.audit_token.value) {
        return Decision::deny(DenialReason::AuditTokenGrantMismatch,
                              "grant and access context use different audit tokens", now,
                              grant, grant.call_chain);
    }
    if (grant.process_id != context.process_id) {
        return Decision::deny(DenialReason::ProcessMismatch,
                              "grant belongs to a different process", now, grant,
                              grant.call_chain);
    }
    if (grant.generation != context.generation) {
        return Decision::deny(DenialReason::GenerationMismatch,
                              "grant belongs to a different process generation", now, grant,
                              grant.call_chain);
    }
    if (grant.is_background() != is_background(context)) {
        return Decision::deny(DenialReason::BackgroundMismatch,
                              "foreground and background grants are evaluated separately",
                              now, grant, grant.call_chain);
    }
    if (purpose.empty() || grant.purpose.empty()) {
        return Decision::deny(DenialReason::PurposeRequired,
                              "access checks must provide the grant purpose", now, grant,
                              grant.call_chain);
    }
    if (grant.purpose != purpose) {
        return Decision::deny(DenialReason::PurposeMismatch,
                              "purpose does not match the bound grant", now, grant,
                              grant.call_chain);
    }
    if (permissions.empty()) {
        return Decision::deny(DenialReason::EmptyPermissionSet,
                              "access check requested no permissions", now, grant,
                              grant.call_chain);
    }
    if (grant.temporary && !grant.expires_at.has_value()) {
        return Decision::deny(DenialReason::ExpiryRequired,
                              "temporary grant has no expiration deadline", now, grant,
                              grant.call_chain);
    }
    if (grant.is_expired(now)) {
        return Decision::deny(DenialReason::Expired, "grant has expired", now, grant,
                              grant.call_chain);
    }
    if (!grant.permissions.contains(permissions)) {
        return Decision::deny(DenialReason::PermissionNotGranted,
                              "requested permission is outside the grant", now, grant,
                              grant.call_chain);
    }

    return Decision::allow(grant, grant.call_chain, now);
}

Decision PermissionBroker::check(std::string_view grant_id, const RequestContext& context,
                                 PermissionSet permissions, std::string_view purpose,
                                 std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    const auto iterator = grants_.find(std::string(grant_id));
    if (iterator == grants_.end()) {
        Decision decision = Decision::deny(DenialReason::GrantNotFound,
                                           "grant id is not known to this broker",
                                           evaluated_at, std::nullopt, context.call_chain);
        AuditRecord record;
        record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
        record.at = evaluated_at;
        record.event = AuditEvent::AccessDenied;
        record.subject_id = context.subject.id;
        record.signer = context.subject.signer;
        record.grant_id = std::string(grant_id);
        record.audit_token_id = context.audit_token.value;
        record.process_id = context.process_id;
        record.generation = context.generation;
        record.process_kind = context.process_kind;
        record.execution_mode = effective_mode(context);
        record.permissions = permissions;
        record.purpose = std::string(purpose);
        record.reason = decision.reason;
        record.detail = decision.detail;
        record.call_chain = context.call_chain;
        audit_.push_back(std::move(record));
        return decision;
    }

    Decision decision = evaluate_bound_grant(iterator->second, context, permissions, purpose,
                                             evaluated_at, true, true);

    AuditRecord record;
    record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
    record.at = evaluated_at;
    record.event = decision.allowed ? AuditEvent::AccessGranted : AuditEvent::AccessDenied;
    record.subject_id = context.subject.id;
    record.signer = context.subject.signer;
    record.grant_id = iterator->second.id;
    record.audit_token_id = context.audit_token.value;
    record.process_id = context.process_id;
    record.generation = context.generation;
    record.process_kind = context.process_kind;
    record.execution_mode = effective_mode(context);
    record.permissions = permissions;
    record.purpose = std::string(purpose);
    record.reason = decision.reason;
    record.detail = decision.detail;
    record.call_chain = decision.call_chain;
    audit_.push_back(std::move(record));
    return decision;
}

Decision PermissionBroker::authorize(const RequestContext& context,
                                     PermissionSet permissions, std::string_view purpose,
                                     std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    std::optional<Decision> best_denial;
    std::optional<Grant> best_grant;
    int best_score = -1;

    if (policy_.verify_subject_on_access && !verify_subject(context.subject)) {
        best_denial = Decision::deny(DenialReason::InvalidSubject,
                                     "access context does not carry a valid signed subject",
                                     evaluated_at, std::nullopt, context.call_chain);
    } else {
        const DenialReason token_status = validate_context_token(context);
        if (token_status != DenialReason::Allowed) {
            best_denial = Decision::deny(token_status,
                                         "audit token does not bind this access context",
                                         evaluated_at, std::nullopt, context.call_chain);
        }
    }

    if (!best_denial.has_value()) {
        if (permissions.empty()) {
            best_denial = Decision::deny(DenialReason::EmptyPermissionSet,
                                         "access check requested no permissions",
                                         evaluated_at, std::nullopt, context.call_chain);
        } else if (purpose.empty()) {
            best_denial = Decision::deny(DenialReason::PurposeRequired,
                                         "access check must provide a purpose",
                                         evaluated_at, std::nullopt, context.call_chain);
        }
    }

    if (!best_denial.has_value()) {
        for (const auto& [id, grant] : grants_) {
            (void)id;
            Decision candidate =
                evaluate_bound_grant(grant, context, permissions, purpose, evaluated_at, true,
                                     true);
            if (candidate.allowed) {
                best_denial.reset();
                best_grant = grant;
                break;
            }

            const int score = grant_match_score(grant, context);
            if (score > best_score) {
                best_score = score;
                best_denial = std::move(candidate);
                best_grant = grant;
            }
        }
    }

    if (!best_denial.has_value() && !best_grant.has_value()) {
        best_denial =
            Decision::deny(DenialReason::GrantNotFound,
                           "no grant matches this subject and process", evaluated_at,
                           std::nullopt, context.call_chain);
    }

    if (!best_denial.has_value()) {
        Decision decision =
            best_grant.has_value()
                ? Decision::allow(*best_grant, best_grant->call_chain, evaluated_at)
                : Decision::deny(DenialReason::GrantNotFound,
                                 "no grant matches this subject and process", evaluated_at,
                                 std::nullopt, context.call_chain);

        AuditRecord record;
        record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
        record.at = evaluated_at;
        record.event = AuditEvent::AccessGranted;
        record.subject_id = context.subject.id;
        record.signer = context.subject.signer;
        if (best_grant.has_value()) {
            record.grant_id = best_grant->id;
        }
        record.audit_token_id = context.audit_token.value;
        record.process_id = context.process_id;
        record.generation = context.generation;
        record.process_kind = context.process_kind;
        record.execution_mode = effective_mode(context);
        record.permissions = permissions;
        record.purpose = std::string(purpose);
        record.reason = DenialReason::Allowed;
        record.detail = decision.detail;
        record.call_chain = decision.call_chain;
        audit_.push_back(std::move(record));
        return decision;
    }

    if (!best_grant.has_value() &&
        best_denial->reason == DenialReason::NotEvaluated) {
        best_denial->reason = DenialReason::GrantNotFound;
    }

    AuditRecord record;
    record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
    record.at = evaluated_at;
    record.event = AuditEvent::AccessDenied;
    record.subject_id = context.subject.id;
    record.signer = context.subject.signer;
    if (best_grant.has_value()) {
        record.grant_id = best_grant->id;
    }
    record.audit_token_id = context.audit_token.value;
    record.process_id = context.process_id;
    record.generation = context.generation;
    record.process_kind = context.process_kind;
    record.execution_mode = effective_mode(context);
    record.permissions = permissions;
    record.purpose = std::string(purpose);
    record.reason = best_denial->reason;
    record.detail = best_denial->detail;
    record.call_chain = context.call_chain;
    audit_.push_back(std::move(record));
    return *best_denial;
}

Decision PermissionBroker::inspect_grant(const Grant& grant,
                                         const RequestContext& context,
                                         PermissionSet permissions,
                                         std::string_view purpose,
                                         std::optional<TimePoint> now) const {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);
    return evaluate_bound_grant(grant, context, permissions, purpose, evaluated_at, false,
                                false);
}

std::optional<Grant> PermissionBroker::find_grant(std::string_view grant_id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto iterator = grants_.find(std::string(grant_id));
    if (iterator == grants_.end()) {
        return std::nullopt;
    }
    return iterator->second;
}

std::vector<Grant> PermissionBroker::grants() const {
    std::lock_guard<std::mutex> lock(mutex_);
    std::vector<Grant> result;
    result.reserve(grants_.size());
    for (const auto& [id, grant] : grants_) {
        (void)id;
        result.push_back(grant);
    }
    return result;
}

bool PermissionBroker::revoke(std::string_view grant_id,
                              std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    const auto iterator = grants_.find(std::string(grant_id));
    if (iterator == grants_.end()) {
        return false;
    }

    const bool changed = !iterator->second.revoked;
    iterator->second.revoked = true;

    if (changed) {
        AuditRecord record;
        record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
        record.at = evaluated_at;
        record.event = AuditEvent::GrantRevoked;
        record.subject_id = iterator->second.subject.id;
        record.signer = iterator->second.subject.signer;
        record.grant_id = iterator->second.id;
        record.audit_token_id = iterator->second.audit_token_id;
        record.process_id = iterator->second.process_id;
        record.generation = iterator->second.generation;
        record.process_kind = iterator->second.process_kind;
        record.execution_mode = iterator->second.execution_mode;
        record.permissions = iterator->second.permissions;
        record.purpose = iterator->second.purpose;
        record.reason = DenialReason::GrantRevoked;
        record.detail = "grant revoked";
        record.call_chain = iterator->second.call_chain;
        audit_.push_back(std::move(record));
    }
    return true;
}

std::size_t PermissionBroker::revoke_subject(std::string_view subject_id,
                                             std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    std::size_t count = 0;
    for (auto& [id, grant] : grants_) {
        (void)id;
        if (grant.subject.id != subject_id || grant.revoked) {
            continue;
        }
        grant.revoked = true;
        ++count;

        AuditRecord record;
        record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
        record.at = evaluated_at;
        record.event = AuditEvent::GrantRevoked;
        record.subject_id = grant.subject.id;
        record.signer = grant.subject.signer;
        record.grant_id = grant.id;
        record.audit_token_id = grant.audit_token_id;
        record.process_id = grant.process_id;
        record.generation = grant.generation;
        record.process_kind = grant.process_kind;
        record.execution_mode = grant.execution_mode;
        record.permissions = grant.permissions;
        record.purpose = grant.purpose;
        record.reason = DenialReason::GrantRevoked;
        record.detail = "all grants for subject revoked";
        record.call_chain = grant.call_chain;
        audit_.push_back(std::move(record));
    }
    return count;
}

std::size_t PermissionBroker::revoke_process(ProcessId process_id,
                                             std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    std::size_t count = 0;
    for (auto& [id, grant] : grants_) {
        (void)id;
        if (grant.process_id != process_id || grant.revoked) {
            continue;
        }
        grant.revoked = true;
        ++count;

        AuditRecord record;
        record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
        record.at = evaluated_at;
        record.event = AuditEvent::GrantRevoked;
        record.subject_id = grant.subject.id;
        record.signer = grant.subject.signer;
        record.grant_id = grant.id;
        record.audit_token_id = grant.audit_token_id;
        record.process_id = grant.process_id;
        record.generation = grant.generation;
        record.process_kind = grant.process_kind;
        record.execution_mode = grant.execution_mode;
        record.permissions = grant.permissions;
        record.purpose = grant.purpose;
        record.reason = DenialReason::GrantRevoked;
        record.detail = "all grants for process revoked";
        record.call_chain = grant.call_chain;
        audit_.push_back(std::move(record));
    }
    return count;
}

Generation PermissionBroker::advance_generation(ProcessId process_id,
                                                Generation generation,
                                                std::optional<TimePoint> now) {
    const TimePoint evaluated_at = resolve_now(now);
    std::lock_guard<std::mutex> lock(mutex_);

    auto [iterator, inserted] = generation_floor_.emplace(process_id, generation);
    if (!inserted && generation < iterator->second) {
        return iterator->second;
    }
    iterator->second = generation;

    std::size_t revoked = 0;
    for (auto& [id, grant] : grants_) {
        (void)id;
        if (grant.process_id == process_id && grant.generation < generation &&
            !grant.revoked) {
            grant.revoked = true;
            ++revoked;
        }
    }

    AuditRecord record;
    record.sequence = static_cast<std::uint64_t>(audit_.size()) + 1;
    record.at = evaluated_at;
    record.event = AuditEvent::GenerationAdvanced;
    record.process_id = process_id;
    record.generation = generation;
    record.reason = DenialReason::Allowed;
    record.detail = "advanced process generation and revoked " + std::to_string(revoked) +
                    " older grant(s)";
    audit_.push_back(std::move(record));

    return iterator->second;
}

Generation PermissionBroker::current_generation(ProcessId process_id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto iterator = generation_floor_.find(process_id);
    return iterator == generation_floor_.end() ? 0 : iterator->second;
}

std::vector<AuditRecord> PermissionBroker::audit_trail() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return audit_;
}

std::size_t PermissionBroker::audit_size() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return audit_.size();
}

void PermissionBroker::clear_audit_trail() {
    std::lock_guard<std::mutex> lock(mutex_);
    audit_.clear();
}

std::string PermissionBroker::format_call_chain(
    const std::vector<CallChainEntry>& chain) {
    if (chain.empty()) {
        return "<empty>";
    }

    std::ostringstream stream;
    for (std::size_t index = 0; index < chain.size(); ++index) {
        if (index != 0) {
            stream << " -> ";
        }
        const CallChainEntry& entry = chain[index];
        stream << principal_text(entry.subject_id, entry.signer) << "("
               << to_string(entry.process_kind) << ",pid=" << entry.process_id
               << ",gen=" << entry.generation;
        if (!entry.operation.empty()) {
            stream << "," << entry.operation;
        }
        if (!entry.grant_id.empty()) {
            stream << "," << entry.grant_id;
        }
        stream << ")";
    }
    return stream.str();
}

}  // namespace netsec

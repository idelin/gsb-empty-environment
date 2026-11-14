#pragma once
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <initializer_list>
#include <mutex>
#include <optional>
#include <set>
#include <string>
#include <string_view>
#include <unordered_map>
#include <utility>
#include <vector>

namespace netsec {

using TimePoint = std::chrono::system_clock::time_point;
using Duration = std::chrono::milliseconds;
using ProcessId = std::uint64_t;
using Generation = std::uint64_t;

// A permission is intentionally a coarse capability. File access is split
// into read/write so that a grant cannot silently acquire a stronger file
// capability than the caller asked for.
enum class Permission : std::uint32_t {
    Camera = 1u << 0,
    Microphone = 1u << 1,
    FileRead = 1u << 2,
    FileWrite = 1u << 3,
    Location = 1u << 4,
    Network = 1u << 5,
    Clipboard = 1u << 6,
    Contacts = 1u << 7,
    Calendar = 1u << 8,
    ProcessControl = 1u << 9,
};

std::string_view to_string(Permission permission) noexcept;

class PermissionSet {
public:
    constexpr PermissionSet() noexcept = default;

    constexpr PermissionSet(Permission permission) noexcept
        : bits_(static_cast<std::uint32_t>(permission)) {}

    constexpr PermissionSet(std::initializer_list<Permission> permissions) noexcept {
        for (Permission permission : permissions) {
            bits_ |= static_cast<std::uint32_t>(permission);
        }
    }

    explicit constexpr PermissionSet(std::uint32_t bits) noexcept : bits_(bits) {}

    [[nodiscard]] constexpr std::uint32_t bits() const noexcept { return bits_; }
    [[nodiscard]] constexpr bool empty() const noexcept { return bits_ == 0; }
    [[nodiscard]] constexpr explicit operator bool() const noexcept { return bits_ != 0; }

    [[nodiscard]] constexpr bool contains(Permission permission) const noexcept {
        const auto bit = static_cast<std::uint32_t>(permission);
        return bit != 0 && (bits_ & bit) == bit;
    }

    [[nodiscard]] constexpr bool contains(PermissionSet other) const noexcept {
        return (bits_ & other.bits_) == other.bits_;
    }

    [[nodiscard]] constexpr bool operator==(const PermissionSet&) const noexcept = default;

    constexpr PermissionSet& operator|=(PermissionSet other) noexcept {
        bits_ |= other.bits_;
        return *this;
    }

    constexpr PermissionSet& operator&=(PermissionSet other) noexcept {
        bits_ &= other.bits_;
        return *this;
    }

    [[nodiscard]] constexpr PermissionSet operator|(PermissionSet other) const noexcept {
        return PermissionSet(bits_ | other.bits_);
    }

    [[nodiscard]] constexpr PermissionSet operator&(PermissionSet other) const noexcept {
        return PermissionSet(bits_ & other.bits_);
    }

    [[nodiscard]] constexpr PermissionSet operator~() const noexcept {
        return PermissionSet(~bits_ & known_bits());
    }

    [[nodiscard]] static constexpr std::uint32_t known_bits() noexcept {
        return (1u << 10) - 1u;
    }

    [[nodiscard]] static constexpr PermissionSet all() noexcept {
        return PermissionSet(known_bits());
    }

private:
    std::uint32_t bits_ = 0;
};

std::string to_string(PermissionSet permissions);

// A subject is the code-signing principal, not a process or a PID. The
// signature is normally produced by the platform's code-signing service. A
// caller may install a SubjectVerifier to check the attestation cryptographically.
struct SignedSubject {
    std::string id;
    std::string signer;
    std::string signature;
    std::string display_name;

    [[nodiscard]] bool same_principal(const SignedSubject& other) const noexcept {
        return id == other.id && signer == other.signer;
    }

    [[nodiscard]] bool empty() const noexcept {
        return id.empty() || signer.empty() || signature.empty();
    }

    bool operator==(const SignedSubject&) const = default;
};

using Subject = SignedSubject;
using SubjectVerifier = std::function<bool(const SignedSubject&)>;

[[nodiscard]] SignedSubject make_signed_subject(
    std::string id,
    std::string signer = "trusted-signer",
    std::string signature = "attested",
    std::string display_name = {});

// An opaque audit token is issued by the broker and stored server-side. Its
// fields are informative; authorization always uses the broker's stored copy,
// so mutating a token in transit cannot change its binding.
struct AuditToken {
    std::string value;
    std::string subject_id;
    std::string signer;
    ProcessId process_id = 0;
    Generation generation = 0;

    [[nodiscard]] bool empty() const noexcept { return value.empty(); }
    bool operator==(const AuditToken&) const = default;
};

enum class ProcessKind : std::uint8_t {
    HostApplication,
    Host = HostApplication,
    ChildProcess,
    Child = ChildProcess,
    Plugin,
    BackgroundTask,
    Background = BackgroundTask,
};

enum class ExecutionMode : std::uint8_t {
    Foreground,
    Background,
};

std::string_view to_string(ProcessKind kind) noexcept;
std::string_view to_string(ExecutionMode mode) noexcept;

struct CallChainEntry {
    std::string subject_id;
    std::string signer;
    std::string operation;
    ProcessId process_id = 0;
    Generation generation = 0;
    ProcessKind process_kind = ProcessKind::HostApplication;
    ExecutionMode execution_mode = ExecutionMode::Foreground;
    std::string grant_id;
    std::string purpose;
};

struct RequestContext {
    SignedSubject subject;
    AuditToken audit_token;
    ProcessId process_id = 0;
    Generation generation = 0;
    ProcessKind process_kind = ProcessKind::HostApplication;
    ExecutionMode execution_mode = ExecutionMode::Foreground;
    std::vector<CallChainEntry> call_chain;
};

// A request is a fresh request. Calling request() never inherits a grant: the
// issued grant is bound to the exact token, process generation, kind, mode and
// purpose in this request.
struct PermissionRequest : RequestContext {
    PermissionSet permissions;
    std::string purpose;
    std::optional<Duration> ttl;
    std::string parent_grant_id;
};

struct Grant {
    std::string id;
    SignedSubject subject;
    std::string audit_token_id;
    ProcessId process_id = 0;
    Generation generation = 0;
    ProcessKind process_kind = ProcessKind::HostApplication;
    ExecutionMode execution_mode = ExecutionMode::Foreground;
    PermissionSet permissions;
    std::string purpose;
    TimePoint issued_at{};
    std::optional<TimePoint> expires_at;
    bool temporary = false;
    std::vector<CallChainEntry> call_chain;
    std::string parent_grant_id;
    bool revoked = false;

    [[nodiscard]] bool is_expired(TimePoint now) const noexcept {
        return expires_at.has_value() && now >= *expires_at;
    }

    [[nodiscard]] bool is_background() const noexcept {
        return execution_mode == ExecutionMode::Background ||
               process_kind == ProcessKind::BackgroundTask;
    }
};

enum class DenialReason : std::uint16_t {
    Allowed = 0,
    None = Allowed,
    NotEvaluated,
    InvalidSubject,
    InvalidAuditToken,
    AuditTokenSubjectMismatch,
    AuditTokenProcessMismatch,
    AuditTokenGenerationMismatch,
    AuditTokenGrantMismatch,
    GrantNotFound,
    GrantRevoked,
    SubjectMismatch,
    ProcessMismatch,
    GenerationMismatch,
    ProcessKindMismatch,
    InheritanceDenied,
    ChildInheritanceDenied,
    PluginInheritanceDenied,
    BackgroundMismatch,
    BackgroundGrantDisabled,
    BackgroundPermissionDenied,
    PurposeRequired,
    PurposeMismatch,
    PermissionNotGranted,
    ExpiryRequired,
    Expired,
    TemporaryGrantDisabled,
    InvalidTtl,
    CallChainRequired,
    CallChainInvalid,
    ParentGrantNotFound,
    ParentGrantInvalid,
    CrossSubjectDelegation,
    PermissionEscalation,
    ChildGrantDisabled,
    PluginGrantDisabled,
    EmptyPermissionSet,
};

std::string_view to_string(DenialReason reason) noexcept;

struct Decision {
    bool allowed = false;
    DenialReason reason = DenialReason::NotEvaluated;
    std::string detail;
    std::optional<Grant> grant;
    std::vector<CallChainEntry> call_chain;
    TimePoint evaluated_at{};

    [[nodiscard]] bool ok() const noexcept { return allowed; }
    [[nodiscard]] explicit operator bool() const noexcept { return allowed; }

    [[nodiscard]] static Decision allow(Grant grant, std::vector<CallChainEntry> chain,
                                        TimePoint now);
    [[nodiscard]] static Decision deny(DenialReason reason, std::string detail,
                                       TimePoint now,
                                       std::optional<Grant> grant = std::nullopt,
                                       std::vector<CallChainEntry> chain = {});
};

enum class AuditEvent : std::uint8_t {
    TokenIssued,
    GrantIssued,
    GrantDenied,
    AccessGranted,
    AccessDenied,
    GrantRevoked,
    GenerationAdvanced,
};

std::string_view to_string(AuditEvent event) noexcept;

struct AuditRecord {
    std::uint64_t sequence = 0;
    TimePoint at{};
    AuditEvent event = AuditEvent::GrantDenied;
    std::string subject_id;
    std::string signer;
    std::string grant_id;
    std::string audit_token_id;
    ProcessId process_id = 0;
    Generation generation = 0;
    ProcessKind process_kind = ProcessKind::HostApplication;
    ExecutionMode execution_mode = ExecutionMode::Foreground;
    PermissionSet permissions;
    std::string purpose;
    DenialReason reason = DenialReason::NotEvaluated;
    std::string detail;
    std::vector<CallChainEntry> call_chain;
};

struct BrokerPolicy {
    bool require_signed_subject = true;
    std::set<std::string> trusted_signers;

    bool allow_child_grants = true;
    bool allow_plugin_grants = true;
    bool allow_background_grants = true;

    bool require_call_chain_for_descendants = true;
    bool require_call_chain_for_background = false;
    std::size_t max_call_chain_depth = 64;

    // A descendant may name the grant that sponsored it. The request can only
    // narrow that permission set unless the policy explicitly opts in to
    // escalation. A different signed principal may use the parent grant as
    // provenance for a fresh grant, but that fresh grant is still bound to the
    // descendant's own subject; the parent grant itself is never inherited.
    bool allow_descendant_escalation = false;
    bool allow_cross_subject_sponsorship = true;

    bool allow_temporary_grants = true;
    Duration min_temporary_ttl = std::chrono::milliseconds(1);
    Duration max_temporary_ttl = std::chrono::hours(1);

    // Empty means all purposes are acceptable. When non-empty the purpose must
    // be registered before a grant can be created.
    std::set<std::string> allowed_purposes;

    // std::nullopt means all permissions are eligible in background mode; the
    // grant is still a separate, mode-bound grant.
    std::optional<PermissionSet> background_allowed_permissions;

    bool verify_subject_on_access = true;
    bool verify_grant_subject_on_access = true;
};

class PermissionBroker {
public:
    PermissionBroker();
    explicit PermissionBroker(BrokerPolicy policy);
    PermissionBroker(BrokerPolicy policy, SubjectVerifier verifier);

    // Configure these before sharing the broker between threads.
    void set_clock(std::function<TimePoint()> clock);
    void set_subject_verifier(SubjectVerifier verifier);

    [[nodiscard]] const BrokerPolicy& policy() const noexcept { return policy_; }

    // Issues the opaque token that binds subject, PID and process generation.
    // An empty token is returned when the subject is not valid or the
    // generation has already been superseded.
    [[nodiscard]] AuditToken issue_audit_token(const SignedSubject& subject,
                                               ProcessId process_id,
                                               Generation generation);
    [[nodiscard]] AuditToken issueAuditToken(const SignedSubject& subject,
                                             ProcessId process_id,
                                             Generation generation) {
        return issue_audit_token(subject, process_id, generation);
    }

    [[nodiscard]] bool is_audit_token_valid(const AuditToken& token) const;

    // Issues a new grant. This is an explicit authorization operation: it does
    // not copy any parent grant. Temporary grants have a purpose and deadline.
    [[nodiscard]] Decision request(const PermissionRequest& request,
                                   std::optional<TimePoint> now = std::nullopt);
    [[nodiscard]] Decision requestAccess(const PermissionRequest& req,
                                         std::optional<TimePoint> now = std::nullopt) {
        return request(req, now);
    }
    [[nodiscard]] Decision grant(const PermissionRequest& req,
                                 std::optional<TimePoint> now = std::nullopt) {
        return request(req, now);
    }

    // Checks a particular grant. This is the API to use when a caller presents
    // a grant id: all of the grant's bindings are compared with the live context.
    [[nodiscard]] Decision check(std::string_view grant_id,
                                 const RequestContext& context,
                                 PermissionSet permissions,
                                 std::string_view purpose,
                                 std::optional<TimePoint> now = std::nullopt);
    [[nodiscard]] Decision check(std::string_view grant_id,
                                 const RequestContext& context,
                                 Permission permission,
                                 std::string_view purpose,
                                 std::optional<TimePoint> now = std::nullopt) {
        return check(grant_id, context, PermissionSet(permission), purpose, now);
    }

    // Finds a live grant for the exact context. It is never enough to match
    // only a subject or only a PID.
    [[nodiscard]] Decision authorize(const RequestContext& context,
                                     PermissionSet permissions,
                                     std::string_view purpose,
                                     std::optional<TimePoint> now = std::nullopt);
    [[nodiscard]] Decision authorize(const RequestContext& context,
                                     Permission permission,
                                     std::string_view purpose,
                                     std::optional<TimePoint> now = std::nullopt) {
        return authorize(context, PermissionSet(permission), purpose, now);
    }
    [[nodiscard]] Decision check(const RequestContext& context,
                                 PermissionSet permissions,
                                 std::string_view purpose,
                                 std::optional<TimePoint> now = std::nullopt) {
        return authorize(context, permissions, purpose, now);
    }
    [[nodiscard]] Decision verifyAccess(const RequestContext& context,
                                        PermissionSet permissions,
                                        std::string_view purpose,
                                        std::optional<TimePoint> now = std::nullopt) {
        return authorize(context, permissions, purpose, now);
    }

    // Local consistency check for a grant obtained from a trusted store. The
    // broker still recommends check(grant.id, ...), which also verifies the
    // token was issued here and that the grant has not been revoked.
    [[nodiscard]] Decision inspect_grant(const Grant& grant,
                                         const RequestContext& context,
                                         PermissionSet permissions,
                                         std::string_view purpose,
                                         std::optional<TimePoint> now = std::nullopt) const;
    [[nodiscard]] Decision check(const Grant& grant,
                                 const RequestContext& context,
                                 PermissionSet permissions,
                                 std::string_view purpose,
                                 std::optional<TimePoint> now = std::nullopt) const {
        return inspect_grant(grant, context, permissions, purpose, now);
    }

    [[nodiscard]] std::optional<Grant> find_grant(std::string_view grant_id) const;
    [[nodiscard]] std::vector<Grant> grants() const;

    [[nodiscard]] bool revoke(std::string_view grant_id,
                              std::optional<TimePoint> now = std::nullopt);
    [[nodiscard]] std::size_t revoke_subject(std::string_view subject_id,
                                             std::optional<TimePoint> now = std::nullopt);
    [[nodiscard]] std::size_t revoke_process(ProcessId process_id,
                                             std::optional<TimePoint> now = std::nullopt);

    // Marks a new process generation. Grants and tokens from older generations
    // cannot be used again, even if the PID is reused.
    [[nodiscard]] Generation advance_generation(ProcessId process_id,
                                                Generation generation,
                                                std::optional<TimePoint> now = std::nullopt);
    [[nodiscard]] Generation current_generation(ProcessId process_id) const;

    [[nodiscard]] std::vector<AuditRecord> audit_trail() const;
    [[nodiscard]] std::size_t audit_size() const;
    void clear_audit_trail();

    [[nodiscard]] static std::string format_call_chain(
        const std::vector<CallChainEntry>& chain);

private:
    [[nodiscard]] TimePoint resolve_now(std::optional<TimePoint> now) const;
    [[nodiscard]] bool verify_subject(const SignedSubject& subject) const;
    [[nodiscard]] DenialReason validate_context_token(const RequestContext& context) const;
    [[nodiscard]] Decision evaluate_bound_grant(
        const Grant& grant, const RequestContext& context, PermissionSet permissions,
        std::string_view purpose, TimePoint now, bool check_token_store,
        bool check_revocation) const;

    BrokerPolicy policy_;
    SubjectVerifier verifier_;
    std::function<TimePoint()> clock_;

    mutable std::mutex mutex_;
    std::unordered_map<std::string, AuditToken> tokens_;
    std::unordered_map<std::string, Grant> grants_;
    std::unordered_map<ProcessId, Generation> generation_floor_;
    std::vector<AuditRecord> audit_;
};

using PermissionManager = PermissionBroker;
using AuthorizationBroker = PermissionBroker;
using AttributionBroker = PermissionBroker;
using ResponsibilityBroker = PermissionBroker;
using PermissionGrant = Grant;
using GrantRequest = PermissionRequest;
using AccessRequest = PermissionRequest;
using AuthorizationResult = Decision;
using PermissionDecision = Decision;
using ProcessGeneration = Generation;

}  // namespace netsec

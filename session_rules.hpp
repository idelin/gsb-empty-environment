#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec::session_rule {

// HMAC-SHA256 authenticator length in bytes.
inline constexpr std::size_t kMacSize = 32;
using Mac = std::array<std::byte, kMacSize>;

// Lifecycle of a rule inside this session's rule store.
enum class RuleState : std::uint8_t {
    Pending = 0, // accepted but not yet confirmed by read-back
    Active = 1,  // read-back confirmed; may influence charging/forwarding
};

// Charging decision carried by a policy rule. "Free" is the security
// sensitive value an attacker would try to install to bypass charging.
enum class ChargingAction : std::uint8_t {
    Metered = 0,
    Free = 1,
};

// Forwarding decision carried by a policy rule. "Redirect" is the security
// sensitive value used to divert traffic to an attacker-controlled target.
enum class ForwardingAction : std::uint8_t {
    Forward = 0,
    Redirect = 1,
    Drop = 2,
};

enum class RejectReason {
    Ok,
    UnauthorizedNode,   // sender node is not on the allow-list
    BadMac,             // message authentication failed (tampering/forgery)
    SessionMismatch,    // message is bound to a different session
    UserMismatch,       // message is bound to a different user
    StaleSequence,      // sequence number is older than the last accepted one
    DuplicateSequence,  // sequence number equals the last accepted one
    UnknownRule,        // referenced rule id does not exist in this session
    ReadbackMismatch,   // read-back content does not match the stored rule
    AlreadyActive,      // rule was confirmed and activated previously
};

// Policy rule payload. All fields are covered by the message authenticator.
struct RuleBody {
    std::string rule_id;
    ChargingAction charging = ChargingAction::Metered;
    ForwardingAction forwarding = ForwardingAction::Forward;
    std::string redirect_target; // meaningful when forwarding == Redirect
    std::string traffic_filter;  // traffic selector the rule applies to

    bool operator==(const RuleBody&) const = default;
};

// Control-plane -> session rule message. The MAC is computed over every
// other field, so the message is cryptographically bound to session, user,
// control-plane node, sequence number and rule payload.
struct RuleMessage {
    std::string session_id;
    std::string user_id;
    std::string node_id;
    std::uint64_t seq = 0;
    RuleBody rule;
    Mac mac{};
};

// Session -> control-plane read-back report, signed with the session
// component's egress key. This is the second leg of mutual authentication:
// the control plane verifies that the report originates from this component.
struct ReadbackReport {
    std::string session_id;
    std::string user_id;
    std::string node_id;
    std::uint64_t seq = 0;
    RuleBody rule;
    RuleState state = RuleState::Pending;
    Mac mac{};
};

// Static identity and keying material of one session rule component.
// Each authorized control-plane node shares an ingress key (node -> component
// direction); the component holds an egress key for signing read-back reports
// (component -> control-plane direction).
struct EngineIdentity {
    std::string session_id;
    std::string user_id;
    std::string egress_key;
    std::map<std::string, std::string> authorized_nodes; // node id -> ingress key
};

struct Result {
    bool ok = false;
    RejectReason reason = RejectReason::Ok;
    std::string detail;

    explicit operator bool() const noexcept { return ok; }
};

// Importable session rule component. One instance guards exactly one
// (session, user) pair; every accepted rule is scoped to that pair and cannot
// leak into or be confirmed by another session.
class SessionRuleEngine {
public:
    explicit SessionRuleEngine(EngineIdentity identity);

    // Ingest a signed rule message from the control plane. On success the
    // rule is stored in Pending state and cannot affect traffic until a
    // read-back confirmation activates it.
    Result ingest(const RuleMessage& message);

    // Produce a signed read-back report for a rule held by this component.
    // Returns std::nullopt when the rule id is unknown to this session.
    std::optional<ReadbackReport> readback(std::string_view rule_id) const;

    // Control-plane side check: verify the report signature, session/user
    // binding and that the reported payload equals the intended rule.
    Result verifyReadback(const ReadbackReport& report, const RuleBody& expected) const;

    // Move a Pending rule to Active after re-verifying its signed read-back.
    // Only Active rules are exposed through effectiveRule().
    Result activate(const ReadbackReport& report);

    // Return the rule only when it is Active; Pending/unknown rules yield
    // std::nullopt, so unconfirmed rules never influence policy decisions.
    std::optional<RuleBody> effectiveRule(std::string_view rule_id) const;

    std::optional<RuleState> ruleState(std::string_view rule_id) const;
    std::vector<std::string> listRuleIds() const;
    std::optional<std::uint64_t> lastSequence(std::string_view node_id) const;

    const std::string& sessionId() const noexcept { return identity_.session_id; }
    const std::string& userId() const noexcept { return identity_.user_id; }

private:
    struct StoredRule {
        RuleBody body;
        std::string node_id;
        std::uint64_t seq = 0;
        RuleState state = RuleState::Pending;
    };

    const StoredRule* findLocked(std::string_view rule_id) const;
    StoredRule* findLocked(std::string_view rule_id);

    EngineIdentity identity_;
    std::map<std::string, StoredRule> rules_;
    std::map<std::string, std::uint64_t> last_sequence_; // per authorized node
    mutable std::mutex mutex_;
};

// Signing / verification primitives. A component calls the verifiers
// implicitly through ingest()/activate(); they are exposed so the control
// plane side can authenticate the opposite direction of the channel.
Mac computeRuleMac(const RuleMessage& message, std::string_view ingress_key);
void signRuleMessage(RuleMessage& message, std::string_view ingress_key);
bool verifyRuleMessageMac(const RuleMessage& message, std::string_view ingress_key);

Mac computeReadbackMac(const ReadbackReport& report, std::string_view egress_key);
void signReadbackReport(ReadbackReport& report, std::string_view egress_key);
bool verifyReadbackMac(const ReadbackReport& report, std::string_view egress_key);

std::string toHex(std::span<const std::byte> data);
const char* toString(RejectReason reason) noexcept;
const char* toString(RuleState state) noexcept;
const char* toString(ChargingAction action) noexcept;
const char* toString(ForwardingAction action) noexcept;

} // namespace netsec::session_rule

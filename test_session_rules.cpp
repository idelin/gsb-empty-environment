// White-box test: including the implementation gives access to the local
// SHA-256/HMAC primitives for known-answer tests, while all security
// behaviour is exercised through the public component API.
#include "session_rules.cpp"

#include <cstdio>
#include <string>

using namespace netsec::session_rule;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& label) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::printf("  [FAIL] %s\n", label.c_str());
    }
}

std::string hex(const std::array<std::uint8_t, 32>& raw) {
    std::string out;
    static constexpr char kHex[] = "0123456789abcdef";
    for (std::uint8_t byte : raw) {
        out.push_back(kHex[byte >> 4U]);
        out.push_back(kHex[byte & 0x0FU]);
    }
    return out;
}

EngineIdentity makeIdentity(std::string session, std::string user) {
    EngineIdentity identity;
    identity.session_id = std::move(session);
    identity.user_id = std::move(user);
    identity.egress_key = "session-egress-key-" + identity.session_id;
    identity.authorized_nodes["pcf-primary"] = "ingress-key-pcf-primary";
    identity.authorized_nodes["pcf-backup"] = "ingress-key-pcf-backup";
    return identity;
}

RuleBody makeRule(std::string rule_id, ChargingAction charging,
                  ForwardingAction forwarding, std::string redirect = "") {
    RuleBody rule;
    rule.rule_id = std::move(rule_id);
    rule.charging = charging;
    rule.forwarding = forwarding;
    rule.redirect_target = std::move(redirect);
    rule.traffic_filter = "apn=ims";
    return rule;
}

RuleMessage makeMessage(const SessionRuleEngine& engine, const std::string& node,
                        std::uint64_t seq, RuleBody rule) {
    RuleMessage message;
    message.session_id = engine.sessionId();
    message.user_id = engine.userId();
    message.node_id = node;
    message.seq = seq;
    message.rule = std::move(rule);
    signRuleMessage(message, node == "pcf-primary" ? "ingress-key-pcf-primary"
                                                   : "ingress-key-pcf-backup");
    return message;
}

} // namespace

// Full legitimate flow: signed delivery -> Pending -> read-back -> Active.
void testLegitimateDeliveryAndReadback() {
    std::printf("[test] legitimate delivery and read-back confirmation\n");
    SessionRuleEngine engine(makeIdentity("session-A", "user-1"));
    RuleBody rule = makeRule("rule-1", ChargingAction::Metered, ForwardingAction::Forward);
    RuleMessage message = makeMessage(engine, "pcf-primary", 100, rule);

    check(bool(engine.ingest(message)), "legitimate message is accepted");
    check(engine.ruleState("rule-1") == RuleState::Pending, "new rule is Pending");
    check(!engine.effectiveRule("rule-1").has_value(),
          "pending rule must not influence policy yet");

    auto report = engine.readback("rule-1");
    check(report.has_value(), "read-back report is produced");
    check(report->session_id == "session-A" && report->user_id == "user-1",
          "read-back is bound to the session and user");
    check(bool(engine.verifyReadback(*report, rule)),
          "control plane confirms read-back matches intended rule");
    check(bool(engine.activate(*report)), "rule activates after confirmation");
    check(engine.ruleState("rule-1") == RuleState::Active, "rule is Active");

    auto effective = engine.effectiveRule("rule-1");
    check(effective.has_value() && *effective == rule,
          "only the confirmed rule becomes effective");
    check(engine.lastSequence("pcf-primary") == 100, "last sequence is tracked per node");

    auto second = engine.activate(*report);
    check(!second && second.reason == RejectReason::AlreadyActive,
          "double activation is rejected");
}

// Attack: flip charging to Free / forwarding to Redirect after signing.
void testRuleTampering() {
    std::printf("[test] rule tampering is rejected\n");
    SessionRuleEngine engine(makeIdentity("session-A", "user-1"));

    RuleMessage free_message =
        makeMessage(engine, "pcf-primary", 1,
                    makeRule("r-free", ChargingAction::Metered, ForwardingAction::Forward));
    free_message.rule.charging = ChargingAction::Free;
    Result result = engine.ingest(free_message);
    check(!result && result.reason == RejectReason::BadMac,
          "charging bypass (Free) tampering fails MAC check");

    RuleMessage redirect_message = makeMessage(
        engine, "pcf-primary", 2,
        makeRule("r-redirect", ChargingAction::Metered, ForwardingAction::Forward));
    redirect_message.rule.forwarding = ForwardingAction::Redirect;
    redirect_message.rule.redirect_target = "attacker.example";
    result = engine.ingest(redirect_message);
    check(!result && result.reason == RejectReason::BadMac,
          "traffic redirection tampering fails MAC check");

    RuleMessage filter_message = makeMessage(
        engine, "pcf-primary", 3,
        makeRule("r-filter", ChargingAction::Metered, ForwardingAction::Forward));
    filter_message.rule.traffic_filter = "apn=attacker";
    result = engine.ingest(filter_message);
    check(!result && result.reason == RejectReason::BadMac,
          "traffic filter tampering fails MAC check");

    check(engine.listRuleIds().empty(), "no tampered rule enters the store");
}

// Attack: replay an old sequence number or reuse the current one.
void testSequenceMonotonicity() {
    std::printf("[test] sequence rollback and replay are rejected\n");
    SessionRuleEngine engine(makeIdentity("session-A", "user-1"));

    check(bool(engine.ingest(makeMessage(engine, "pcf-primary", 10,
                                         makeRule("r1", ChargingAction::Metered,
                                                  ForwardingAction::Forward)))),
          "seq 10 accepted");

    Result rolled_back = engine.ingest(makeMessage(
        engine, "pcf-primary", 9,
        makeRule("r2", ChargingAction::Free, ForwardingAction::Redirect, "evil")));
    check(!rolled_back && rolled_back.reason == RejectReason::StaleSequence,
          "older sequence is rejected as stale");

    Result duplicate = engine.ingest(makeMessage(
        engine, "pcf-primary", 10,
        makeRule("r3", ChargingAction::Free, ForwardingAction::Redirect, "evil")));
    check(!duplicate && duplicate.reason == RejectReason::DuplicateSequence,
          "equal sequence is rejected as duplicate");

    check(bool(engine.ingest(makeMessage(engine, "pcf-primary", 11,
                                         makeRule("r4", ChargingAction::Metered,
                                                  ForwardingAction::Forward)))),
          "higher sequence is accepted");

    // Sequence windows are independent per authorized node.
    check(bool(engine.ingest(makeMessage(engine, "pcf-backup", 1,
                                         makeRule("r5", ChargingAction::Metered,
                                                  ForwardingAction::Forward)))),
          "backup node starts its own sequence window");
}

// Attack: a node outside the allow-list, or a message signed with the wrong
// node key while claiming an authorized identity.
void testNodeForgery() {
    std::printf("[test] unauthorized and forged nodes are rejected\n");
    SessionRuleEngine engine(makeIdentity("session-A", "user-1"));

    RuleMessage rogue = makeMessage(engine, "pcf-primary", 1,
                                    makeRule("r1", ChargingAction::Metered,
                                             ForwardingAction::Forward));
    rogue.node_id = "pcf-rogue";
    signRuleMessage(rogue, "attacker-owned-key");
    Result result = engine.ingest(rogue);
    check(!result && result.reason == RejectReason::UnauthorizedNode,
          "unknown control-plane node is rejected before MAC use");

    RuleMessage forged_identity = makeMessage(
        engine, "pcf-backup", 1,
        makeRule("r2", ChargingAction::Free, ForwardingAction::Redirect, "evil"));
    forged_identity.node_id = "pcf-primary"; // claim primary, keep backup's MAC
    result = engine.ingest(forged_identity);
    check(!result && result.reason == RejectReason::BadMac,
          "node identity spoofing fails the primary node's MAC check");

    check(!engine.lastSequence("pcf-rogue").has_value(),
          "rogue node never gets a sequence window");
}

// Messages and rules are scoped to one (session, user) pair.
void testSessionAndUserBinding() {
    std::printf("[test] cross-session and cross-user messages are rejected\n");
    SessionRuleEngine engine(makeIdentity("session-A", "user-1"));

    RuleMessage other_session = makeMessage(
        engine, "pcf-primary", 1,
        makeRule("r1", ChargingAction::Free, ForwardingAction::Redirect, "evil"));
    other_session.session_id = "session-B";
    signRuleMessage(other_session, "ingress-key-pcf-primary");
    Result result = engine.ingest(other_session);
    check(!result && result.reason == RejectReason::SessionMismatch,
          "message bound to another session is rejected");

    RuleMessage other_user = makeMessage(
        engine, "pcf-primary", 2,
        makeRule("r2", ChargingAction::Free, ForwardingAction::Redirect, "evil"));
    other_user.user_id = "user-2";
    signRuleMessage(other_user, "ingress-key-pcf-primary");
    result = engine.ingest(other_user);
    check(!result && result.reason == RejectReason::UserMismatch,
          "message bound to another user is rejected");

    check(engine.listRuleIds().empty(), "foreign rules never enter this session");
}

// A rule accepted in one session component cannot be read back or confirmed
// by a component guarding a different session.
void testRulesBelongOnlyToOwnSession() {
    std::printf("[test] rules belong only to their own session\n");
    SessionRuleEngine owner(makeIdentity("session-A", "user-1"));
    SessionRuleEngine other(makeIdentity("session-B", "user-1"));

    RuleBody rule = makeRule("shared-id", ChargingAction::Metered,
                             ForwardingAction::Forward);
    check(bool(owner.ingest(makeMessage(owner, "pcf-primary", 5, rule))),
          "owner session accepts its rule");

    check(!other.readback("shared-id").has_value(),
          "second session cannot read back the owner's rule");
    check(!other.effectiveRule("shared-id").has_value(),
          "second session never sees the owner's rule as effective");

    auto owner_report = owner.readback("shared-id");
    check(owner_report.has_value(), "owner can read back its own rule");

    // Replay the owner's signed report at the other component: egress keys
    // differ and the rule is unknown there, so confirmation must fail.
    Result foreign = other.verifyReadback(*owner_report, rule);
    check(!foreign && foreign.reason == RejectReason::BadMac,
          "foreign component rejects the other session's read-back MAC");
    Result activation = other.activate(*owner_report);
    check(!activation, "foreign component cannot activate another session's rule");

    // Even with a valid node MAC, a report claiming the foreign session is
    // rejected on session binding.
    RuleMessage injected = makeMessage(other, "pcf-primary", 5, rule);
    injected.session_id = "session-A";
    signRuleMessage(injected, "ingress-key-pcf-primary");
    Result injected_result = other.ingest(injected);
    check(!injected_result &&
              injected_result.reason == RejectReason::SessionMismatch,
          "foreign component rejects a rule claiming the other session");
}

// Read-back reports are themselves authenticated end to end.
void testReadbackTampering() {
    std::printf("[test] read-back tampering is rejected\n");
    SessionRuleEngine engine(makeIdentity("session-A", "user-1"));
    RuleBody rule = makeRule("r1", ChargingAction::Metered, ForwardingAction::Forward);
    check(bool(engine.ingest(makeMessage(engine, "pcf-primary", 7, rule))),
          "rule ingested for read-back test");

    auto report = engine.readback("r1");
    check(report.has_value(), "report exists");

    ReadbackReport tampered = *report;
    tampered.rule.charging = ChargingAction::Free;
    Result result = engine.verifyReadback(tampered, rule);
    check(!result && result.reason == RejectReason::BadMac,
          "modified read-back fails MAC verification");
    check(!engine.activate(tampered), "tampered read-back cannot activate the rule");

    RuleBody attacker_rule = rule;
    attacker_rule.forwarding = ForwardingAction::Redirect;
    attacker_rule.redirect_target = "attacker.example";
    result = engine.verifyReadback(*report, attacker_rule);
    check(!result && result.reason == RejectReason::ReadbackMismatch,
          "read-back differing from the intended rule is rejected");

    check(!engine.effectiveRule("r1").has_value(),
          "rule stays pending after failed confirmations");
    check(!engine.readback("missing").has_value(),
          "unknown rule read-back returns no report");
}

// Known-answer checks pin the local crypto to published test vectors.
void testCryptoKnownAnswers() {
    std::printf("[test] SHA-256 and HMAC-SHA256 known-answer vectors\n");
    const std::string abc = "abc";
    const auto digest = detail::sha256(
        reinterpret_cast<const std::uint8_t*>(abc.data()), abc.size());
    check(hex(digest) ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "SHA-256(\"abc\") matches FIPS 180-4 vector");

    const std::string key(20, static_cast<char>(0x0b));
    const std::string data = "Hi There";
    const std::vector<std::uint8_t> message(data.begin(), data.end());
    const auto mac = detail::hmacSha256(key, message);
    check(hex(mac) ==
              "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
          "HMAC-SHA256 matches RFC 4231 test case 1");
}

int main() {
    testCryptoKnownAnswers();
    testLegitimateDeliveryAndReadback();
    testRuleTampering();
    testSequenceMonotonicity();
    testNodeForgery();
    testSessionAndUserBinding();
    testRulesBelongOnlyToOwnSession();
    testReadbackTampering();

    std::printf("\n%d checks, %d failure(s)\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}

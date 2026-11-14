#include "netsec.hpp"

#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const std::string& description) {
    if (!condition) {
        std::cerr << "FAIL: " << description << '\n';
        ++failures;
    }
}

void expect_decision(const netsec::Decision& decision, bool allowed,
                     netsec::RejectReason reason,
                     const std::string& description) {
    check(decision.allowed == allowed, description + " (allowed flag)");
    check(decision.reason == reason, description + " (reason)");
    if (allowed) {
        check(decision.code() == "allowed", description + " (code)");
    }
}

void configure(netsec::SliceBindingManager& manager) {
    check(manager.register_slice("embb", 3, 20).allowed,
          "register embb policy");
    check(manager.register_slice("urllc", 2, 10).allowed,
          "register urllc policy");
    check(manager.register_slice("private", 1, 5).allowed,
          "register private policy");

    check(manager
              .register_subscription("sub-alice", "supi-alice",
                                     {"embb", "urllc"})
              .allowed,
          "register alice subscription");
    check(manager
              .register_subscription("sub-bob", "supi-bob", {"private"})
              .allowed,
          "register bob subscription");

    check(manager.register_resource("video", "embb").allowed,
          "register embb resource");
    check(manager.register_resource("robot", "urllc").allowed,
          "register urllc resource");
    check(manager.register_resource("vault", "private").allowed,
          "register private resource");
}

netsec::SessionRequest alice_embb() {
    return netsec::SessionRequest{"embb", "sub-alice", "sess-alice-embb",
                                  "supi-alice", {}, 0, {}};
}

netsec::SessionRequest bob_private() {
    return netsec::SessionRequest{"private", "sub-bob", "sess-bob-private",
                                 "supi-bob", {}, 0, {}};
}

void test_legal_binding_and_isolation(netsec::SliceBindingManager& manager) {
    const netsec::SessionRequest request = alice_embb();
    expect_decision(manager.establish(request), true,
                    netsec::RejectReason::None, "establish legal session");

    netsec::SessionRequest access = request;
    access.resource_id = "video";
    access.resource_units = 2;
    expect_decision(manager.validate(access), true,
                    netsec::RejectReason::None,
                    "same-slice resource is authorized");

    const auto binding = manager.binding_for(request.session_id);
    check(binding.has_value(), "binding is retained");
    check(binding->slice_id == "embb", "binding remains in embb");
    check(binding->subscription_id == "sub-alice",
          "subscription id is bound");
    check(binding->supi == "supi-alice", "SUPI is bound");
    check(binding->session_id == request.session_id, "session id is bound");
    check(binding->consumed_resource_units == 2,
          "resource consumption is recorded on the binding");
}

void test_slice_substitution(netsec::SliceBindingManager& manager) {
    netsec::SessionRequest request = alice_embb();
    const auto before_embb = manager.usage_for("embb");
    const auto before_urllc = manager.usage_for("urllc");

    request.slice_id = "urllc";
    request.resource_slice_id = "urllc";
    request.resource_units = 3;
    expect_decision(manager.validate(request), false,
                    netsec::RejectReason::CrossSliceAccess,
                    "slice replacement is rejected");

    const auto after_embb = manager.usage_for("embb");
    const auto after_urllc = manager.usage_for("urllc");
    check(after_embb.consumed_resource_units ==
              before_embb.consumed_resource_units,
          "rejected slice replacement charges no embb quota");
    check(after_urllc.consumed_resource_units ==
              before_urllc.consumed_resource_units,
          "rejected slice replacement charges no urllc quota");
}

void test_subscription_forgery(netsec::SliceBindingManager& manager) {
    netsec::SessionRequest forged = alice_embb();
    forged.supi = "supi-bob";
    expect_decision(manager.establish(forged), false,
                    netsec::RejectReason::SubscriptionForgery,
                    "SUPI does not match subscription");

    netsec::SessionRequest unknown = alice_embb();
    unknown.subscription_id = "sub-made-up";
    expect_decision(manager.establish(unknown), false,
                    netsec::RejectReason::UnknownSubscription,
                    "unknown subscription is rejected");

    netsec::SessionRequest unsubscribed = bob_private();
    unsubscribed.slice_id = "embb";
    expect_decision(manager.establish(unsubscribed), false,
                    netsec::RejectReason::UnsubscribedSlice,
                    "slice absent from subscription is rejected");

    netsec::SessionRequest valid = alice_embb();
    expect_decision(manager.validate(valid), true,
                    netsec::RejectReason::None,
                    "baseline legal access remains valid");

    netsec::SessionRequest swapped_subscription = valid;
    swapped_subscription.subscription_id = "sub-bob";
    expect_decision(manager.validate(swapped_subscription), false,
                    netsec::RejectReason::SubscriptionMismatch,
                    "subsequent subscription replacement is rejected");

    netsec::SessionRequest swapped_supi = valid;
    swapped_supi.supi = "supi-bob";
    expect_decision(manager.validate(swapped_supi), false,
                    netsec::RejectReason::UserIdentityMismatch,
                    "subsequent SUPI replacement is rejected");
}

void test_session_mixing(netsec::SliceBindingManager& manager) {
    netsec::SessionRequest bob = bob_private();
    expect_decision(manager.establish(bob), true,
                    netsec::RejectReason::None, "establish bob session");

    netsec::SessionRequest mixed = alice_embb();
    mixed.session_id = bob.session_id;
    expect_decision(manager.validate(mixed), false,
                    netsec::RejectReason::CrossSliceAccess,
                    "mixing an embb request with a private session is rejected");

    netsec::SessionRequest duplicate = alice_embb();
    expect_decision(manager.establish(duplicate), false,
                    netsec::RejectReason::SessionAlreadyBound,
                    "session id cannot be rebound");

    netsec::SessionRequest nonexistent = alice_embb();
    nonexistent.session_id = "sess-does-not-exist";
    expect_decision(manager.validate(nonexistent), false,
                    netsec::RejectReason::UnknownSession,
                    "unknown session id is rejected");
}

void test_resource_ownership(netsec::SliceBindingManager& manager) {
    netsec::SessionRequest access = alice_embb();
    access.resource_id = "robot";
    expect_decision(manager.validate(access), false,
                    netsec::RejectReason::CrossSliceResource,
                    "resource registered to another slice is rejected");

    access.resource_id = "video";
    access.resource_slice_id = "urllc";
    expect_decision(manager.validate(access), false,
                    netsec::RejectReason::ResourceOwnershipMismatch,
                    "resource slice declared by client must match catalogue");

    access.resource_id.clear();
    access.resource_slice_id = "urllc";
    access.resource_units = 1;
    expect_decision(manager.validate(access), false,
                    netsec::RejectReason::CrossSliceResource,
                    "resource-scope slice replacement is rejected");

    access.resource_slice_id.clear();
    access.resource_id = "missing";
    expect_decision(manager.validate(access), false,
                    netsec::RejectReason::UnknownResource,
                    "unknown resource is rejected");
}

void test_policy_failures(netsec::SliceBindingManager& manager) {
    netsec::SlicePolicy shared_policy{"shared", 1, 1, false, true};
    expect_decision(manager.register_slice(shared_policy), false,
                    netsec::RejectReason::IsolationPolicyViolation,
                    "non-isolated slice policy is rejected");

    check(manager.register_slice("disabled", 1, 1, true).allowed,
          "register disabled policy for test");
    manager.clear();

    check(manager.register_slice(
                     netsec::SlicePolicy{"disabled-slice", 1, 1, true, false})
              .allowed,
          "disabled slice policy can be stored");
    check(manager.register_subscription("sub-disabled", "supi-disabled",
                                        {"disabled-slice"})
              .allowed,
          "subscription for disabled slice can be stored");
    expect_decision(manager.establish("disabled-slice", "sub-disabled",
                                      "sess-disabled", "supi-disabled"),
                    false, netsec::RejectReason::SliceDisabled,
                    "disabled slice cannot establish a session");
}

void test_quotas() {
    netsec::SliceBindingManager manager;
    check(manager.register_slice("quota", 1, 10).allowed,
          "register quota slice");
    check(manager
              .register_subscription("sub-quota", "supi-quota", {"quota"})
              .allowed,
          "register quota subscription");

    netsec::SessionRequest first{"quota", "sub-quota", "sess-quota-1",
                                 "supi-quota", {}, 0, {}};
    expect_decision(manager.establish(first), true,
                    netsec::RejectReason::None,
                    "first session fits session quota");

    netsec::SessionRequest second{"quota", "sub-quota", "sess-quota-2",
                                  "supi-quota", {}, 0, {}};
    expect_decision(manager.establish(second), false,
                    netsec::RejectReason::SessionQuotaExceeded,
                    "second session exceeds session quota");

    netsec::SessionRequest allocation = first;
    allocation.resource_units = 7;
    allocation.resource_slice_id = "quota";
    expect_decision(manager.validate(allocation), true,
                    netsec::RejectReason::None,
                    "resource allocation within quota");
    check(manager.usage_for("quota").consumed_resource_units == 7,
          "quota usage is charged once");

    allocation.resource_units = 4;
    expect_decision(manager.validate(allocation), false,
                    netsec::RejectReason::ResourceQuotaExceeded,
                    "resource allocation over quota is rejected");
    check(manager.usage_for("quota").consumed_resource_units == 7,
          "over-quota allocation does not change usage");

    allocation.resource_units = 3;
    expect_decision(manager.validate(allocation), true,
                    netsec::RejectReason::None,
                    "allocation may exactly fill remaining quota");
    check(manager.usage_for("quota").consumed_resource_units == 10,
          "quota is exactly exhausted");

    allocation.resource_units = 1;
    expect_decision(manager.validate(allocation), false,
                    netsec::RejectReason::ResourceQuotaExceeded,
                    "allocation after exhaustion is rejected");

    expect_decision(manager.release_session(first.session_id), true,
                    netsec::RejectReason::None, "release session slot");
    check(manager.usage_for("quota").active_sessions == 0,
          "release returns the session quota slot");
    check(manager.usage_for("quota").consumed_resource_units == 10,
          "release does not refund resources");

    expect_decision(manager.establish(second), true,
                    netsec::RejectReason::None,
                    "session slot can be reused after release");
}

void test_establishment_resource_quota_is_atomic() {
    netsec::SliceBindingManager manager;
    check(manager.register_slice("quota-establish", 2, 5).allowed,
          "register establishment-quota slice");
    check(manager
              .register_subscription("sub-est", "supi-est",
                                     {"quota-establish"})
              .allowed,
          "register establishment-quota subscription");

    netsec::SessionRequest request{"quota-establish", "sub-est",
                                   "sess-est-1", "supi-est", "quota-establish",
                                   6, {}};
    expect_decision(manager.establish(request), false,
                    netsec::RejectReason::ResourceQuotaExceeded,
                    "establishment cannot reserve over-quota resources");
    check(!manager.binding_for(request.session_id).has_value(),
          "failed establishment commits no binding");
    check(manager.usage_for("quota-establish").active_sessions == 0,
          "failed establishment commits no session slot");
    check(manager.usage_for("quota-establish").consumed_resource_units == 0,
          "failed establishment commits no resource quota");

    request.resource_units = 5;
    expect_decision(manager.establish(request), true,
                    netsec::RejectReason::None,
                    "establishment may reserve exactly available quota");
    check(manager.binding_for(request.session_id)
              ->consumed_resource_units == 5,
          "establishment records reserved resources");
}

void test_registration_and_identity_errors() {
    netsec::SliceBindingManager manager;
    check(manager.register_slice("s", 1, 1).allowed,
          "register baseline slice");
    check(manager.register_slice("s", 1, 1).allowed,
          "identical slice registration is idempotent");
    expect_decision(manager.register_slice("s", 2, 1), false,
                    netsec::RejectReason::DuplicateRegistration,
                    "conflicting slice registration is rejected");

    check(manager.register_subscription("sub", "supi", {"s"}).allowed,
          "register baseline subscription");
    expect_decision(manager.register_subscription("sub", "other", {"s"}),
                    false, netsec::RejectReason::DuplicateRegistration,
                    "conflicting subscription registration is rejected");

    netsec::SessionRequest empty = alice_embb();
    empty.session_id.clear();
    expect_decision(manager.establish(empty), false,
                    netsec::RejectReason::InvalidRequest,
                    "empty identity component is rejected");
}

}  // namespace

int main() {
    netsec::SliceBindingManager manager;
    configure(manager);

    test_legal_binding_and_isolation(manager);
    test_slice_substitution(manager);
    test_subscription_forgery(manager);
    test_session_mixing(manager);
    test_resource_ownership(manager);
    test_policy_failures(manager);
    test_quotas();
    test_establishment_resource_quota_is_atomic();
    test_registration_and_identity_errors();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all slice binding tests passed\n";
    return 0;
}

#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <exception>
#include <functional>
#include <iostream>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using netsec::radius::Attribute;
using netsec::radius::AttributeRules;
using netsec::radius::ChainError;
using netsec::radius::HomeServer;
using netsec::radius::IngressInfo;
using netsec::radius::IngressKind;
using netsec::radius::KeyRing;
using netsec::radius::Message;
using netsec::radius::Octets;
using netsec::radius::ParseError;
using netsec::radius::PolicyDecision;
using netsec::radius::PolicyRoute;
using netsec::radius::ProxyEngine;
using netsec::radius::RadiusError;
using netsec::radius::SourceBinding;
using netsec::radius::SourceIdentity;
using netsec::radius::TrustError;
using netsec::radius::VendorAttribute;

int failures = 0;

void check(bool condition, std::string_view name) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << name << '\n';
    }
}

template <typename Exception, typename Callable>
void expect_throw(Callable&& callable, std::string_view name) {
    try {
        std::invoke(std::forward<Callable>(callable));
        ++failures;
        std::cerr << "FAIL: expected " << name << " to throw\n";
    } catch (const Exception&) {
    } catch (const std::exception& error) {
        ++failures;
        std::cerr << "FAIL: " << name << " threw the wrong exception: "
                  << error.what() << '\n';
    }
}

Octets bytes(std::string_view value) {
    return Octets(value.begin(), value.end());
}

Octets u32(std::uint32_t value) {
    return Octets{static_cast<std::uint8_t>((value >> 24U) & 0xffU),
                  static_cast<std::uint8_t>((value >> 16U) & 0xffU),
                  static_cast<std::uint8_t>((value >> 8U) & 0xffU),
                  static_cast<std::uint8_t>(value & 0xffU)};
}

Attribute text_attribute(std::uint8_t type, std::string_view value) {
    return Attribute{type, bytes(value)};
}

struct Fixture {
    Octets nas_secret = bytes("nas-shared-secret-generation-1");
    Octets proxy_a_secret = bytes("proxy-a-to-proxy-b-generation-1");
    Octets proxy_b_secret = bytes("proxy-b-to-home-c-generation-1");

    SourceIdentity origin{"NAS-01", 1, "roam-op"};
    std::vector<Attribute> trusted_operator_attributes{
        text_attribute(netsec::radius::kOperatorNameAttribute, "roam-op"),
        netsec::radius::encode_vendor_specific(
            VendorAttribute{10415, 77, bytes("billing-gold")})};

    PolicyRoute route_a;
    PolicyRoute route_b;
    PolicyRoute route_home;

    ProxyEngine proxy_a;
    ProxyEngine proxy_b;
    HomeServer home;

    Fixture()
        : route_a{origin, "home-c", "billing-gold",
                  trusted_operator_attributes, "proxy-b", 1},
          route_b{origin, "home-c", "billing-gold",
                  trusted_operator_attributes, "home-c", 1},
          route_home{origin, "home-c", "billing-gold",
                     trusted_operator_attributes, "proxy-b", 1},
          proxy_a("proxy-a", make_proxy_a_keys(), make_a_sources(), {route_a}),
          proxy_b("proxy-b", make_proxy_b_keys(), make_b_sources(), {route_b}),
          home("home-c", make_home_keys(), {route_home}) {}

    [[nodiscard]] KeyRing make_proxy_a_keys() const {
        KeyRing keys;
        keys.install("nas-1", 1, nas_secret);
        keys.install("proxy-b", 1, proxy_a_secret);
        return keys;
    }

    [[nodiscard]] KeyRing make_proxy_b_keys() const {
        KeyRing keys;
        keys.install("proxy-a", 1, proxy_a_secret);
        keys.install("home-c", 1, proxy_b_secret);
        return keys;
    }

    [[nodiscard]] KeyRing make_home_keys() const {
        KeyRing keys;
        keys.install("proxy-b", 1, proxy_b_secret);
        return keys;
    }

    [[nodiscard]] std::vector<SourceBinding> make_a_sources() const {
        return {SourceBinding{"nas-1", IngressKind::Nas, "NAS-01", 1,
                              "roam-op"}};
    }

    [[nodiscard]] std::vector<SourceBinding> make_b_sources() const {
        return {SourceBinding{"proxy-a", IngressKind::Proxy, {}, 1, {}}};
    }

    [[nodiscard]] Message access_request(
        std::vector<Attribute> extra = {}) const {
        Message request;
        request.code = netsec::radius::kAccessRequest;
        request.identifier = 17;
        request.authenticator = netsec::radius::secure_random(16);
        request.attributes = {
            text_attribute(netsec::radius::kNasIdentifierAttribute, "NAS-01"),
            text_attribute(1, "alice"),
            Attribute{6, u32(1)},
        };
        for (auto& attribute : extra) {
            request.attributes.push_back(std::move(attribute));
        }
        netsec::radius::apply_message_authenticator(request, nas_secret);
        return request;
    }

    [[nodiscard]] Message accounting_request(
        std::vector<Attribute> extra = {}) const {
        Message request;
        request.code = netsec::radius::kAccountingRequest;
        request.identifier = 23;
        request.authenticator.assign(16, 0);
        request.attributes = {
            text_attribute(netsec::radius::kNasIdentifierAttribute, "NAS-01"),
            Attribute{40, u32(1)},
            text_attribute(44, "session-42"),
        };
        for (auto& attribute : extra) {
            request.attributes.push_back(std::move(attribute));
        }
        netsec::radius::compute_accounting_request_authenticator(
            request, nas_secret);
        return request;
    }
};

IngressInfo nas_ingress() {
    return IngressInfo{"nas-1", true};
}

IngressInfo proxy_a_ingress() {
    return IngressInfo{"proxy-a", true};
}

IngressInfo proxy_b_ingress() {
    return IngressInfo{"proxy-b", true};
}

struct TwoHopResult {
    netsec::radius::ForwardedRequest first;
    netsec::radius::ForwardedRequest second;
    Message original;
};

TwoHopResult forward_two_hops(const Fixture& fixture, const Message& request) {
    TwoHopResult result;
    result.original = request;
    result.first = fixture.proxy_a.forward(request.serialize(), nas_ingress());
    result.second =
        fixture.proxy_b.forward(result.first.wire, proxy_a_ingress());
    return result;
}

bool attribute_equal(const Attribute& lhs, const Attribute& rhs) {
    return lhs.type == rhs.type && lhs.value == rhs.value;
}

void test_strict_codec_and_cardinality() {
    const Octets nas = bytes("NAS-01");
    const Message valid = Message::parse(
        [] {
            Message message;
            message.code = netsec::radius::kAccessRequest;
            message.identifier = 1;
            message.authenticator.assign(16, 0);
            message.attributes = {
                Attribute{netsec::radius::kNasIdentifierAttribute, bytes("NAS-01")},
                Attribute{netsec::radius::kClassAttribute, bytes("class-a")},
                Attribute{netsec::radius::kClassAttribute, bytes("class-b")},
                Attribute{netsec::radius::kProxyStateAttribute, bytes("opaque")},
            };
            return message.serialize();
        }());
    check(valid.count_of_type(netsec::radius::kClassAttribute) == 2,
          "Class is repeatable");

    Message duplicate_nas = valid;
    duplicate_nas.attributes.push_back(
        Attribute{netsec::radius::kNasIdentifierAttribute, nas});
    expect_throw<ParseError>(
        [&] { (void)duplicate_nas.serialize(); }, "duplicate NAS-Identifier");

    Message duplicate_operator;
    duplicate_operator.code = netsec::radius::kAccessRequest;
    duplicate_operator.identifier = 2;
    duplicate_operator.authenticator.assign(16, 0);
    duplicate_operator.attributes = {
        text_attribute(netsec::radius::kOperatorNameAttribute, "op-1"),
        text_attribute(netsec::radius::kOperatorNameAttribute, "op-2"),
    };
    expect_throw<ParseError>(
        [&] { (void)duplicate_operator.serialize(); },
        "duplicate single-value Operator-Name");

    AttributeRules vendor_rules = AttributeRules::secure_defaults();
    vendor_rules.single_value_vendor_attributes.insert({10415, 77});
    Message duplicate_vendor;
    duplicate_vendor.code = netsec::radius::kAccessRequest;
    duplicate_vendor.identifier = 3;
    duplicate_vendor.authenticator.assign(16, 0);
    duplicate_vendor.attributes = {
        netsec::radius::encode_vendor_specific(
            VendorAttribute{10415, 77, bytes("first")}),
        netsec::radius::encode_vendor_specific(
            VendorAttribute{10415, 77, bytes("second")}),
    };
    expect_throw<ParseError>(
        [&] {
            (void)netsec::radius::Message::parse(
                duplicate_vendor.serialize(),
                netsec::radius::ParseOptions{vendor_rules, true});
        },
        "duplicate single-value vendor attribute");

    Attribute malformed_vendor{netsec::radius::kVendorSpecificAttribute,
                               {0x00, 0x00, 0x28, 0x4f, 0x01, 0x09, 0x00}};
    Message malformed;
    malformed.code = netsec::radius::kAccessRequest;
    malformed.identifier = 4;
    malformed.authenticator.assign(16, 0);
    malformed.attributes = {malformed_vendor};
    expect_throw<ParseError>(
        [&] { (void)malformed.serialize(); }, "strict vendor length");

    Message bad_fixed;
    bad_fixed.code = netsec::radius::kAccountingRequest;
    bad_fixed.identifier = 5;
    bad_fixed.authenticator.assign(16, 0);
    bad_fixed.attributes = {Attribute{40, {0x01, 0x02, 0x03}}};
    expect_throw<ParseError>(
        [&] { (void)bad_fixed.serialize(); }, "fixed attribute length");

    Message bad_class;
    bad_class.code = netsec::radius::kAccessRequest;
    bad_class.identifier = 6;
    bad_class.authenticator.assign(16, 0);
    bad_class.attributes = {
        Attribute{netsec::radius::kClassAttribute, {}}};
    expect_throw<ParseError>(
        [&] { (void)bad_class.serialize(); }, "empty Class attribute");
}

void test_forward_appends_state_in_order() {
    Fixture fixture;
    Message client = fixture.accounting_request();
    const std::vector<Attribute> original_attributes = client.attributes;

    const auto first = fixture.proxy_a.forward(client.serialize(), nas_ingress());
    check(first.proxy_state_chain.size() == 1, "first hop adds one Proxy-State");
    check(first.user_writable_attributes.size() == original_attributes.size(),
          "user attributes stay separate from proxy attributes");
    check(first.proxy_generated_attributes.size() == 1,
          "one proxy-generated attribute");
    check(first.proxy_generated_attributes.front().type ==
              netsec::radius::kProxyStateAttribute,
          "generated attribute is Proxy-State");
    check(std::any_of(first.state.nonce.begin(), first.state.nonce.end(),
                      [](std::uint8_t byte) { return byte != 0; }),
          "Proxy-State carries a random nonce");

    const Message forwarded = Message::parse(first.wire);
    check(forwarded.attributes.size() == original_attributes.size() + 1,
          "forwarded request has exactly one extra attribute");
    for (std::size_t i = 0; i < original_attributes.size(); ++i) {
        check(attribute_equal(forwarded.attributes[i], original_attributes[i]),
              "original attribute order is preserved");
    }
    check(forwarded.attributes.back().type ==
              netsec::radius::kProxyStateAttribute,
          "Proxy-State is appended at the end");

    const auto again = fixture.proxy_a.forward(client.serialize(), nas_ingress());
    check(again.generated_proxy_state != first.generated_proxy_state,
          "Proxy-State is unpredictable across forwards");
}

void test_two_hop_roaming_and_response_chain() {
    Fixture fixture;
    Message client = fixture.access_request();
    const auto hops = forward_two_hops(fixture, client);

    check(hops.first.proxy_state_chain.size() == 1,
          "valid first hop chain size");
    check(hops.second.proxy_state_chain.size() == 2,
          "valid second hop chain size");
    check(hops.first.generated_proxy_state != hops.second.generated_proxy_state,
          "each hop adds a fresh Proxy-State");

    const auto verified =
        fixture.home.verify_request(hops.second.wire, proxy_b_ingress());
    check(verified.message.code == netsec::radius::kAccessRequest,
          "home verifies the access request");

    const PolicyDecision decision =
        fixture.home.authorize(hops.second.wire, proxy_b_ingress());
    check(decision.accepted, "home accepts the trusted two-hop chain");
    check(decision.home_server == "home-c", "home server from trusted route");
    check(decision.billing_group == "billing-gold", "billing group from route");
    check(decision.roaming_peer == "roam-op", "roaming peer from route");
    check(decision.origin_key_generation == 1, "key generation from chain");
    check(decision.trusted_operator_attributes ==
              fixture.trusted_operator_attributes,
          "operator attributes come from the trusted route");

    const Octets response =
        fixture.home.respond(hops.second.wire, proxy_b_ingress(),
                             netsec::radius::kAccessAccept,
                             {text_attribute(18, "welcome")});
    const auto second_response =
        fixture.proxy_b.accept_response(response, hops.second);
    check(second_response.message.code == netsec::radius::kAccessAccept,
          "second hop accepts the response");
    const Message upstream_first = Message::parse(second_response.upstream_wire);
    check(upstream_first.count_of_type(netsec::radius::kProxyStateAttribute) == 1,
          "second hop removes only its own Proxy-State");
    check(netsec::radius::verify_response_authenticator(
              upstream_first, hops.first.forwarded_request_authenticator,
              fixture.proxy_a_secret),
          "second hop recomputes the upstream response authenticator");

    const auto first_response =
        fixture.proxy_a.accept_response(second_response.upstream_wire, hops.first);
    const Message final_response = Message::parse(first_response.upstream_wire);
    check(final_response.count_of_type(netsec::radius::kProxyStateAttribute) == 0,
          "first hop removes the full chain before the NAS");
    check(netsec::radius::verify_response_authenticator(
              final_response, client.authenticator, fixture.nas_secret),
          "first hop recomputes the NAS response authenticator");
}

void test_authorization_and_accounting_share_ownership() {
    Fixture fixture;
    const auto access = forward_two_hops(fixture, fixture.access_request());
    const auto accounting =
        forward_two_hops(fixture, fixture.accounting_request());

    const PolicyDecision auth =
        fixture.home.authorize(access.second.wire, proxy_b_ingress());
    const PolicyDecision acct =
        fixture.home.account(accounting.second.wire, proxy_b_ingress());
    check(auth.accepted && acct.accepted, "both request kinds are trusted");
    check(auth.home_server == acct.home_server,
          "authorization and accounting select the same home");
    check(auth.billing_group == acct.billing_group,
          "authorization and accounting select the same billing group");
    check(auth.roaming_peer == acct.roaming_peer,
          "authorization and accounting select the same roaming peer");
    check(auth.origin_key_generation == acct.origin_key_generation,
          "authorization and accounting use the same key generation");
    check(auth.trusted_operator_attributes ==
              acct.trusted_operator_attributes,
          "authorization and accounting use the same trusted operator attributes");

    const Octets acct_response =
        fixture.home.respond(accounting.second.wire, proxy_b_ingress(),
                             netsec::radius::kAccountingResponse);
    const auto second =
        fixture.proxy_b.accept_response(acct_response, accounting.second);
    const auto first =
        fixture.proxy_a.accept_response(second.upstream_wire, accounting.first);
    check(Message::parse(first.upstream_wire).code ==
              netsec::radius::kAccountingResponse,
          "accounting response traverses the two-hop chain");
}

void test_stripped_proxy_state_is_rejected() {
    Fixture fixture;
    const auto hops = forward_two_hops(fixture, fixture.access_request());

    Message stripped = Message::parse(hops.second.wire);
    stripped.attributes.erase(
        std::remove_if(stripped.attributes.begin(), stripped.attributes.end(),
                       [](const Attribute& attribute) {
                           return attribute.type ==
                                  netsec::radius::kProxyStateAttribute;
                       }),
        stripped.attributes.end());
    const Octets stripped_wire = stripped.serialize();

    const PolicyDecision decision = fixture.home.evaluate(
        stripped_wire, proxy_b_ingress());
    check(!decision.accepted,
          "home rejects a request whose Proxy-State chain was stripped");

    Message appended_forgery = Message::parse(hops.second.wire);
    appended_forgery.attributes.push_back(
        Attribute{netsec::radius::kProxyStateAttribute,
                  netsec::radius::secure_random(64)});
    const PolicyDecision forged_chain = fixture.home.evaluate(
        appended_forgery.serialize(), proxy_b_ingress());
    check(!forged_chain.accepted,
          "home rejects an attacker-appended Proxy-State");
}

void test_client_proxy_state_is_untrusted() {
    Fixture fixture;
    const std::vector<Attribute> client_state{
        Attribute{netsec::radius::kProxyStateAttribute, bytes("client-forgery")}};
    const auto benign = forward_two_hops(fixture, fixture.access_request());
    const auto forged =
        forward_two_hops(fixture, fixture.access_request(client_state));

    check(forged.first.proxy_state_chain.size() == 2,
          "client Proxy-State is preserved before the generated state");
    check(forged.first.untrusted_proxy_state_count == 1,
          "client Proxy-State is counted as untrusted");
    const PolicyDecision baseline =
        fixture.home.authorize(benign.second.wire, proxy_b_ingress());
    const PolicyDecision decision =
        fixture.home.authorize(forged.second.wire, proxy_b_ingress());
    check(decision.accepted, "client Proxy-State does not break the chain");
    check(decision.home_server == baseline.home_server &&
              decision.billing_group == baseline.billing_group &&
              decision.roaming_peer == baseline.roaming_peer,
          "client Proxy-State cannot alter ownership");

    const Octets response = fixture.home.respond(
        forged.second.wire, proxy_b_ingress(),
        netsec::radius::kAccessAccept);
    const auto from_b = fixture.proxy_b.accept_response(response, forged.second);
    const auto from_a =
        fixture.proxy_a.accept_response(from_b.upstream_wire, forged.first);
    check(Message::parse(from_a.upstream_wire)
              .count_of_type(netsec::radius::kProxyStateAttribute) == 0,
          "all untrusted and local Proxy-State is removed before the NAS");

    Message stripped_upstream = Message::parse(forged.first.wire);
    auto last = std::find_if(
        stripped_upstream.attributes.rbegin(),
        stripped_upstream.attributes.rend(),
        [](const Attribute& attribute) {
            return attribute.type == netsec::radius::kProxyStateAttribute;
        });
    if (last != stripped_upstream.attributes.rend()) {
        stripped_upstream.attributes.erase(std::next(last).base());
    }
    expect_throw<RadiusError>(
        [&] {
            (void)fixture.proxy_b.forward(stripped_upstream.serialize(),
                                         proxy_a_ingress());
        },
        "upstream Proxy-State removal is detected");
}

void test_forged_operator_attributes_are_ignored() {
    Fixture fixture;
    const std::vector<Attribute> forged{
        text_attribute(netsec::radius::kOperatorNameAttribute, "evil-op"),
        netsec::radius::encode_vendor_specific(
            VendorAttribute{10415, 77, bytes("platinum")}),
        netsec::radius::encode_vendor_specific(
            VendorAttribute{10415, 78, bytes("home-evil")}),
    };

    const auto benign = forward_two_hops(fixture, fixture.access_request());
    const auto tampered =
        forward_two_hops(fixture, fixture.access_request(forged));

    check(tampered.first.user_writable_attributes.size() >
              benign.first.user_writable_attributes.size(),
          "forged attributes remain in the client partition");
    check(netsec::radius::operator_attributes(
              tampered.first.user_writable_attributes)
              .size() >= forged.size(),
          "forged operator attributes are identified as client data");

    const PolicyDecision baseline =
        fixture.home.authorize(benign.second.wire, proxy_b_ingress());
    const PolicyDecision attack =
        fixture.home.authorize(tampered.second.wire, proxy_b_ingress());
    check(baseline.accepted && attack.accepted,
          "forged client attributes do not break chain validation");
    check(baseline.home_server == attack.home_server,
          "forged attribute cannot change the home server");
    check(baseline.billing_group == attack.billing_group,
          "forged attribute cannot change the billing group");
    check(baseline.roaming_peer == attack.roaming_peer,
          "forged attribute cannot change the roaming identity");
    check(baseline.trusted_operator_attributes ==
              attack.trusted_operator_attributes,
          "policy operator attributes do not come from the client");
    check(baseline.trusted_operator_attributes != forged,
          "forged operator attributes are not adopted as trusted policy");
}

void test_chain_mismatch_is_rejected() {
    Fixture fixture;
    const auto hops = forward_two_hops(fixture, fixture.access_request());
    const Octets home_response =
        fixture.home.respond(hops.second.wire, proxy_b_ingress(),
                             netsec::radius::kAccessAccept);

    Message missing = Message::parse(home_response);
    missing.attributes.erase(
        std::remove_if(missing.attributes.begin(), missing.attributes.end(),
                       [](const Attribute& attribute) {
                           return attribute.type ==
                                  netsec::radius::kProxyStateAttribute;
                       }),
        missing.attributes.end());
    expect_throw<ChainError>(
        [&] { (void)fixture.proxy_b.accept_response(missing.serialize(), hops.second); },
        "response with stripped Proxy-State");

    Message reordered = Message::parse(home_response);
    std::vector<Attribute> states;
    for (auto it = reordered.attributes.begin(); it != reordered.attributes.end();) {
        if (it->type == netsec::radius::kProxyStateAttribute) {
            states.push_back(*it);
            it = reordered.attributes.erase(it);
        } else {
            ++it;
        }
    }
    std::reverse(states.begin(), states.end());
    for (auto& state : states) {
        reordered.attributes.push_back(std::move(state));
    }
    expect_throw<ChainError>(
        [&] {
            (void)fixture.proxy_b.accept_response(reordered.serialize(),
                                                  hops.second);
        },
        "response with reordered Proxy-State chain");

    Message altered = Message::parse(home_response);
    for (auto& attribute : altered.attributes) {
        if (attribute.type == netsec::radius::kProxyStateAttribute) {
            attribute.value.back() ^= 0x01;
            break;
        }
    }
    expect_throw<ChainError>(
        [&] {
            (void)fixture.proxy_b.accept_response(altered.serialize(),
                                                  hops.second);
        },
        "response with an altered Proxy-State value");
}

void test_key_rotation() {
    Fixture fixture;
    Message client = fixture.access_request();
    const auto first = fixture.proxy_a.forward(client.serialize(), nas_ingress());

    KeyRing rotated_home_keys;
    const Octets next_secret = bytes("proxy-b-to-home-c-generation-2");
    rotated_home_keys.install("proxy-b", 1, fixture.proxy_b_secret, false);
    rotated_home_keys.install("proxy-b", 2, next_secret);

    const PolicyRoute rotated_route = fixture.route_home;
    HomeServer rotated_home("home-c", rotated_home_keys, {rotated_route});
    const auto second =
        fixture.proxy_b.forward(first.wire, proxy_a_ingress());
    check(!rotated_home.evaluate(second.wire, proxy_b_ingress()).accepted,
          "retired key generation is not trusted");

    KeyRing active_old;
    active_old.install("proxy-b", 1, fixture.proxy_b_secret, true);
    HomeServer old_home("home-c", active_old, {rotated_route});
    check(old_home.evaluate(second.wire, proxy_b_ingress()).accepted,
          "active old generation remains trusted");
    active_old.retire("proxy-b", 1);
    HomeServer retired_old("home-c", active_old, {rotated_route});
    check(!retired_old.evaluate(second.wire, proxy_b_ingress()).accepted,
          "rotating the key generation invalidates an old chain");

    const PolicyRoute route2{fixture.origin, "home-c", "billing-gold",
                             fixture.trusted_operator_attributes, "home-c", 2};
    KeyRing proxy_b_keys2;
    proxy_b_keys2.install("proxy-a", 1, fixture.proxy_a_secret);
    proxy_b_keys2.install("home-c", 2, next_secret);
    ProxyEngine proxy_b2("proxy-b", proxy_b_keys2, fixture.make_b_sources(),
                         {route2});
    KeyRing home_keys2;
    home_keys2.install("proxy-b", 2, next_secret);
    HomeServer home2("home-c", home_keys2,
                     {PolicyRoute{fixture.origin, "home-c", "billing-gold",
                                  fixture.trusted_operator_attributes,
                                  "proxy-b", 2}});
    const auto second2 = proxy_b2.forward(first.wire, proxy_a_ingress());
    const PolicyDecision rotation = home2.evaluate(second2.wire, proxy_b_ingress());
    check(rotation.accepted,
          "new key generation is accepted after rotation");
    check(rotation.signing_generation == 2,
          "policy records the rotated key generation");
}

void test_source_identity_binding() {
    Fixture fixture;
    const auto hops = forward_two_hops(fixture, fixture.access_request());

    Message bad_nas = Message::parse(hops.second.wire);
    for (auto& attribute : bad_nas.attributes) {
        if (attribute.type == netsec::radius::kNasIdentifierAttribute) {
            attribute.value = bytes("NAS-02");
        }
    }
    check(!fixture.home.evaluate(bad_nas.serialize(), proxy_b_ingress()).accepted,
          "NAS-Identifier is bound by the trusted chain");

    PolicyRoute wrong_peer = fixture.route_home;
    wrong_peer.source.roaming_peer = "other-roam-op";
    HomeServer wrong_peer_home("home-c", fixture.make_home_keys(), {wrong_peer});
    check(!wrong_peer_home.evaluate(hops.second.wire, proxy_b_ingress()).accepted,
          "roaming peer identity must match trusted policy");

    PolicyRoute wrong_generation = fixture.route_home;
    wrong_generation.source.key_generation = 2;
    HomeServer wrong_generation_home("home-c", fixture.make_home_keys(),
                                     {wrong_generation});
    check(!wrong_generation_home.evaluate(hops.second.wire,
                                          proxy_b_ingress()).accepted,
          "shared key generation must match trusted policy");

    check(!fixture.home.evaluate(hops.second.wire,
                                 IngressInfo{"proxy-x", true}).accepted,
          "unknown forwarding peer is rejected");
}

void test_native_authenticators() {
    Fixture fixture;
    Message request = fixture.accounting_request();
    check(netsec::radius::verify_accounting_request_authenticator(
              request, fixture.nas_secret),
          "Accounting-Request authenticator verifies");

    Message tampered = request;
    tampered.attributes.front().value = bytes("NAS-99");
    check(!netsec::radius::verify_accounting_request_authenticator(
              tampered, fixture.nas_secret),
          "modified Accounting-Request breaks its authenticator");

    Message access = fixture.access_request();
    check(netsec::radius::verify_message_authenticator(
              access, fixture.nas_secret),
          "Access-Request Message-Authenticator verifies");
    Message access_tampered = access;
    access_tampered.attributes[1].value = bytes("mallory");
    check(!netsec::radius::verify_message_authenticator(
              access_tampered, fixture.nas_secret),
          "modified Access-Request breaks Message-Authenticator");

    const Octets known_secret = bytes("secret");
    Message known_accounting;
    known_accounting.code = netsec::radius::kAccountingRequest;
    known_accounting.identifier = 23;
    known_accounting.authenticator.assign(16, 0);
    known_accounting.attributes = {
        text_attribute(netsec::radius::kNasIdentifierAttribute, "NAS-01"),
        Attribute{40, u32(1)},
        text_attribute(44, "session-42"),
    };
    netsec::radius::compute_accounting_request_authenticator(
        known_accounting, known_secret);
    const Octets expected_accounting{
        0x71, 0xe5, 0xc4, 0x14, 0x40, 0x9a, 0x43, 0xd1,
        0xd1, 0xb1, 0x41, 0xd2, 0x95, 0x8c, 0xf8, 0xae};
    check(known_accounting.authenticator == expected_accounting,
          "MD5 matches an independent RADIUS accounting vector");

    Message known_access;
    known_access.code = netsec::radius::kAccessRequest;
    known_access.identifier = 1;
    known_access.authenticator.resize(16);
    for (std::size_t i = 0; i < 16; ++i) {
        known_access.authenticator[i] = static_cast<std::uint8_t>(i);
    }
    known_access.attributes = {text_attribute(1, "alice")};
    netsec::radius::apply_message_authenticator(known_access, known_secret);
    const Octets expected_ma{
        0x4e, 0xed, 0x1d, 0xa4, 0x84, 0xf7, 0x36, 0xb2,
        0x82, 0x5c, 0xb6, 0x2f, 0x4d, 0x0f, 0x6d, 0xe8};
    check(known_access.attributes.back().value == expected_ma,
          "HMAC-MD5 matches an independent Message-Authenticator vector");

    const std::array<std::uint8_t, 16> expected_identity{
        0x59, 0x06, 0x58, 0xd3, 0xea, 0x23, 0x9f, 0x9d,
        0xe3, 0x71, 0x4f, 0x51, 0x50, 0xd9, 0x7f, 0x53};
    check(netsec::radius::identity_hash("NAS-01") == expected_identity,
          "SHA-256 identity binding matches an independent vector");
}

void test_packet_parser_rejects_truncation() {
    Fixture fixture;
    const Octets wire = fixture.access_request().serialize();

    Octets truncated = wire;
    truncated.resize(truncated.size() - 1);
    expect_throw<ParseError>(
        [&] { (void)Message::parse(truncated); }, "truncated packet");

    Octets bad_length = wire;
    bad_length[2] = 0;
    bad_length[3] = 20;
    expect_throw<ParseError>(
        [&] { (void)Message::parse(bad_length); }, "lying packet length");

    Octets zero_attribute = wire;
    zero_attribute[20] = 0;
    expect_throw<ParseError>(
        [&] { (void)Message::parse(zero_attribute); }, "reserved attribute type");
}

}  // namespace

int main() {
    const std::vector<std::pair<std::string_view, std::function<void()>>> tests{
        {"strict codec and cardinality", test_strict_codec_and_cardinality},
        {"forward appends state in order", test_forward_appends_state_in_order},
        {"two-hop roaming and response chain",
         test_two_hop_roaming_and_response_chain},
        {"authorization and accounting share ownership",
         test_authorization_and_accounting_share_ownership},
        {"stripped Proxy-State is rejected",
         test_stripped_proxy_state_is_rejected},
        {"client Proxy-State is untrusted",
         test_client_proxy_state_is_untrusted},
        {"forged operator attributes are ignored",
         test_forged_operator_attributes_are_ignored},
        {"chain mismatch is rejected", test_chain_mismatch_is_rejected},
        {"key rotation", test_key_rotation},
        {"source identity binding", test_source_identity_binding},
        {"native authenticators", test_native_authenticators},
        {"packet parser rejects truncation",
         test_packet_parser_rejects_truncation},
    };

    for (const auto& [name, test] : tests) {
        try {
            test();
        } catch (const std::exception& error) {
            ++failures;
            std::cerr << "FAIL: " << name << " threw: " << error.what()
                      << '\n';
        }
    }

    if (failures == 0) {
        std::cout << "all netsec tests passed\n";
        return 0;
    }
    std::cerr << failures << " netsec test(s) failed\n";
    return 1;
}

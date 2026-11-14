#include "netsec.hpp"

#include <chrono>
#include <cstdint>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>

namespace {

int failures = 0;
int checks = 0;

void report_failure(const char* file, int line, const std::string& message) {
    ++failures;
    std::cerr << file << ":" << line << ": FAIL: " << message << "\n";
}

#define CHECK(condition)                                                     \
    do {                                                                     \
        ++checks;                                                            \
        if (!(condition)) {                                                  \
            report_failure(__FILE__, __LINE__, "CHECK(" #condition ")");     \
        }                                                                    \
    } while (false)

using netsec::DataConnectionPolicy;
using netsec::DenyReason;
using netsec::Endpoint;
using netsec::IpAddress;
using netsec::Subnet;
using TimePoint = DataConnectionPolicy::TimePoint;

IpAddress ip(std::string_view text) {
    const auto parsed = IpAddress::parse(text);
    if (!parsed) {
        throw std::runtime_error("bad test IP: " + std::string(text));
    }
    return *parsed;
}

Endpoint ep(std::string_view text) {
    const auto parsed = Endpoint::parse(text);
    if (!parsed) {
        throw std::runtime_error("bad test endpoint: " + std::string(text));
    }
    return *parsed;
}

Subnet sn(std::string_view text) {
    const auto parsed = Subnet::parse(text);
    if (!parsed) {
        throw std::runtime_error("bad test subnet: " + std::string(text));
    }
    return *parsed;
}

struct FakeClock {
    TimePoint value{std::chrono::seconds(1000)};

    TimePoint now() const { return value; }

    void advance(std::chrono::milliseconds delta) { value += delta; }
};

DataConnectionPolicy make_policy(netsec::PolicyConfig config,
                                 FakeClock& clock) {
    return DataConnectionPolicy(std::move(config),
                                [&clock] { return clock.now(); });
}

netsec::CommandConnection command(std::uint64_t id, std::string_view peer,
                                  std::uint16_t port = 2121) {
    netsec::CommandConnection connection;
    connection.session_id = id;
    connection.transport_peer = ep(peer);
    connection.transport_peer.port = port;
    connection.local_endpoint = ep("203.0.113.1:21");
    return connection;
}

void expect_allowed(const netsec::Decision& decision, const char* what) {
    ++checks;
    if (!decision.allowed) {
        report_failure(__FILE__, __LINE__,
                       std::string(what) + " should be allowed, got " +
                           netsec::to_string(decision.reason) + ": " +
                           decision.detail);
    }
}

void expect_denied(const netsec::Decision& decision, DenyReason reason,
                   const char* what) {
    ++checks;
    if (decision.allowed || decision.reason != reason) {
        report_failure(__FILE__, __LINE__,
                       std::string(what) + " should be denied with " +
                           netsec::to_string(reason) + ", got " +
                           (decision.allowed
                                ? "allowed"
                                : netsec::to_string(decision.reason)) +
                           ": " + decision.detail);
    }
}

void expect_auth_denied(const netsec::AuthorizationResult& result,
                        DenyReason reason, const char* what) {
    ++checks;
    if (result.decision.allowed || result.decision.reason != reason) {
        report_failure(__FILE__, __LINE__,
                       std::string(what) + " should be denied with " +
                           netsec::to_string(reason) + ", got " +
                           (result.decision.allowed
                                ? "allowed"
                                : netsec::to_string(result.decision.reason)) +
                           ": " + result.decision.detail);
    }
    ++checks;
    if (result.grant.has_value()) {
        report_failure(__FILE__, __LINE__,
                       std::string(what) + " leaked a grant while denying");
    }
}

netsec::DataRequest pasv(std::string_view address, std::uint16_t port) {
    return netsec::DataRequest{netsec::DataMode::pasv, ip(address), port};
}

netsec::DataRequest active(std::string_view address, std::uint16_t port) {
    return netsec::DataRequest{netsec::DataMode::active, ip(address), port};
}

void test_address_and_subnet_primitives() {
    CHECK(ip("198.51.100.10").to_string() == "198.51.100.10");
    CHECK(ip("2001:db8::1").to_string() == "2001:db8::1");
    CHECK(ip("::1").to_string() == "::1");
    CHECK(ip("::").to_string() == "::");
    CHECK(ip("2001:db8:0:0:1:0:0:1").to_string() ==
          "2001:db8::1:0:0:1");
    CHECK(ip("[2001:db8::2]") == ip("2001:db8::2"));
    CHECK(!IpAddress::parse("2001:db8:::1"));
    CHECK(!IpAddress::parse("1::2::3"));
    CHECK(!IpAddress::parse("fe80::1%en0"));
    CHECK(!IpAddress::parse("1.2.3.256"));
    CHECK(!IpAddress::parse("1.2.3"));

    CHECK(ep("198.51.100.10:2121").to_string() == "198.51.100.10:2121");
    CHECK(ep("[2001:db8::1]:40000").to_string() == "[2001:db8::1]:40000");
    CHECK(!Endpoint::parse("2001:db8::1:40000"));

    const Subnet v4 = sn("198.51.100.0/24");
    CHECK(v4.contains(ip("198.51.100.10")));
    CHECK(!v4.contains(ip("198.51.101.10")));
    CHECK(!v4.contains(ip("2001:db8::1")));
    CHECK(sn("0.0.0.0/0").contains(ip("203.0.113.5")));

    const Subnet v6 = sn("2001:db8:1::/64");
    CHECK(v6.contains(ip("2001:db8:1::abcd")));
    CHECK(!v6.contains(ip("2001:db8:2::1")));
    CHECK(Subnet::parse("198.51.100.0/33") == std::nullopt);
}

void test_address_classification() {
    CHECK(netsec::classify_address(ip("10.1.2.3")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("172.16.0.1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("192.168.1.1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("127.0.0.1")) ==
          netsec::AddressScope::loopback);
    CHECK(netsec::classify_address(ip("169.254.1.1")) ==
          netsec::AddressScope::link_local);
    CHECK(netsec::classify_address(ip("100.64.0.1")) ==
          netsec::AddressScope::shared_address);
    CHECK(netsec::classify_address(ip("224.0.0.1")) ==
          netsec::AddressScope::multicast);
    CHECK(netsec::classify_address(ip("0.0.0.0")) ==
          netsec::AddressScope::unspecified);
    CHECK(netsec::classify_address(ip("::")) ==
          netsec::AddressScope::unspecified);
    CHECK(netsec::classify_address(ip("::1")) ==
          netsec::AddressScope::loopback);
    CHECK(netsec::classify_address(ip("fe80::1")) ==
          netsec::AddressScope::link_local);
    CHECK(netsec::classify_address(ip("fec0::1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("fd12::1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("ff02::1")) ==
          netsec::AddressScope::multicast);
    CHECK(netsec::classify_address(ip("::ffff:10.0.0.1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("::ffff:127.0.0.1")) ==
          netsec::AddressScope::loopback);
    CHECK(netsec::classify_address(ip("::10.0.0.1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("64:ff9b::10.0.0.1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("64:ff9b:1::1")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("2002:0a00:0001::")) ==
          netsec::AddressScope::private_network);
    CHECK(netsec::classify_address(ip("198.51.100.10")) ==
          netsec::AddressScope::global);
    CHECK(netsec::classify_address(ip("2001:db8::1")) ==
          netsec::AddressScope::global);
}

void test_session_registration() {
    FakeClock clock;
    auto policy = make_policy({}, clock);

    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "valid session");
    CHECK(policy.has_session(1));
    expect_denied(policy.open_session(command(1, "198.51.100.10:2121")),
                  DenyReason::session_conflict, "duplicate session id");

    const Subnet source = policy.source_subnet_for(ip("198.51.100.10"));
    CHECK(source.to_string() == "198.51.100.0/24");
    CHECK(source.contains(ip("198.51.100.200")));
    CHECK(!source.contains(ip("198.51.101.1")));

    expect_denied(policy.open_session(command(0, "198.51.100.10:2121")),
                  DenyReason::invalid_input, "session id zero");

    expect_allowed(policy.close_session(1), "close session");
    CHECK(!policy.has_session(1));
}

void test_internal_targets_are_rejected() {
    FakeClock clock;
    auto policy = make_policy({}, clock);
    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "control session");

    const char* targets[] = {
        "10.0.0.5",        "172.16.5.5",      "192.168.1.5",
        "127.0.0.1",       "169.254.1.1",     "100.64.0.5",
        "224.0.0.1",       "0.0.0.0",         "255.255.255.255",
        "::1",             "fe80::1",         "fd00::1",
        "fec0::1",         "ff02::1",         "::ffff:10.0.0.1",
        "::ffff:127.0.0.1",
    };
    for (const char* target : targets) {
        const auto result = policy.authorize(1, pasv(target, 40000));
        expect_auth_denied(result, DenyReason::forbidden_address, target);
    }

    netsec::PolicyConfig private_client_config;
    private_client_config.ipv4_client_prefix = 8;
    auto private_policy = make_policy(private_client_config, clock);
    expect_allowed(private_policy.open_session(command(2, "10.0.0.10:2121")),
                   "private control session");
    expect_auth_denied(private_policy.authorize(2, pasv("10.0.0.10", 40000)),
                       DenyReason::forbidden_address,
                       "private client data target");
}

void test_port_range_enforcement() {
    FakeClock clock;
    auto policy = make_policy({}, clock);
    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "control session");

    for (std::uint16_t port : {std::uint16_t{0}, std::uint16_t{21},
                               std::uint16_t{22}, std::uint16_t{23},
                               std::uint16_t{1023}}) {
        const auto result = policy.authorize(1, pasv("198.51.100.10", port));
        expect_auth_denied(result, DenyReason::port_not_allowed,
                           "jump/well-known port");
    }
    expect_allowed(policy.authorize(1, pasv("198.51.100.10", 1024)).decision,
                   "lowest allowed port");
    expect_allowed(policy.authorize(1, pasv("198.51.100.10", 65535)).decision,
                   "highest allowed port");

    netsec::PolicyConfig narrow;
    narrow.allowed_data_ports = {40000, 40010};
    auto narrow_policy = make_policy(narrow, clock);
    expect_allowed(
        narrow_policy.open_session(command(2, "198.51.100.10:2121")),
        "narrow policy session");
    expect_allowed(
        narrow_policy.authorize(2, pasv("198.51.100.10", 40005)).decision,
        "port in custom range");
    expect_auth_denied(
        narrow_policy.authorize(2, pasv("198.51.100.10", 40011)),
        DenyReason::port_not_allowed, "port above custom range");
}

void test_address_mismatch_and_subnet_checks() {
    FakeClock clock;
    auto policy = make_policy({}, clock);
    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "control session");

    expect_auth_denied(policy.authorize(1, pasv("198.51.100.99", 40000)),
                       DenyReason::address_mismatch,
                       "same-subnet alternate host");
    expect_auth_denied(policy.authorize(1, pasv("203.0.113.9", 40000)),
                       DenyReason::address_outside_client_subnet,
                       "different public subnet");
    expect_auth_denied(policy.authorize(1, active("198.51.100.99", 40000)),
                       DenyReason::address_mismatch,
                       "active alternate host");
    expect_auth_denied(
        policy.authorize(1, netsec::DataRequest{netsec::DataMode::epsv,
                                                ip("198.51.100.99"), 40000}),
        DenyReason::address_mismatch, "EPSV address override");

    netsec::PolicyConfig relaxed;
    relaxed.require_exact_client_address = false;
    auto relaxed_policy = make_policy(relaxed, clock);
    expect_allowed(
        relaxed_policy.open_session(command(2, "198.51.100.10:2121")),
        "relaxed policy session");
    expect_allowed(
        relaxed_policy.authorize(2, pasv("198.51.100.99", 40000)).decision,
        "same-subnet alternate host when exact matching is disabled");
    expect_auth_denied(
        relaxed_policy.authorize(2, pasv("198.51.101.1", 40000)),
        DenyReason::address_outside_client_subnet,
        "outside subnet with relaxed matching");
}

void test_legitimate_passive_flow() {
    FakeClock clock;
    auto policy = make_policy({}, clock);
    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "control session");

    auto auth = policy.authorize(1, pasv("198.51.100.10", 40000));
    expect_allowed(auth.decision, "legitimate PASV");
    CHECK(auth.grant.has_value());
    if (!auth.grant) {
        return;
    }
    CHECK(auth.grant->target == ep("198.51.100.10:40000"));
    CHECK(auth.grant->expected_peer.address == ip("198.51.100.10"));
    CHECK(!auth.grant->compare_peer_port);

    expect_denied(policy.confirm_peer(*auth.grant, ep("198.51.100.99:50000")),
                  DenyReason::peer_mismatch, "other host on client subnet");
    expect_denied(policy.confirm_peer(*auth.grant, ep("10.0.0.5:50000")),
                  DenyReason::peer_mismatch, "internal data peer");
    expect_allowed(policy.confirm_peer(*auth.grant, ep("198.51.100.10:50000")),
                   "data connection back to the client");
    expect_denied(policy.confirm_peer(*auth.grant, ep("198.51.100.10:50001")),
                  DenyReason::grant_consumed, "replayed grant");

    auto second = policy.authorize(1, pasv("198.51.100.10", 40001));
    expect_allowed(second.decision, "second PASV");
    CHECK(second.grant.has_value());
    if (second.grant) {
        expect_allowed(
            policy.confirm_peer(*second.grant, ep("198.51.100.10:61000")),
            "passive data connection may use any ephemeral source port");
    }
}

void test_active_and_proxy_modes() {
    FakeClock clock;
    auto policy = make_policy({}, clock);
    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "control session");

    auto active_auth = policy.authorize(1, active("198.51.100.10", 40001));
    expect_allowed(active_auth.decision, "legitimate PORT");
    CHECK(active_auth.grant.has_value());
    if (active_auth.grant) {
        CHECK(active_auth.grant->compare_peer_port);
        CHECK(active_auth.grant->target == ep("198.51.100.10:40001"));
        expect_denied(
            policy.confirm_peer(*active_auth.grant, ep("198.51.100.10:40002")),
            DenyReason::peer_mismatch, "active wrong port");
        expect_denied(
            policy.confirm_peer(*active_auth.grant, ep("198.51.100.99:40001")),
            DenyReason::peer_mismatch, "active wrong host");
        expect_allowed(
            policy.confirm_peer(*active_auth.grant, ep("198.51.100.10:40001")),
            "active connection to the client endpoint");
    }

    expect_auth_denied(policy.authorize(1, active("10.0.0.5", 40001)),
                       DenyReason::forbidden_address, "active internal target");
    expect_auth_denied(policy.authorize(1, active("198.51.100.10", 22)),
                       DenyReason::port_not_allowed, "active jump port");
    expect_auth_denied(
        policy.authorize(1,
                         netsec::DataRequest{netsec::DataMode::active,
                                             std::nullopt, 40001}),
        DenyReason::address_required, "PORT without an address");

    auto proxy_auth = policy.authorize(
        1, netsec::DataRequest{netsec::DataMode::proxy,
                               ip("198.51.100.10"), 40003});
    expect_allowed(proxy_auth.decision, "proxy-mode client endpoint");
    if (proxy_auth.grant) {
        CHECK(proxy_auth.grant->compare_peer_port);
        expect_allowed(
            policy.confirm_peer(*proxy_auth.grant, ep("198.51.100.10:40003")),
            "proxy-mode peer match");
    }
}

void test_epsv_mode() {
    FakeClock clock;
    auto policy = make_policy({}, clock);
    expect_allowed(policy.open_session(command(1, "[2001:db8::10]:2121")),
                   "IPv6 control session");

    auto auth = policy.authorize(
        1, netsec::DataRequest{netsec::DataMode::epsv, std::nullopt, 40002});
    expect_allowed(auth.decision, "EPSV without declared address");
    CHECK(auth.grant.has_value());
    if (auth.grant) {
        CHECK(auth.grant->target == ep("[2001:db8::10]:40002"));
        CHECK(!auth.grant->compare_peer_port);
        expect_allowed(policy.confirm_peer(*auth.grant,
                                           ep("[2001:db8::10]:52000")),
                       "EPSV peer is the control client");
    }

    expect_allowed(
        policy
            .authorize(1, netsec::DataRequest{netsec::DataMode::epsv,
                                              ip("2001:db8::10"), 40004})
            .decision,
        "EPSV with matching declared address");
    expect_auth_denied(
        policy.authorize(1, netsec::DataRequest{netsec::DataMode::epsv,
                                                ip("2001:db8::11"), 40004}),
        DenyReason::address_mismatch, "EPSV address override");
    expect_auth_denied(
        policy.authorize(1, netsec::DataRequest{netsec::DataMode::epsv,
                                                std::nullopt, 0}),
        DenyReason::port_not_allowed, "EPSV port zero");
}

void test_proxy_protocol_policy() {
    FakeClock clock;
    netsec::PolicyConfig config;
    config.trusted_proxies.push_back(sn("10.0.0.0/8"));
    auto policy = make_policy(config, clock);

    netsec::CommandConnection proxied =
        command(1, "10.0.0.1:9999");
    proxied.proxied_client = ep("198.51.100.10:2121");
    expect_allowed(policy.open_session(proxied), "trusted PROXY session");

    CHECK(policy.source_subnet_for(ip("198.51.100.10")).to_string() ==
          "198.51.100.0/24");
    expect_allowed(policy.authorize(1, pasv("198.51.100.10", 40000)).decision,
                   "proxy pasv to proxied client");
    expect_auth_denied(policy.authorize(1, pasv("10.0.0.5", 40000)),
                       DenyReason::forbidden_address,
                       "proxy session targeting private network");
    expect_auth_denied(policy.authorize(1, pasv("198.51.100.99", 40000)),
                       DenyReason::address_mismatch,
                       "proxy session alternate host");

    netsec::CommandConnection untrusted = command(2, "203.0.113.9:9999");
    untrusted.proxied_client = ep("198.51.100.10:2121");
    expect_denied(policy.open_session(untrusted), DenyReason::proxy_untrusted,
                  "PROXY header from untrusted peer");

    netsec::CommandConnection invalid_source = command(3, "10.0.0.1:9999");
    invalid_source.proxied_client = ep("0.0.0.0:2121");
    expect_denied(policy.open_session(invalid_source),
                  DenyReason::proxy_invalid, "unspecified PROXY source");
}

void test_session_and_grant_binding() {
    FakeClock clock;
    netsec::PolicyConfig config;
    config.session_idle_timeout = std::chrono::milliseconds(1000);
    config.grant_ttl = std::chrono::milliseconds(500);
    auto policy = make_policy(config, clock);

    expect_auth_denied(policy.authorize(99, pasv("198.51.100.10", 40000)),
                       DenyReason::session_unknown,
                       "request without a command session");
    expect_allowed(policy.open_session(command(1, "198.51.100.10:2121")),
                   "control session");

    auto grant = policy.authorize(1, pasv("198.51.100.10", 40000));
    expect_allowed(grant.decision, "grant before expiry");
    CHECK(grant.grant.has_value());
    if (grant.grant) {
        clock.advance(std::chrono::milliseconds(500));
        expect_denied(
            policy.confirm_peer(*grant.grant, ep("198.51.100.10:50000")),
            DenyReason::grant_expired, "grant at exact TTL");
    }

    auto second = policy.authorize(1, pasv("198.51.100.10", 40001));
    expect_allowed(second.decision, "authorization refreshes session activity");
    if (second.grant) {
        policy.close_session(1);
        expect_denied(
            policy.confirm_peer(*second.grant, ep("198.51.100.10:50000")),
            DenyReason::grant_unknown, "grant after session close");
    }

    expect_allowed(policy.open_session(command(2, "198.51.100.10:2121")),
                   "second control session");
    clock.advance(std::chrono::milliseconds(1001));
    expect_auth_denied(policy.authorize(2, pasv("198.51.100.10", 40000)),
                       DenyReason::session_expired,
                       "idle command session expired");
    CHECK(!policy.has_session(2));

    netsec::PolicyConfig limited;
    limited.max_sessions = 1;
    limited.max_pending_grants_per_session = 2;
    auto limited_policy = make_policy(limited, clock);
    expect_allowed(
        limited_policy.open_session(command(10, "198.51.100.10:2121")),
        "limited first session");
    expect_denied(
        limited_policy.open_session(command(11, "198.51.100.11:2121")),
        DenyReason::resource_limit, "session table limit");
    auto first_grant =
        limited_policy.authorize(10, pasv("198.51.100.10", 40000));
    auto second_grant =
        limited_policy.authorize(10, pasv("198.51.100.10", 40001));
    expect_allowed(first_grant.decision, "first pending grant");
    expect_allowed(second_grant.decision, "second pending grant");
    expect_auth_denied(
        limited_policy.authorize(10, pasv("198.51.100.10", 40002)),
        DenyReason::resource_limit, "pending grant limit");
    if (first_grant.grant) {
        expect_allowed(
            limited_policy.confirm_peer(*first_grant.grant,
                                        ep("198.51.100.10:50000")),
            "consume first limited grant");
    }
    if (second_grant.grant) {
        expect_allowed(
            limited_policy.confirm_peer(*second_grant.grant,
                                        ep("198.51.100.10:50001")),
            "consume second limited grant");
    }
    expect_allowed(
        limited_policy.authorize(10, pasv("198.51.100.10", 40002)).decision,
        "used grants do not consume the pending-grant budget");
}

void test_parsers() {
    const auto port = netsec::parse_ftp_port_argument("198,51,100,10,156,64");
    CHECK(port.has_value());
    if (port) {
        CHECK(*port == ep("198.51.100.10:40000"));
    }
    CHECK(!netsec::parse_ftp_port_argument("198,51,100,10,156"));
    CHECK(!netsec::parse_ftp_port_argument("198,51,100,256,156,64"));
    CHECK(!netsec::parse_ftp_port_argument("0,0,0,0,0,0"));

    const auto eprt4 = netsec::parse_eprt_argument("|1|198.51.100.10|40000|");
    CHECK(eprt4.has_value());
    if (eprt4) {
        CHECK(*eprt4 == ep("198.51.100.10:40000"));
    }
    const auto eprt6 = netsec::parse_eprt_argument("|2|2001:db8::1|40001|");
    CHECK(eprt6.has_value());
    if (eprt6) {
        CHECK(*eprt6 == ep("[2001:db8::1]:40001"));
    }
    CHECK(!netsec::parse_eprt_argument("|1|2001:db8::1|40001|"));
    CHECK(!netsec::parse_eprt_argument("|1|198.51.100.10|0|"));
    CHECK(!netsec::parse_eprt_argument("|1|198.51.100.10|40001"));

    const auto pasv_reply = netsec::parse_pasv_response(
        "227 Entering Passive Mode (198,51,100,10,156,64)\r\n");
    CHECK(pasv_reply.has_value());
    if (pasv_reply) {
        CHECK(*pasv_reply == ep("198.51.100.10:40000"));
    }
    CHECK(!netsec::parse_pasv_response("227 missing endpoint"));

    const auto epsv_reply = netsec::parse_epsv_response(
        "229 Entering Extended Passive Mode (|||40002|)\r\n");
    CHECK(epsv_reply.has_value());
    if (epsv_reply) {
        CHECK(*epsv_reply == 40002);
    }
    CHECK(!netsec::parse_epsv_response("229 Entering Extended Passive Mode"));
    CHECK(!netsec::parse_epsv_response("229 ... (|||0|)"));

    const auto proxy = netsec::parse_proxy_v1(
        "PROXY TCP4 198.51.100.10 203.0.113.1 51000 21\r\n");
    CHECK(proxy.has_value());
    if (proxy) {
        CHECK(proxy->source == ep("198.51.100.10:51000"));
        CHECK(proxy->destination == ep("203.0.113.1:21"));
    }
    const auto proxy6 = netsec::parse_proxy_v1(
        "PROXY TCP6 2001:db8::10 2001:db8::1 51000 21\r\n");
    CHECK(proxy6.has_value());
    CHECK(!netsec::parse_proxy_v1(
        "PROXY TCP4 2001:db8::10 203.0.113.1 51000 21\r\n"));
    CHECK(!netsec::parse_proxy_v1("PROXY UNKNOWN\r\n"));
    CHECK(!netsec::parse_proxy_v1("GET / HTTP/1.1\r\n"));

    const auto formatted = netsec::format_ftp_port_argument(
        ep("198.51.100.10:40000"));
    CHECK(formatted.has_value());
    if (formatted) {
        CHECK(*formatted == "198,51,100,10,156,64");
    }
    CHECK(!netsec::format_ftp_port_argument(ep("[2001:db8::1]:40000")));
    CHECK(netsec::format_epsv_argument(40002) == "(|||40002|)");
}

}  // namespace

int main() {
    try {
        test_address_and_subnet_primitives();
        test_address_classification();
        test_session_registration();
        test_internal_targets_are_rejected();
        test_port_range_enforcement();
        test_address_mismatch_and_subnet_checks();
        test_legitimate_passive_flow();
        test_active_and_proxy_modes();
        test_epsv_mode();
        test_proxy_protocol_policy();
        test_session_and_grant_binding();
        test_parsers();
    } catch (const std::exception& error) {
        std::cerr << "fatal: " << error.what() << "\n";
        return 2;
    }

    if (failures != 0) {
        std::cerr << failures << " failure(s) across " << checks
                  << " checks\n";
        return 1;
    }
    std::cout << "all " << checks << " checks passed\n";
    return 0;
}

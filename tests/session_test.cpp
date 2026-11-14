#include "netsec.hpp"

#include <atomic>
#include <mutex>
#include <set>
#include <string>
#include <thread>
#include <utility>
#include <vector>

#include <cstdio>

using namespace netsec;

namespace {

int g_failures = 0;
std::mutex g_setMutex;

#define CHECK(cond)                                                          \
    do {                                                                     \
        if (!(cond)) {                                                       \
            std::printf("CHECK failed: %s (%s:%d)\n", #cond, __FILE__,      \
                        __LINE__);                                           \
            ++g_failures;                                                    \
        }                                                                    \
    } while (0)

RequestSource makeSource(std::string ip, std::uint16_t port,
                         std::string principal = "",
                         bool authenticated = false) {
    RequestSource s;
    s.peerIp = std::move(ip);
    s.peerPort = port;
    s.principal = std::move(principal);
    s.authenticated = authenticated;
    return s;
}

TransportParams udpTransport(std::string mediaIp = "",
                             std::uint16_t portA = 5000,
                             std::uint16_t portB = 5001) {
    TransportParams t;
    t.mode = TransportMode::UnicastUdp;
    t.unicast = true;
    t.mediaIp = std::move(mediaIp);
    t.clientPortA = portA;
    t.clientPortB = portB;
    t.hasClientPort = true;
    return t;
}

void testSessionIdUnpredictableAndOneTime() {
    SessionManager manager;
    std::set<std::string> ids;
    std::vector<std::pair<std::string, RequestSource>> bindings;
    constexpr int kCount = 2000;
    for (int i = 0; i < kCount; ++i) {
        std::string id;
        RequestSource origin = makeSource(
            "198.51.100.10", static_cast<std::uint16_t>(40000 + i));
        VerifyResult result =
            manager.setup(origin, udpTransport(), id);
        CHECK(result.ok);
        CHECK(id.size() == kSessionIdEncodedLen);
        CHECK(ids.insert(id).second); // globally unique, never reissued
        CHECK(manager.hasSession(id));
        bindings.emplace_back(id, origin);
    }
    CHECK(manager.activeCount() == static_cast<std::size_t>(kCount));

    std::string raw;
    CHECK(SessionManager::generateRandomId(raw));
    CHECK(raw.size() == kSessionIdEncodedLen);
    CHECK(ids.count(raw) == 0);

    // TEARDOWN tombstones the id: it can never be played or re-bound.
    auto [victim, victimSource] = bindings.front();
    CHECK(manager.teardown(victim, victimSource).ok);
    CHECK(!manager.hasSession(victim));
    VerifyResult replay = manager.play(victim, victimSource, udpTransport());
    CHECK(!replay.ok);
    CHECK(replay.reason == RejectReason::OneTimeSessionReused);
}

void testCopiedSessionIdCannotHijack() {
    SessionManager manager;

    std::string id;
    auto victim = makeSource("198.51.100.10", 40000, "alice", true);
    CHECK(manager.setup(victim, udpTransport("198.51.100.10"), id).ok);
    CHECK(manager.play(id, victim, udpTransport("198.51.100.10")).ok);

    // Attacker copies the id and asks for media at its own address/ports.
    auto attacker = makeSource("203.0.113.66", 51000, "mallory", true);
    VerifyResult attack = manager.play(
        id, attacker, udpTransport("203.0.113.66", 6000, 6001));
    CHECK(!attack.ok);
    CHECK(attack.reason == RejectReason::SourceAddressMismatch);
    CHECK(!manager.mediaTarget(id, attacker).has_value());

    // The legitimate client still receives media at the bound destination.
    auto target = manager.mediaTarget(id, victim);
    CHECK(target.has_value());
    CHECK(target->ip == "198.51.100.10");
    CHECK(target->port == 5000);
}

void testAddressChangeFollowsExplicitPolicy() {
    // Strict default: any source change (even a port change) is rejected.
    SessionManager strict;
    std::string id;
    auto origin = makeSource("198.51.100.10", 40000);
    CHECK(strict.setup(origin, udpTransport(), id).ok);
    auto rebindPort = makeSource("198.51.100.10", 41111);
    auto newIp = makeSource("198.51.100.11", 40000);
    CHECK(strict.play(id, rebindPort, udpTransport()).reason ==
          RejectReason::SourceAddressMismatch);
    CHECK(strict.play(id, newIp, udpTransport()).reason ==
          RejectReason::SourceAddressMismatch);

    // Same IP, changing port allowed (classic UDP NAT rebinding).
    SessionManager portPolicy(NatPolicy::SameIpAllowPortChange);
    std::string id2;
    CHECK(portPolicy.setup(origin, udpTransport(), id2).ok);
    CHECK(portPolicy.play(id2, rebindPort, udpTransport()).ok);
    CHECK(!portPolicy.play(id2, newIp, udpTransport()).ok);

    // Same /24 network rebind allowed; a foreign network is rejected.
    SessionManager netPolicy(NatPolicy::SameNetworkAllowRebind);
    std::string id3;
    CHECK(netPolicy.setup(origin, udpTransport(), id3).ok);
    CHECK(netPolicy.play(id3, newIp, udpTransport()).ok);
    auto foreign = makeSource("203.0.113.10", 40000);
    CHECK(!netPolicy.play(id3, foreign, udpTransport()).ok);

    // Authenticated rebind: the same principal may move anywhere; anonymous
    // or different-principal requests cannot.
    SessionManager authPolicy(NatPolicy::AuthenticatedRebind);
    std::string id4;
    auto authedOrigin = makeSource("198.51.100.10", 40000, "alice", true);
    CHECK(authPolicy.setup(authedOrigin, udpTransport(), id4).ok);
    auto authedMoved = makeSource("198.51.100.99", 40000, "alice", true);
    CHECK(authPolicy.play(id4, authedMoved, udpTransport()).ok);
    auto anonymousMoved = makeSource("198.51.100.99", 40000);
    CHECK(!authPolicy.play(id4, anonymousMoved, udpTransport()).ok);
    auto otherPrincipal =
        makeSource("198.51.100.99", 40000, "mallory", true);
    CHECK(!authPolicy.play(id4, otherPrincipal, udpTransport()).ok);
}

void testTransportSubstitutionRejected() {
    SessionManager manager;
    std::string id;
    auto victim = makeSource("198.51.100.10", 40000);
    CHECK(manager.setup(victim, udpTransport("198.51.100.10"), id).ok);

    // Same source, but each attempt redirects a transport field.
    CHECK(manager.play(id, victim, udpTransport("203.0.113.66")).reason ==
          RejectReason::TransportMismatch);
    CHECK(manager
              .play(id, victim,
                    udpTransport("198.51.100.10", 7000, 7001))
              .reason == RejectReason::TransportMismatch);

    TransportParams ttlChanged = udpTransport("198.51.100.10");
    ttlChanged.ttl = 7;
    CHECK(manager.play(id, victim, ttlChanged).reason ==
          RejectReason::TransportMismatch);

    // Raw Transport header with a swapped destination/ports is also rejected.
    VerifyResult headerAttack = manager.play(
        id, victim,
        "RTP/AVP;unicast;client_port=8000-8001;destination=203.0.113.66");
    CHECK(!headerAttack.ok);
    CHECK(headerAttack.reason == RejectReason::TransportMismatch);

    // Exact match from the original source is accepted.
    CHECK(manager.play(id, victim, udpTransport("198.51.100.10")).ok);
}

void testLegitimateSessionFlowAndPrincipalBinding() {
    SessionManager manager;
    std::string id;
    auto alice = makeSource("198.51.100.10", 40000, "alice", true);
    auto transport = udpTransport();

    VerifyResult setupResult = manager.setup(
        alice, "RTP/AVP;unicast;client_port=5000-5001", id);
    CHECK(setupResult.ok);
    CHECK(setupResult.generation == 1);

    auto info = manager.lookup(id);
    CHECK(info.has_value());
    CHECK(info->state == SessionState::Init);
    CHECK(info->transport == transport);

    VerifyResult playResult = manager.play(id, alice, transport);
    CHECK(playResult.ok);
    CHECK(playResult.generation == 2);

    auto target = manager.mediaTarget(id, alice);
    CHECK(target.has_value());
    // No explicit destination: fall back to the bound peer IP.
    CHECK(target->ip == "198.51.100.10");
    CHECK(target->port == 5000);
    CHECK(target->generation == 2);

    // A request claiming another principal (or dropping credentials) fails.
    auto mallory = makeSource("198.51.100.10", 40000, "mallory", true);
    CHECK(manager.play(id, mallory, transport).reason ==
          RejectReason::PrincipalMismatch);
    auto anonymous = makeSource("198.51.100.10", 40000);
    CHECK(manager.play(id, anonymous, transport).reason ==
          RejectReason::PrincipalMismatch);

    // Pause stops media; mediaTarget returns nothing until PLAY resumes.
    CHECK(manager.pause(id, alice).ok);
    CHECK(!manager.mediaTarget(id, alice).has_value());
    CHECK(manager.play(id, alice, transport).ok);
    CHECK(manager.mediaTarget(id, alice).has_value());

    CHECK(!manager.mediaTarget("not-a-real-session", alice).has_value());
}

void testTransportHeaderParsing() {
    auto tcp = parseTransportHeader("RTP/AVP/TCP;unicast;interleaved=0-1");
    CHECK(tcp.has_value());
    CHECK(tcp->mode == TransportMode::UnicastTcp);
    CHECK(tcp->hasInterleaved);
    CHECK(tcp->interleavedA == 0);
    CHECK(tcp->interleavedB == 1);

    auto udp = parseTransportHeader(
        "RTP/AVP;unicast;client_port=5000-5001;ttl=16");
    CHECK(udp.has_value());
    CHECK(udp->mode == TransportMode::UnicastUdp);
    CHECK(udp->clientPortA == 5000);
    CHECK(udp->clientPortB == 5001);
    CHECK(udp->ttl == 16);

    auto other = parseTransportHeader("RTP/AVP/MULTICAST;multicast");
    CHECK(other.has_value());
    CHECK(other->mode == TransportMode::Other);
    CHECK(!other->unicast);

    CHECK(!parseTransportHeader("UNKNOWN/PROFILE").has_value());
}

void testConcurrentPlayOnlyDeliversToBoundClient() {
    SessionManager manager;
    std::string id;
    auto victim = makeSource("198.51.100.10", 40000);
    CHECK(manager.setup(victim, udpTransport("198.51.100.10"), id).ok);
    CHECK(manager.play(id, victim, udpTransport("198.51.100.10")).ok);

    constexpr int kThreads = 16;
    constexpr int kIterations = 2000;
    std::atomic<int> acceptedVictim{0};
    std::atomic<int> rejectedAttacker{0};
    std::atomic<int> leakedToAttacker{0};
    std::vector<std::thread> threads;

    for (int t = 0; t < kThreads; ++t) {
        bool attackerThread = (t % 2 == 1);
        threads.emplace_back([&, attackerThread]() {
            for (int i = 0; i < kIterations; ++i) {
                if (attackerThread) {
                    auto attacker =
                        makeSource("203.0.113.66",
                                   static_cast<std::uint16_t>(51000 +
                                                               (i % 100)));
                    VerifyResult result = manager.play(
                        id, attacker,
                        udpTransport("203.0.113.66", 6000, 6001));
                    if (!result.ok) {
                        rejectedAttacker.fetch_add(
                            1, std::memory_order_relaxed);
                    }
                    if (manager.mediaTarget(id, attacker).has_value()) {
                        leakedToAttacker.fetch_add(
                            1, std::memory_order_relaxed);
                    }
                } else {
                    VerifyResult result = manager.play(
                        id, victim, udpTransport("198.51.100.10"));
                    if (result.ok) {
                        acceptedVictim.fetch_add(
                            1, std::memory_order_relaxed);
                    }
                    auto target = manager.mediaTarget(id, victim);
                    if (target && (target->ip != "198.51.100.10" ||
                                   target->port != 5000)) {
                        leakedToAttacker.fetch_add(
                            1, std::memory_order_relaxed);
                    }
                }
            }
        });
    }
    for (auto& thread : threads) {
        thread.join();
    }

    constexpr int perRole = (kThreads / 2) * kIterations;
    CHECK(acceptedVictim.load() == perRole);
    CHECK(rejectedAttacker.load() == perRole);
    CHECK(leakedToAttacker.load() == 0);

    // Concurrent SETUP under contention never issues duplicate ids.
    SessionManager fresh;
    std::set<std::string> concurrentIds;
    std::vector<std::thread> producers;
    for (int t = 0; t < 8; ++t) {
        producers.emplace_back([&, t]() {
            for (int i = 0; i < 500; ++i) {
                std::string newId;
                VerifyResult result = fresh.setup(
                    makeSource("198.51.100." + std::to_string(t),
                               static_cast<std::uint16_t>(1000 + i)),
                    udpTransport(), newId);
                CHECK(result.ok);
                std::lock_guard<std::mutex> guard(g_setMutex);
                CHECK(concurrentIds.insert(newId).second);
            }
        });
    }
    for (auto& thread : producers) {
        thread.join();
    }
    CHECK(concurrentIds.size() == 8 * 500);
    CHECK(fresh.activeCount() == 8 * 500);
}

} // namespace

int main() {
    testSessionIdUnpredictableAndOneTime();
    testCopiedSessionIdCannotHijack();
    testAddressChangeFollowsExplicitPolicy();
    testTransportSubstitutionRejected();
    testLegitimateSessionFlowAndPrincipalBinding();
    testTransportHeaderParsing();
    testConcurrentPlayOnlyDeliversToBoundClient();
    if (g_failures != 0) {
        std::printf("%d check(s) failed\n", g_failures);
        return 1;
    }
    std::printf("all session security checks passed\n");
    return 0;
}

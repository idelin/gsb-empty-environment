#include "netsec.hpp"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <iostream>
#include <string>
#include <thread>
#include <vector>

namespace {

using namespace std::chrono_literals;

int failures = 0;

void check(bool condition, const std::string& what) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << what << '\n';
    }
}

netsec::DeviceIdentity victim() {
    return netsec::DeviceIdentity{"02:00:00:00:00:01", 34964, "line-1"};
}

netsec::DeviceIdentity attacker() {
    return netsec::DeviceIdentity{"02:00:00:00:00:66", 34964, "line-1"};
}

netsec::Config testConfig(
    netsec::ConflictPolicy policy =
        netsec::ConflictPolicy::AllowAfterReservation) {
    netsec::Config config;
    config.default_lease = 10s;
    config.default_reservation = 20s;
    config.conflict_policy = policy;
    return config;
}

void startClock(netsec::NameOwnership& registry) {
    registry.freeze(netsec::ClockPoint{});
}

void testLegitimateAssignment() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    const auto result = registry.assign("plc-1", victim(), 10s);
    check(result.ok(), "rightful device can claim a free name");

    const auto binding = registry.lookup("plc-1");
    check(binding.has_value(), "binding exists after assignment");
    check(binding && binding->owner == victim(), "binding stores full identity");
    check(binding && binding->mac() == victim().mac, "binding stores MAC");
    check(binding && binding->port() == 34964, "binding stores port");
    check(binding && binding->station() == "line-1", "binding stores station");
    check(binding && binding->lease == 10s, "binding stores lease");
    check(registry.isOwner("plc-1", victim()), "rightful device is owner");
}

void testNamePreemption() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");

    const auto stolen = registry.assign("plc-1", attacker(), 10s);
    check(stolen.status == netsec::Status::Conflict,
          "attacker cannot preempt a live name");
    check(registry.ownerOf("plc-1") == victim(),
          "ownership is unchanged after a failed preemption");

    check(registry.assign("plc-1", attacker(), 10s, true).status ==
              netsec::Status::MulticastRestricted,
          "multicast request cannot preempt a name");
    check(registry.ownerOf("plc-1") == victim(),
          "multicast request leaves ownership unchanged");
}

void testConflictingAllocation() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");

    auto wrongPort = victim();
    wrongPort.port = 34965;
    auto wrongStation = victim();
    wrongStation.station = "line-2";

    check(registry.assign("plc-1", wrongPort, 10s).status ==
              netsec::Status::Conflict,
          "same MAC with a different port conflicts");
    check(registry.assign("plc-1", wrongStation, 10s).status ==
              netsec::Status::Conflict,
          "same MAC with a different station conflicts");
    check(registry.ownerOf("plc-1") == victim(),
          "conflicting allocations do not change ownership");
}

void testReservationPeriod() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");
    check(registry.markOffline("plc-1", victim()).ok(), "owner goes offline");
    check(registry.isReserved("plc-1"), "offline name is reserved");
    check(registry.lookup("plc-1")->reserved_at(registry.now()),
          "public binding reports the reservation");

    registry.advance(19s);
    const auto blocked = registry.assign("plc-1", attacker(), 10s);
    check(blocked.status == netsec::Status::ReservationActive,
          "offline name cannot be preempted during the reservation");
    check(registry.ownerOf("plc-1") == victim(),
          "rejected attacker did not become the owner");

    registry.advance(1s);
    check(!registry.lookup("plc-1")->reserved_at(registry.now()),
          "reservation ends at the retention boundary");
    const auto allowed = registry.assign("plc-1", attacker(), 10s);
    check(allowed.ok(), "name may be reused after the reservation elapses");
    check(registry.ownerOf("plc-1") == attacker(),
          "takeover only happens after the retention period");
}

void testLeaseExpiryStartsReservation() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");

    registry.advance(10s);
    check(registry.assign("plc-1", attacker(), 10s).status ==
              netsec::Status::ReservationActive,
          "expired lease enters the reservation period");

    registry.advance(20s);
    check(registry.assign("plc-1", attacker(), 10s).ok(),
          "expired lease becomes reusable after reservation");
}

void testRightfulReclaimDuringReservation() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");
    check(registry.markOffline("plc-1", victim()).ok(), "owner goes offline");
    registry.advance(5s);

    check(registry.assign("plc-1", victim(), 10s).ok(),
          "original device may reclaim its own reserved name");
    check(registry.lookup("plc-1")->online,
          "rightful reclaim restores the online state");
    check(registry.assign("plc-1", attacker(), 10s).status ==
              netsec::Status::Conflict,
          "attacker remains locked out after rightful reclaim");
}

void testRenameRestrictions() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");
    check(registry.assign("plc-2", attacker(), 10s).ok(), "attacker owns another name");

    check(registry.rename("plc-1", "plc-x", attacker()).status ==
              netsec::Status::OwnershipMismatch,
          "non-owner cannot rename a name");
    check(registry.rename("plc-1", "plc-2", victim()).status ==
              netsec::Status::Conflict,
          "rename onto a live owned name is rejected");
    check(registry.ownerOf("plc-1") == victim(),
          "failed rename leaves the old binding intact");
    check(registry.ownerOf("plc-2") == attacker(),
          "failed rename leaves the target binding intact");

    check(registry.rename("plc-1", "plc-x", victim(), 10s, true).status ==
              netsec::Status::MulticastRestricted,
          "multicast rename is rejected");

    check(registry.rename("plc-1", "plc-x", victim(), 10s).ok(),
          "owner can rename its own name");
    check(registry.ownerOf("plc-x") == victim(), "renamed name has same owner");
    check(!registry.lookup("plc-1"), "old name is released after rename");
    check(registry.reset("plc-x", attacker()).status ==
              netsec::Status::OwnershipMismatch,
          "attacker cannot reset the renamed name");

    check(registry.assign("plc-y", victim(), 10s).ok(), "victim owns third name");
    registry.advance(31s);
    check(registry.rename("plc-y", "plc-2", victim(), 10s).ok(),
          "rename may reuse a target after its reservation expires");
    check(registry.ownerOf("plc-2") == victim(),
          "renamed target is bound to the renaming device");
}

void testResetRestrictions() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");
    check(registry.markOffline("plc-1", victim()).ok(), "owner goes offline");

    check(registry.reset("plc-1", attacker()).status ==
              netsec::Status::OwnershipMismatch,
          "attacker cannot reset an offline device's name");
    check(registry.reset("plc-1", attacker(), true).status ==
              netsec::Status::MulticastRestricted,
          "multicast reset is rejected");
    check(registry.ownerOf("plc-1") == victim(),
          "failed resets preserve ownership");

    check(registry.reset("plc-1", victim()).ok(), "owner can reset its name");
    check(!registry.lookup("plc-1"), "owner reset releases the name");
    check(registry.assign("plc-1", attacker(), 10s).ok(),
          "name is free after the rightful owner resets it");
}

void testMulticastAndStrictPolicy() {
    netsec::NameOwnership registry(
        testConfig(netsec::ConflictPolicy::KeepOwner));
    startClock(registry);
    check(registry.assign("plc-1", victim(), 10s, true).status ==
              netsec::Status::MulticastRestricted,
          "multicast assignment is rejected");
    check(!registry.lookup("plc-1"), "rejected multicast creates no binding");

    check(registry.assign("plc-1", victim(), 10s).ok(), "victim owns name");
    registry.advance(31s);
    check(registry.assign("plc-1", attacker(), 10s).status ==
              netsec::Status::Conflict,
          "strict policy never permits replacement");

    check(registry.assign("PLC-1", attacker(), 10s).status ==
              netsec::Status::Conflict,
          "name matching is case-insensitive");
}

void testInvalidOwnerIsRejected() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);
    const netsec::DeviceIdentity zeroMac{
        netsec::MacAddress{}, 34964, "line-1"};
    check(registry.assign("plc-1", zeroMac) == netsec::Status::InvalidMac,
          "a non-unicast/zero owner MAC is rejected");
    check(!registry.lookup("plc-1"),
          "invalid owner does not create a binding");
}

void testConcurrentAllocationIsAtomic() {
    netsec::NameOwnership registry(testConfig());
    startClock(registry);

    constexpr int contenders = 8;
    std::atomic<int> successes{0};
    std::vector<std::thread> threads;
    threads.reserve(contenders);
    for (int i = 0; i < contenders; ++i) {
        threads.emplace_back([&, i] {
            auto identity = netsec::DeviceIdentity{
                netsec::MacAddress{std::uint8_t{0x02}, 0, 0, 0, 0,
                                   static_cast<std::uint8_t>(i + 1)},
                34964, "line-1"};
            if (registry.assign("race", identity, 10s)) {
                successes.fetch_add(1, std::memory_order_relaxed);
            }
        });
    }
    for (auto& thread : threads) {
        thread.join();
    }

    check(successes.load(std::memory_order_relaxed) == 1,
          "exactly one concurrent allocator wins a free name");
    check(registry.ownerOf("race").has_value(),
          "concurrent allocation records one owner");
}

}  // namespace

int main() {
    testLegitimateAssignment();
    testNamePreemption();
    testConflictingAllocation();
    testReservationPeriod();
    testLeaseExpiryStartsReservation();
    testRightfulReclaimDuringReservation();
    testRenameRestrictions();
    testResetRestrictions();
    testMulticastAndStrictPolicy();
    testInvalidOwnerIsRejected();
    testConcurrentAllocationIsAtomic();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all netsec tests passed\n";
    return EXIT_SUCCESS;
}

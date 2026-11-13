#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ':' << __LINE__                         \
                      << ": check failed: " #condition << '\n';              \
            ++failures;                                                      \
        }                                                                    \
    } while (false)

netsec::MacAddress mac(const char* text) {
    return netsec::MacAddress::parse(text);
}

netsec::SvFrame frame(const std::string& svID, const netsec::MacAddress& source,
                      std::uint32_t confRev, std::uint32_t smpCnt) {
    return netsec::SvFrame{svID, source, confRev, smpCnt};
}

void testMacAddressParsing() {
    const auto colon = mac("00:11:22:33:44:55");
    const auto dash = mac("00-11-22-33-44-55");
    const auto compact = mac("001122334455");

    CHECK(colon == dash);
    CHECK(colon == compact);
    CHECK(colon.toString() == "00:11:22:33:44:55");
    CHECK(!netsec::MacAddress::tryParse("00:11:22:33:44"));
    CHECK(!netsec::MacAddress::tryParse("00:11:22:33:44:55:66"));
    CHECK(!netsec::MacAddress::tryParse("00:11:22:33:44:zz"));
    CHECK(netsec::MacAddress(0x00, 0x11, 0x22, 0x33, 0x44, 0x55) == colon);
}

void testFirstFrameBindsIdentity() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    const auto decision = subscriber.receive(frame("mu01", source, 7, 123));
    CHECK(decision.accepted());
    CHECK(decision.status == netsec::SvStatus::Accepted);
    CHECK(decision.receivedSmpCnt == 123);
    CHECK(decision.expectedSmpCnt == 123);
    CHECK(decision.lostSamples == 0);
    CHECK(subscriber.isBound("mu01"));
    CHECK(!subscriber.isLocked("mu01"));

    const auto binding = subscriber.binding("mu01");
    CHECK(binding.has_value());
    CHECK(binding->sourceMac == source);
    CHECK(binding->confRev == 7);
    CHECK(!binding->awaitingReconfirmation);
}

void testSequentialFrames() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 10)).accepted());
    for (std::uint32_t counter = 11; counter < 30; ++counter) {
        const auto decision = subscriber.receive(frame("mu01", source, 1, counter));
        CHECK(decision.accepted());
        CHECK(decision.lostSamples == 0);
        CHECK(decision.expectedSmpCnt == counter);
        CHECK(decision.invalidSmpCnts.empty());
    }

    const auto stats = subscriber.stats("mu01");
    CHECK(stats.receivedFrames == 20);
    CHECK(stats.acceptedFrames == 20);
    CHECK(stats.duplicateFrames == 0);
    CHECK(stats.replayFrames == 0);
    CHECK(stats.lostSamples == 0);
    CHECK(stats.lastSmpCnt == 29);
}

void testReplayAndDuplicateRejection() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    for (std::uint32_t counter = 0; counter <= 10; ++counter) {
        CHECK(subscriber.receive(frame("mu01", source, 1, counter)).accepted());
    }

    const auto duplicate = subscriber.receive(frame("mu01", source, 1, 10));
    CHECK(!duplicate.accepted());
    CHECK(duplicate.status == netsec::SvStatus::Duplicate);
    CHECK(duplicate.duplicate());
    CHECK(duplicate.lostSamples == 0);

    const auto stale = subscriber.receive(frame("mu01", source, 1, 5));
    CHECK(!stale.accepted());
    CHECK(stale.status == netsec::SvStatus::Replay);
    CHECK(stale.replay());
    CHECK(stale.lostSamples == 0);

    CHECK(subscriber.receive(frame("mu01", source, 1, 11)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 1, 10)).status ==
          netsec::SvStatus::Replay);
    CHECK(subscriber.receive(frame("mu01", source, 1, 11)).status ==
          netsec::SvStatus::Duplicate);
    CHECK(subscriber.receive(frame("mu01", source, 1, 12)).accepted());

    const auto stats = subscriber.stats("mu01");
    CHECK(stats.acceptedFrames == 13);
    CHECK(stats.duplicateFrames == 2);
    CHECK(stats.replayFrames == 2);
    CHECK(stats.lostSamples == 0);
}

void testGapMarksIntermediateSamplesInvalid() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 100)).accepted());

    const auto firstGap = subscriber.receive(frame("mu01", source, 1, 104));
    CHECK(firstGap.accepted());
    CHECK(firstGap.status == netsec::SvStatus::Accepted);
    CHECK(firstGap.receivedSmpCnt == 104);
    CHECK(firstGap.expectedSmpCnt == 101);
    CHECK(firstGap.lostSamples == 3);
    CHECK(firstGap.lost() == 3);
    CHECK(firstGap.gapStart == 101);
    CHECK(firstGap.gapEnd == 103);
    CHECK(firstGap.invalidSmpCnts == std::vector<std::uint32_t>({101, 102, 103}));
    CHECK(firstGap.totalLostSamples == 3);

    const auto duplicate = subscriber.receive(frame("mu01", source, 1, 104));
    CHECK(duplicate.status == netsec::SvStatus::Duplicate);
    CHECK(duplicate.lostSamples == 0);

    const auto secondGap = subscriber.receive(frame("mu01", source, 1, 108));
    CHECK(secondGap.accepted());
    CHECK(secondGap.lostSamples == 3);
    CHECK(secondGap.invalidSmpCnts == std::vector<std::uint32_t>({105, 106, 107}));
    CHECK(secondGap.totalLostSamples == 6);

    const auto stats = subscriber.stats("mu01");
    CHECK(stats.acceptedFrames == 3);
    CHECK(stats.lostSamples == 6);
    CHECK(stats.invalidSamples == 6);
    CHECK(stats.gapEvents == 2);
}

void testCounterWrap() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    for (std::uint32_t counter = 3996; counter <= 3999; ++counter) {
        CHECK(subscriber.receive(frame("mu01", source, 1, counter)).accepted());
    }

    const auto wrap = subscriber.receive(frame("mu01", source, 1, 0));
    CHECK(wrap.accepted());
    CHECK(wrap.expectedSmpCnt == 0);
    CHECK(wrap.lostSamples == 0);
    CHECK(subscriber.receive(frame("mu01", source, 1, 1)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 1, 1)).status ==
          netsec::SvStatus::Duplicate);
    CHECK(subscriber.receive(frame("mu01", source, 1, 3999)).status ==
          netsec::SvStatus::Replay);
    CHECK(subscriber.receive(frame("mu01", source, 1, 2)).accepted());

    const auto stats = subscriber.stats("mu01");
    CHECK(stats.acceptedFrames == 7);
    CHECK(stats.lostSamples == 0);
    CHECK(stats.lastSmpCnt == 2);
}

void testFullCounterCycle() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 0)).accepted());
    for (std::uint32_t counter = 1; counter < netsec::kSmpCntModulus; ++counter) {
        const auto decision = subscriber.receive(frame("mu01", source, 1, counter));
        CHECK(decision.accepted());
        CHECK(decision.lostSamples == 0);
    }
    CHECK(subscriber.receive(frame("mu01", source, 1, 0)).accepted());

    const auto stats = subscriber.stats("mu01");
    CHECK(stats.acceptedFrames == 4001);
    CHECK(stats.lostSamples == 0);
    CHECK(stats.replayFrames == 0);
    CHECK(stats.lastSmpCnt == 0);
}

void testGapAcrossWrap() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 3996)).accepted());
    const auto gap = subscriber.receive(frame("mu01", source, 1, 0));
    CHECK(gap.accepted());
    CHECK(gap.expectedSmpCnt == 3997);
    CHECK(gap.lostSamples == 3);
    CHECK(gap.gapStart == 3997);
    CHECK(gap.gapEnd == 3999);
    CHECK(gap.invalidSmpCnts ==
          std::vector<std::uint32_t>({3997, 3998, 3999}));
    CHECK(gap.totalLostSamples == 3);
    CHECK(subscriber.receive(frame("mu01", source, 1, 1)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 1, 4)).accepted());
    CHECK(subscriber.stats("mu01").lostSamples == 5);
}

void testMacChangeLatchesUntilReconfirmation() {
    netsec::SvSubscriber subscriber;
    const auto sourceA = mac("00:11:22:33:44:55");
    const auto sourceB = mac("00:11:22:33:44:66");

    CHECK(subscriber.receive(frame("mu01", sourceA, 1, 0)).accepted());

    const auto changed = subscriber.receive(frame("mu01", sourceB, 1, 1));
    CHECK(!changed.accepted());
    CHECK(changed.status == netsec::SvStatus::RejectedSourceMac);
    CHECK(subscriber.isLocked("mu01"));
    CHECK(subscriber.binding("mu01")->awaitingReconfirmation);

    CHECK(subscriber.receive(frame("mu01", sourceB, 1, 2)).status ==
          netsec::SvStatus::RejectedAwaitingReconfirmation);
    CHECK(subscriber.receive(frame("mu01", sourceA, 1, 1)).status ==
          netsec::SvStatus::RejectedAwaitingReconfirmation);

    CHECK(subscriber.reconfirm("mu01", sourceB, 1));
    CHECK(!subscriber.isLocked("mu01"));
    CHECK(subscriber.binding("mu01")->sourceMac == sourceB);
    CHECK(subscriber.receive(frame("mu01", sourceB, 1, 0)).accepted());
    CHECK(subscriber.receive(frame("mu01", sourceB, 1, 1)).accepted());

    const auto oldSource = subscriber.receive(frame("mu01", sourceA, 1, 2));
    CHECK(oldSource.status == netsec::SvStatus::RejectedSourceMac);
    CHECK(subscriber.isLocked("mu01"));
    CHECK(subscriber.reconfirm("mu01", sourceA, 1));
    CHECK(subscriber.receive(frame("mu01", sourceA, 1, 500)).accepted());

    const auto stats = subscriber.stats("mu01");
    CHECK(stats.identityRejectedFrames == 2);
    CHECK(stats.awaitingReconfirmationFrames == 2);
}

void testConfRevChangeLatchesUntilReconfirmation() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 7, 0)).accepted());
    const auto changed = subscriber.receive(frame("mu01", source, 8, 1));
    CHECK(changed.status == netsec::SvStatus::RejectedConfRev);
    CHECK(subscriber.isLocked("mu01"));
    CHECK(subscriber.receive(frame("mu01", source, 7, 1)).status ==
          netsec::SvStatus::RejectedAwaitingReconfirmation);

    CHECK(subscriber.reconfirm("mu01", source, 8));
    CHECK(subscriber.receive(frame("mu01", source, 8, 25)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 8, 26)).accepted());

    const auto oldRev = subscriber.receive(frame("mu01", source, 7, 27));
    CHECK(oldRev.status == netsec::SvStatus::RejectedConfRev);
    CHECK(subscriber.isLocked("mu01"));
}

void testMacAndConfRevChangeTogether() {
    netsec::SvSubscriber subscriber;
    const auto sourceA = mac("00:11:22:33:44:55");
    const auto sourceB = mac("00:11:22:33:44:66");

    CHECK(subscriber.receive(frame("mu01", sourceA, 1, 0)).accepted());
    const auto changed = subscriber.receive(frame("mu01", sourceB, 2, 1));
    CHECK(changed.status == netsec::SvStatus::RejectedSourceMacAndConfRev);
    CHECK(subscriber.isLocked("mu01"));
}

void testSvIDsAreIndependent() {
    netsec::SvSubscriber subscriber;
    const auto sourceA = mac("00:11:22:33:44:55");
    const auto sourceB = mac("00:11:22:33:44:66");

    CHECK(subscriber.receive(frame("muA", sourceA, 1, 0)).accepted());
    CHECK(subscriber.receive(frame("muB", sourceB, 9, 2000)).accepted());
    CHECK(subscriber.receive(frame("muA", sourceB, 1, 1)).status ==
          netsec::SvStatus::RejectedSourceMac);
    CHECK(subscriber.receive(frame("muB", sourceB, 9, 2001)).accepted());
    CHECK(subscriber.isLocked("muA"));
    CHECK(!subscriber.isLocked("muB"));
    CHECK(subscriber.size() == 2);
}

void testExplicitConfigurationBeforeFirstFrame() {
    netsec::SvSubscriber subscriber;
    const auto sourceA = mac("00:11:22:33:44:55");
    const auto sourceB = mac("00:11:22:33:44:66");

    CHECK(subscriber.confirm("mu01", sourceA, 4));
    CHECK(subscriber.isBound("mu01"));
    CHECK(subscriber.receive(frame("mu01", sourceB, 4, 0)).status ==
          netsec::SvStatus::RejectedSourceMac);
    CHECK(subscriber.receive(frame("mu01", sourceA, 4, 0)).status ==
          netsec::SvStatus::RejectedAwaitingReconfirmation);
    CHECK(subscriber.isAwaitingReconfirmation("mu01"));
    CHECK(subscriber.binding("mu01")->sourceMac == sourceA);
    CHECK(subscriber.binding("mu01")->confRev == 4);
    CHECK(!subscriber.confirm("", sourceA, 4));

    const auto confirmedByFrame = frame("mu02", sourceB, 11, 3000);
    CHECK(subscriber.confirm(confirmedByFrame));
    CHECK(subscriber.receive(confirmedByFrame).accepted());
}

void testMalformedFrames() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 0)).accepted());
    const auto badCounter = subscriber.receive(frame("mu01", source, 1, 4000));
    CHECK(badCounter.status == netsec::SvStatus::RejectedMalformed);
    CHECK(!subscriber.isLocked("mu01"));
    CHECK(subscriber.receive(frame("mu01", source, 1, 1)).accepted());
    CHECK(subscriber.stats("mu01").malformedFrames == 1);
    CHECK(subscriber.stats("mu01").lastSmpCnt == 1);

    const auto emptyID = subscriber.receive(frame("", source, 1, 2));
    CHECK(emptyID.status == netsec::SvStatus::RejectedMalformed);
    CHECK(subscriber.size() == 1);
}

void testResetSequenceKeepsBinding() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 0)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 1, 1)).accepted());
    CHECK(subscriber.resetSequence("mu01"));
    CHECK(subscriber.receive(frame("mu01", source, 1, 2500)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 1, 2501)).accepted());
    CHECK(!subscriber.resetSequence("missing"));
}

void testUnbindAndClear() {
    netsec::SvSubscriber subscriber;
    const auto sourceA = mac("00:11:22:33:44:55");
    const auto sourceB = mac("00:11:22:33:44:66");

    CHECK(subscriber.receive(frame("mu01", sourceA, 1, 0)).accepted());
    CHECK(subscriber.unbind("mu01"));
    CHECK(!subscriber.isBound("mu01"));
    CHECK(!subscriber.unbind("missing"));
    CHECK(subscriber.receive(frame("mu01", sourceB, 2, 0)).accepted());
    CHECK(subscriber.binding("mu01")->sourceMac == sourceB);

    subscriber.clear();
    CHECK(subscriber.size() == 0);
    CHECK(!subscriber.isBound("mu01"));
}

void testLongOutageCanResynchronize() {
    netsec::SvSubscriber subscriber;
    const auto source = mac("00:11:22:33:44:55");

    CHECK(subscriber.receive(frame("mu01", source, 1, 0)).accepted());
    const auto staleLooking = subscriber.receive(frame("mu01", source, 1, 3500));
    CHECK(staleLooking.status == netsec::SvStatus::Replay);
    CHECK(subscriber.resetSequence("mu01"));
    CHECK(subscriber.receive(frame("mu01", source, 1, 3500)).accepted());
    CHECK(subscriber.receive(frame("mu01", source, 1, 3501)).accepted());
}

}  // namespace

int main() {
    testMacAddressParsing();
    testFirstFrameBindsIdentity();
    testSequentialFrames();
    testReplayAndDuplicateRejection();
    testGapMarksIntermediateSamplesInvalid();
    testCounterWrap();
    testFullCounterCycle();
    testGapAcrossWrap();
    testMacChangeLatchesUntilReconfirmation();
    testConfRevChangeLatchesUntilReconfirmation();
    testMacAndConfRevChangeTogether();
    testSvIDsAreIndependent();
    testExplicitConfigurationBeforeFirstFrame();
    testMalformedFrames();
    testResetSequenceKeepsBinding();
    testUnbindAndClear();
    testLongOutageCanResynchronize();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all SV subscriber checks passed\n";
    return 0;
}

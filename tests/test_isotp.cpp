#include "isotp.hpp"

#include <cstdint>
#include <iostream>
#include <vector>

using namespace netsec::isotp;

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                          \
    do {                                                                     \
        ++g_checks;                                                          \
        if (!(cond)) {                                                       \
            ++g_failures;                                                    \
            std::cerr << "CHECK failed: " #cond " (" << __FILE__ << ":"      \
                      << __LINE__ << ")\n";                                  \
        }                                                                    \
    } while (0)

CanFrame makeFrame(std::uint32_t id, const std::vector<std::uint8_t>& bytes) {
    CanFrame frame{};
    frame.id = id;
    frame.dlc = static_cast<std::uint8_t>(bytes.size());
    for (std::size_t i = 0; i < bytes.size(); ++i) {
        frame.data[i] = bytes[i];
    }
    return frame;
}

CanFrame firstFrame(std::uint32_t id, std::uint16_t length) {
    return makeFrame(id, {static_cast<std::uint8_t>(0x10 | ((length >> 8) & 0x0F)),
                          static_cast<std::uint8_t>(length & 0xFF),
                          0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF});
}

CanFrame firstFrameEscape(std::uint32_t id, std::uint32_t length) {
    return makeFrame(id, {0x10, 0x00,
                          static_cast<std::uint8_t>((length >> 24) & 0xFF),
                          static_cast<std::uint8_t>((length >> 16) & 0xFF),
                          static_cast<std::uint8_t>((length >> 8) & 0xFF),
                          static_cast<std::uint8_t>(length & 0xFF),
                          0xAA, 0xBB});
}

CanFrame consecutiveFrame(std::uint32_t id, std::uint8_t sn,
                          const std::vector<std::uint8_t>& payload) {
    std::vector<std::uint8_t> bytes{static_cast<std::uint8_t>(0x20 | (sn & 0x0F))};
    bytes.insert(bytes.end(), payload.begin(), payload.end());
    while (bytes.size() < 8) bytes.push_back(0x00);
    return makeFrame(id, bytes);
}

bool fcIs(const RxResult& result, std::uint32_t id, std::uint8_t flowStatus) {
    return result.sendFlowControl && result.flowControl.id == id &&
           result.flowControl.dlc == 8 &&
           (result.flowControl.data[0] & 0x0F) == flowStatus;
}

std::vector<std::uint8_t> expectedMultiPayload() {
    std::vector<std::uint8_t> payload{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF};
    for (std::uint8_t i = 0; i < 12; ++i) {
        for (std::uint8_t b = 0; b < 7; ++b) {
            payload.push_back(static_cast<std::uint8_t>(i * 10 + b + 1));
        }
    }
    payload.push_back(0x77);
    return payload;  // 6 + 84 + 1 = 91 bytes
}

void testSingleFrame() {
    IsoTpChannel channel(64, 100);
    RxResult ok = channel.onCanFrame(makeFrame(0x100, {0x03, 'a', 'b', 'c'}), 0);
    CHECK(ok.completed());
    CHECK(ok.length == 3);
    CHECK(ok.payload[0] == 'a' && ok.payload[2] == 'c');
    CHECK(!channel.active());

    RxResult zero = channel.onCanFrame(makeFrame(0x100, {0x00}), 0);
    CHECK(zero.status == RxStatus::kRejected);

    RxResult tooLong = channel.onCanFrame(
        makeFrame(0x100, {0x08, 1, 2, 3, 4, 5, 6, 7}), 0);
    CHECK(tooLong.status == RxStatus::kRejected);

    RxResult shortDlc = channel.onCanFrame(makeFrame(0x100, {0x04, 1, 2}), 0);
    CHECK(shortDlc.status == RxStatus::kRejected);
}

void testMultiFrameSequenceWrap() {
    IsoTpChannel channel(256, 100);
    const std::uint32_t id = 0x200;
    const std::vector<std::uint8_t> want = expectedMultiPayload();

    RxResult ff = channel.onCanFrame(
        firstFrame(id, static_cast<std::uint16_t>(want.size())), 0);
    CHECK(ff.status == RxStatus::kFlowControl);
    CHECK(fcIs(ff, id, 0x00));  // CTS
    CHECK(channel.active());

    std::size_t offset = 6;
    std::uint8_t sn = 1;
    std::uint64_t now = 10;
    RxResult result = ff;
    while (offset < want.size()) {
        std::vector<std::uint8_t> chunk;
        while (chunk.size() < 7 && offset + chunk.size() < want.size()) {
            chunk.push_back(want[offset + chunk.size()]);
        }
        result = channel.onCanFrame(consecutiveFrame(id, sn, chunk), now);
        offset += chunk.size();
        now += 10;
        sn = static_cast<std::uint8_t>((sn + 1) % 16);  // wraps 15 -> 0
        if (offset < want.size()) {
            CHECK(result.status == RxStatus::kIgnored);
        }
    }

    CHECK(result.completed());
    CHECK(result.length == want.size());
    for (std::size_t i = 0; i < want.size(); ++i) {
        CHECK(result.payload[i] == want[i]);
    }
    CHECK(!channel.active());
}

void testEscapeLengthSmallValueRejected() {
    IsoTpChannel channel(256, 100);
    RxResult small = channel.onCanFrame(firstFrameEscape(0x300, 4095), 0);
    CHECK(small.status == RxStatus::kRejected);
    CHECK(!small.sendFlowControl);

    RxResult smaller = channel.onCanFrame(firstFrameEscape(0x300, 1), 0);
    CHECK(smaller.status == RxStatus::kRejected);
    CHECK(!channel.active());
}

void testEscapeLengthAcceptedAndOverflow() {
    IsoTpChannel channel(4200, 100);
    const std::uint32_t length = 4100;
    RxResult ff = channel.onCanFrame(firstFrameEscape(0x301, length), 0);
    CHECK(ff.status == RxStatus::kFlowControl);
    CHECK(fcIs(ff, 0x301, 0x00));
    CHECK(channel.active());
    channel.reset();

    IsoTpChannel smallBuffer(100, 100);
    RxResult overflow = smallBuffer.onCanFrame(firstFrameEscape(0x301, length), 0);
    CHECK(overflow.status == RxStatus::kRejected);
    CHECK(fcIs(overflow, 0x301, 0x02));  // OVFLW
    CHECK(!smallBuffer.active());

    RxResult classicOverflow = smallBuffer.onCanFrame(firstFrame(0x301, 500), 0);
    CHECK(classicOverflow.status == RxStatus::kRejected);
    CHECK(fcIs(classicOverflow, 0x301, 0x02));
}

void testOutOfOrderAndDuplicateAbort() {
    IsoTpChannel channel(256, 100);
    const std::uint32_t id = 0x400;
    channel.onCanFrame(firstFrame(id, 20), 0);

    // Expected SN 1, send SN 2 (skipped) -> whole reassembly discarded.
    RxResult skipped = channel.onCanFrame(
        consecutiveFrame(id, 2, {1, 2, 3, 4, 5, 6, 7}), 10);
    CHECK(skipped.status == RxStatus::kRejected);
    CHECK(!channel.active());

    // A further CF is now an orphan, not a continuation.
    RxResult orphan = channel.onCanFrame(
        consecutiveFrame(id, 3, {1, 2, 3, 4, 5, 6, 7}), 20);
    CHECK(orphan.status == RxStatus::kIgnored);

    // Duplicate SN (expected 2 after a valid SN 1, retransmitted SN 1).
    channel.onCanFrame(firstFrame(id, 20), 30);
    channel.onCanFrame(consecutiveFrame(id, 1, {1, 2, 3, 4, 5, 6, 7}), 40);
    RxResult duplicate = channel.onCanFrame(
        consecutiveFrame(id, 1, {1, 2, 3, 4, 5, 6, 7}), 50);
    CHECK(duplicate.status == RxStatus::kRejected);
    CHECK(!channel.active());
}

void testOrphanConsecutiveFrame() {
    IsoTpChannel channel(256, 100);
    RxResult orphan = channel.onCanFrame(
        consecutiveFrame(0x500, 1, {1, 2, 3, 4, 5, 6, 7}), 0);
    CHECK(orphan.status == RxStatus::kIgnored);
    CHECK(!channel.active());

    // SN 0 is never a valid first consecutive frame (SN must start at 1).
    channel.onCanFrame(firstFrame(0x500, 20), 10);
    RxResult wrongStart = channel.onCanFrame(
        consecutiveFrame(0x500, 0, {1, 2, 3, 4, 5, 6, 7}), 20);
    CHECK(wrongStart.status == RxStatus::kRejected);
}

void testNCrTimeout() {
    IsoTpChannel channel(256, 100);
    const std::uint32_t id = 0x600;
    channel.onCanFrame(firstFrame(id, 20), 1000);
    CHECK(channel.active());

    // Timer-driven timeout check: expiry is strictly greater than N_Cr.
    CHECK(!channel.pollTimeouts(1099));
    CHECK(channel.active());
    CHECK(channel.pollTimeouts(1101));
    CHECK(!channel.active());

    // A late CF after the timeout cannot resurrect anything.
    RxResult late = channel.onCanFrame(
        consecutiveFrame(id, 1, {1, 2, 3, 4, 5, 6, 7}), 2000);
    CHECK(late.status == RxStatus::kIgnored);

    // Timeout detected inside the receive callback itself.
    channel.onCanFrame(firstFrame(id, 20), 3000);
    RxResult lateFf = channel.onCanFrame(firstFrame(id, 8), 4200);
    CHECK(lateFf.timedOut);
    CHECK(lateFf.status == RxStatus::kFlowControl);
}

void testFirstFrameRestartsReassembly() {
    IsoTpChannel channel(256, 100);
    const std::uint32_t id = 0x700;
    channel.onCanFrame(firstFrame(id, 100), 0);
    channel.onCanFrame(consecutiveFrame(id, 1,
                                        std::vector<std::uint8_t>(7, 0xAB)), 10);

    // A new FF abandons the partial message and starts cleanly at SN 1.
    // Declared 20 bytes: FF itself contributes 6, then two CFs of 7 each.
    RxResult restart = channel.onCanFrame(firstFrame(id, 20), 20);
    CHECK(restart.status == RxStatus::kFlowControl);

    RxResult cf1 = channel.onCanFrame(
        consecutiveFrame(id, 1, {1, 2, 3, 4, 5, 6, 7}), 30);
    CHECK(cf1.status == RxStatus::kIgnored);
    RxResult done = channel.onCanFrame(
        consecutiveFrame(id, 2, {8, 9, 10, 11, 12, 13, 14}), 40);
    CHECK(done.completed());
    CHECK(done.length == 20);
    CHECK(done.payload[6] == 1 && done.payload[19] == 14);
}

void testRouterIsolationAndIdCheck() {
    IsoTpRouter router(256, 100);

    RxResult a = router.onCanFrame(firstFrame(0x100, 20), 0);
    RxResult b = router.onCanFrame(firstFrame(0x101, 20), 0);
    CHECK(a.status == RxStatus::kFlowControl);
    CHECK(b.status == RxStatus::kFlowControl);

    // Wrong SN on ID 0x100 must not affect the 0x101 reassembly.
    RxResult bad = router.onCanFrame(
        consecutiveFrame(0x100, 5, {1, 2, 3, 4, 5, 6, 7}), 10);
    CHECK(bad.status == RxStatus::kRejected);
    RxResult goodB = router.onCanFrame(
        consecutiveFrame(0x101, 1, {1, 2, 3, 4, 5, 6, 7}), 20);
    CHECK(goodB.status == RxStatus::kIgnored);

    RxResult extendedId = router.onCanFrame(firstFrame(0x800, 20), 0);
    CHECK(extendedId.status == RxStatus::kRejected);

    CHECK(router.pollTimeouts(121));
    router.resetAll();
}

void testMalformedFirstFrame() {
    IsoTpChannel channel(256, 100);
    // Classic-CAN first frames always occupy a full 8-byte frame.
    RxResult shortFf = channel.onCanFrame(makeFrame(0x100, {0x10, 0x08, 1, 2}), 0);
    CHECK(shortFf.status == RxStatus::kRejected);
    CHECK(!channel.active());

    // Zero-length DLC frames cannot carry a PCI.
    RxResult empty = channel.onCanFrame(makeFrame(0x100, {}), 0);
    CHECK(empty.status == RxStatus::kRejected);
}

}  // namespace

int main() {
    testSingleFrame();
    testMultiFrameSequenceWrap();
    testEscapeLengthSmallValueRejected();
    testEscapeLengthAcceptedAndOverflow();
    testOutOfOrderAndDuplicateAbort();
    testOrphanConsecutiveFrame();
    testNCrTimeout();
    testFirstFrameRestartsReassembly();
    testRouterIsolationAndIdCheck();
    testMalformedFirstFrame();

    std::cout << g_checks << " checks, " << g_failures << " failures\n";
    return g_failures == 0 ? 0 : 1;
}

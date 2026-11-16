#include "netsec.hpp"

#include <cassert>
#include <cstdio>
#include <string>
#include <vector>

using netsec::mailbox::FrameDisposition;
using netsec::mailbox::MailboxValidator;
using netsec::mailbox::RejectReason;
using netsec::mailbox::kHeaderSize;
using netsec::mailbox::kTypeCoe;
using netsec::mailbox::kTypeFoe;

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                          \
    do {                                                                     \
        ++g_checks;                                                          \
        if (!(cond)) {                                                       \
            ++g_failures;                                                    \
            std::printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);   \
        }                                                                    \
    } while (0)

std::vector<uint8_t> makeFrame(uint8_t type, uint8_t counter,
                               const std::vector<uint8_t>& payload,
                               uint16_t lengthOverride = 0xFFFF,
                               std::size_t truncateTo = 0) {
    const uint16_t length =
        (lengthOverride != 0xFFFF) ? lengthOverride
                                   : static_cast<uint16_t>(payload.size());
    std::vector<uint8_t> frame(kHeaderSize + payload.size(), 0);
    frame[0] = static_cast<uint8_t>(length & 0xFF);
    frame[1] = static_cast<uint8_t>(length >> 8);
    frame[2] = 0x10; // address, not validated
    frame[3] = 0x00;
    frame[4] = 0x00; // channel/priority, not validated
    frame[5] = static_cast<uint8_t>((type << 4) | (counter & 0x07));
    for (std::size_t i = 0; i < payload.size(); ++i) {
        frame[kHeaderSize + i] = payload[i];
    }
    if (truncateTo > 0 && truncateTo < frame.size()) {
        frame.resize(truncateTo);
    }
    return frame;
}

std::vector<uint8_t> foeWrite(const std::vector<uint8_t>& nameWithNul) {
    std::vector<uint8_t> payload(4, 0);
    payload[0] = 2; // FoE WRQ
    payload.insert(payload.end(), nameWithNul.begin(), nameWithNul.end());
    return payload;
}

std::vector<uint8_t> strBytes(const char* text) {
    std::vector<uint8_t> out;
    for (const char* p = text; *p != 0; ++p) {
        out.push_back(static_cast<uint8_t>(*p));
    }
    return out;
}

// CoE header: 10-bit length, 2 reserved bits, 4-bit service (2 = SDO response),
// then the SDO command byte.
std::vector<uint8_t> coeFrame(uint8_t commandByte) {
    return {0x01, 0x02, commandByte};
}

void run(const char* name, void (*fn)()) {
    std::printf("[ RUN  ] %s\n", name);
    fn();
}

void testValidFrame() {
    MailboxValidator validator(128);
    validator.enableType(kTypeFoe);
    const auto frame = makeFrame(kTypeFoe, 1, {2, 0, 0, 0, 'a', 0});
    const auto result = validator.validate(frame.data(), frame.size());
    CHECK(result.accepted());
    CHECK(result.reason == RejectReason::None);
    CHECK(result.payload_length == 6);
    CHECK(result.payload == frame.data() + kHeaderSize);
    CHECK(validator.lastCounter() == 1);
}

void testLengthTooLargeForSyncManager() {
    MailboxValidator validator(10); // max payload length = 4
    validator.enableType(kTypeFoe);
    const auto frame = makeFrame(kTypeFoe, 1, {1, 2, 3, 4, 5, 6}, 6);
    const auto result = validator.validate(frame.data(), frame.size());
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::LengthExceedsSm);
}

void testLengthLargerThanReceived() {
    MailboxValidator validator(128);
    validator.enableType(kTypeFoe);
    const auto frame =
        makeFrame(kTypeFoe, 1, {1, 2, 3, 4}, 10 /*length field*/, 10 /*bytes*/);
    const auto result = validator.validate(frame.data(), frame.size());
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::LengthExceedsReceived);
}

void testFrameTooShort() {
    MailboxValidator validator(128);
    const uint8_t tiny[3] = {0, 0, 0};
    const auto result = validator.validate(tiny, sizeof(tiny));
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::TooShort);
}

void testCounterZeroRejected() {
    MailboxValidator validator(128);
    validator.enableType(kTypeFoe);
    const auto frame = makeFrame(kTypeFoe, 0, {1});
    const auto result = validator.validate(frame.data(), frame.size());
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::CounterZero);
    CHECK(validator.lastCounter() == 0);
}

void testRetransmitDiscarded() {
    MailboxValidator validator(128);
    validator.enableType(kTypeFoe);
    const std::vector<uint8_t> control = {1, 0, 0, 0}; // RRQ, no filename
    const auto first = makeFrame(kTypeFoe, 3, control);
    const auto second = makeFrame(kTypeFoe, 3, control);
    CHECK(validator.validate(first.data(), first.size()).accepted());
    const auto retransmit = validator.validate(second.data(), second.size());
    CHECK(retransmit.disposition == FrameDisposition::RetransmitDiscard);
    CHECK(retransmit.reason == RejectReason::Retransmit);

    const auto next = makeFrame(kTypeFoe, 4, control);
    CHECK(validator.validate(next.data(), next.size()).accepted());
}

void testCounterGapLoggedButAccepted() {
    std::vector<std::string> logs;
    MailboxValidator validator(128, [&](const std::string& msg) {
        logs.push_back(msg);
    });
    validator.enableType(kTypeFoe);
    const std::vector<uint8_t> control = {1, 0, 0, 0};
    const auto first = makeFrame(kTypeFoe, 1, control);
    const auto skipped = makeFrame(kTypeFoe, 3, control);
    CHECK(validator.validate(first.data(), first.size()).accepted());
    const auto result = validator.validate(skipped.data(), skipped.size());
    CHECK(result.accepted());
    CHECK(validator.lastCounter() == 3);
    CHECK(logs.size() == 1);
    CHECK(logs[0].find("gap") != std::string::npos);

    // 7 -> 1 wrap-around is normal and must not be reported as a gap.
    logs.clear();
    const auto wrap = makeFrame(kTypeFoe, 1, control);
    validator.reset();
    const auto seven = makeFrame(kTypeFoe, 7, control);
    CHECK(validator.validate(seven.data(), seven.size()).accepted());
    CHECK(validator.validate(wrap.data(), wrap.size()).accepted());
    CHECK(logs.empty());
}

void testUnregisteredTypeRejected() {
    MailboxValidator validator(128);
    validator.enableType(kTypeFoe);
    const auto frame = makeFrame(kTypeCoe, 1, coeFrame(0x20));
    const auto result = validator.validate(frame.data(), frame.size());
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::TypeNotRegistered);
}

void testFoeWriteNameValidation() {
    MailboxValidator validator(128);
    validator.enableType(kTypeFoe);

    auto goodName = strBytes("firmware.bin");
    goodName.push_back(0);
    const auto good = makeFrame(kTypeFoe, 1, foeWrite(goodName));
    CHECK(validator.validate(good.data(), good.size()).accepted());

    struct NameCase {
        std::vector<uint8_t> name;
        RejectReason expected;
        bool append_terminator;
    };
    const NameCase cases[] = {
        {strBytes("../firmware.bin"), RejectReason::FoePathTraversal, true},
        {strBytes(".."), RejectReason::FoePathTraversal, true},
        {strBytes("a..b"), RejectReason::FoePathTraversal, true},
        {strBytes("dir/firmware.bin"), RejectReason::FoeIllegalNameByte, true},
        {strBytes("dir\\firmware.bin"), RejectReason::FoeIllegalNameByte, true},
        {strBytes(""), RejectReason::FoeEmptyName, true},
        {strBytes("no-terminator"), RejectReason::FoeUnterminatedName, false},
        {{'a', 0, 'b'}, RejectReason::FoeEmbeddedNul, true},
    };

    for (std::size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
        std::vector<uint8_t> name = cases[i].name;
        if (cases[i].append_terminator) {
            name.push_back(0);
        }
        const uint8_t counters[] = {2, 3, 4, 5, 6, 7, 1, 2};
        uint8_t counter = counters[i];
        const auto frame = makeFrame(kTypeFoe, counter, foeWrite(name));
        const auto result = validator.validate(frame.data(), frame.size());
        CHECK(!result.accepted());
        CHECK(result.reason == cases[i].expected);
    }

    // Non-write FoE opcodes carry no filename and must bypass name checks.
    std::vector<uint8_t> readRequest(8, 0);
    readRequest[0] = 1; // RRQ
    const auto readFrame = makeFrame(kTypeFoe, 3, readRequest);
    CHECK(validator.validate(readFrame.data(), readFrame.size()).accepted());
}

void testCoeToggleAlternation() {
    MailboxValidator validator(128);
    validator.enableType(kTypeCoe);

    const auto initiate = makeFrame(kTypeCoe, 1, coeFrame(0x20)); // cs=1
    CHECK(validator.validate(initiate.data(), initiate.size()).accepted());
    CHECK(validator.expectedToggle() == 0);

    const auto segment0 = makeFrame(kTypeCoe, 2, coeFrame(0x00)); // cs=0, t=0
    CHECK(validator.validate(segment0.data(), segment0.size()).accepted());
    CHECK(validator.expectedToggle() == 1);

    const auto badToggle = makeFrame(kTypeCoe, 3, coeFrame(0x00)); // t=0 again
    const auto bad = validator.validate(badToggle.data(), badToggle.size());
    CHECK(!bad.accepted());
    CHECK(bad.reason == RejectReason::CoeToggleError);
    // Failed segment must not advance the expected toggle.
    CHECK(validator.expectedToggle() == 1);

    const auto segment1 = makeFrame(kTypeCoe, 4, coeFrame(0x10)); // t=1
    CHECK(validator.validate(segment1.data(), segment1.size()).accepted());
    CHECK(validator.expectedToggle() == 0);
}

void testCoeFirstSegmentMustToggleZero() {
    MailboxValidator validator(128);
    validator.enableType(kTypeCoe);

    const auto orphan = makeFrame(kTypeCoe, 1, coeFrame(0x10)); // cs=0, t=1
    const auto result = validator.validate(orphan.data(), orphan.size());
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::CoeToggleError);

    const auto properFirst = makeFrame(kTypeCoe, 2, coeFrame(0x00));
    CHECK(validator.validate(properFirst.data(), properFirst.size()).accepted());
}

void testCoeTooShort() {
    MailboxValidator validator(128);
    validator.enableType(kTypeCoe);
    const auto frame = makeFrame(kTypeCoe, 1, {0x00}); // only 1 CoE byte
    const auto result = validator.validate(frame.data(), frame.size());
    CHECK(!result.accepted());
    CHECK(result.reason == RejectReason::CoeTooShort);
}

} // namespace

int main() {
    run("valid frame", testValidFrame);
    run("length exceeds sync manager", testLengthTooLargeForSyncManager);
    run("length exceeds received bytes", testLengthLargerThanReceived);
    run("frame shorter than header", testFrameTooShort);
    run("counter zero reserved", testCounterZeroRejected);
    run("retransmit discarded", testRetransmitDiscarded);
    run("counter gap logged not rejected", testCounterGapLoggedButAccepted);
    run("unregistered mailbox type", testUnregisteredTypeRejected);
    run("FoE write filename hardening", testFoeWriteNameValidation);
    run("CoE toggle alternation", testCoeToggleAlternation);
    run("CoE orphan first segment", testCoeFirstSegmentMustToggleZero);
    run("CoE too short", testCoeTooShort);

    std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}

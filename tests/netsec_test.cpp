#include "netsec.hpp"

#include <cstdint>
#include <iostream>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) check(static_cast<bool>(expression), #expression, __LINE__)

std::uint8_t payload[netsec::kCanFdMaxPayload]{};

netsec::CanFdFrame frame(std::uint32_t id,
                         netsec::CanFdIde ide,
                         std::uint8_t dlc,
                         std::uint64_t timestampUs,
                         std::uint32_t counter = 0) {
    netsec::CanFdFrame result;
    result.id = id;
    result.ide = ide;
    result.dlc = dlc;
    result.payload = payload;
    result.payloadLength = netsec::canFdPayloadLength(dlc);
    result.timestampUs = timestampUs;
    result.counter = counter;
    return result;
}

netsec::CanFdWhitelistEntry entry(std::uint32_t id,
                                  netsec::CanFdIde ide,
                                  std::uint8_t dlc,
                                  std::uint64_t minPeriodUs = 0,
                                  bool fresh = false,
                                  bool mac = false) {
    netsec::CanFdWhitelistEntry result;
    result.id = id;
    result.ide = ide;
    result.dlc = dlc;
    result.minPeriodUs = minPeriodUs;
    result.requiresFreshCounter = fresh;
    result.requiresMac = mac;
    return result;
}

struct MacContext {
    bool valid;
    unsigned calls;
};

bool verifyMac(const netsec::CanFdFrame&,
               const netsec::CanFdWhitelistEntry&,
               void* rawContext) {
    auto* context = static_cast<MacContext*>(rawContext);
    ++context->calls;
    return context->valid;
}

void testDlcMapping() {
    const std::size_t expected[] = {
        0, 1, 2, 3, 4, 5, 6, 7, 8,
        12, 16, 20, 24, 32, 48, 64,
    };
    for (std::uint8_t dlc = 0; dlc <= 15; ++dlc) {
        CHECK(netsec::isValidCanFdDlc(dlc));
        CHECK(netsec::canFdPayloadLength(dlc) == expected[dlc]);
    }
    CHECK(!netsec::isValidCanFdDlc(16));
    CHECK(!netsec::isValidCanFdDlc(255));
    CHECK(netsec::canFdPayloadLength(16) == 0);
}

void testIdeIsPartOfWhitelistKey() {
    netsec::CanFdFilter filter;
    CHECK(filter.addEntry(entry(0x123, netsec::CanFdIde::Standard, 8)));
    CHECK(filter.size() == 1);

    auto standard = frame(0x123, netsec::CanFdIde::Standard, 8, 0);
    auto confused = frame(0x123, netsec::CanFdIde::Extended, 8, 0);

    CHECK(filter.evaluate(confused) == netsec::CanFdFilterStatus::IdeMismatch);
    CHECK(filter.evaluate(standard) == netsec::CanFdFilterStatus::Accepted);

    netsec::CanFdFilter both;
    CHECK(both.addEntry(entry(0x123, netsec::CanFdIde::Standard, 8)));
    CHECK(both.addEntry(entry(0x123, netsec::CanFdIde::Extended, 8)));
    CHECK(both.evaluate(standard) == netsec::CanFdFilterStatus::Accepted);
    CHECK(both.evaluate(confused) == netsec::CanFdFilterStatus::Accepted);
}

void testDlcAndPayloadLengthAgreement() {
    netsec::CanFdFilter filter;
    CHECK(filter.addEntry(entry(0x200, netsec::CanFdIde::Standard, 9)));

    auto valid12 = frame(0x200, netsec::CanFdIde::Standard, 9, 0);
    CHECK(valid12.payloadLength == 12);
    CHECK(filter.evaluate(valid12) == netsec::CanFdFilterStatus::Accepted);

    auto wrongDlc = frame(0x200, netsec::CanFdIde::Standard, 10, 1000);
    CHECK(wrongDlc.payloadLength == 16);
    CHECK(filter.evaluate(wrongDlc) == netsec::CanFdFilterStatus::DlcMismatch);

    auto shortPayload = valid12;
    shortPayload.timestampUs = 1000;
    shortPayload.payloadLength = 11;
    CHECK(filter.evaluate(shortPayload) ==
          netsec::CanFdFilterStatus::PayloadLengthMismatch);

    auto nullPayload = valid12;
    nullPayload.timestampUs = 1000;
    nullPayload.payload = nullptr;
    CHECK(filter.evaluate(nullPayload) ==
          netsec::CanFdFilterStatus::PayloadUnavailable);

    netsec::CanFdFilter maximum;
    CHECK(maximum.addEntry(entry(0x201, netsec::CanFdIde::Standard, 15)));
    auto valid64 = frame(0x201, netsec::CanFdIde::Standard, 15, 0);
    CHECK(valid64.payloadLength == 64);
    CHECK(maximum.evaluate(valid64) == netsec::CanFdFilterStatus::Accepted);

    auto short63 = frame(0x201, netsec::CanFdIde::Standard, 15, 1000);
    short63.payloadLength = 63;
    CHECK(maximum.evaluate(short63) ==
          netsec::CanFdFilterStatus::PayloadLengthMismatch);

    auto invalidDlc = frame(0x201, netsec::CanFdIde::Standard, 16, 1000);
    CHECK(maximum.evaluate(invalidDlc) == netsec::CanFdFilterStatus::InvalidDlc);
}

void testMinimumPeriodAndTimestampOrder() {
    netsec::CanFdFilter filter;
    CHECK(filter.addEntry(
        entry(0x300, netsec::CanFdIde::Standard, 8, 1000)));

    CHECK(filter.evaluate(frame(0x300, netsec::CanFdIde::Standard, 8, 100)) ==
          netsec::CanFdFilterStatus::Accepted);
    CHECK(filter.evaluate(frame(0x300, netsec::CanFdIde::Standard, 8, 1050)) ==
          netsec::CanFdFilterStatus::TooSoon);
    CHECK(filter.evaluate(frame(0x300, netsec::CanFdIde::Standard, 8, 1100)) ==
          netsec::CanFdFilterStatus::Accepted);
    CHECK(filter.evaluate(frame(0x300, netsec::CanFdIde::Standard, 8, 1099)) ==
          netsec::CanFdFilterStatus::TimestampRegressed);
    CHECK(filter.evaluate(frame(0x300, netsec::CanFdIde::Standard, 8, 1200)) ==
          netsec::CanFdFilterStatus::TooSoon);
}

void testCounterIsStrictlyIncreasing() {
    netsec::CanFdFilter filter;
    CHECK(filter.addEntry(
        entry(0x400, netsec::CanFdIde::Extended, 8, 1000, true)));

    CHECK(filter.evaluate(
              frame(0x400, netsec::CanFdIde::Extended, 8, 0, 10)) ==
          netsec::CanFdFilterStatus::Accepted);
    CHECK(filter.evaluate(
              frame(0x400, netsec::CanFdIde::Extended, 8, 1100, 10)) ==
          netsec::CanFdFilterStatus::ReplayedCounter);
    CHECK(filter.evaluate(
              frame(0x400, netsec::CanFdIde::Extended, 8, 1100, 9)) ==
          netsec::CanFdFilterStatus::ReplayedCounter);
    CHECK(filter.evaluate(
              frame(0x400, netsec::CanFdIde::Extended, 8, 1100, 11)) ==
          netsec::CanFdFilterStatus::Accepted);
    CHECK(filter.evaluate(
              frame(0x400, netsec::CanFdIde::Extended, 8, 2200, 11)) ==
          netsec::CanFdFilterStatus::ReplayedCounter);

    filter.resetTracking();
    CHECK(filter.evaluate(
              frame(0x400, netsec::CanFdIde::Extended, 8, 0, 0)) ==
          netsec::CanFdFilterStatus::Accepted);
}

void testMacFailureDoesNotAdvanceState() {
    MacContext context{false, 0};
    netsec::CanFdFilter filter(verifyMac, &context);
    CHECK(filter.addEntry(
        entry(0x500, netsec::CanFdIde::Standard, 8, 1000, true, true)));

    CHECK(filter.evaluate(
              frame(0x500, netsec::CanFdIde::Standard, 8, 0, 1)) ==
          netsec::CanFdFilterStatus::MacRejected);
    CHECK(context.calls == 1);

    context.valid = true;
    CHECK(filter.evaluate(
              frame(0x500, netsec::CanFdIde::Standard, 8, 0, 1)) ==
          netsec::CanFdFilterStatus::Accepted);
    CHECK(context.calls == 2);

    context.valid = false;
    CHECK(filter.evaluate(
              frame(0x500, netsec::CanFdIde::Standard, 8, 1000, 100)) ==
          netsec::CanFdFilterStatus::MacRejected);
    CHECK(context.calls == 3);

    context.valid = true;
    CHECK(filter.evaluate(
              frame(0x500, netsec::CanFdIde::Standard, 8, 1000, 2)) ==
          netsec::CanFdFilterStatus::Accepted);
    CHECK(context.calls == 4);
}

void testMacVerifierRequirements() {
    netsec::CanFdFilter missing;
    CHECK(missing.addEntry(
        entry(0x600, netsec::CanFdIde::Standard, 8, 0, false, true)));
    CHECK(missing.evaluate(frame(0x600, netsec::CanFdIde::Standard, 8, 0)) ==
          netsec::CanFdFilterStatus::MacVerifierMissing);
    CHECK(missing.evaluate(
              frame(0x600, netsec::CanFdIde::Standard, 8, 0), true) ==
          netsec::CanFdFilterStatus::Accepted);

    MacContext context{true, 0};
    netsec::CanFdFilter noMacNeeded(verifyMac, &context);
    CHECK(noMacNeeded.addEntry(entry(0x601, netsec::CanFdIde::Standard, 8)));
    CHECK(noMacNeeded.accept(frame(0x601, netsec::CanFdIde::Standard, 8, 0)));
    CHECK(context.calls == 0);
}

void testConfigurationValidation() {
    netsec::CanFdFilter filter;
    CHECK(!filter.addEntry(entry(0x800, netsec::CanFdIde::Standard, 8)));
    CHECK(!filter.addEntry(entry(0x20000000, netsec::CanFdIde::Extended, 8)));
    CHECK(!filter.addEntry(entry(0x700, netsec::CanFdIde::Standard, 16)));

    CHECK(filter.addEntry(entry(0x700, netsec::CanFdIde::Standard, 8)));
    CHECK(!filter.addEntry(entry(0x700, netsec::CanFdIde::Standard, 8)));
    CHECK(filter.addEntry(entry(0x700, netsec::CanFdIde::Extended, 8)));
    CHECK(filter.size() == 2);
}

}  // namespace

int main() {
    testDlcMapping();
    testIdeIsPartOfWhitelistKey();
    testDlcAndPayloadLengthAgreement();
    testMinimumPeriodAndTimestampOrder();
    testCounterIsStrictlyIncreasing();
    testMacFailureDoesNotAdvanceState();
    testMacVerifierRequirements();
    testConfigurationValidation();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all CAN FD filter checks passed\n";
    return 0;
}

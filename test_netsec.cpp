#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <functional>
#include <iomanip>
#include <iostream>
#include <numeric>
#include <sstream>
#include <string>
#include <vector>

using namespace netsec;

namespace {

int g_checks = 0;
int g_failures = 0;
std::string g_currentTest;

#define CHECK(condition)                                                      \
    do {                                                                      \
        ++g_checks;                                                           \
        if (!(condition)) {                                                   \
            ++g_failures;                                                     \
            std::cerr << "FAIL [" << g_currentTest << "] " #condition         \
                      << " at line " << __LINE__ << '\n';                     \
        }                                                                     \
    } while (0)

void runTest(const std::string& name, const std::function<void()>& body) {
    g_currentTest = name;
    int failuresBefore = g_failures;
    std::cout << "[ RUN  ] " << name << '\n';
    body();
    std::cout << (g_failures == failuresBefore ? "[ PASS ] " : "[ FAIL ] ")
              << name << '\n';
}

Pin makePin(int firstHalf, int secondHalf, uint8_t checkDigit) {
    std::array<uint8_t, 8> digits{};
    digits[0] = static_cast<uint8_t>(firstHalf / 1000);
    digits[1] = static_cast<uint8_t>((firstHalf / 100) % 10);
    digits[2] = static_cast<uint8_t>((firstHalf / 10) % 10);
    digits[3] = static_cast<uint8_t>(firstHalf % 10);
    digits[4] = static_cast<uint8_t>(secondHalf / 100);
    digits[5] = static_cast<uint8_t>((secondHalf / 10) % 10);
    digits[6] = static_cast<uint8_t>(secondHalf % 10);
    digits[7] = checkDigit;
    return Pin(digits);
}

Pin validPin(int firstHalf, int secondHalf) {
    std::array<uint8_t, 7> seven{};
    seven[0] = static_cast<uint8_t>(firstHalf / 1000);
    seven[1] = static_cast<uint8_t>((firstHalf / 100) % 10);
    seven[2] = static_cast<uint8_t>((firstHalf / 10) % 10);
    seven[3] = static_cast<uint8_t>(firstHalf % 10);
    seven[4] = static_cast<uint8_t>(secondHalf / 100);
    seven[5] = static_cast<uint8_t>((secondHalf / 10) % 10);
    seven[6] = static_cast<uint8_t>(secondHalf % 10);
    return pinWithCheckDigit(seven);
}

MacAddress makeMac(uint8_t base) {
    MacAddress mac;
    for (std::size_t i = 0; i < mac.octets.size(); ++i) {
        mac.octets[i] = static_cast<uint8_t>(base + i);
    }
    return mac;
}

Nonce makeNonce(uint8_t seed) {
    Nonce nonce;
    for (std::size_t i = 0; i < nonce.octets.size(); ++i) {
        nonce.octets[i] = static_cast<uint8_t>(seed + static_cast<int>(i) * 7);
    }
    return nonce;
}

std::string statePath(const std::string& name) {
    const char* tmp = std::getenv("TMPDIR");
    std::string dir = tmp ? tmp : ".";
    return dir + "/netsec_test_" + name + ".state";
}

void removeState(const std::string& path) {
    std::remove(path.c_str());
    std::remove((path + ".tmp").c_str());
}

struct FakeClock {
    int64_t seconds = 1000000;
    Seconds operator()() const {
        return Seconds(seconds);
    }
};

class CountingScheme : public CommitmentScheme {
public:
    explicit CountingScheme(std::shared_ptr<CommitmentScheme> inner)
        : inner_(std::move(inner)) {}

    Commitment deriveFirst(const Pin& pin, const MacAddress& mac,
                           const Nonce& nonce) override {
        ++firstCalls;
        return inner_->deriveFirst(pin, mac, nonce);
    }

    Commitment deriveSecond(const Pin& pin, const MacAddress& mac,
                            const Nonce& nonce) override {
        ++secondCalls;
        return inner_->deriveSecond(pin, mac, nonce);
    }

    int firstCalls = 0;
    int secondCalls = 0;

private:
    std::shared_ptr<CommitmentScheme> inner_;
};

std::vector<uint8_t> bytesFromHex(const std::string& hex) {
    std::vector<uint8_t> out;
    for (std::size_t i = 0; i + 1 < hex.size(); i += 2) {
        auto nibble = [](char c) -> int {
            if (c >= '0' && c <= '9') return c - '0';
            if (c >= 'a' && c <= 'f') return c - 'a' + 10;
            if (c >= 'A' && c <= 'F') return c - 'A' + 10;
            return -1;
        };
        out.push_back(static_cast<uint8_t>((nibble(hex[i]) << 4) |
                                           nibble(hex[i + 1])));
    }
    return out;
}

std::string toHex(const std::array<uint8_t, 32>& data) {
    std::ostringstream stream;
    for (uint8_t byte : data) {
        stream << std::hex << std::setw(2) << std::setfill('0')
               << static_cast<int>(byte);
    }
    return stream.str();
}

}  // namespace

int main() {
    runTest("checkDigitFormula", [] {
        Pin pin;
        CHECK(parsePin("12345670", pin));
        CHECK(computeCheckDigit(pin) == 0);
        CHECK(hasValidCheckDigit(pin));

        CHECK(parsePin("11111115", pin));
        CHECK(computeCheckDigit(pin) == 5);
        CHECK(hasValidCheckDigit(pin));

        Pin allZeros = makePin(0, 0, 0);
        CHECK(computeCheckDigit(allZeros) == 0);
        CHECK(hasValidCheckDigit(allZeros));

        Pin maxPin = makePin(9999, 999, 0);
        CHECK(computeCheckDigit(maxPin) == 5);
        CHECK(!hasValidCheckDigit(maxPin));
        maxPin.digits[7] = 5;
        CHECK(hasValidCheckDigit(maxPin));

        Pin rebuilt = validPin(1234, 567);
        CHECK(rebuilt.toString() == "12345670");
        Pin withBadDigit = makePin(1234, 567, 9);
        CHECK(!hasValidCheckDigit(withBadDigit));

        Pin parsed;
        CHECK(!parsePin("1234", parsed));
        CHECK(!parsePin("1234567a", parsed));
        CHECK(!parsePin("123456700", parsed));
    });

    runTest("macParsing", [] {
        MacAddress mac;
        CHECK(parseMac("00:11:22:33:44:55", mac));
        CHECK(mac.octets[0] == 0x00 && mac.octets[5] == 0x55);
        CHECK(parseMac("AA-BB-CC-DD-EE-FF", mac));
        CHECK(mac.octets[0] == 0xAA && mac.octets[3] == 0xDD);
        CHECK(!parseMac("00-11-22-33-44", mac));
        CHECK(!parseMac("GG:11:22:33:44:55", mac));
    });

    runTest("cryptoVectors", [] {
        auto emptyHash = detail::sha256(nullptr, 0);
        CHECK(toHex(emptyHash) ==
              "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");

        const std::string abc = "abc";
        auto abcHash = detail::sha256(reinterpret_cast<const uint8_t*>(abc.data()),
                                      abc.size());
        CHECK(toHex(abcHash) ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");

        std::string millionA(1000000, 'a');
        auto millionHash =
            detail::sha256(reinterpret_cast<const uint8_t*>(millionA.data()),
                           millionA.size());
        CHECK(toHex(millionHash) ==
              "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0");

        const std::string key = "key";
        const std::string data = "The quick brown fox jumps over the lazy dog";
        auto macValue = detail::hmacSha256(
            reinterpret_cast<const uint8_t*>(key.data()), key.size(),
            reinterpret_cast<const uint8_t*>(data.data()), data.size());
        CHECK(toHex(macValue) ==
              "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8");

        std::string longKey(100, 0xAB);
        auto longKeyMac = detail::hmacSha256(
            reinterpret_cast<const uint8_t*>(longKey.data()), longKey.size(),
            reinterpret_cast<const uint8_t*>(data.data()), data.size());
        CHECK(longKeyMac.size() == 32);

        auto crcEmpty = detail::crc32Ieee(nullptr, 0);
        CHECK(crcEmpty == 0x00000000u);
        const std::string crcText = "123456789";
        auto crcValue = detail::crc32Ieee(
            reinterpret_cast<const uint8_t*>(crcText.data()), crcText.size());
        CHECK(crcValue == 0xCBF43926u);
    });

    runTest("commitmentDeterminism", [] {
        HmacSha256CommitmentScheme scheme(bytesFromHex("01020304"));
        Pin pin = validPin(4321, 876);
        MacAddress mac = makeMac(10);
        Nonce nonce = makeNonce(20);
        Commitment first = scheme.deriveFirst(pin, mac, nonce);
        Commitment second = scheme.deriveSecond(pin, mac, nonce);
        Commitment firstAgain = scheme.deriveFirst(pin, mac, nonce);
        Commitment secondAgain = scheme.deriveSecond(pin, mac, nonce);
        CHECK(constantTimeEqual(first, firstAgain));
        CHECK(constantTimeEqual(second, secondAgain));
        CHECK(!constantTimeEqual(first, second));
        Nonce otherNonce = makeNonce(21);
        Commitment different = scheme.deriveFirst(pin, mac, otherNonce);
        CHECK(!constantTimeEqual(first, different));
    });

    runTest("successAndUniformFailure", [] {
        const std::string path = statePath("uniform");
        removeState(path);
        FakeClock clock;
        auto scheme = std::make_shared<CountingScheme>(
            std::make_shared<HmacSha256CommitmentScheme>(bytesFromHex("aabbccdd")));
        LockoutPolicy policy;
        policy.failureThreshold = 100000;
        Pin devicePin = validPin(2468, 135);
        PinAuthenticator auth(devicePin, path, policy, scheme,
                              [&clock] { return clock(); });

        MacAddress mac = makeMac(1);
        Nonce nonce = makeNonce(2);
        RegistrarRequest good;
        good.mac = mac;
        good.nonce = nonce;
        good.firstCommitment = scheme->deriveFirst(devicePin, mac, nonce);
        good.secondCommitment = scheme->deriveSecond(devicePin, mac, nonce);

        CHECK(auth.verify(good, devicePin) == VerifyResult::Success);
        CHECK(auth.failureCount() == 0);

        RegistrarRequest firstHalfBad = good;
        firstHalfBad.firstCommitment.bytes[3] ^= 0x0F;
        RegistrarRequest secondHalfBad = good;
        secondHalfBad.secondCommitment.bytes[19] ^= 0xF0;
        RegistrarRequest bothBad = good;
        bothBad.firstCommitment.bytes[3] ^= 0x0F;
        bothBad.secondCommitment.bytes[19] ^= 0xF0;

        int callsBefore = scheme->firstCalls;
        CHECK(auth.verify(firstHalfBad, devicePin) == VerifyResult::Failed);
        CHECK(scheme->firstCalls == callsBefore + 1);
        CHECK(scheme->secondCalls == callsBefore + 1);
        CHECK(auth.failureCount() == 1);

        callsBefore = scheme->firstCalls;
        CHECK(auth.verify(secondHalfBad, devicePin) == VerifyResult::Failed);
        CHECK(scheme->firstCalls == callsBefore + 1);
        CHECK(scheme->secondCalls == callsBefore + 1);
        CHECK(auth.failureCount() == 2);

        callsBefore = scheme->firstCalls;
        CHECK(auth.verify(bothBad, devicePin) == VerifyResult::Failed);
        CHECK(scheme->firstCalls == callsBefore + 1);
        CHECK(scheme->secondCalls == callsBefore + 1);

        Pin wrongFirstHalf = validPin(2469, 135);
        CHECK(auth.verify(good, wrongFirstHalf) == VerifyResult::Failed);
        Pin wrongSecondHalf = validPin(2468, 136);
        CHECK(auth.verify(good, wrongSecondHalf) == VerifyResult::Failed);

        CHECK(auth.verify(good, devicePin) == VerifyResult::Success);
        CHECK(auth.failureCount() == 0);
        CHECK(auth.lockTier() == 0);
        CHECK(auth.currentLockDuration() == Seconds(0));
        removeState(path);
    });

    runTest("checkDigitRejectedBeforeProtocol", [] {
        const std::string path = statePath("checksum");
        removeState(path);
        FakeClock clock;
        auto scheme = std::make_shared<CountingScheme>(
            std::make_shared<HmacSha256CommitmentScheme>(bytesFromHex("11223344")));
        PinAuthenticator auth(validPin(1000, 200), path, LockoutPolicy{}, scheme,
                              [&clock] { return clock(); });
        Pin badCheck = makePin(1000, 200, 9);
        RegistrarRequest request;
        request.mac = makeMac(4);
        request.nonce = makeNonce(5);
        CHECK(auth.verify(request, badCheck) == VerifyResult::InvalidCheckDigit);
        CHECK(scheme->firstCalls == 0);
        CHECK(scheme->secondCalls == 0);
        CHECK(auth.failureCount() == 0);

        Pin malformed = makePin(1000, 200, 0);
        malformed.digits[2] = 12;
        CHECK(auth.verify(request, malformed) == VerifyResult::InvalidInput);
        CHECK(scheme->firstCalls == 0);
        CHECK(auth.failureCount() == 0);
        removeState(path);
    });

    runTest("lockoutThresholdAndDoubling", [] {
        const std::string path = statePath("lockout");
        removeState(path);
        FakeClock clock;
        auto scheme = std::make_shared<CountingScheme>(
            std::make_shared<HmacSha256CommitmentScheme>(bytesFromHex("55667788")));
        LockoutPolicy policy;
        policy.failureThreshold = 3;
        policy.baseLockDuration = Seconds(60);
        policy.maxLockDuration = Seconds(24 * 60 * 60);
        Pin devicePin = validPin(1111, 222);
        PinAuthenticator auth(devicePin, path, policy, scheme,
                              [&clock] { return clock(); });

        MacAddress mac = makeMac(7);
        Nonce nonce = makeNonce(8);
        RegistrarRequest good;
        good.mac = mac;
        good.nonce = nonce;
        good.firstCommitment = scheme->deriveFirst(devicePin, mac, nonce);
        good.secondCommitment = scheme->deriveSecond(devicePin, mac, nonce);
        RegistrarRequest bad = good;
        bad.secondCommitment.bytes[0] ^= 0x55;

        CHECK(auth.verify(bad, devicePin) == VerifyResult::Failed);
        CHECK(!auth.isLocked());
        CHECK(auth.verify(bad, devicePin) == VerifyResult::Failed);
        CHECK(!auth.isLocked());
        CHECK(auth.verify(bad, devicePin) == VerifyResult::Failed);
        CHECK(auth.isLocked());
        CHECK(auth.currentLockDuration() == Seconds(60));
        CHECK(auth.remainingLockTime() == Seconds(60));
        int callsBefore = scheme->firstCalls;

        Pin otherPin = validPin(3333, 444);
        CHECK(auth.verify(good, otherPin) == VerifyResult::Locked);
        CHECK(scheme->firstCalls == callsBefore);
        CHECK(scheme->secondCalls == callsBefore);
        CHECK(auth.verify(good, devicePin) == VerifyResult::Locked);

        clock.seconds += 59;
        CHECK(auth.isLocked());
        CHECK(auth.remainingLockTime() == Seconds(1));
        clock.seconds += 1;
        CHECK(!auth.isLocked());
        CHECK(auth.failureCount() == 3);

        const std::vector<int64_t> expectedDurations = {
            120, 240, 480, 960, 1920, 3840, 7680, 15360,
            30720, 61440, 86400, 86400};
        unsigned expectedTier = 2;
        for (int64_t expected : expectedDurations) {
            if (auth.isLocked()) {
                clock.seconds += auth.currentLockDuration().count() + 1;
            }
            CHECK(auth.verify(good, devicePin) == VerifyResult::Success);
            CHECK(auth.failureCount() == 0);
            CHECK(auth.lockTier() == expectedTier - 1);
            for (unsigned attempt = 0; attempt < policy.failureThreshold; ++attempt) {
                CHECK(!auth.isLocked());
                VerifyResult result = auth.verify(bad, devicePin);
                CHECK(result == VerifyResult::Failed);
                if (attempt + 1 < policy.failureThreshold) {
                    CHECK(!auth.isLocked());
                } else {
                    CHECK(auth.isLocked());
                }
            }
            CHECK(auth.lockTier() == expectedTier);
            CHECK(auth.currentLockDuration() == Seconds(expected));
            ++expectedTier;
        }

        clock.seconds += 86400;
        CHECK(!auth.isLocked());
        CHECK(auth.verify(good, devicePin) == VerifyResult::Success);
        CHECK(auth.failureCount() == 0);
        CHECK(auth.lockTier() == 13);
        CHECK(auth.currentLockDuration() == Seconds(0));
        removeState(path);
    });

    runTest("persistenceSurvivesRestartAndMacChange", [] {
        const std::string path = statePath("restart");
        removeState(path);
        FakeClock clock;
        auto makeAuth = [&] {
            return std::make_unique<PinAuthenticator>(
                validPin(7777, 888), path, LockoutPolicy{},
                std::make_shared<HmacSha256CommitmentScheme>(bytesFromHex("09182736")),
                [&clock] { return clock(); });
        };

        auto auth = makeAuth();
        MacAddress firstMac = makeMac(100);
        Nonce nonce = makeNonce(30);
        Pin devicePin = validPin(7777, 888);
        auto scheme = HmacSha256CommitmentScheme(bytesFromHex("09182736"));
        RegistrarRequest good;
        good.mac = firstMac;
        good.nonce = nonce;
        good.firstCommitment = scheme.deriveFirst(devicePin, firstMac, nonce);
        good.secondCommitment = scheme.deriveSecond(devicePin, firstMac, nonce);
        RegistrarRequest bad = good;
        bad.firstCommitment.bytes[11] ^= 0x33;

        CHECK(auth->verify(bad, devicePin) == VerifyResult::Failed);
        CHECK(auth->verify(bad, devicePin) == VerifyResult::Failed);

        auth.reset();
        auto restarted = makeAuth();
        CHECK(restarted->failureCount() == 2);
        CHECK(!restarted->isLocked());

        MacAddress secondMac = makeMac(200);
        RegistrarRequest otherMacBad = bad;
        otherMacBad.mac = secondMac;
        otherMacBad.nonce = makeNonce(31);
        otherMacBad.firstCommitment =
            scheme.deriveFirst(devicePin, secondMac, otherMacBad.nonce);
        otherMacBad.secondCommitment =
            scheme.deriveSecond(devicePin, secondMac, otherMacBad.nonce);
        otherMacBad.firstCommitment.bytes[11] ^= 0x33;

        CHECK(restarted->verify(otherMacBad, devicePin) == VerifyResult::Failed);
        CHECK(restarted->failureCount() == 3);
        CHECK(restarted->isLocked());
        CHECK(restarted->currentLockDuration() == Seconds(60));

        RegistrarRequest otherMacGood = otherMacBad;
        otherMacGood.firstCommitment =
            scheme.deriveFirst(devicePin, secondMac, otherMacBad.nonce);
        CHECK(restarted->verify(otherMacGood, devicePin) == VerifyResult::Locked);

        restarted.reset();
        auto afterReboot = makeAuth();
        CHECK(afterReboot->isLocked());
        CHECK(afterReboot->failureCount() == 3);
        CHECK(afterReboot->lockTier() == 1);
        CHECK(afterReboot->remainingLockTime() > Seconds(0));
        CHECK(afterReboot->verify(otherMacGood, devicePin) == VerifyResult::Locked);

        clock.seconds += 60;
        CHECK(!afterReboot->isLocked());
        CHECK(afterReboot->verify(otherMacGood, devicePin) == VerifyResult::Success);
        CHECK(afterReboot->failureCount() == 0);
        removeState(path);
    });

    runTest("corruptOrMissingStateStartsClean", [] {
        const std::string path = statePath("corrupt");
        removeState(path);
        FakeClock clock;
        auto build = [&] {
            return PinAuthenticator(
                validPin(1212, 343), path, LockoutPolicy{},
                std::make_shared<HmacSha256CommitmentScheme>(bytesFromHex("deadbeef")),
                [&clock] { return clock(); });
        };
        auto auth = build();
        CHECK(auth.failureCount() == 0);
        auth.resetState();

        std::ofstream tamper(path, std::ios::binary | std::ios::trunc);
        tamper << "not-a-state";
        tamper.close();
        auto repaired = build();
        CHECK(repaired.failureCount() == 0);
        CHECK(!repaired.isLocked());

        repaired.resetState();
        std::ofstream truncated(path, std::ios::binary | std::ios::trunc);
        truncated.write("0123456789", 10);
        truncated.close();
        auto truncatedAuth = build();
        CHECK(truncatedAuth.failureCount() == 0);
        removeState(path);
    });

    runTest("enumerateAllFirstHalvesTimingEqual", [] {
        const std::string path = statePath("timing");
        removeState(path);
        FakeClock clock;
        auto scheme = std::make_shared<CountingScheme>(
            std::make_shared<HmacSha256CommitmentScheme>(bytesFromHex("fedcba98")));
        LockoutPolicy policy;
        policy.failureThreshold = 1000000;
        const Pin devicePin = validPin(5678, 901);
        PinAuthenticator auth(devicePin, path, policy, scheme,
                              [&clock] { return clock(); });

        const MacAddress mac = makeMac(44);
        const Nonce nonce = makeNonce(55);
        RegistrarRequest validCommitments;
        validCommitments.mac = mac;
        validCommitments.nonce = nonce;
        validCommitments.firstCommitment =
            scheme->deriveFirst(devicePin, mac, nonce);
        validCommitments.secondCommitment =
            scheme->deriveSecond(devicePin, mac, nonce);

        for (int i = 0; i < 200; ++i) {
            Pin warmPin = validPin(i % kFirstHalfValues, 901);
            RegistrarRequest warm = validCommitments;
            warm.firstCommitment.bytes[0] ^= 0x01;
            CHECK(auth.verify(warm, warmPin) == VerifyResult::Failed);
        }

        auto measure = [&](bool corruptFirst, int submittedSecond) {
            std::vector<int64_t> samples;
            samples.reserve(kFirstHalfValues);
            for (int half = 0; half < static_cast<int>(kFirstHalfValues); ++half) {
                Pin submitted = validPin(half, submittedSecond);
                RegistrarRequest request = validCommitments;
                if (corruptFirst) {
                    request.firstCommitment.bytes[0] ^= 0x01;
                }
                auto begin = std::chrono::steady_clock::now();
                VerifyResult result = auth.verify(request, submitted);
                auto end = std::chrono::steady_clock::now();
                CHECK(result == VerifyResult::Failed);
                samples.push_back(
                    std::chrono::duration_cast<std::chrono::nanoseconds>(end - begin)
                        .count());
            }
            return samples;
        };

        int firstCallsBefore = scheme->firstCalls;
        int secondCallsBefore = scheme->secondCalls;
        std::vector<int64_t> firstHalfWrong = measure(true, 901);
        std::vector<int64_t> secondHalfWrong = measure(false, 902);

        CHECK(scheme->firstCalls - firstCallsBefore ==
              static_cast<int>(2 * kFirstHalfValues));
        CHECK(scheme->secondCalls - secondCallsBefore ==
              static_cast<int>(2 * kFirstHalfValues));
        CHECK(firstHalfWrong.size() == kFirstHalfValues);
        CHECK(secondHalfWrong.size() == kFirstHalfValues);

        auto stats = [](std::vector<int64_t> values) {
            std::sort(values.begin(), values.end());
            int64_t sum = std::accumulate(values.begin(), values.end(), int64_t{0});
            struct Result {
                double mean;
                int64_t median;
                int64_t minimum;
                int64_t maximum;
            };
            return Result{static_cast<double>(sum) / values.size(),
                          values[values.size() / 2], values.front(), values.back()};
        };
        auto left = stats(firstHalfWrong);
        auto right = stats(secondHalfWrong);
        double medianRatio = static_cast<double>(left.median) / right.median;
        double meanRatio = left.mean / right.mean;

        std::cout << "  first-half-wrong  ns mean=" << left.mean
                  << " median=" << left.median << " min=" << left.minimum
                  << " max=" << left.maximum << '\n';
        std::cout << "  second-half-wrong ns mean=" << right.mean
                  << " median=" << right.median << " min=" << right.minimum
                  << " max=" << right.maximum << '\n';
        std::cout << "  ratios median=" << medianRatio << " mean=" << meanRatio
                  << '\n';

        CHECK(medianRatio >= 0.75 && medianRatio <= 1.3333333);
        CHECK(meanRatio >= 0.70 && meanRatio <= 1.4285714);

        Pin correctPin = devicePin;
        RegistrarRequest good = validCommitments;
        CHECK(auth.verify(good, correctPin) == VerifyResult::Success);
        CHECK(auth.failureCount() == 0);
        removeState(path);
    });

    std::cout << (g_failures == 0 ? "ALL TESTS PASSED" : "TESTS FAILED")
              << " (" << g_checks << " assertions, " << g_failures << " failures)\n";
    return g_failures == 0 ? 0 : 1;
}

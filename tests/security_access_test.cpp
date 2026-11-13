#include "netsec.hpp"

#include <chrono>
#include <cstdint>
#include <iostream>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        std::cerr << "line " << line << ": check failed: " << expression
                  << '\n';
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

netsec::Seed makeSeed(std::uint8_t first) {
    return {first, static_cast<std::uint8_t>(first + 1),
            static_cast<std::uint8_t>(first + 2),
            static_cast<std::uint8_t>(first + 3)};
}

netsec::Key makeKey(const netsec::Seed& seed) {
    netsec::Key key(seed.size());
    for (std::size_t index = 0; index < seed.size(); ++index) {
        key[index] = static_cast<std::uint8_t>(seed[index] ^ 0xA5U);
    }
    return key;
}

struct TestContext {
    std::uint8_t nextSeed = 0x10;
    std::chrono::steady_clock::time_point now{};
    netsec::SecurityAccessStateMachine security{
        [this] { return makeSeed(nextSeed++); },
        [this] { return now; }};

    TestContext() {
        security.registerLevel(0x01, [](const netsec::Seed& seed,
                                        const netsec::Key& key) {
            return key == makeKey(seed);
        });
        security.registerLevel(0x03, [](const netsec::Seed& seed,
                                        const netsec::Key& key) {
            return key == makeKey(seed);
        });
    }
};

void test_unlocked_level_returns_zero_seed_and_replay_fails_after_session_change() {
    TestContext context;

    auto seed = context.security.requestSeed(0x01);
    CHECK(seed.positive);
    CHECK(seed.seed == makeSeed(0x10));

    auto key = makeKey(seed.seed);
    auto unlock = context.security.sendKey(0x02, key);
    CHECK(unlock.positive);
    CHECK(context.security.isUnlocked(0x01));

    auto zeroSeed = context.security.requestSeed(0x01);
    CHECK(zeroSeed.positive);
    CHECK(zeroSeed.seed == netsec::Seed(4, 0));

    context.security.changeSession(0x02);
    CHECK(!context.security.isUnlocked(0x01));

    auto freshSeed = context.security.requestSeed(0x01);
    CHECK(freshSeed.positive);
    CHECK(freshSeed.seed == makeSeed(0x11));
    CHECK(freshSeed.seed != seed.seed);

    auto replay = context.security.sendKey(0x02, key);
    CHECK(replay.failed());
    CHECK(replay.nrc == netsec::Nrc::InvalidKey);
    CHECK(!context.security.isUnlocked(0x01));

    auto reusedSeed = context.security.sendKey(0x02, makeKey(freshSeed.seed));
    CHECK(reusedSeed.failed());
    CHECK(reusedSeed.nrc == netsec::Nrc::RequestSequenceError);
}

void test_two_wrong_keys_then_correct_key_resets_failure_count() {
    TestContext context;

    auto firstSeed = context.security.requestSeed(0x01);
    auto firstFailure = context.security.sendKey(0x02, netsec::Key{0, 0, 0, 0});
    CHECK(firstFailure.failed());
    CHECK(firstFailure.nrc == netsec::Nrc::InvalidKey);

    auto secondSeed = context.security.requestSeed(0x01);
    auto secondFailure =
        context.security.sendKey(0x02, netsec::Key{1, 1, 1, 1});
    CHECK(secondFailure.failed());
    CHECK(secondFailure.nrc == netsec::Nrc::InvalidKey);

    auto thirdSeed = context.security.requestSeed(0x01);
    auto correct = context.security.sendKey(0x02, makeKey(thirdSeed.seed));
    CHECK(correct.positive);
    CHECK(context.security.isUnlocked(0x01));
    CHECK(context.security.failedAttempts() == 0);
}

void test_three_failures_lock_for_ten_seconds() {
    TestContext context;

    for (int attempt = 0; attempt < 3; ++attempt) {
        auto seed = context.security.requestSeed(0x01);
        CHECK(seed.positive);
        auto failure =
            context.security.sendKey(0x02, netsec::Key{0xEE, 0xEE, 0xEE, 0xEE});
        CHECK(failure.failed());
        if (attempt < 2) {
            CHECK(failure.nrc == netsec::Nrc::InvalidKey);
        } else {
            CHECK(failure.nrc == netsec::Nrc::ExceededNumberOfAttempts);
        }
    }

    CHECK(!context.security.isUnlocked(0x01));
    CHECK(context.security.isLockedOut());

    context.security.changeSession(0x02);
    auto duringLockout = context.security.requestSeed(0x01);
    CHECK(duringLockout.failed());
    CHECK(duringLockout.nrc == netsec::Nrc::RequiredTimeDelayNotExpired);

    context.now += std::chrono::seconds(9);
    auto almostExpired = context.security.requestSeed(0x01);
    CHECK(almostExpired.failed());
    CHECK(almostExpired.nrc == netsec::Nrc::RequiredTimeDelayNotExpired);

    context.now += std::chrono::seconds(1);
    auto afterLockout = context.security.requestSeed(0x01);
    CHECK(afterLockout.positive);
    CHECK(!context.security.isLockedOut());
}

void test_sequence_and_subfunction_errors() {
    TestContext context;

    auto keyWithoutSeed = context.security.sendKey(0x02, netsec::Key{});
    CHECK(keyWithoutSeed.failed());
    CHECK(keyWithoutSeed.nrc == netsec::Nrc::RequestSequenceError);

    auto unknownSeedSubfunction = context.security.requestSeed(0x05);
    CHECK(unknownSeedSubfunction.failed());
    CHECK(unknownSeedSubfunction.nrc == netsec::Nrc::InvalidSubFunction);

    auto unknownKeySubfunction =
        context.security.sendKey(0x06, netsec::Key{});
    CHECK(unknownKeySubfunction.failed());
    CHECK(unknownKeySubfunction.nrc == netsec::Nrc::InvalidSubFunction);
}

void test_session_change_and_timeout_lock_every_level() {
    TestContext context;

    auto levelOneSeed = context.security.requestSeed(0x01);
    CHECK(context.security.sendKey(0x02, makeKey(levelOneSeed.seed)).positive);

    auto levelThreeSeed = context.security.requestSeed(0x03);
    CHECK(context.security.sendKey(0x04, makeKey(levelThreeSeed.seed)).positive);
    CHECK(context.security.isUnlocked(0x01));
    CHECK(context.security.isUnlocked(0x03));

    context.security.changeSession(0x01);
    CHECK(context.security.isUnlocked(0x01));
    CHECK(context.security.isUnlocked(0x03));

    context.security.changeSession(0x02);
    CHECK(!context.security.isUnlocked(0x01));
    CHECK(!context.security.isUnlocked(0x03));

    auto newSeed = context.security.requestSeed(0x03);
    CHECK(newSeed.seed != netsec::Seed(4, 0));
    CHECK(context.security.sendKey(0x04, makeKey(newSeed.seed)).positive);
    CHECK(context.security.isUnlocked(0x03));

    context.security.onSessionTimeout();
    CHECK(!context.security.isUnlocked(0x01));
    CHECK(!context.security.isUnlocked(0x03));
}

}

int main() {
    test_unlocked_level_returns_zero_seed_and_replay_fails_after_session_change();
    test_two_wrong_keys_then_correct_key_resets_failure_count();
    test_three_failures_lock_for_ten_seconds();
    test_sequence_and_subfunction_errors();
    test_session_change_and_timeout_lock_every_level();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }

    std::cout << "all security access tests passed\n";
    return 0;
}

#include "netsec.hpp"

#include <chrono>
#include <cstdlib>
#include <filesystem>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

using namespace netsec;

int failures = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ':' << __LINE__                         \
                      << ": check failed: " #condition << '\n';              \
            return false;                                                    \
        }                                                                    \
    } while (false)

Octets bytes(std::string_view text) {
    return Octets(text.begin(), text.end());
}

Octets testKey(std::size_t size, std::uint8_t seed) {
    Octets key(size);
    for (std::size_t index = 0; index < size; ++index) {
        key[index] =
            static_cast<std::uint8_t>(seed + index * 17u);
    }
    return key;
}

struct FakeClock {
    std::chrono::steady_clock::time_point epoch =
        std::chrono::steady_clock::time_point{} +
        std::chrono::hours{24};
    std::uint32_t seconds = 0;

    MonotonicClock function() {
        return [this] { return epoch + std::chrono::seconds(seconds); };
    }
};

EngineConfig testConfig(std::uint32_t boots = 3) {
    EngineConfig config;
    config.engine_id = {0x80, 0x00, 0x1f, 0x88, 0x80, 0x01, 0x02, 0x03};
    config.boots = boots;
    config.engine_time = 100;
    config.time_window_seconds = 150;
    config.replay_capacity = 64;
    return config;
}

UsmUser testUser(std::uint32_t generation = 1) {
    UsmUser user;
    user.name = "operator";
    user.key_generation = generation;
    user.auth_protocol = AuthProtocol::HmacSha1;
    user.priv_protocol = PrivProtocol::Aes128;
    user.auth_key = testKey(20, 0x31);
    user.privacy_key = testKey(16, 0x91);
    return user;
}

UsmRequest requestFor(
    const ReplayEngine& engine,
    const UsmUser& user,
    std::int64_t time_offset,
    std::string_view pdu) {
    const EngineSnapshot state = engine.snapshot();
    UsmRequest request;
    request.authoritative_engine_id = state.engine_id;
    request.boots = state.boots;
    const std::int64_t requested_time =
        static_cast<std::int64_t>(state.engine_time) + time_offset;
    request.engine_time =
        requested_time < 0 ? 0u : static_cast<std::uint32_t>(requested_time);
    request.user_name = user.name;
    request.key_generation = user.key_generation;
    request.auth_protocol = user.auth_protocol;
    request.priv_protocol = user.priv_protocol;
    request.message = bytes(pdu);
    request.authentication_parameters = makeAuthenticator(
        user.auth_protocol, user.auth_key, request.message);
    return request;
}

bool testHashAndHmacVectors() {
    const Octets hmac_md5 = hmac(
        AuthProtocol::HmacMd5,
        bytes("key"),
        bytes("The quick brown fox jumps over the lazy dog"));
    const Octets expected_md5 = {
        0x80, 0x07, 0x07, 0x13, 0x46, 0x3e, 0x77, 0x49,
        0xb9, 0x0c, 0x2d, 0xc2, 0x49, 0x11, 0xe2, 0x75,
    };
    CHECK(hmac_md5 == expected_md5);

    const Octets hmac_sha1 = hmac(
        AuthProtocol::HmacSha1,
        bytes("key"),
        bytes("The quick brown fox jumps over the lazy dog"));
    const Octets expected_sha1 = {
        0xde, 0x7c, 0x9b, 0x85, 0xb8, 0xb7, 0x8a, 0xa6, 0xbc, 0x8a,
        0x7a, 0x36, 0xf7, 0x0a, 0x90, 0x70, 0x1c, 0x9d, 0xb4, 0xd9,
    };
    CHECK(hmac_sha1 == expected_sha1);

    const Octets hmac_sha256 = hmac(
        AuthProtocol::HmacSha256,
        bytes("key"),
        bytes("The quick brown fox jumps over the lazy dog"));
    const Octets expected_sha256 = {
        0xf7, 0xbc, 0x83, 0xf4, 0x30, 0x53, 0x84, 0x24, 0xb1, 0x32,
        0x98, 0xe6, 0xaa, 0x6f, 0xb1, 0x43, 0xef, 0x4d, 0x59, 0xa1,
        0x49, 0x46, 0x17, 0x59, 0x97, 0x47, 0x9d, 0xbc, 0x2d, 0x1a,
        0x3c, 0xd8,
    };
    CHECK(hmac_sha256 == expected_sha256);
    CHECK(makeAuthenticator(
              AuthProtocol::HmacSha1, bytes("key"), bytes("message"))
              .size() == 12u);
    CHECK(makeAuthenticator(
              AuthProtocol::HmacMd5, bytes("key"), bytes("message"))
              .size() == 12u);
    CHECK(makeAuthenticator(
              AuthProtocol::HmacSha256, bytes("key"), bytes("message"))
              .size() == 24u);
    return true;
}

bool testBootsRollbackAndResynchronization() {
    auto store = std::make_shared<MemoryEngineStateStore>();
    store->save(PersistedEngineState{testConfig().engine_id, 12, 900});

    FakeClock clock;
    ReplayEngine engine(testConfig(3), store, clock.function());
    CHECK(engine.snapshot().boots == 12);
    CHECK(engine.addUser(testUser()) == UserMutationResult::Added);

    const UsmRequest before_restore =
        requestFor(engine, testUser(), 0, "old-getbulk");
    CHECK(engine.verify(before_restore).accepted());

    const RestoreResult restored =
        engine.restoreConfiguration(testConfig(4));
    CHECK(restored == RestoreResult::RequiresResynchronization);
    CHECK(engine.snapshot().boots == 12);
    CHECK(engine.resynchronizationRequired());
    CHECK(!engine.verify(
               requestFor(engine, testUser(), 0, "restore-not-resynced"))
               .accepted());

    const std::uint32_t resynchronized_boots = engine.resynchronize();
    CHECK(resynchronized_boots == 13);
    CHECK(!engine.resynchronizationRequired());
    CHECK(store->load()->boots == 13);

    const ReplayResult old_result = engine.verify(before_restore);
    CHECK(!old_result.accepted());
    CHECK(old_result.reason == ReplayReason::OutsideTimeWindow);
    CHECK(engine.statistics().time_window_rejections == 2u);

    UsmUser current_user = testUser();
    CHECK(engine.verify(
              requestFor(engine, current_user, 0, "fresh-getbulk"))
              .accepted());
    return true;
}

bool testMonotonicRestoreAndPersistence() {
    const char* temporary_directory = std::getenv("TMPDIR");
    CHECK(temporary_directory != nullptr);
    const std::filesystem::path path =
        std::filesystem::path(temporary_directory) /
        "netsec-engine-state-test";
    std::error_code ignored;
    std::filesystem::remove(path, ignored);

    auto store = std::make_shared<FileEngineStateStore>(path);
    FakeClock clock;
    {
        ReplayEngine engine(testConfig(7), store, clock.function());
        CHECK(engine.snapshot().boots == 7);
        CHECK(engine.engineBooted() == 8);
        CHECK(engine.restoreConfiguration(testConfig(9)) ==
              RestoreResult::Advanced);
        CHECK(engine.snapshot().boots == 9);
    }

    {
        ReplayEngine restored_from_backup(
            testConfig(2), store, clock.function());
        CHECK(restored_from_backup.snapshot().boots == 9);
        CHECK(restored_from_backup.restoreConfiguration(testConfig(5)) ==
              RestoreResult::RequiresResynchronization);
        CHECK(restored_from_backup.snapshot().boots == 9);
        CHECK(restored_from_backup.resynchronize() == 10);
    }

    {
        auto reloaded_store = std::make_shared<FileEngineStateStore>(path);
        ReplayEngine reloaded(testConfig(1), reloaded_store, clock.function());
        CHECK(reloaded.snapshot().boots == 10);
    }

    std::filesystem::remove(path, ignored);
    return true;
}

bool testClockSkewAndWindowOrdering() {
    FakeClock clock;
    EngineConfig config = testConfig(4);
    config.engine_time = 1'000;
    ReplayEngine engine(config, {}, clock.function());
    CHECK(engine.addUser(testUser()) == UserMutationResult::Added);

    UsmUser user = testUser();
    CHECK(engine.verify(requestFor(engine, user, 150, "edge-late")).accepted());
    CHECK(engine.verify(requestFor(engine, user, -150, "edge-early")).accepted());

    const ReplayResult future = engine.verify(
        requestFor(engine, user, 151, "too-future"));
    CHECK(!future.accepted());
    CHECK(future.reason == ReplayReason::OutsideTimeWindow);

    const ReplayResult past = engine.verify(
        requestFor(engine, user, -151, "too-old"));
    CHECK(!past.accepted());
    CHECK(past.reason == ReplayReason::OutsideTimeWindow);

    const UsmRequest first =
        requestFor(engine, user, 10, "in-window-a");
    const UsmRequest second =
        requestFor(engine, user, -10, "in-window-b");
    const UsmRequest third =
        requestFor(engine, user, 5, "in-window-c");
    CHECK(engine.verify(first).accepted());
    CHECK(engine.verify(second).accepted());
    CHECK(engine.verify(third).accepted());
    CHECK(!engine.verify(second).accepted());
    CHECK(engine.statistics().replay_rejections == 1u);
    return true;
}

bool testKeyRotationAndAlgorithmBinding() {
    FakeClock clock;
    ReplayEngine engine(testConfig(5), {}, clock.function());
    const UsmUser generation_one = testUser(1);
    CHECK(engine.addUser(generation_one) == UserMutationResult::Added);

    const UsmRequest old_request =
        requestFor(engine, generation_one, 0, "pre-rekey");
    CHECK(engine.verify(old_request).accepted());

    UsmUser generation_two = generation_one;
    generation_two.key_generation = 2;
    generation_two.auth_protocol = AuthProtocol::HmacSha256;
    generation_two.priv_protocol = PrivProtocol::Aes256;
    generation_two.auth_key = testKey(32, 0x44);
    generation_two.privacy_key = testKey(32, 0xa2);
    CHECK(engine.updateUser(generation_two) == UserMutationResult::Updated);

    const ReplayResult old_generation = engine.verify(old_request);
    CHECK(!old_generation.accepted());
    CHECK(old_generation.reason == ReplayReason::InvalidSecurityParameters);
    CHECK(engine.statistics().key_generation_rejections == 1u);

    UsmRequest forged;
    forged.authoritative_engine_id = engine.snapshot().engine_id;
    forged.boots = engine.snapshot().boots;
    forged.engine_time = engine.snapshot().engine_time;
    forged.user_name = generation_one.name;
    forged.key_generation = 1;
    forged.auth_protocol = AuthProtocol::HmacSha256;
    forged.priv_protocol = PrivProtocol::Aes256;
    forged.message = bytes("old-key-new-generation");
    forged.authentication_parameters = makeAuthenticator(
        AuthProtocol::HmacSha256,
        generation_one.auth_key,
        forged.message);
    CHECK(!engine.verify(forged).accepted());

    CHECK(engine.verify(
              requestFor(engine, generation_two, 0, "post-rekey"))
              .accepted());

    UsmRequest wrong_privacy = requestFor(
        engine, generation_two, 1, "wrong-privacy-algorithm");
    wrong_privacy.priv_protocol = PrivProtocol::Aes128;
    wrong_privacy.authentication_parameters = makeAuthenticator(
        generation_two.auth_protocol,
        generation_two.auth_key,
        wrong_privacy.message);
    CHECK(!engine.verify(wrong_privacy).accepted());

    const UsmUser stale = generation_two;
    CHECK(engine.updateUser(stale) ==
          UserMutationResult::StaleKeyGeneration);
    return true;
}

bool testReplayAndOpaqueErrors() {
    FakeClock clock;
    ReplayEngine engine(testConfig(6), {}, clock.function());
    UsmUser user = testUser();
    CHECK(engine.addUser(user) == UserMutationResult::Added);

    const UsmRequest original =
        requestFor(engine, user, 0, "same-pdu-id");
    CHECK(engine.verify(original).accepted());
    EngineConfig tightened = testConfig(6);
    tightened.time_window_seconds = 100;
    tightened.replay_capacity = 1;
    CHECK(engine.restoreConfiguration(tightened) ==
          RestoreResult::Unchanged);
    const ReplayResult replay = engine.verify(original);
    CHECK(!replay.accepted());
    CHECK(replay.reason == ReplayReason::ReplayedPdu);
    CHECK(replay.diagnostic() == "request rejected");
    CHECK(engine.statistics().replay_rejections == 1u);

    UsmRequest bad_engine = requestFor(engine, user, 1, "bad-engine-id");
    bad_engine.authoritative_engine_id[0] ^= 0xffu;
    const ReplayResult engine_error = engine.verify(bad_engine);
    CHECK(!engine_error.accepted());
    CHECK(engine_error.reason == ReplayReason::InvalidSecurityParameters);
    CHECK(engine_error.diagnostic() == "request rejected");

    UsmRequest unknown_user = requestFor(engine, user, 2, "unknown-user");
    unknown_user.user_name = "not-present";
    const ReplayResult user_error = engine.verify(unknown_user);
    CHECK(!user_error.accepted());
    CHECK(user_error.reason == engine_error.reason);
    CHECK(user_error.diagnostic() == engine_error.diagnostic());

    UsmRequest bad_auth = requestFor(engine, user, 3, "bad-authenticator");
    bad_auth.authentication_parameters[0] ^= 0x01u;
    CHECK(!engine.verify(bad_auth).accepted());
    CHECK(engine.statistics().engine_id_rejections == 1u);
    CHECK(engine.statistics().authentication_rejections == 2u);
    return true;
}

bool testWindowCapacityFailsClosed() {
    SlidingReplayWindow window(10, 2);
    CHECK(window.accept(1, 100, 100, bytes("a")) ==
          WindowResult::Accepted);
    CHECK(window.accept(1, 99, 100, bytes("b")) ==
          WindowResult::Accepted);
    CHECK(window.accept(1, 101, 100, bytes("c")) ==
          WindowResult::Full);
    CHECK(window.accept(1, 120, 120, bytes("d")) ==
          WindowResult::Accepted);
    CHECK(window.accept(1, 100, 120, bytes("a")) ==
          WindowResult::OutsideTimeWindow);
    return true;
}

void run(const char* name, bool (*test)()) {
    try {
        if (!test()) {
            ++failures;
            std::cerr << "FAILED: " << name << '\n';
        } else {
            std::cout << "PASSED: " << name << '\n';
        }
    } catch (const std::exception& exception) {
        ++failures;
        std::cerr << "FAILED: " << name << ": " << exception.what() << '\n';
    }
}

}  // namespace

int main() {
    run("hash and HMAC vectors", testHashAndHmacVectors);
    run("boots rollback and resynchronization",
        testBootsRollbackAndResynchronization);
    run("monotonic restore and persistence",
        testMonotonicRestoreAndPersistence);
    run("clock skew and window ordering",
        testClockSkewAndWindowOrdering);
    run("key rotation and algorithm binding",
        testKeyRotationAndAlgorithmBinding);
    run("replay and opaque errors", testReplayAndOpaqueErrors);
    run("window capacity", testWindowCapacityFailsClosed);

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    return 0;
}

#include "netsec.hpp"

#include <cstdlib>
#include <iostream>
#include <span>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

void expect(bool condition, std::string_view expression, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) expect((expression), #expression, __LINE__)

[[nodiscard]] std::span<const std::byte> bytes(std::string_view text) {
    return std::as_bytes(std::span<const char>(text.data(), text.size()));
}

[[nodiscard]] netsec::CycleFrame frame(
    std::uint64_t cycle,
    std::string_view input,
    std::uint64_t sync_epoch = 7,
    std::uint64_t sync_counter = 0) {
    return netsec::CycleFrame{
        cycle,
        sync_epoch,
        sync_counter == 0 ? cycle : sync_counter,
        bytes(input),
    };
}

void test_sha256_vectors() {
    const netsec::Digest empty = netsec::sha256("");
    const netsec::Digest abc = netsec::sha256("abc");
    const netsec::Digest block_boundary = netsec::sha256(
        "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq");
    const netsec::Digest two_blocks = netsec::sha256(
        "abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmn"
        "hijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu");
    CHECK(empty.to_hex() ==
          "e3b0c44298fc1c149afbf4c8996fb924"
          "27ae41e4649b934ca495991b7852b855");
    CHECK(abc.to_hex() ==
          "ba7816bf8f01cfea414140de5dae2223"
          "b00361a396177a9cb410ff61f20015ad");
    CHECK(block_boundary.to_hex() ==
          "248d6a61d20638b8e5c026930c3e6039"
          "a33ce45964ff2167f6ecedd419db06c1");
    CHECK(two_blocks.to_hex() ==
          "cf5b16a778af8380036ce59e7b049237"
          "0b249b11e8f07a51afac45037afee9d1");
}

void test_legitimate_pair_actuates() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(41, "torque=12; mode=hold");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("hold:12"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("hold:12"));

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::Actuate);
    CHECK(result.reason == netsec::StopReason::None);
    CHECK(result.actuator_authorized());
    CHECK(!result.is_isolated(netsec::ChannelId::A));
    CHECK(!result.is_isolated(netsec::ChannelId::B));
    CHECK(result.output == std::vector<std::byte>(bytes("hold:12").begin(),
                                                  bytes("hold:12").end()));
}

void test_identity_is_derived_not_argument_order() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(42, "position=5");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:5"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("move:5"));

    const netsec::VoteDecision result = voter.vote(current, b, a);
    CHECK(result.actuator_authorized());
    CHECK(result.output == std::vector<std::byte>(bytes("move:5").begin(),
                                                  bytes("move:5").end()));
}

void test_single_channel_input_tamper_stops_and_isolates_channel() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(43, "sensor=normal");
    netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("open-valve"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("open-valve"));
    a.input_digest.bytes[0] ^= 0x01U;

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::InputMismatch);
    CHECK(!result.actuator_authorized());
    CHECK(result.output.empty());
    CHECK(result.is_isolated(netsec::ChannelId::A));
    CHECK(!result.is_isolated(netsec::ChannelId::B));
    CHECK(voter.is_isolated(netsec::ChannelId::A));
    CHECK(!voter.is_isolated(netsec::ChannelId::B));
}

void test_single_channel_result_tamper_never_selects_a_winner() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(44, "command=close");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("actuator=close"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("actuator=open"));

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::ResultMismatch);
    CHECK(!result.actuator_authorized());
    CHECK(result.output.empty());
    CHECK(result.is_isolated(netsec::ChannelId::A));
    CHECK(result.is_isolated(netsec::ChannelId::B));
}

void test_result_digest_tamper_is_attributed_to_one_channel() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(45, "command=hold");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("actuator=hold"));
    netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("actuator=hold"));
    b.result_digest.bytes[31] ^= 0x80U;

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::ResultDigestMismatch);
    CHECK(!result.actuator_authorized());
    CHECK(!result.is_isolated(netsec::ChannelId::A));
    CHECK(result.is_isolated(netsec::ChannelId::B));
}

void test_sync_counter_drift_stops_and_isolates_drifted_channel() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(46, "sensor=9", 3, 46);
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:9"));
    netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("move:9"));
    b.sync_counter = 47;

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::SyncMismatch);
    CHECK(!result.actuator_authorized());
    CHECK(!result.is_isolated(netsec::ChannelId::A));
    CHECK(result.is_isolated(netsec::ChannelId::B));
}

void test_sync_epoch_mismatch_and_bad_sync_state_stop() {
    {
        netsec::VotingComponent voter;
        const netsec::CycleFrame current = frame(47, "sensor=3", 5, 47);
        const netsec::ChannelReport a =
            netsec::make_report(current, netsec::ChannelId::A, bytes("move:3"));
        netsec::ChannelReport b =
            netsec::make_report(current, netsec::ChannelId::B, bytes("move:3"));
        b.sync_epoch = 6;
        const netsec::VoteDecision result = voter.vote(current, a, b);
        CHECK(result.decision == netsec::Decision::SafeStop);
        CHECK(result.reason == netsec::StopReason::SyncMismatch);
        CHECK(result.is_isolated(netsec::ChannelId::B));
    }

    {
        netsec::VotingComponent voter;
        const netsec::CycleFrame current = frame(48, "sensor=3", 5, 48);
        netsec::ChannelReport a =
            netsec::make_report(current, netsec::ChannelId::A, bytes("move:3"));
        const netsec::ChannelReport b =
            netsec::make_report(current, netsec::ChannelId::B, bytes("move:3"));
        a.sync = netsec::SyncState::Drifting;
        const netsec::VoteDecision result = voter.vote(current, a, b);
        CHECK(result.decision == netsec::Decision::SafeStop);
        CHECK(result.reason == netsec::StopReason::SyncStateInvalid);
        CHECK(result.is_isolated(netsec::ChannelId::A));
        CHECK(!result.is_isolated(netsec::ChannelId::B));
    }
}

void test_cycle_mismatch_stops_from_channel() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(49, "sensor=4");
    netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:4"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("move:4"));
    a.cycle = 50;

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::CycleMismatch);
    CHECK(result.is_isolated(netsec::ChannelId::A));
    CHECK(!result.is_isolated(netsec::ChannelId::B));
}

void test_duplicate_identity_is_rejected() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(51, "sensor=5");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:5"));

    const netsec::VoteDecision result = voter.vote(current, a, a);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::DuplicateChannelIdentity);
    CHECK(result.is_isolated(netsec::ChannelId::A));
    CHECK(result.is_isolated(netsec::ChannelId::B));
}

void test_invalid_identity_is_rejected() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(51, "sensor=5");
    netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:5"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("move:5"));
    a.id = static_cast<netsec::ChannelId>(99);

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.decision == netsec::Decision::SafeStop);
    CHECK(result.reason == netsec::StopReason::InvalidChannelIdentity);
    CHECK(!result.actuator_authorized());
}

void test_replay_window_and_reset() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame current = frame(52, "sensor=6");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:6"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("move:6"));

    CHECK(voter.vote(current, a, b).actuator_authorized());

    const netsec::VoteDecision replay = voter.vote(current, a, b);
    CHECK(replay.decision == netsec::Decision::SafeStop);
    CHECK(replay.reason == netsec::StopReason::ReplayedCycle);
    CHECK(!replay.actuator_authorized());

    const netsec::CycleFrame next = frame(53, "sensor=6");
    const netsec::ChannelReport next_a =
        netsec::make_report(next, netsec::ChannelId::A, bytes("move:6"));
    const netsec::ChannelReport next_b =
        netsec::make_report(next, netsec::ChannelId::B, bytes("move:6"));
    CHECK(voter.vote(next, next_a, next_b).actuator_authorized());

    voter.reset();
    CHECK(voter.vote(current, a, b).actuator_authorized());
}

void test_isolation_is_latched_until_explicit_recovery() {
    netsec::VotingComponent voter;
    const netsec::CycleFrame bad_frame = frame(54, "sensor=6");
    netsec::ChannelReport bad_a =
        netsec::make_report(bad_frame, netsec::ChannelId::A, bytes("move:6"));
    const netsec::ChannelReport bad_b =
        netsec::make_report(bad_frame, netsec::ChannelId::B, bytes("move:6"));
    bad_a.input_digest.bytes[4] ^= 0xffU;

    CHECK(voter.vote(bad_frame, bad_a, bad_b).reason ==
          netsec::StopReason::InputMismatch);
    CHECK(voter.is_isolated(netsec::ChannelId::A));

    const netsec::CycleFrame repaired_frame = frame(55, "sensor=6");
    const netsec::ChannelReport repaired_a =
        netsec::make_report(repaired_frame, netsec::ChannelId::A, bytes("move:6"));
    const netsec::ChannelReport repaired_b =
        netsec::make_report(repaired_frame, netsec::ChannelId::B, bytes("move:6"));

    const netsec::VoteDecision blocked = voter.vote(repaired_frame, repaired_a,
                                                   repaired_b);
    CHECK(blocked.reason == netsec::StopReason::ChannelIsolated);
    CHECK(!blocked.actuator_authorized());

    CHECK(voter.clear_isolation(netsec::ChannelId::A));
    CHECK(!voter.is_isolated(netsec::ChannelId::A));
    const netsec::VoteDecision resumed =
        voter.vote(repaired_frame, repaired_a, repaired_b);
    CHECK(resumed.actuator_authorized());
}

void test_invalid_configuration_fails_closed() {
    netsec::VotingComponent voter(
        netsec::VoterConfig{netsec::ChannelId::A, netsec::ChannelId::A});
    const netsec::CycleFrame current = frame(56, "sensor=1");
    const netsec::ChannelReport a =
        netsec::make_report(current, netsec::ChannelId::A, bytes("move:1"));
    const netsec::ChannelReport b =
        netsec::make_report(current, netsec::ChannelId::B, bytes("move:1"));

    const netsec::VoteDecision result = voter.vote(current, a, b);
    CHECK(result.reason == netsec::StopReason::InvalidConfiguration);
    CHECK(!result.actuator_authorized());
    CHECK(result.is_isolated(netsec::ChannelId::A));
    CHECK(result.is_isolated(netsec::ChannelId::B));
    CHECK(!voter.clear_isolation(netsec::ChannelId::A));
}

} // namespace

int main() {
    test_sha256_vectors();
    test_legitimate_pair_actuates();
    test_identity_is_derived_not_argument_order();
    test_single_channel_input_tamper_stops_and_isolates_channel();
    test_single_channel_result_tamper_never_selects_a_winner();
    test_result_digest_tamper_is_attributed_to_one_channel();
    test_sync_counter_drift_stops_and_isolates_drifted_channel();
    test_sync_epoch_mismatch_and_bad_sync_state_stop();
    test_cycle_mismatch_stops_from_channel();
    test_duplicate_identity_is_rejected();
    test_invalid_identity_is_rejected();
    test_replay_window_and_reset();
    test_isolation_is_latched_until_explicit_recovery();
    test_invalid_configuration_fails_closed();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all voting tests passed\n";
    return EXIT_SUCCESS;
}

#include "netsec.hpp"

#include <cstdint>
#include <cstdlib>
#include <exception>
#include <iostream>
#include <span>
#include <string>
#include <vector>

namespace {

int failures = 0;
int checks = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        ++checks;                                                            \
        if (!(condition)) {                                                   \
            std::cerr << __FILE__ << ':' << __LINE__ << ": CHECK failed: "    \
                      << #condition << '\n';                                  \
            ++failures;                                                       \
        }                                                                     \
    } while (false)

#define CHECK_EQ(left, right)                                                \
    do {                                                                     \
        ++checks;                                                            \
        const auto& check_left = (left);                                      \
        const auto& check_right = (right);                                    \
        if (!(check_left == check_right)) {                                   \
            std::cerr << __FILE__ << ':' << __LINE__ << ": CHECK_EQ failed: " \
                      << #left << " == " << #right << " (" << check_left      \
                      << " != " << check_right << ")\n";                     \
            ++failures;                                                       \
        }                                                                     \
    } while (false)

using netsec::ByteSpan;
using netsec::Bytes;
using netsec::GapPolicy;
using netsec::Key;
using netsec::MediaPacket;
using netsec::Receiver;
using netsec::ReceiverConfig;
using netsec::ReorderPolicy;
using netsec::Sender;
using netsec::SenderConfig;
using netsec::VerifyStatus;

constexpr std::uint32_t kSession = 0x10203040U;
constexpr std::uint32_t kSsrc = 0xCAFEBABEU;

Key key_with(std::uint8_t seed) {
    Key key{};
    for (std::size_t index = 0; index < key.size(); ++index) {
        key[index] = static_cast<std::uint8_t>(seed + index * 7U);
    }
    return key;
}

SenderConfig sender_config(Key key = key_with(1U), std::uint16_t epoch = 3U) {
    SenderConfig config;
    config.session_id = kSession;
    config.ssrc = kSsrc;
    config.master_key = key;
    config.initial_epoch = epoch;
    return config;
}

ReceiverConfig receiver_config(Key key = key_with(1U),
                               std::uint16_t epoch = 3U) {
    ReceiverConfig config;
    config.session_id = kSession;
    config.ssrc = kSsrc;
    config.master_key = key;
    config.initial_epoch = epoch;
    return config;
}

Bytes payload_for(std::uint16_t sequence) {
    Bytes payload(24U);
    for (std::size_t index = 0; index < payload.size(); ++index) {
        payload[index] = static_cast<std::uint8_t>(sequence + index);
    }
    return payload;
}

Bytes seal(Sender& sender, std::uint16_t sequence, std::uint32_t timestamp,
           const Bytes& payload) {
    return sender.seal(sequence, timestamp, ByteSpan(payload));
}

void test_legitimate_stream() {
    Sender sender(sender_config());
    Receiver receiver(receiver_config());
    for (std::uint16_t index = 0; index < 20U; ++index) {
        const std::uint16_t sequence = static_cast<std::uint16_t>(1000U + index);
        const std::uint32_t timestamp = 90000U + index * 960U;
        const Bytes payload = payload_for(sequence);
        const Bytes wire = seal(sender, sequence, timestamp, payload);
        MediaPacket packet;
        const auto result = receiver.receive(wire, packet);
        CHECK_EQ(static_cast<int>(result.status),
                 static_cast<int>(VerifyStatus::accepted));
        CHECK_EQ(packet.session_id, kSession);
        CHECK_EQ(packet.ssrc, kSsrc);
        CHECK_EQ(packet.sequence, sequence);
        CHECK_EQ(packet.timestamp, timestamp);
        CHECK_EQ(packet.key_epoch, 3U);
        CHECK(packet.payload == payload);
    }
    CHECK_EQ(receiver.stats().accepted, 20U);
    CHECK_EQ(receiver.stats().reordered, 0U);

    MediaPacket empty_packet;
    CHECK(receiver
              .receive(sender.seal(2000U, 424242U, ByteSpan{}), empty_packet)
              .accepted());
    CHECK(empty_packet.payload.empty());
}

void test_tampering_and_wrong_source() {
    Sender sender(sender_config());
    Receiver receiver(receiver_config());
    const Bytes payload = payload_for(7U);
    const Bytes wire = seal(sender, 7U, 777U, payload);

    for (std::size_t offset :
         {std::size_t{0U}, std::size_t{12U}, std::size_t{16U},
          std::size_t{18U}, std::size_t{26U}, wire.size() - 1U}) {
        Bytes modified = wire;
        modified[offset] ^= 0x01U;
        MediaPacket packet;
        const auto result = receiver.receive(modified, packet);
        CHECK(result.rejected());
    }
    CHECK_EQ(static_cast<int>(receiver.receive(ByteSpan{}).status),
             static_cast<int>(VerifyStatus::rejected_malformed));
    CHECK_EQ(static_cast<int>(
                 receiver.receive(ByteSpan(wire.data(), wire.size() - 1U))
                     .status),
             static_cast<int>(VerifyStatus::rejected_malformed));
    Bytes trailing = wire;
    trailing.push_back(0U);
    CHECK_EQ(static_cast<int>(receiver.receive(trailing).status),
             static_cast<int>(VerifyStatus::rejected_malformed));

    // A different master key cannot produce media for this session.
    Sender attacker(sender_config(key_with(99U)));
    const Bytes injected = seal(attacker, 500U, 999U, payload);
    MediaPacket packet;
    CHECK_EQ(static_cast<int>(receiver.receive(injected, packet).status),
             static_cast<int>(VerifyStatus::rejected_authentication));

    // The failed injection did not poison replay state.
    CHECK(receiver.receive(wire).accepted());

    // A packet claiming another SSRC is rejected even if it is otherwise
    // well-formed. Changing the SSRC invalidates the session-bound tag.
    Bytes spoofed = wire;
    spoofed[12] ^= 0x80U;
    CHECK_EQ(static_cast<int>(receiver.receive(spoofed).status),
             static_cast<int>(VerifyStatus::rejected_ssrc));

    // A packet from another authorized session with the same master key is
    // rejected before it can switch the player to a different stream.
    SenderConfig other_config = sender_config();
    other_config.session_id ^= 1U;
    Sender other_session(other_config);
    CHECK_EQ(static_cast<int>(receiver.receive(
                                  seal(other_session, 8U, 778U, payload))
                                  .status),
             static_cast<int>(VerifyStatus::rejected_session));

    SenderConfig other_ssrc_config = sender_config();
    other_ssrc_config.ssrc ^= 1U;
    Sender other_ssrc(other_ssrc_config);
    CHECK_EQ(static_cast<int>(receiver.receive(
                                  seal(other_ssrc, 9U, 779U, payload))
                                  .status),
             static_cast<int>(VerifyStatus::rejected_ssrc));
}

void test_forged_high_sequence_does_not_initialize_state() {
    Sender attacker(sender_config(key_with(42U)));
    Receiver receiver(receiver_config());
    const Bytes forged =
        seal(attacker, 60000U, 123456U, payload_for(60000U));
    CHECK_EQ(static_cast<int>(receiver.receive(forged).status),
             static_cast<int>(VerifyStatus::rejected_authentication));

    Sender sender(sender_config());
    // A low legitimate sequence must still be treated as the first packet.
    const Bytes legitimate = seal(sender, 1U, 100U, payload_for(1U));
    CHECK(receiver.receive(legitimate).accepted());
}

void test_reordering_duplicates_and_replay_window() {
    Sender sender(sender_config());
    ReceiverConfig config = receiver_config();
    config.replay.window = 8U;
    config.replay.max_forward_gap = 32U;
    Receiver receiver(config);

    CHECK(receiver.receive(seal(sender, 100U, 1000U, payload_for(100U)))
              .accepted());
    CHECK(receiver.receive(seal(sender, 102U, 1020U, payload_for(102U)))
              .accepted());
    const auto reordered =
        receiver.receive(seal(sender, 101U, 1010U, payload_for(101U)));
    CHECK_EQ(static_cast<int>(reordered.status),
             static_cast<int>(VerifyStatus::accepted_reordered));
    CHECK_EQ(receiver.stats().reordered, 1U);

    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 102U, 1020U, payload_for(102U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_duplicate));
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 101U, 1010U, payload_for(101U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_duplicate));

    for (std::uint16_t sequence = 103U; sequence <= 110U; ++sequence) {
        CHECK(receiver
                  .receive(seal(sender, sequence, sequence * 10U,
                                payload_for(sequence)))
                  .accepted());
    }
    // 101 is now older than the eight-entry replay window.
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 101U, 1010U, payload_for(101U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_rollback));

    CHECK_EQ(receiver.stats().duplicates, 2U);
}

void test_forward_gap_policies() {
    Sender sender(sender_config());
    ReceiverConfig strict_config = receiver_config();
    strict_config.replay.window = 32U;
    strict_config.replay.max_forward_gap = 16U;
    strict_config.replay.gap = GapPolicy::reject;
    Receiver strict_receiver(strict_config);

    CHECK(strict_receiver
              .receive(seal(sender, 100U, 1000U, payload_for(100U)))
              .accepted());
    CHECK_EQ(static_cast<int>(
                 strict_receiver
                     .receive(seal(sender, 200U, 2000U, payload_for(200U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_forward_gap));
    // The rejected jump did not move the replay window.
    CHECK(strict_receiver
              .receive(seal(sender, 101U, 1010U, payload_for(101U)))
              .accepted());

    ReceiverConfig resync_config = strict_config;
    resync_config.replay.gap = GapPolicy::resync;
    Receiver resync_receiver(resync_config);
    CHECK(resync_receiver
              .receive(seal(sender, 10U, 100U, payload_for(10U)))
              .accepted());
    CHECK(resync_receiver
              .receive(seal(sender, 3000U, 30000U, payload_for(10U)))
              .accepted());
    CHECK_EQ(static_cast<int>(
                 resync_receiver
                     .receive(seal(sender, 1U, 10U, payload_for(1U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_rollback));
}

void test_sequence_wrap_and_rollover() {
    Sender sender(sender_config());
    Receiver receiver(receiver_config());
    for (std::uint16_t sequence : {65534U, 65535U, 0U, 1U, 2U}) {
        CHECK(receiver.receive(seal(sender, sequence, sequence * 10U,
                                    payload_for(sequence)))
                  .accepted());
    }
}

void test_reorder_policy_reject() {
    Sender sender(sender_config());
    ReceiverConfig config = receiver_config();
    config.replay.reorder = ReorderPolicy::reject;
    Receiver receiver(config);
    CHECK(receiver.receive(seal(sender, 10U, 10U, payload_for(10U)))
              .accepted());
    CHECK(receiver.receive(seal(sender, 12U, 12U, payload_for(12U)))
              .accepted());
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 11U, 11U, payload_for(11U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_rollback));
}

void test_replay_window_boundaries() {
    Sender sender(sender_config());
    ReceiverConfig config = receiver_config();
    config.replay.window = 65U;
    config.replay.max_forward_gap = 128U;
    Receiver receiver(config);

    // Leave holes at 6, 20 and 64. Sequence 6 is exactly 64 behind the highest
    // accepted value and must still fit; sequence 5 is one outside the window.
    for (std::uint16_t sequence = 0U; sequence <= 70U; ++sequence) {
        if (sequence == 6U || sequence == 20U || sequence == 64U) {
            continue;
        }
        CHECK(receiver
                  .receive(seal(sender, sequence, sequence * 10U,
                                payload_for(sequence)))
                  .accepted());
    }
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 6U, 60U, payload_for(6U))).status),
             static_cast<int>(VerifyStatus::accepted_reordered));
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 20U, 200U, payload_for(20U)))
                     .status),
             static_cast<int>(VerifyStatus::accepted_reordered));
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 64U, 640U, payload_for(64U)))
                     .status),
             static_cast<int>(VerifyStatus::accepted_reordered));
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(sender, 5U, 50U, payload_for(5U))).status),
             static_cast<int>(VerifyStatus::rejected_rollback));

    ReceiverConfig one_bit_config = receiver_config();
    one_bit_config.replay.window = 1U;
    Receiver one_bit_receiver(one_bit_config);
    CHECK(one_bit_receiver
              .receive(seal(sender, 500U, 500U, payload_for(10U)))
              .accepted());
    CHECK(one_bit_receiver
              .receive(seal(sender, 501U, 501U, payload_for(11U)))
              .accepted());
    CHECK_EQ(static_cast<int>(
                 one_bit_receiver
                     .receive(seal(sender, 500U, 500U, payload_for(10U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_rollback));
}

void test_key_rotation_and_expiry() {
    Sender sender(sender_config(key_with(1U), 5U));
    ReceiverConfig config = receiver_config(key_with(1U), 5U);
    config.retained_epochs = 2U;
    config.max_epoch_advance = 2U;
    Receiver receiver(config);

    CHECK(receiver.receive(seal(sender, 100U, 100U, payload_for(100U)))
              .accepted());

    CHECK(sender.rotate_key());
    CHECK_EQ(sender.key_epoch(), 6U);
    CHECK(receiver.receive(seal(sender, 101U, 101U, payload_for(101U)))
              .accepted());
    CHECK_EQ(receiver.active_epoch(), 6U);

    // Packets protected with the previous epoch are still accepted inside the
    // configured overlap, and a replayed old packet remains a duplicate.
    Sender old_epoch_sender(sender_config(key_with(1U), 5U));
    Bytes old_wire;
    for (std::uint16_t sequence = 100U; sequence <= 102U; ++sequence) {
        old_wire = seal(old_epoch_sender, sequence, sequence, payload_for(sequence));
    }
    CHECK(receiver.receive(old_wire).accepted());
    CHECK_EQ(static_cast<int>(receiver.receive(
                                  seal(old_epoch_sender, 100U, 100U,
                                       payload_for(100U)))
                                  .status),
             static_cast<int>(VerifyStatus::rejected_duplicate));

    CHECK(sender.rotate_key());  // epoch 7
    CHECK(sender.rotate_key());  // epoch 8
    Sender epoch8_sender(sender_config(key_with(1U), 8U));
    CHECK(receiver.receive(seal(epoch8_sender, 103U, 103U, payload_for(103U)))
              .accepted());
    CHECK_EQ(receiver.active_epoch(), 8U);

    // Epoch 5 is now outside the two-epoch retention window.
    Sender expired_sender(sender_config(key_with(1U), 5U));
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(expired_sender, 104U, 104U,
                                       payload_for(104U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_expired_epoch));
}

void test_epoch_advance_limit() {
    ReceiverConfig config = receiver_config(key_with(1U), 3U);
    config.max_epoch_advance = 1U;
    Receiver receiver(config);

    Sender too_far(sender_config(key_with(1U), 5U));
    CHECK_EQ(static_cast<int>(receiver.receive(
                                  seal(too_far, 50U, 50U, payload_for(50U)))
                                  .status),
             static_cast<int>(VerifyStatus::rejected_unknown_epoch));
    CHECK_EQ(receiver.active_epoch(), 3U);

    Sender next_epoch(sender_config(key_with(1U), 4U));
    CHECK(receiver.receive(seal(next_epoch, 51U, 51U, payload_for(51U)))
              .accepted());
    CHECK_EQ(receiver.active_epoch(), 4U);
}

void test_rekey_with_new_master_key() {
    Sender sender(sender_config(key_with(1U), 20U));
    ReceiverConfig config = receiver_config(key_with(1U), 20U);
    config.max_epoch_advance = 1U;
    Receiver receiver(config);

    CHECK(receiver.receive(seal(sender, 1U, 1U, payload_for(1U))).accepted());
    Key new_key = key_with(77U);
    CHECK(sender.set_master_key(new_key, 21U));
    CHECK(receiver.set_master_key(new_key, 21U));
    // The prior master key remains usable for the retained transition epoch.
    Sender old_key_sender(sender_config(key_with(1U), 20U));
    CHECK(receiver.receive(seal(old_key_sender, 2U, 2U, payload_for(2U)))
              .accepted());
    CHECK(receiver.receive(seal(sender, 3U, 3U, payload_for(3U))).accepted());
    CHECK_EQ(receiver.active_epoch(), 21U);

    // The old master key no longer authenticates epoch 21.
    Sender stale_key_sender(sender_config(key_with(1U), 21U));
    CHECK_EQ(static_cast<int>(receiver.receive(
                                  seal(stale_key_sender, 4U, 4U, payload_for(4U)))
                                  .status),
             static_cast<int>(VerifyStatus::rejected_authentication));
}

void test_key_lifetime_limit() {
    SenderConfig config = sender_config();
    config.max_packets_per_epoch = 2U;
    Sender sender(config);
    Bytes first;
    Bytes second;
    CHECK(sender.try_seal(1U, 1U, ByteSpan(payload_for(1U)), first));
    CHECK(sender.try_seal(2U, 2U, ByteSpan(payload_for(2U)), second));
    Bytes rejected;
    CHECK(!sender.try_seal(3U, 3U, ByteSpan(payload_for(3U)), rejected));
    CHECK(rejected.empty());

    ReceiverConfig receiver_cfg = receiver_config();
    receiver_cfg.max_packets_per_epoch = 2U;
    Receiver receiver(receiver_cfg);
    CHECK(receiver.receive(first).accepted());
    CHECK(receiver.receive(second).accepted());
    Sender another(sender_config());
    CHECK_EQ(static_cast<int>(
                 receiver.receive(seal(another, 3U, 3U, payload_for(3U)))
                     .status),
             static_cast<int>(VerifyStatus::rejected_key_exhausted));
}

void test_configuration_bounds() {
    bool threw = false;
    try {
        ReceiverConfig config = receiver_config();
        config.replay.window = 0U;
        Receiver receiver(config);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        ReceiverConfig config = receiver_config();
        config.replay.window = netsec::kMaxReplayWindow + 1U;
        Receiver receiver(config);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        ReceiverConfig config = receiver_config();
        config.retained_epochs = netsec::kMaxRetainedEpochs + 1U;
        Receiver receiver(config);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        SenderConfig config = sender_config();
        config.max_packets_per_epoch = netsec::kMaxPacketsPerEpoch + 1U;
        Sender sender(config);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);
}

}  // namespace

int main() {
    try {
        test_legitimate_stream();
        test_tampering_and_wrong_source();
        test_forged_high_sequence_does_not_initialize_state();
        test_reordering_duplicates_and_replay_window();
        test_forward_gap_policies();
        test_sequence_wrap_and_rollover();
        test_reorder_policy_reject();
        test_replay_window_boundaries();
        test_key_rotation_and_expiry();
        test_epoch_advance_limit();
        test_rekey_with_new_master_key();
        test_key_lifetime_limit();
        test_configuration_bounds();
    } catch (const std::exception& error) {
        std::cerr << "unexpected exception: " << error.what() << '\n';
        return 2;
    }

    std::cout << checks << " checks, " << failures << " failures\n";
    return failures == 0 ? 0 : 1;
}

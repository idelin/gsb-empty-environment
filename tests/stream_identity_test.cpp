#include "netsec.hpp"

#include <cstdint>
#include <functional>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

using netsec::Alert;
using netsec::AlertCode;
using netsec::Endpoint;
using netsec::IngestStatus;
using netsec::KeyMaterial;
using netsec::MediaPacket;
using netsec::RegisterStatus;
using netsec::StreamBinding;
using netsec::StreamId;
using netsec::StreamRegistry;

int failures = 0;

void check(bool condition, std::string_view expression, std::string_view test,
           int line) {
  if (!condition) {
    ++failures;
    std::cerr << "FAIL " << test << ':' << line << ": " << expression << '\n';
  }
}

#define CHECK(condition) check((condition), #condition, __func__, __LINE__)

KeyMaterial key(std::initializer_list<std::uint8_t> bytes) {
  return KeyMaterial(bytes);
}

Endpoint endpoint(std::string address, std::uint16_t port) {
  return Endpoint{std::move(address), port};
}

StreamBinding binding(std::string session, std::uint32_t ssrc,
                      std::string address, std::uint16_t port,
                      std::uint8_t payload_type, const KeyMaterial& key_material,
                      std::uint32_t clock_rate = 90000) {
  return StreamBinding{std::move(session), ssrc, endpoint(std::move(address), port),
                       payload_type, key_material, clock_rate};
}

MediaPacket packet(const StreamBinding& stream, std::uint16_t sequence,
                   std::uint32_t timestamp, std::string_view payload) {
  MediaPacket value;
  value.session_id = stream.session_id;
  value.ssrc = stream.ssrc;
  value.source = stream.source;
  value.payload_type = stream.payload_type;
  value.sequence_number = sequence;
  value.timestamp = timestamp;
  value.payload.assign(payload.begin(), payload.end());
  value.key_material = stream.key_material;
  return value;
}

MediaPacket packet_from(const StreamBinding& stream, const Endpoint& source,
                        std::uint16_t sequence, std::uint32_t timestamp,
                        std::string_view payload,
                        const KeyMaterial& key_material) {
  MediaPacket value = packet(stream, sequence, timestamp, payload);
  value.source = source;
  value.key_material = key_material;
  return value;
}

std::vector<std::string> payloads(const std::vector<netsec::BufferedPacket>& in) {
  std::vector<std::string> out;
  out.reserve(in.size());
  for (const auto& item : in) {
    out.emplace_back(item.payload.begin(), item.payload.end());
  }
  return out;
}

void test_ssrc_source_conflict_is_isolated_and_alerted() {
  StreamRegistry registry;
  std::vector<Alert> alerts;
  registry.set_alert_sink([&alerts](const Alert& alert) { alerts.push_back(alert); });

  const auto secret = key({0x10, 0x20, 0x30, 0x40});
  const auto victim = binding("session-a", 0x11223344u, "10.0.0.10", 5000, 96,
                              secret);
  const auto victim_id = registry.register_stream(victim);
  CHECK(victim_id.status == RegisterStatus::inserted);

  CHECK(registry.ingest(packet(victim, 10, 90000, "victim-1")).accepted());

  const auto attacker_source = endpoint("10.0.0.66", 6000);
  auto attack = packet_from(victim, attacker_source, 11, 99000, "attacker-1",
                            secret);
  const auto attack_result = registry.ingest(attack);
  CHECK(attack_result.status == IngestStatus::ssrc_conflict);
  CHECK(attack_result.quarantined);
  CHECK(attack_result.alert_raised);
  CHECK(attack_result.stream_id == victim_id.stream_id);
  CHECK(alerts.size() == 1);
  CHECK(alerts[0].code == AlertCode::ssrc_source_conflict);
  CHECK(alerts[0].stream_id == victim_id.stream_id);
  CHECK(registry.is_quarantined("session-a", 0x11223344u, attacker_source));

  const auto repeated = registry.ingest(
      packet_from(victim, attacker_source, 12, 108000, "attacker-2", secret));
  CHECK(repeated.status == IngestStatus::ssrc_conflict);
  CHECK(!repeated.alert_raised);
  CHECK(alerts.size() == 1);

  const auto quarantined = registry.quarantined_sources();
  CHECK(quarantined.size() == 1);
  CHECK(quarantined[0].packet_count == 2);
  CHECK(quarantined[0].source == attacker_source);

  const auto victim_stats = registry.stats(victim_id.stream_id);
  CHECK(victim_stats.has_value());
  CHECK(victim_stats->received == 1);
  CHECK(victim_stats->accepted == 1);
  CHECK(victim_stats->sequence_replays == 0);
  CHECK(victim_stats->sequence_jumps == 0);
  CHECK(registry.buffered_count(victim_id.stream_id) == 1);

  CHECK(registry.ingest(packet(victim, 11, 108000, "victim-2")).accepted());
  const auto mixed = payloads(registry.drain(victim_id.stream_id));
  CHECK((mixed == std::vector<std::string>{"victim-1", "victim-2"}));
}

void test_source_change_does_not_migrate_stream() {
  StreamRegistry registry;
  const auto secret = key({0xaa, 0xbb, 0xcc});
  const auto stream = binding("session-b", 77, "192.0.2.5", 40000, 111, secret);
  const auto id = registry.register_stream(stream);
  CHECK(id.status == RegisterStatus::inserted);

  CHECK(registry.ingest(packet(stream, 1, 1000, "from-a-1")).accepted());

  auto same_ip_new_port =
      packet_from(stream, endpoint("192.0.2.5", 40001), 2, 2000, "spoofed",
                  secret);
  const auto changed = registry.ingest(same_ip_new_port);
  CHECK(changed.status == IngestStatus::ssrc_conflict);
  CHECK(changed.quarantined);
  CHECK(changed.alert_raised);
  CHECK(registry.buffered_count(id.stream_id) == 1);

  const auto victim_stats = registry.stats(id.stream_id);
  CHECK(victim_stats->received == 1);
  CHECK(victim_stats->accepted == 1);
  CHECK(victim_stats->highest_sequence == 1);
  CHECK(victim_stats->highest_timestamp == 1000);

  CHECK(registry.ingest(packet(stream, 2, 2000, "from-a-2")).accepted());
  const auto delivered = payloads(registry.drain(id.stream_id));
  CHECK((delivered == std::vector<std::string>{"from-a-1", "from-a-2"}));
}

void test_sequence_replay_and_jumps_are_dropped() {
  StreamRegistry registry;
  const auto secret = key({1, 3, 5, 7});
  const auto stream = binding("session-c", 9001, "198.51.100.8", 32000, 97,
                              secret);
  const auto id = registry.register_stream(stream);

  CHECK(registry.ingest(packet(stream, 100, 100000, "a")).accepted());
  CHECK(registry.ingest(packet(stream, 101, 101000, "b")).accepted());
  CHECK(registry.ingest(packet(stream, 102, 102000, "c")).accepted());
  CHECK(registry.buffered_count(id.stream_id) == 3);

  const auto replay = registry.ingest(packet(stream, 101, 101000, "replayed"));
  CHECK(replay.status == IngestStatus::sequence_replay);
  CHECK(!replay.accepted());
  CHECK(registry.buffered_count(id.stream_id) == 3);

  const auto sequence_jump =
      registry.ingest(packet(stream, 50000, 103000, "sequence-jump"));
  CHECK(sequence_jump.status == IngestStatus::sequence_jump);
  CHECK(registry.buffered_count(id.stream_id) == 3);

  const auto timestamp_jump = registry.ingest(
      packet(stream, 103, 102000 + 11 * 90000, "timestamp-jump"));
  CHECK(timestamp_jump.status == IngestStatus::timestamp_jump);
  CHECK(registry.buffered_count(id.stream_id) == 3);

  CHECK(registry.ingest(packet(stream, 103, 103000, "d")).accepted());
  CHECK(registry.ingest(packet(stream, 99, 99000, "pre")).accepted());

  const auto stats = registry.stats(id.stream_id);
  CHECK(stats->sequence_replays == 1);
  CHECK(stats->sequence_jumps == 1);
  CHECK(stats->timestamp_jumps == 1);
  CHECK(stats->out_of_order == 1);
  CHECK(stats->accepted == 5);
  CHECK(stats->received == 8);
  CHECK(stats->highest_sequence == 103);

  const auto delivered = payloads(registry.drain(id.stream_id));
  CHECK((delivered ==
         std::vector<std::string>{"pre", "a", "b", "c", "d"}));
}

void test_session_switch_gets_independent_state() {
  StreamRegistry registry;
  const auto first_key = key({0x01, 0x02});
  const auto second_key = key({0x03, 0x04});
  const auto first =
      binding("session-old", 4242, "203.0.113.9", 5004, 0, first_key);
  const auto second =
      binding("session-new", 4242, "203.0.113.9", 5004, 0, second_key);

  const auto first_id = registry.register_stream(first);
  const auto second_id = registry.register_stream(second);
  CHECK(first_id.status == RegisterStatus::inserted);
  CHECK(second_id.status == RegisterStatus::inserted);
  CHECK(first_id.stream_id != second_id.stream_id);

  CHECK(registry.ingest(packet(first, 1, 5000000, "old-1")).accepted());
  CHECK(registry.ingest(packet(second, 1, 700, "new-1")).accepted());
  CHECK(registry.ingest(packet(first, 2, 5001000, "old-2")).accepted());
  CHECK(registry.ingest(packet(second, 2, 1700, "new-2")).accepted());

  const auto wrong_key = packet_from(second, second.source, 3, 2700,
                                     "wrong-session-key", first_key);
  const auto mismatch = registry.ingest(wrong_key);
  CHECK(mismatch.status == IngestStatus::key_mismatch);

  CHECK(registry.stats(first_id.stream_id)->accepted == 2);
  CHECK(registry.stats(second_id.stream_id)->accepted == 2);
  CHECK(registry.stats(second_id.stream_id)->sequence_replays == 0);

  const auto old_payloads = payloads(registry.drain(first_id.stream_id));
  const auto new_payloads = payloads(registry.drain(second_id.stream_id));
  CHECK((old_payloads == std::vector<std::string>{"old-1", "old-2"}));
  CHECK((new_payloads == std::vector<std::string>{"new-1", "new-2"}));
}

void test_legitimate_multiple_streams_never_mix() {
  StreamRegistry registry;
  const auto key_a = key({0x11});
  const auto key_b = key({0x22});
  const auto key_c = key({0x33});

  const auto voice =
      binding("call-1", 1000, "10.1.1.1", 5000, 96, key_a);
  const auto video =
      binding("call-1", 2000, "10.1.1.2", 5002, 97, key_b);
  const auto other_call =
      binding("call-2", 1000, "10.1.1.1", 5000, 96, key_c);

  const auto voice_id = registry.register_stream(voice);
  const auto video_id = registry.register_stream(video);
  const auto other_id = registry.register_stream(other_call);
  CHECK(voice_id.ok());
  CHECK(video_id.ok());
  CHECK(other_id.ok());
  CHECK(registry.size() == 3);

  for (std::uint16_t sequence = 1; sequence <= 3; ++sequence) {
    const auto timestamp = static_cast<std::uint32_t>(sequence * 3000);
    const auto suffix = std::to_string(sequence);
    CHECK(registry
              .ingest(packet(voice, sequence, timestamp, "voice-" + suffix))
              .accepted());
    CHECK(registry
              .ingest(packet(video, sequence, timestamp, "video-" + suffix))
              .accepted());
    CHECK(registry
              .ingest(packet(other_call, sequence, timestamp, "other-" + suffix))
              .accepted());
  }

  const auto voice_payloads = payloads(registry.drain(voice_id.stream_id));
  const auto video_payloads = payloads(registry.drain(video_id.stream_id));
  const auto other_payloads = payloads(registry.drain(other_id.stream_id));

  CHECK((voice_payloads ==
         std::vector<std::string>{"voice-1", "voice-2", "voice-3"}));
  CHECK((video_payloads ==
         std::vector<std::string>{"video-1", "video-2", "video-3"}));
  CHECK((other_payloads ==
         std::vector<std::string>{"other-1", "other-2", "other-3"}));

  CHECK(registry.stats(voice_id.stream_id)->accepted == 3);
  CHECK(registry.stats(video_id.stream_id)->accepted == 3);
  CHECK(registry.stats(other_id.stream_id)->accepted == 3);
}

void test_registration_conflicts_and_key_binding() {
  StreamRegistry registry;
  std::vector<Alert> alerts;
  registry.set_alert_sink([&alerts](const Alert& alert) { alerts.push_back(alert); });

  const auto secret = key({0x55, 0x66});
  const auto original =
      binding("session-d", 5, "10.2.2.2", 6000, 100, secret);
  const auto id = registry.register_stream(original);
  CHECK(id.status == RegisterStatus::inserted);

  const auto same = registry.register_stream(original);
  CHECK(same.status == RegisterStatus::already_registered);
  CHECK(same.stream_id == id.stream_id);

  const auto other_source =
      binding("session-d", 5, "10.2.2.3", 6000, 100, secret);
  const auto source_conflict = registry.register_stream(other_source);
  CHECK(source_conflict.status == RegisterStatus::conflict);
  CHECK(source_conflict.alert_raised);
  CHECK(registry.size() == 1);

  const auto other_payload =
      binding("session-d", 5, "10.2.2.2", 6000, 101, secret);
  const auto payload_conflict = registry.register_stream(other_payload);
  CHECK(payload_conflict.status == RegisterStatus::conflict);
  CHECK(registry.size() == 1);

  const auto other_session =
      binding("session-e", 5, "10.2.2.2", 6000, 100, secret);
  const auto session_ok = registry.register_stream(other_session);
  CHECK(session_ok.status == RegisterStatus::inserted);
  CHECK(registry.size() == 2);

  auto bad_key =
      packet_from(original, original.source, 1, 1000, "unauthenticated",
                  key({0x99}));
  const auto bad_key_result = registry.ingest(bad_key);
  CHECK(bad_key_result.status == IngestStatus::key_mismatch);
  CHECK(registry.buffered_count(id.stream_id) == 0);
  CHECK(registry.stats(id.stream_id)->received == 0);

  CHECK(registry.ingest(packet(original, 1, 1000, "authenticated")).accepted());
  CHECK(registry.stats(id.stream_id)->received == 1);
  CHECK(alerts.size() == 3);
}

void test_jitter_buffer_is_per_stream_and_bounded() {
  netsec::MediaConfig config;
  config.jitter_buffer_capacity = 3;
  StreamRegistry registry(config);

  const auto secret = key({0x77});
  const auto one = binding("s", 1, "10.0.0.1", 1000, 96, secret);
  const auto two = binding("s", 2, "10.0.0.2", 2000, 96, secret);
  const auto one_id = registry.register_stream(one);
  const auto two_id = registry.register_stream(two);

  for (std::uint16_t sequence = 1; sequence <= 5; ++sequence) {
    CHECK(registry
              .ingest(packet(one, sequence,
                             static_cast<std::uint32_t>(sequence * 1000),
                             "one-" + std::to_string(sequence)))
              .accepted());
  }
  CHECK(registry.ingest(packet(two, 1, 5000, "two-1")).accepted());

  const auto stats = registry.stats(one_id.stream_id);
  CHECK(stats->jitter_buffer_drops == 2);
  CHECK(registry.buffered_count(one_id.stream_id) == 3);
  CHECK(registry.buffered_count(two_id.stream_id) == 1);

  const auto one_payloads = payloads(registry.drain(one_id.stream_id));
  const auto two_payloads = payloads(registry.drain(two_id.stream_id));
  CHECK((one_payloads ==
         std::vector<std::string>{"one-3", "one-4", "one-5"}));
  CHECK((two_payloads == std::vector<std::string>{"two-1"}));
}

void test_sequence_and_timestamp_wrap() {
  StreamRegistry registry;
  const auto secret = key({0x42});
  const auto stream = binding("wrap", 8080, "10.9.9.9", 9000, 96, secret, 8000);
  const auto id = registry.register_stream(stream);

  CHECK(registry.ingest(packet(stream, 65534, 0xFFFF0000u, "a")).accepted());
  CHECK(registry.ingest(packet(stream, 65535, 0xFFFF1000u, "b")).accepted());
  CHECK(registry.ingest(packet(stream, 0, 0x00000000u, "c")).accepted());
  CHECK(registry.ingest(packet(stream, 1, 0x00001000u, "d")).accepted());

  const auto replayed =
      registry.ingest(packet(stream, 65535, 0xFFFF1000u, "replay"));
  CHECK(replayed.status == IngestStatus::sequence_replay);

  const auto stats = registry.stats(id.stream_id);
  CHECK(stats->accepted == 4);
  CHECK(stats->highest_sequence == 1);
  CHECK(stats->highest_timestamp == 0x1000u);
  CHECK((payloads(registry.drain(id.stream_id)) ==
         std::vector<std::string>{"a", "b", "c", "d"}));
}

}  // namespace

int main() {
  const std::vector<std::pair<std::string_view, std::function<void()>>> tests{
      {"ssrc_source_conflict", test_ssrc_source_conflict_is_isolated_and_alerted},
      {"source_change", test_source_change_does_not_migrate_stream},
      {"sequence_replay_and_jump", test_sequence_replay_and_jumps_are_dropped},
      {"session_switch", test_session_switch_gets_independent_state},
      {"legitimate_multiple_streams",
       test_legitimate_multiple_streams_never_mix},
      {"registration_conflicts", test_registration_conflicts_and_key_binding},
      {"bounded_per_stream_jitter", test_jitter_buffer_is_per_stream_and_bounded},
      {"sequence_and_timestamp_wrap", test_sequence_and_timestamp_wrap},
  };

  for (const auto& [name, test] : tests) {
    const auto before = failures;
    test();
    if (failures == before) {
      std::cout << "PASS " << name << '\n';
    }
  }

  if (failures != 0) {
    std::cerr << failures << " assertion(s) failed\n";
    return 1;
  }
  std::cout << "All stream identity tests passed\n";
  return 0;
}

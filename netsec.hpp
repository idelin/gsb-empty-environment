#pragma once

#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace netsec {

using Ssrc = std::uint32_t;
using StreamId = std::uint64_t;
using KeyMaterial = std::vector<std::uint8_t>;

inline constexpr StreamId kInvalidStreamId = 0;

struct Endpoint {
  std::string address;
  std::uint16_t port{0};

  friend bool operator==(const Endpoint&, const Endpoint&) = default;
  friend auto operator<=>(const Endpoint&, const Endpoint&) = default;
};

struct StreamBinding {
  std::string session_id;
  Ssrc ssrc{0};
  Endpoint source;
  std::uint8_t payload_type{0};
  KeyMaterial key_material;
  std::uint32_t clock_rate{90000};
};

struct MediaPacket {
  std::string session_id;
  Ssrc ssrc{0};
  Endpoint source;
  std::uint8_t payload_type{0};
  std::uint16_t sequence_number{0};
  std::uint32_t timestamp{0};
  std::vector<std::uint8_t> payload;
  KeyMaterial key_material;
};

struct MediaConfig {
  std::uint16_t reorder_window{512};
  std::uint16_t max_sequence_gap{3000};
  std::chrono::milliseconds max_timestamp_forward_jump{10000};
  std::chrono::milliseconds max_timestamp_backward_jump{2000};
  std::size_t jitter_buffer_capacity{512};
};

enum class IngestStatus {
  accepted,
  unknown_stream,
  ssrc_conflict,
  payload_type_mismatch,
  key_mismatch,
  sequence_replay,
  sequence_jump,
  timestamp_jump,
};

using Verdict = IngestStatus;

struct IngestResult {
  IngestStatus status{IngestStatus::unknown_stream};
  StreamId stream_id{kInvalidStreamId};
  bool quarantined{false};
  bool alert_raised{false};
  std::string detail;

  [[nodiscard]] bool accepted() const noexcept {
    return status == IngestStatus::accepted;
  }

  [[nodiscard]] bool rejected() const noexcept { return !accepted(); }
};

enum class RegisterStatus {
  inserted,
  already_registered,
  invalid_binding,
  conflict,
};

struct RegistrationResult {
  RegisterStatus status{RegisterStatus::invalid_binding};
  StreamId stream_id{kInvalidStreamId};
  bool alert_raised{false};
  std::string detail;

  [[nodiscard]] bool ok() const noexcept {
    return status == RegisterStatus::inserted ||
           status == RegisterStatus::already_registered;
  }
};

enum class AlertCode {
  ssrc_source_conflict,
  ssrc_identity_conflict,
  key_mismatch,
  registration_conflict,
};

struct Alert {
  AlertCode code{AlertCode::ssrc_source_conflict};
  StreamId stream_id{kInvalidStreamId};
  std::string session_id;
  Ssrc ssrc{0};
  Endpoint source;
  std::uint8_t payload_type{0};
  std::uint16_t sequence_number{0};
  std::uint32_t timestamp{0};
  std::string detail;
  std::chrono::steady_clock::time_point detected_at{};
};

using AlertSink = std::function<void(const Alert&)>;

struct BufferedPacket {
  StreamId stream_id{kInvalidStreamId};
  std::string session_id;
  Ssrc ssrc{0};
  Endpoint source;
  std::uint8_t payload_type{0};
  std::uint16_t sequence_number{0};
  std::uint32_t timestamp{0};
  std::uint64_t extended_sequence{0};
  std::vector<std::uint8_t> payload;
};

struct StreamStats {
  std::uint64_t received{0};
  std::uint64_t accepted{0};
  std::uint64_t rejected{0};
  std::uint64_t sequence_replays{0};
  std::uint64_t sequence_jumps{0};
  std::uint64_t timestamp_jumps{0};
  std::uint64_t out_of_order{0};
  std::uint64_t jitter_buffer_drops{0};
  std::size_t buffered_packets{0};
  std::uint64_t highest_extended_sequence{0};
  std::uint16_t highest_sequence{0};
  std::uint32_t highest_timestamp{0};
  double jitter{0.0};
};

struct QuarantineEntry {
  std::string session_id;
  Ssrc ssrc{0};
  Endpoint source;
  StreamId victim_stream_id{kInvalidStreamId};
  std::uint64_t packet_count{0};
  std::uint64_t byte_count{0};
  std::chrono::steady_clock::time_point first_seen{};
  std::chrono::steady_clock::time_point last_seen{};
};

struct RegistryStats {
  std::size_t active_streams{0};
  std::size_t quarantined_sources{0};
  std::uint64_t packets_seen{0};
  std::uint64_t accepted_packets{0};
  std::uint64_t unknown_packets{0};
  std::uint64_t ssrc_conflict_packets{0};
  std::uint64_t identity_conflict_packets{0};
  std::uint64_t auth_failures{0};
};

class StreamRegistry {
 public:
  explicit StreamRegistry(MediaConfig config = {});
  ~StreamRegistry();

  StreamRegistry(StreamRegistry&&) noexcept;
  StreamRegistry& operator=(StreamRegistry&&) noexcept;

  StreamRegistry(const StreamRegistry&) = delete;
  StreamRegistry& operator=(const StreamRegistry&) = delete;

  RegistrationResult register_stream(const StreamBinding& binding);
  RegistrationResult add_stream(const StreamBinding& binding) {
    return register_stream(binding);
  }
  RegistrationResult bind(const StreamBinding& binding) {
    return register_stream(binding);
  }

  bool unregister(StreamId stream_id);
  void clear();

  [[nodiscard]] std::size_t size() const;
  [[nodiscard]] std::optional<StreamBinding> binding(StreamId stream_id) const;
  [[nodiscard]] std::optional<StreamStats> stats(StreamId stream_id) const;

  IngestResult ingest(const MediaPacket& packet);
  IngestResult ingest_packet(const MediaPacket& packet) { return ingest(packet); }

  [[nodiscard]] std::size_t buffered_count(StreamId stream_id) const;
  std::optional<BufferedPacket> pop_next(StreamId stream_id);
  std::vector<BufferedPacket> drain(StreamId stream_id);

  bool reset_media_state(StreamId stream_id);

  void set_alert_sink(AlertSink sink);
  [[nodiscard]] std::vector<QuarantineEntry> quarantined_sources() const;
  [[nodiscard]] bool is_quarantined(const std::string& session_id, Ssrc ssrc,
                                    const Endpoint& source) const;
  void clear_quarantine();

  [[nodiscard]] RegistryStats registry_stats() const;

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

using StreamIdentityTable = StreamRegistry;
using MediaStreamRegistry = StreamRegistry;

}  // namespace netsec

#include "netsec.hpp"

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <map>
#include <mutex>
#include <set>
#include <string>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint32_t kSequenceModulus = 1u << 16;
constexpr std::uint32_t kSequenceHalf = 1u << 15;
constexpr std::int64_t kMaxTimestampWindow = 0x7fffffffLL;

bool secure_equal(const KeyMaterial& lhs, const KeyMaterial& rhs) {
  if (lhs.size() != rhs.size()) {
    return false;
  }
  std::uint8_t difference = 0;
  for (std::size_t i = 0; i < lhs.size(); ++i) {
    difference |= static_cast<std::uint8_t>(lhs[i] ^ rhs[i]);
  }
  return difference == 0;
}

std::int64_t ticks_for(std::chrono::milliseconds window,
                       std::uint32_t clock_rate) {
  if (window.count() <= 0 || clock_rate == 0) {
    return 1;
  }
  const long double ticks =
      static_cast<long double>(window.count()) * clock_rate / 1000.0L;
  if (ticks < 1.0L) {
    return 1;
  }
  if (ticks > static_cast<long double>(kMaxTimestampWindow)) {
    return kMaxTimestampWindow;
  }
  return static_cast<std::int64_t>(ticks);
}

struct LookupKey {
  std::string session_id;
  Ssrc ssrc{0};
  std::string address;
  std::uint16_t port{0};
  std::uint8_t payload_type{0};

  friend auto operator<=>(const LookupKey&, const LookupKey&) = default;
};

LookupKey lookup_key(const std::string& session_id, Ssrc ssrc,
                     const Endpoint& source, std::uint8_t payload_type) {
  return LookupKey{session_id, ssrc, source.address, source.port, payload_type};
}

struct SessionSsrcKey {
  std::string session_id;
  Ssrc ssrc{0};

  friend auto operator<=>(const SessionSsrcKey&,
                          const SessionSsrcKey&) = default;
};

struct QuarantineKey {
  std::string session_id;
  Ssrc ssrc{0};
  std::string address;
  std::uint16_t port{0};

  friend auto operator<=>(const QuarantineKey&, const QuarantineKey&) = default;
};

QuarantineKey quarantine_key(const std::string& session_id, Ssrc ssrc,
                             const Endpoint& source) {
  return QuarantineKey{session_id, ssrc, source.address, source.port};
}

struct AlertDedupKey {
  std::string session_id;
  Ssrc ssrc{0};
  std::string address;
  std::uint16_t port{0};
  std::uint8_t code{0};

  friend auto operator<=>(const AlertDedupKey&,
                          const AlertDedupKey&) = default;
};

AlertDedupKey alert_key(const Alert& alert) {
  return AlertDedupKey{alert.session_id, alert.ssrc, alert.source.address,
                       alert.source.port, static_cast<std::uint8_t>(alert.code)};
}

struct StoredPacket {
  std::uint64_t extended_sequence{0};
  std::uint16_t sequence_number{0};
  std::uint32_t timestamp{0};
  std::uint8_t payload_type{0};
  std::vector<std::uint8_t> payload;
};

struct StreamState {
  StreamId id{kInvalidStreamId};
  StreamBinding binding;

  bool sequence_initialized{false};
  std::uint64_t highest_extended_sequence{0};
  std::set<std::uint64_t> seen_sequences;

  bool timestamp_initialized{false};
  std::int64_t highest_extended_timestamp{0};

  bool transit_initialized{false};
  double last_transit{0.0};
  double jitter{0.0};

  std::map<std::uint64_t, StoredPacket> jitter_buffer;
  StreamStats stats;
};

struct QuarantineRecord {
  QuarantineEntry entry;
};

enum class SequenceVerdict {
  ok,
  replay,
  jump,
};

struct SequenceDecision {
  SequenceVerdict verdict{SequenceVerdict::ok};
  std::uint64_t extended_sequence{0};
  bool advance_highest{false};
  bool out_of_order{false};
};

SequenceDecision evaluate_sequence(const StreamState& state,
                                   std::uint16_t sequence_number,
                                   const MediaConfig& config) {
  if (!state.sequence_initialized) {
    return SequenceDecision{SequenceVerdict::ok, sequence_number, true, false};
  }

  const auto highest_low =
      static_cast<std::uint16_t>(state.highest_extended_sequence);
  const auto distance =
      static_cast<std::uint16_t>(sequence_number - highest_low);

  std::uint64_t extended = state.highest_extended_sequence;
  bool advance = false;
  bool out_of_order = false;

  if (distance == 0) {
    extended = state.highest_extended_sequence;
  } else if (distance < kSequenceHalf) {
    if (distance > config.max_sequence_gap) {
      return SequenceDecision{SequenceVerdict::jump, 0, false, false};
    }
    extended = state.highest_extended_sequence + distance;
    advance = true;
  } else {
    const auto backward =
        static_cast<std::uint16_t>(kSequenceModulus - distance);
    if (backward > config.reorder_window ||
        backward > state.highest_extended_sequence) {
      return SequenceDecision{SequenceVerdict::jump, 0, false, false};
    }
    extended = state.highest_extended_sequence - backward;
    out_of_order = true;
  }

  if (state.seen_sequences.contains(extended)) {
    return SequenceDecision{SequenceVerdict::replay, extended, false, false};
  }
  return SequenceDecision{SequenceVerdict::ok, extended, advance, out_of_order};
}

void commit_sequence(StreamState& state, const SequenceDecision& decision,
                     const MediaConfig& config) {
  if (!state.sequence_initialized || decision.advance_highest) {
    state.highest_extended_sequence = decision.extended_sequence;
  }
  state.sequence_initialized = true;
  state.seen_sequences.insert(decision.extended_sequence);

  if (state.highest_extended_sequence > config.reorder_window) {
    const auto threshold =
        state.highest_extended_sequence - config.reorder_window;
    state.seen_sequences.erase(state.seen_sequences.begin(),
                               state.seen_sequences.lower_bound(threshold));
  }
}

enum class TimestampVerdict {
  ok,
  jump,
};

struct TimestampDecision {
  TimestampVerdict verdict{TimestampVerdict::ok};
  std::int64_t extended_timestamp{0};
  bool advance_highest{false};
};

TimestampDecision evaluate_timestamp(const StreamState& state,
                                     std::uint32_t timestamp,
                                     const MediaConfig& config) {
  if (!state.timestamp_initialized) {
    return TimestampDecision{TimestampVerdict::ok, timestamp, true};
  }

  const auto highest_low =
      static_cast<std::uint32_t>(state.highest_extended_timestamp);
  const std::uint32_t raw_delta = timestamp - highest_low;
  const std::int64_t delta =
      raw_delta <= 0x7fffffffu
          ? static_cast<std::int64_t>(raw_delta)
          : static_cast<std::int64_t>(raw_delta) - 0x100000000LL;

  const auto forward_window =
      ticks_for(config.max_timestamp_forward_jump,
                state.binding.clock_rate);
  const auto backward_window =
      ticks_for(config.max_timestamp_backward_jump,
                state.binding.clock_rate);

  if (delta > forward_window || delta < -backward_window) {
    return TimestampDecision{TimestampVerdict::jump, 0, false};
  }

  return TimestampDecision{TimestampVerdict::ok,
                           state.highest_extended_timestamp + delta,
                           delta > 0};
}

void commit_timestamp(StreamState& state,
                      const TimestampDecision& decision,
                      std::uint32_t timestamp) {
  if (!state.timestamp_initialized || decision.advance_highest) {
    state.highest_extended_timestamp = decision.extended_timestamp;
    state.stats.highest_timestamp = timestamp;
  }
  state.timestamp_initialized = true;
}

void update_jitter(StreamState& state, std::int64_t extended_timestamp) {
  const auto now = std::chrono::steady_clock::now().time_since_epoch();
  const long double arrival_ns =
      static_cast<long double>(
          std::chrono::duration_cast<std::chrono::nanoseconds>(now).count());
  const long double arrival_ticks =
      arrival_ns * state.binding.clock_rate / 1000000000.0L;
  const long double transit =
      arrival_ticks - static_cast<long double>(extended_timestamp);

  if (!state.transit_initialized) {
    state.last_transit = transit;
    state.transit_initialized = true;
    return;
  }

  const long double variation = std::fabs(transit - state.last_transit);
  state.jitter += (variation - state.jitter) / 16.0L;
  state.last_transit = transit;
  state.stats.jitter = static_cast<double>(state.jitter);
}

void insert_buffered(StreamState& state, const MediaPacket& packet,
                     std::uint64_t extended_sequence,
                     const MediaConfig& config) {
  StoredPacket stored;
  stored.extended_sequence = extended_sequence;
  stored.sequence_number = packet.sequence_number;
  stored.timestamp = packet.timestamp;
  stored.payload_type = packet.payload_type;
  stored.payload = packet.payload;

  state.jitter_buffer.emplace(extended_sequence, std::move(stored));
  while (state.jitter_buffer.size() > config.jitter_buffer_capacity) {
    state.jitter_buffer.erase(state.jitter_buffer.begin());
    ++state.stats.jitter_buffer_drops;
  }
  state.stats.buffered_packets = state.jitter_buffer.size();
}

BufferedPacket public_packet(const StreamState& state,
                             const StoredPacket& stored) {
  return BufferedPacket{
      state.id,
      state.binding.session_id,
      state.binding.ssrc,
      state.binding.source,
      stored.payload_type,
      stored.sequence_number,
      stored.timestamp,
      stored.extended_sequence,
      stored.payload,
  };
}

bool valid_binding(const StreamBinding& binding, std::string& reason) {
  if (binding.session_id.empty()) {
    reason = "session_id must not be empty";
    return false;
  }
  if (binding.payload_type > 127) {
    reason = "payload_type must be in the RTP 7-bit range";
    return false;
  }
  if (binding.clock_rate == 0) {
    reason = "clock_rate must be non-zero";
    return false;
  }
  if (binding.key_material.empty()) {
    reason = "key_material must not be empty";
    return false;
  }
  return true;
}

MediaConfig sanitized(MediaConfig config) {
  if (config.reorder_window > kSequenceHalf - 1) {
    config.reorder_window = kSequenceHalf - 1;
  }
  if (config.max_sequence_gap == 0) {
    config.max_sequence_gap = 1;
  }
  if (config.max_sequence_gap > kSequenceHalf - 1) {
    config.max_sequence_gap = kSequenceHalf - 1;
  }
  if (config.jitter_buffer_capacity == 0) {
    config.jitter_buffer_capacity = 1;
  }
  return config;
}

}  // namespace

struct StreamRegistry::Impl {
  explicit Impl(MediaConfig value) : config(sanitized(value)) {}

  MediaConfig config;
  StreamId next_id{1};
  std::map<LookupKey, StreamId> exact;
  std::map<SessionSsrcKey, std::vector<StreamId>> by_session_ssrc;
  std::map<StreamId, StreamState> streams;
  std::map<QuarantineKey, QuarantineRecord> quarantine;
  std::set<AlertDedupKey> sent_alerts;
  AlertSink alert_sink;
  RegistryStats counters;
  mutable std::mutex mutex;

  bool should_alert(const Alert& alert) {
    return sent_alerts.insert(alert_key(alert)).second;
  }
};

StreamRegistry::StreamRegistry(MediaConfig config)
    : impl_(std::make_unique<Impl>(config)) {}

StreamRegistry::~StreamRegistry() = default;
StreamRegistry::StreamRegistry(StreamRegistry&&) noexcept = default;
StreamRegistry& StreamRegistry::operator=(StreamRegistry&&) noexcept = default;

RegistrationResult StreamRegistry::register_stream(
    const StreamBinding& binding) {
  RegistrationResult result;
  std::string reason;
  if (!valid_binding(binding, reason)) {
    result.status = RegisterStatus::invalid_binding;
    result.detail = std::move(reason);
    return result;
  }

  std::optional<Alert> pending_alert;
  AlertSink sink;
  {
    const std::lock_guard lock(impl_->mutex);
    sink = impl_->alert_sink;

    const LookupKey key =
        lookup_key(binding.session_id, binding.ssrc, binding.source,
                   binding.payload_type);
    const auto exact = impl_->exact.find(key);
    if (exact != impl_->exact.end()) {
      const auto& existing = impl_->streams.at(exact->second);
      if (secure_equal(existing.binding.key_material, binding.key_material) &&
          existing.binding.clock_rate == binding.clock_rate) {
        result.status = RegisterStatus::already_registered;
        result.stream_id = existing.id;
        return result;
      }

      result.status = RegisterStatus::conflict;
      result.detail =
          "SSRC identity already registered with different key material or "
          "clock rate";

      Alert alert;
      alert.code = AlertCode::ssrc_identity_conflict;
      alert.stream_id = existing.id;
      alert.session_id = binding.session_id;
      alert.ssrc = binding.ssrc;
      alert.source = binding.source;
      alert.payload_type = binding.payload_type;
      alert.detail = result.detail;
      alert.detected_at = std::chrono::steady_clock::now();
      if (impl_->should_alert(alert)) {
        result.alert_raised = true;
        pending_alert = std::move(alert);
      }
    } else {
      const auto same_ssrc =
          impl_->by_session_ssrc.find({binding.session_id, binding.ssrc});
      if (same_ssrc != impl_->by_session_ssrc.end()) {
        StreamId victim = kInvalidStreamId;
        bool same_source = false;
        for (const auto id : same_ssrc->second) {
          const auto& existing = impl_->streams.at(id);
          if (victim == kInvalidStreamId) {
            victim = id;
          }
          if (existing.binding.source == binding.source) {
            victim = id;
            same_source = true;
            break;
          }
        }

        result.status = RegisterStatus::conflict;
        result.detail =
            same_source
                ? "SSRC is already bound to another payload type in this "
                  "session"
                : "SSRC is already bound to a different source in this "
                  "session";

        Alert alert;
        alert.code = same_source ? AlertCode::ssrc_identity_conflict
                                 : AlertCode::ssrc_source_conflict;
        alert.stream_id = victim;
        alert.session_id = binding.session_id;
        alert.ssrc = binding.ssrc;
        alert.source = binding.source;
        alert.payload_type = binding.payload_type;
        alert.detail = result.detail;
        alert.detected_at = std::chrono::steady_clock::now();
        if (impl_->should_alert(alert)) {
          result.alert_raised = true;
          pending_alert = std::move(alert);
        }
      }
    }

    if (result.status != RegisterStatus::conflict) {
      const StreamId id = impl_->next_id++;
      StreamState state;
      state.id = id;
      state.binding = binding;
      impl_->streams.emplace(id, std::move(state));
      impl_->exact.emplace(key, id);
      impl_->by_session_ssrc[{binding.session_id, binding.ssrc}].push_back(id);

      result.status = RegisterStatus::inserted;
      result.stream_id = id;
    }
  }

  if (pending_alert && sink) {
    sink(*pending_alert);
  }
  return result;
}

bool StreamRegistry::unregister(StreamId stream_id) {
  const std::lock_guard lock(impl_->mutex);
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end()) {
    return false;
  }

  const auto& binding = stream->second.binding;
  impl_->exact.erase(
      lookup_key(binding.session_id, binding.ssrc, binding.source,
                 binding.payload_type));

  const SessionSsrcKey session_key{binding.session_id, binding.ssrc};
  const auto same_ssrc = impl_->by_session_ssrc.find(session_key);
  if (same_ssrc != impl_->by_session_ssrc.end()) {
    auto& ids = same_ssrc->second;
    ids.erase(std::remove(ids.begin(), ids.end(), stream_id), ids.end());
    if (ids.empty()) {
      impl_->by_session_ssrc.erase(same_ssrc);
    }
  }

  impl_->streams.erase(stream);
  return true;
}

void StreamRegistry::clear() {
  const std::lock_guard lock(impl_->mutex);
  impl_->exact.clear();
  impl_->by_session_ssrc.clear();
  impl_->streams.clear();
  impl_->quarantine.clear();
  impl_->sent_alerts.clear();
  impl_->counters = {};
  impl_->next_id = 1;
}

std::size_t StreamRegistry::size() const {
  const std::lock_guard lock(impl_->mutex);
  return impl_->streams.size();
}

std::optional<StreamBinding> StreamRegistry::binding(
    StreamId stream_id) const {
  const std::lock_guard lock(impl_->mutex);
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end()) {
    return std::nullopt;
  }
  return stream->second.binding;
}

std::optional<StreamStats> StreamRegistry::stats(StreamId stream_id) const {
  const std::lock_guard lock(impl_->mutex);
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end()) {
    return std::nullopt;
  }
  return stream->second.stats;
}

IngestResult StreamRegistry::ingest(const MediaPacket& packet) {
  IngestResult result;
  std::optional<Alert> pending_alert;
  AlertSink sink;

  {
    const std::lock_guard lock(impl_->mutex);
    sink = impl_->alert_sink;
    ++impl_->counters.packets_seen;

    const LookupKey key =
        lookup_key(packet.session_id, packet.ssrc, packet.source,
                   packet.payload_type);
    const auto exact = impl_->exact.find(key);

    if (exact != impl_->exact.end()) {
      auto& stream = impl_->streams.at(exact->second);
      result.stream_id = stream.id;

      if (!secure_equal(stream.binding.key_material, packet.key_material)) {
        ++impl_->counters.auth_failures;
        result.status = IngestStatus::key_mismatch;
        result.detail =
            "packet key material does not match the bound stream identity";

        Alert alert;
        alert.code = AlertCode::key_mismatch;
        alert.stream_id = stream.id;
        alert.session_id = packet.session_id;
        alert.ssrc = packet.ssrc;
        alert.source = packet.source;
        alert.payload_type = packet.payload_type;
        alert.sequence_number = packet.sequence_number;
        alert.timestamp = packet.timestamp;
        alert.detail = result.detail;
        alert.detected_at = std::chrono::steady_clock::now();
        if (impl_->should_alert(alert)) {
          result.alert_raised = true;
          pending_alert = std::move(alert);
        }
      } else {
        ++stream.stats.received;

        const auto sequence =
            evaluate_sequence(stream, packet.sequence_number, impl_->config);
        if (sequence.verdict == SequenceVerdict::replay) {
          ++stream.stats.sequence_replays;
          ++stream.stats.rejected;
          result.status = IngestStatus::sequence_replay;
          result.detail = "duplicate or replayed sequence number";
        } else if (sequence.verdict == SequenceVerdict::jump) {
          ++stream.stats.sequence_jumps;
          ++stream.stats.rejected;
          result.status = IngestStatus::sequence_jump;
          result.detail = "sequence discontinuity exceeds the configured gap";
        } else {
          const auto timestamp =
              evaluate_timestamp(stream, packet.timestamp, impl_->config);
          if (timestamp.verdict == TimestampVerdict::jump) {
            ++stream.stats.timestamp_jumps;
            ++stream.stats.rejected;
            result.status = IngestStatus::timestamp_jump;
            result.detail =
                "timestamp discontinuity exceeds the configured window";
          } else {
            commit_sequence(stream, sequence, impl_->config);
            commit_timestamp(stream, timestamp, packet.timestamp);
            if (sequence.out_of_order) {
              ++stream.stats.out_of_order;
            }
            update_jitter(stream, timestamp.extended_timestamp);
            insert_buffered(stream, packet, sequence.extended_sequence,
                            impl_->config);

            ++stream.stats.accepted;
            ++impl_->counters.accepted_packets;
            stream.stats.highest_extended_sequence =
                stream.highest_extended_sequence;
            stream.stats.highest_sequence =
                static_cast<std::uint16_t>(stream.highest_extended_sequence);
            result.status = IngestStatus::accepted;
          }
        }
      }
    } else {
      const auto same_ssrc =
          impl_->by_session_ssrc.find({packet.session_id, packet.ssrc});
      if (same_ssrc == impl_->by_session_ssrc.end()) {
        ++impl_->counters.unknown_packets;
        result.status = IngestStatus::unknown_stream;
        result.detail = "no registered stream matches the packet identity";
      } else {
        StreamId victim = kInvalidStreamId;
        bool same_source = false;
        for (const auto id : same_ssrc->second) {
          const auto& existing = impl_->streams.at(id);
          if (victim == kInvalidStreamId) {
            victim = id;
          }
          if (existing.binding.source == packet.source) {
            victim = id;
            same_source = true;
            break;
          }
        }
        result.stream_id = victim;

        if (same_source) {
          ++impl_->counters.identity_conflict_packets;
          result.status = IngestStatus::payload_type_mismatch;
          result.detail =
              "source and SSRC match a stream but the payload type does not";
        } else {
          ++impl_->counters.ssrc_conflict_packets;
          result.status = IngestStatus::ssrc_conflict;
          result.quarantined = true;
          result.detail =
              "SSRC collision from a different source; packet isolated";

          const QuarantineKey qkey =
              quarantine_key(packet.session_id, packet.ssrc, packet.source);
          const auto now = std::chrono::steady_clock::now();
          auto [entry, inserted] = impl_->quarantine.try_emplace(qkey);
          if (inserted) {
            entry->second.entry.session_id = packet.session_id;
            entry->second.entry.ssrc = packet.ssrc;
            entry->second.entry.source = packet.source;
            entry->second.entry.victim_stream_id = victim;
            entry->second.entry.first_seen = now;
          }
          entry->second.entry.victim_stream_id = victim;
          entry->second.entry.last_seen = now;
          ++entry->second.entry.packet_count;
          entry->second.entry.byte_count += packet.payload.size();
        }

        Alert alert;
        alert.code = same_source ? AlertCode::ssrc_identity_conflict
                                 : AlertCode::ssrc_source_conflict;
        alert.stream_id = victim;
        alert.session_id = packet.session_id;
        alert.ssrc = packet.ssrc;
        alert.source = packet.source;
        alert.payload_type = packet.payload_type;
        alert.sequence_number = packet.sequence_number;
        alert.timestamp = packet.timestamp;
        alert.detail = result.detail;
        alert.detected_at = std::chrono::steady_clock::now();
        if (impl_->should_alert(alert)) {
          result.alert_raised = true;
          pending_alert = std::move(alert);
        }
      }
    }
  }

  if (pending_alert && sink) {
    sink(*pending_alert);
  }
  return result;
}

std::size_t StreamRegistry::buffered_count(StreamId stream_id) const {
  const std::lock_guard lock(impl_->mutex);
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end()) {
    return 0;
  }
  return stream->second.jitter_buffer.size();
}

std::optional<BufferedPacket> StreamRegistry::pop_next(StreamId stream_id) {
  const std::lock_guard lock(impl_->mutex);
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end() ||
      stream->second.jitter_buffer.empty()) {
    return std::nullopt;
  }
  auto node = stream->second.jitter_buffer.extract(
      stream->second.jitter_buffer.begin());
  stream->second.stats.buffered_packets = stream->second.jitter_buffer.size();
  return public_packet(stream->second, node.mapped());
}

std::vector<BufferedPacket> StreamRegistry::drain(StreamId stream_id) {
  const std::lock_guard lock(impl_->mutex);
  std::vector<BufferedPacket> packets;
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end()) {
    return packets;
  }

  packets.reserve(stream->second.jitter_buffer.size());
  for (const auto& [sequence, stored] : stream->second.jitter_buffer) {
    (void)sequence;
    packets.push_back(public_packet(stream->second, stored));
  }
  stream->second.jitter_buffer.clear();
  stream->second.stats.buffered_packets = 0;
  return packets;
}

bool StreamRegistry::reset_media_state(StreamId stream_id) {
  const std::lock_guard lock(impl_->mutex);
  const auto stream = impl_->streams.find(stream_id);
  if (stream == impl_->streams.end()) {
    return false;
  }

  auto& state = stream->second;
  state.sequence_initialized = false;
  state.highest_extended_sequence = 0;
  state.seen_sequences.clear();
  state.timestamp_initialized = false;
  state.highest_extended_timestamp = 0;
  state.transit_initialized = false;
  state.last_transit = 0.0;
  state.jitter = 0.0;
  state.jitter_buffer.clear();
  state.stats = {};
  return true;
}

void StreamRegistry::set_alert_sink(AlertSink sink) {
  const std::lock_guard lock(impl_->mutex);
  impl_->alert_sink = std::move(sink);
}

std::vector<QuarantineEntry> StreamRegistry::quarantined_sources() const {
  const std::lock_guard lock(impl_->mutex);
  std::vector<QuarantineEntry> entries;
  entries.reserve(impl_->quarantine.size());
  for (const auto& [key, record] : impl_->quarantine) {
    (void)key;
    entries.push_back(record.entry);
  }
  return entries;
}

bool StreamRegistry::is_quarantined(const std::string& session_id, Ssrc ssrc,
                                    const Endpoint& source) const {
  const std::lock_guard lock(impl_->mutex);
  return impl_->quarantine.contains(quarantine_key(session_id, ssrc, source));
}

void StreamRegistry::clear_quarantine() {
  const std::lock_guard lock(impl_->mutex);
  impl_->quarantine.clear();
  impl_->sent_alerts.clear();
}

RegistryStats StreamRegistry::registry_stats() const {
  const std::lock_guard lock(impl_->mutex);
  RegistryStats stats = impl_->counters;
  stats.active_streams = impl_->streams.size();
  stats.quarantined_sources = impl_->quarantine.size();
  return stats;
}

}  // namespace netsec

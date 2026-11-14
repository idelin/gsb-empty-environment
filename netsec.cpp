#include "netsec.hpp"

#include "detail/crypto.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <limits>
#include <mutex>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint8_t kProtocolVersion = 1;
constexpr std::size_t kFrameHeaderSize = 12;
constexpr std::size_t kMaxFrameSize = 64U * 1024U * 1024U;
constexpr std::size_t kMaxIdentitySize = 255;
constexpr std::size_t kMaxTicketIdSize = 128;
constexpr std::size_t kMaxEndpointHostSize = 1024;

constexpr std::array<Byte, 4> kMagic{0x4eU, 0x53U, 0x4dU, 0x31U};
constexpr std::array<Byte, 32> kX25519Basepoint{
    9U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
    0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U};

enum class MessageType : Byte {
  client_hello = 1,
  server_hello = 2,
  client_finished = 3,
  ready = 4,
  page = 5,
  commit = 6,
  complete = 7,
};

struct ProtocolFailure : std::exception {
  ProtocolFailure(ErrorCode error_code, std::string error_detail)
      : code(error_code), detail(std::move(error_detail)) {}

  const char* what() const noexcept override { return detail.c_str(); }

  ErrorCode code;
  std::string detail;
};

[[noreturn]] void fail(ErrorCode code, std::string detail) {
  throw ProtocolFailure(code, std::move(detail));
}

void append_u8(Bytes& out, std::uint8_t value) { out.push_back(value); }

void append_u16(Bytes& out, std::uint16_t value) {
  out.push_back(static_cast<Byte>(value));
  out.push_back(static_cast<Byte>(value >> 8U));
}

void append_u32(Bytes& out, std::uint32_t value) {
  for (unsigned i = 0; i < 4; ++i) {
    out.push_back(static_cast<Byte>(value >> (8U * i)));
  }
}

void append_u64(Bytes& out, std::uint64_t value) {
  for (unsigned i = 0; i < 8; ++i) {
    out.push_back(static_cast<Byte>(value >> (8U * i)));
  }
}

void append_bytes(Bytes& out, std::span<const Byte> value) {
  out.insert(out.end(), value.begin(), value.end());
}

void append_label(Bytes& out, std::string_view label) {
  out.insert(out.end(), label.begin(), label.end());
}

void append_string(Bytes& out, std::string_view value) {
  if (value.size() > 65535U) {
    fail(ErrorCode::invalid_config, "string is too long for the wire format");
  }
  append_u16(out, static_cast<std::uint16_t>(value.size()));
  out.insert(out.end(), value.begin(), value.end());
}

void append_sized_bytes(Bytes& out, std::span<const Byte> value) {
  if (value.size() > std::numeric_limits<std::uint32_t>::max()) {
    fail(ErrorCode::invalid_request, "byte string is too large");
  }
  append_u32(out, static_cast<std::uint32_t>(value.size()));
  append_bytes(out, value);
}

void append_endpoint(Bytes& out, const Endpoint& endpoint) {
  append_string(out, endpoint.host);
  append_u16(out, endpoint.port);
}

class Decoder {
 public:
  explicit Decoder(std::span<const Byte> input) : input_(input) {}

  std::uint8_t u8() {
    require(1);
    return input_[offset_++];
  }

  std::uint16_t u16() {
    require(2);
    const std::uint16_t value = static_cast<std::uint16_t>(
        static_cast<std::uint32_t>(input_[offset_]) |
        (static_cast<std::uint32_t>(input_[offset_ + 1]) << 8U));
    offset_ += 2;
    return value;
  }

  std::uint32_t u32() {
    require(4);
    std::uint32_t value = 0;
    for (unsigned i = 0; i < 4; ++i) {
      value |= static_cast<std::uint32_t>(input_[offset_ + i]) << (8U * i);
    }
    offset_ += 4;
    return value;
  }

  std::uint64_t u64() {
    require(8);
    std::uint64_t value = 0;
    for (unsigned i = 0; i < 8; ++i) {
      value |= static_cast<std::uint64_t>(input_[offset_ + i]) << (8U * i);
    }
    offset_ += 8;
    return value;
  }

  std::string string(std::size_t maximum) {
    const std::uint16_t size = u16();
    if (size > maximum) {
      fail(ErrorCode::malformed_message, "wire string exceeds its limit");
    }
    require(size);
    std::string value(reinterpret_cast<const char*>(input_.data() + offset_),
                      size);
    offset_ += size;
    return value;
  }

  std::span<const Byte> fixed(std::size_t size) {
    require(size);
    const auto value = input_.subspan(offset_, size);
    offset_ += size;
    return value;
  }

  std::span<const Byte> sized_bytes(std::size_t maximum) {
    const std::uint32_t size = u32();
    if (size > maximum) {
      fail(ErrorCode::malformed_message, "wire byte string exceeds its limit");
    }
    return fixed(size);
  }

  Endpoint endpoint() {
    Endpoint value;
    value.host = string(kMaxEndpointHostSize);
    value.port = u16();
    return value;
  }

  std::size_t remaining() const { return input_.size() - offset_; }
  bool empty() const { return offset_ == input_.size(); }

  void finish() const {
    if (!empty()) {
      fail(ErrorCode::malformed_message, "trailing bytes in protocol message");
    }
  }

 private:
  void require(std::size_t size) const {
    if (size > input_.size() - offset_) {
      fail(ErrorCode::malformed_message, "truncated protocol message");
    }
  }

  std::span<const Byte> input_;
  std::size_t offset_{};
};

void validate_nonempty(std::string_view value, std::string_view name,
                       std::size_t maximum) {
  if (value.empty()) {
    throw std::invalid_argument(std::string(name) + " must not be empty");
  }
  if (value.size() > maximum) {
    throw std::invalid_argument(std::string(name) + " is too long");
  }
}

bool all_zero(std::span<const Byte> value) {
  Byte combined = 0;
  for (const Byte byte : value) {
    combined = static_cast<Byte>(combined | byte);
  }
  return combined == 0;
}

void validate_common(const HostId& source, const HostId& target,
                     const Endpoint& endpoint, const SecretKey& source_key,
                     const SecretKey& target_key, std::uint64_t max_pages,
                     std::uint64_t max_page_bytes,
                     std::uint64_t max_total_bytes) {
  validate_nonempty(source.value, "source host id", kMaxIdentitySize);
  validate_nonempty(target.value, "target host id", kMaxIdentitySize);
  validate_nonempty(endpoint.host, "target endpoint host",
                    kMaxEndpointHostSize);
  if (endpoint.port == 0) {
    throw std::invalid_argument("target endpoint port must not be zero");
  }
  if (all_zero(source_key.bytes) || all_zero(target_key.bytes)) {
    throw std::invalid_argument("authentication keys must not be all zero");
  }
  if (max_pages == 0) {
    throw std::invalid_argument("max_pages must be greater than zero");
  }
  if (max_page_bytes == 0 || max_page_bytes > kMaxFrameSize / 2U) {
    throw std::invalid_argument("max_page_bytes is outside the supported range");
  }
  if (max_total_bytes < max_page_bytes) {
    throw std::invalid_argument(
        "max_total_bytes must not be smaller than max_page_bytes");
  }
}

void validate_binding(const TicketBinding& binding) {
  validate_nonempty(binding.source.value, "ticket source host id",
                    kMaxIdentitySize);
  validate_nonempty(binding.target.value, "ticket target host id",
                    kMaxIdentitySize);
  validate_nonempty(binding.vm_id.value, "ticket VM id", kMaxIdentitySize);
}

Bytes make_frame(MessageType type, std::span<const Byte> payload) {
  if (payload.size() > kMaxFrameSize - kFrameHeaderSize) {
    fail(ErrorCode::invalid_request, "protocol frame exceeds the size limit");
  }
  Bytes frame;
  frame.reserve(kFrameHeaderSize + payload.size());
  append_bytes(frame, kMagic);
  append_u8(frame, kProtocolVersion);
  append_u8(frame, static_cast<Byte>(type));
  append_u16(frame, 0);
  append_u32(frame, static_cast<std::uint32_t>(payload.size()));
  append_bytes(frame, payload);
  return frame;
}

struct Frame {
  MessageType type;
  Bytes payload;
};

void send_frame(MigrationChannel& channel, MessageType type,
                std::span<const Byte> payload) {
  const Bytes frame = make_frame(type, payload);
  try {
    if (!channel.send(frame)) {
      fail(ErrorCode::transport_error, "migration channel rejected a send");
    }
  } catch (const ProtocolFailure&) {
    throw;
  } catch (const std::exception& error) {
    fail(ErrorCode::transport_error,
         std::string("migration channel send failed: ") + error.what());
  }
}

Frame receive_frame(MigrationChannel& channel) {
  std::optional<Bytes> received;
  try {
    received = channel.receive();
  } catch (const std::exception& error) {
    fail(ErrorCode::transport_error,
         std::string("migration channel receive failed: ") + error.what());
  }
  if (!received) {
    fail(ErrorCode::transport_error,
         "migration channel closed before the protocol completed");
  }
  if (received->size() < kFrameHeaderSize || received->size() > kMaxFrameSize) {
    fail(ErrorCode::malformed_message, "invalid protocol frame length");
  }
  if (!std::equal(kMagic.begin(), kMagic.end(), received->begin())) {
    fail(ErrorCode::malformed_message, "invalid protocol frame magic");
  }
  if ((*received)[4] != kProtocolVersion) {
    fail(ErrorCode::malformed_message, "unsupported protocol version");
  }
  const std::uint16_t reserved = static_cast<std::uint16_t>(
      static_cast<std::uint32_t>((*received)[6]) |
      (static_cast<std::uint32_t>((*received)[7]) << 8U));
  if (reserved != 0) {
    fail(ErrorCode::malformed_message, "nonzero protocol frame reserved field");
  }
  const std::uint32_t payload_size =
      static_cast<std::uint32_t>((*received)[8]) |
      (static_cast<std::uint32_t>((*received)[9]) << 8U) |
      (static_cast<std::uint32_t>((*received)[10]) << 16U) |
      (static_cast<std::uint32_t>((*received)[11]) << 24U);
  if (payload_size != received->size() - kFrameHeaderSize) {
    fail(ErrorCode::malformed_message, "protocol frame length mismatch");
  }
  const auto type = static_cast<MessageType>((*received)[5]);
  switch (type) {
    case MessageType::client_hello:
    case MessageType::server_hello:
    case MessageType::client_finished:
    case MessageType::ready:
    case MessageType::page:
    case MessageType::commit:
    case MessageType::complete:
      break;
    default:
      fail(ErrorCode::malformed_message, "unknown protocol message type");
  }
  return Frame{type,
               Bytes(received->begin() + kFrameHeaderSize, received->end())};
}

void expect_type(const Frame& frame, MessageType expected) {
  if (frame.type != expected) {
    fail(ErrorCode::malformed_message, "unexpected protocol message type");
  }
}

Digest digest_fixed(std::span<const Byte> value) {
  if (value.size() != 32) {
    fail(ErrorCode::malformed_message, "invalid digest length");
  }
  Digest digest{};
  std::copy(value.begin(), value.end(), digest.begin());
  return digest;
}

template <std::size_t N>
std::array<Byte, N> random_array() {
  const Bytes random = detail::secure_random(N);
  std::array<Byte, N> value{};
  std::copy(random.begin(), random.end(), value.begin());
  return value;
}

struct SessionKeys {
  std::array<Byte, 32> client_to_target;
  std::array<Byte, 32> target_to_client;
  Digest transcript;
};

SessionKeys derive_keys(std::span<const Byte> client_private,
                        std::span<const Byte> server_public,
                        std::span<const Byte> client_hello,
                        std::span<const Byte> server_hello) {
  const std::array<Byte, 32> shared =
      detail::x25519(client_private, server_public);
  if (all_zero(shared)) {
    fail(ErrorCode::authentication_failed,
         "X25519 produced the forbidden all-zero shared secret");
  }

  Bytes transcript_input;
  append_label(transcript_input, "NSM1 transcript");
  append_sized_bytes(transcript_input, client_hello);
  append_sized_bytes(transcript_input, server_hello);
  const Digest transcript = detail::sha256(transcript_input);

  const Digest prk = detail::hmac_sha256(transcript, shared);
  constexpr std::string_view c2t_label = "NSM1 client-to-target key";
  constexpr std::string_view t2c_label = "NSM1 target-to-client key";
  const Bytes c2t = detail::hkdf_expand(
      prk, std::span<const Byte>(
               reinterpret_cast<const Byte*>(c2t_label.data()),
               c2t_label.size()),
      32);
  const Bytes t2c = detail::hkdf_expand(
      prk, std::span<const Byte>(
               reinterpret_cast<const Byte*>(t2c_label.data()),
               t2c_label.size()),
      32);

  SessionKeys keys;
  std::copy(c2t.begin(), c2t.end(), keys.client_to_target.begin());
  std::copy(t2c.begin(), t2c.end(), keys.target_to_client.begin());
  keys.transcript = transcript;
  return keys;
}

std::array<Byte, 12> record_nonce(std::uint64_t sequence) {
  std::array<Byte, 12> nonce{};
  for (unsigned i = 0; i < 8; ++i) {
    nonce[4 + i] = static_cast<Byte>(sequence >> (8U * i));
  }
  return nonce;
}

Bytes record_aad(const Digest& transcript, MessageType type,
                 std::uint64_t sequence) {
  Bytes aad;
  append_label(aad, "NSM1 record");
  append_bytes(aad, transcript);
  append_u8(aad, static_cast<Byte>(type));
  append_u64(aad, sequence);
  return aad;
}

Bytes seal_record(const std::array<Byte, 32>& key, const Digest& transcript,
                  MessageType type, std::uint64_t sequence,
                  std::span<const Byte> plaintext) {
  const std::array<Byte, 12> nonce = record_nonce(sequence);
  const Bytes aad = record_aad(transcript, type, sequence);
  return detail::aead_seal(key, nonce, aad, plaintext);
}

Bytes open_record(const std::array<Byte, 32>& key, const Digest& transcript,
                  MessageType type, std::uint64_t sequence,
                  std::span<const Byte> sealed) {
  const std::array<Byte, 12> nonce = record_nonce(sequence);
  const Bytes aad = record_aad(transcript, type, sequence);
  std::optional<Bytes> plaintext =
      detail::aead_open(key, nonce, aad, sealed);
  if (!plaintext) {
    fail(ErrorCode::authentication_failed,
         "encrypted protocol record failed authentication");
  }
  return std::move(*plaintext);
}

Bytes client_hello_base(const SourceConfig& config, const VmId& vm_id,
                        std::uint64_t generation, const MigrationTicket& ticket,
                        std::span<const Byte> client_nonce,
                        std::span<const Byte> client_public) {
  Bytes base;
  append_u8(base, kProtocolVersion);
  append_string(base, config.source_id.value);
  append_string(base, config.target_id.value);
  append_string(base, vm_id.value);
  append_u64(base, generation);
  append_string(base, ticket.id);
  append_endpoint(base, config.target_endpoint);
  append_bytes(base, client_nonce);
  append_bytes(base, client_public);
  return base;
}

Digest source_proof(const SecretKey& key, std::span<const Byte> base) {
  Bytes input;
  append_label(input, "NSM1 source-hello-auth");
  append_sized_bytes(input, base);
  return detail::hmac_sha256(key.bytes, input);
}

Digest ticket_proof(const SecretKey& key, std::span<const Byte> base,
                    std::span<const Byte> source_auth) {
  Bytes input;
  append_label(input, "NSM1 ticket-auth");
  append_sized_bytes(input, base);
  append_bytes(input, source_auth);
  return detail::hmac_sha256(key.bytes, input);
}

Bytes server_hello_base(const HostId& target_id, const Endpoint& endpoint,
                        std::span<const Byte> client_nonce,
                        std::span<const Byte> server_nonce,
                        std::span<const Byte> server_public,
                        std::span<const Byte> client_hello) {
  Bytes base;
  append_u8(base, kProtocolVersion);
  append_string(base, target_id.value);
  append_endpoint(base, endpoint);
  append_bytes(base, client_nonce);
  append_bytes(base, server_nonce);
  append_bytes(base, server_public);
  const Digest client_hash = detail::sha256(client_hello);
  append_bytes(base, client_hash);
  return base;
}

Digest target_proof(const SecretKey& key, std::span<const Byte> base) {
  Bytes input;
  append_label(input, "NSM1 target-hello-auth");
  append_sized_bytes(input, base);
  return detail::hmac_sha256(key.bytes, input);
}

bool ticket_binding_matches(const TicketBinding& lhs,
                            const TicketBinding& rhs) {
  return lhs == rhs;
}

void validate_request(const SourceConfig& config,
                      const MigrationRequest& request) {
  validate_nonempty(request.vm_id.value, "VM id", kMaxIdentitySize);
  validate_nonempty(request.ticket.id, "migration ticket id",
                    kMaxTicketIdSize);
  if (all_zero(request.ticket.secret.bytes)) {
    throw std::invalid_argument("migration ticket secret must not be all zero");
  }
  const TicketBinding requested{config.source_id, config.target_id,
                                request.vm_id, request.generation};
  if (!ticket_binding_matches(requested, request.ticket.binding)) {
    throw std::invalid_argument(
        "migration ticket is not bound to this source, target, VM, and generation");
  }
  if (request.pages.size() > config.max_pages) {
    throw std::invalid_argument("migration exceeds max_pages");
  }
  std::uint64_t total_bytes = 0;
  std::unordered_set<std::uint64_t> addresses;
  addresses.reserve(request.pages.size());
  for (std::size_t i = 0; i < request.pages.size(); ++i) {
    const MemoryPage& page = request.pages[i];
    if (page.index != i) {
      throw std::invalid_argument(
          "memory page indexes must be contiguous and zero-based");
    }
    if (page.data.size() > config.max_page_bytes) {
      throw std::invalid_argument("memory page exceeds max_page_bytes");
    }
    if (page.data.size() >
        std::numeric_limits<std::uint64_t>::max() - total_bytes) {
      throw std::invalid_argument("memory image size overflows uint64");
    }
    total_bytes += page.data.size();
    if (total_bytes > config.max_total_bytes) {
      throw std::invalid_argument("memory image exceeds max_total_bytes");
    }
    if (!addresses.insert(page.guest_address).second) {
      throw std::invalid_argument("duplicate guest page address");
    }
  }
}

void validate_target_config(const TargetConfig& config) {
  validate_common(config.source_id, config.target_id, config.endpoint,
                  config.source_auth_key, config.target_auth_key,
                  config.max_pages, config.max_page_bytes,
                  config.max_total_bytes);
}

void validate_source_config(const SourceConfig& config) {
  validate_common(config.source_id, config.target_id, config.target_endpoint,
                  config.source_auth_key, config.target_auth_key,
                  config.max_pages, config.max_page_bytes,
                  config.max_total_bytes);
}

}  // namespace

struct TicketAuthority::Impl {
  struct Entry {
    SecretKey secret;
    TicketBinding binding;
    bool consumed{};
  };

  std::mutex mutex;
  std::unordered_map<std::string, Entry> tickets;
};

struct TicketAuthorityAccess {
  struct Lookup {
    SecretKey secret;
    TicketBinding binding;
    bool consumed{};
  };

  static std::optional<Lookup> find(TicketAuthority& authority,
                                    std::string_view id) {
    std::lock_guard lock(authority.impl_->mutex);
    const auto iterator = authority.impl_->tickets.find(std::string(id));
    if (iterator == authority.impl_->tickets.end()) {
      return std::nullopt;
    }
    return Lookup{iterator->second.secret, iterator->second.binding,
                  iterator->second.consumed};
  }

  static bool consume(TicketAuthority& authority, std::string_view id,
                      const TicketBinding& binding) {
    std::lock_guard lock(authority.impl_->mutex);
    const auto iterator = authority.impl_->tickets.find(std::string(id));
    if (iterator == authority.impl_->tickets.end() ||
        iterator->second.consumed ||
        !ticket_binding_matches(iterator->second.binding, binding)) {
      return false;
    }
    iterator->second.consumed = true;
    return true;
  }
};

SecretKey SecretKey::from_bytes(std::span<const Byte> input) {
  if (input.size() != 32) {
    throw std::invalid_argument("secret keys must be exactly 32 bytes");
  }
  SecretKey key;
  std::copy(input.begin(), input.end(), key.bytes.begin());
  return key;
}

SecretKey SecretKey::from_hex(std::string_view hex) {
  if (hex.size() != 64) {
    throw std::invalid_argument("secret keys must be 64 hexadecimal characters");
  }
  SecretKey key;
  const auto nibble = [](char character) -> unsigned {
    if (character >= '0' && character <= '9') {
      return static_cast<unsigned>(character - '0');
    }
    if (character >= 'a' && character <= 'f') {
      return static_cast<unsigned>(character - 'a' + 10);
    }
    if (character >= 'A' && character <= 'F') {
      return static_cast<unsigned>(character - 'A' + 10);
    }
    throw std::invalid_argument("invalid hexadecimal character");
  };
  for (std::size_t i = 0; i < 32; ++i) {
    const unsigned high = nibble(hex[2 * i]);
    const unsigned low = nibble(hex[2 * i + 1]);
    key.bytes[i] = static_cast<Byte>((high << 4U) | low);
  }
  return key;
}

std::string SecretKey::to_hex() const {
  return netsec::to_hex(bytes);
}

std::string to_hex(std::span<const Byte> bytes) {
  static constexpr char digits[] = "0123456789abcdef";
  std::string output;
  output.reserve(bytes.size() * 2U);
  for (const Byte byte : bytes) {
    output.push_back(digits[byte >> 4U]);
    output.push_back(digits[byte & 0x0fU]);
  }
  return output;
}

TicketAuthority::TicketAuthority() : impl_(std::make_shared<Impl>()) {}
TicketAuthority::~TicketAuthority() = default;
TicketAuthority::TicketAuthority(TicketAuthority&&) noexcept = default;
TicketAuthority& TicketAuthority::operator=(TicketAuthority&&) noexcept =
    default;

MigrationTicket TicketAuthority::issue(const TicketBinding& binding) {
  validate_binding(binding);
  MigrationTicket ticket;
  ticket.binding = binding;
  for (;;) {
    ticket.id = to_hex(detail::secure_random(16));
    const Bytes secret = detail::secure_random(32);
    std::copy(secret.begin(), secret.end(), ticket.secret.bytes.begin());
    std::lock_guard lock(impl_->mutex);
    if (impl_->tickets.find(ticket.id) == impl_->tickets.end()) {
      impl_->tickets.emplace(
          ticket.id, Impl::Entry{ticket.secret, ticket.binding, false});
      return ticket;
    }
  }
}

Digest compute_page_checksum(const VmId& vm_id, std::uint64_t generation,
                             const MemoryPage& page) {
  Bytes input;
  append_label(input, "NSM1 page-checksum");
  append_string(input, vm_id.value);
  append_u64(input, generation);
  append_u64(input, page.index);
  append_u64(input, page.guest_address);
  append_u64(input, page.data.size());
  append_bytes(input, page.data);
  return detail::sha256(input);
}

Digest compute_manifest_checksum(const VmId& vm_id, std::uint64_t generation,
                                 std::span<const MemoryPage> pages) {
  Bytes input;
  append_label(input, "NSM1 image-manifest");
  append_string(input, vm_id.value);
  append_u64(input, generation);
  append_u64(input, pages.size());
  for (const MemoryPage& page : pages) {
    append_u64(input, page.index);
    append_u64(input, page.guest_address);
    append_u64(input, page.data.size());
    const Digest page_digest =
        compute_page_checksum(vm_id, generation, page);
    append_bytes(input, page_digest);
  }
  return detail::sha256(input);
}

struct MigrationSource::Impl {
  explicit Impl(SourceConfig value) : config(std::move(value)) {
    validate_source_config(config);
  }

  SourceConfig config;
};

MigrationSource::MigrationSource(SourceConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {}
MigrationSource::~MigrationSource() = default;
MigrationSource::MigrationSource(MigrationSource&&) noexcept = default;
MigrationSource& MigrationSource::operator=(MigrationSource&&) noexcept =
    default;

MigrationResult MigrationSource::migrate(MigrationChannel& channel,
                                         const MigrationRequest& request) {
  MigrationResult result;
  try {
    validate_request(impl_->config, request);
    const SourceConfig& config = impl_->config;
    const Digest manifest =
        compute_manifest_checksum(request.vm_id, request.generation,
                                  request.pages);

    const std::array<Byte, 32> client_private = random_array<32>();
    const std::array<Byte, 32> client_public =
        detail::x25519(client_private, kX25519Basepoint);
    const std::array<Byte, 32> client_nonce = random_array<32>();

    const Bytes base =
        client_hello_base(config, request.vm_id, request.generation,
                          request.ticket, client_nonce, client_public);
    const Digest source_auth = source_proof(config.source_auth_key, base);
    const Digest ticket_auth =
        ticket_proof(request.ticket.secret, base, source_auth);

    Bytes hello = base;
    append_bytes(hello, source_auth);
    append_bytes(hello, ticket_auth);
    send_frame(channel, MessageType::client_hello, hello);

    const Frame server_frame = receive_frame(channel);
    expect_type(server_frame, MessageType::server_hello);
    if (server_frame.payload.size() < 32) {
      fail(ErrorCode::malformed_message, "truncated server hello");
    }
    Decoder server_decoder(server_frame.payload);
    if (server_decoder.u8() != kProtocolVersion) {
      fail(ErrorCode::malformed_message, "server hello version mismatch");
    }
    const std::string target_id =
        server_decoder.string(kMaxIdentitySize);
    const Endpoint target_endpoint = server_decoder.endpoint();
    const Digest echoed_client_nonce =
        digest_fixed(server_decoder.fixed(32));
    const std::array<Byte, 32> server_nonce =
        digest_fixed(server_decoder.fixed(32));
    const std::array<Byte, 32> server_public =
        digest_fixed(server_decoder.fixed(32));
    const Digest echoed_client_hash =
        digest_fixed(server_decoder.fixed(32));
    const Digest received_proof =
        digest_fixed(server_decoder.fixed(32));
    server_decoder.finish();

    const Bytes expected_server_base =
        server_hello_base(config.target_id, config.target_endpoint,
                          client_nonce, server_nonce, server_public, hello);
    const std::span<const Byte> received_base(
        server_frame.payload.data(), server_frame.payload.size() - 32);
    if (!detail::constant_time_equal(received_base, expected_server_base)) {
      fail(ErrorCode::malformed_message, "non-canonical server hello");
    }
    if (target_id != config.target_id.value) {
      fail(ErrorCode::authentication_failed,
           "migration channel reached the wrong target identity");
    }
    if (target_endpoint != config.target_endpoint) {
      fail(ErrorCode::endpoint_mismatch,
           "migration channel reached an address other than the configured target");
    }
    if (!detail::constant_time_equal(echoed_client_nonce, client_nonce)) {
      fail(ErrorCode::authentication_failed,
           "server hello did not echo this migration's nonce");
    }
    if (!detail::constant_time_equal(echoed_client_hash,
                                     detail::sha256(hello))) {
      fail(ErrorCode::authentication_failed,
           "server hello did not bind the client hello transcript");
    }
    const Digest expected_proof =
        target_proof(config.target_auth_key, expected_server_base);
    if (!detail::constant_time_equal(received_proof, expected_proof)) {
      fail(ErrorCode::authentication_failed,
           "target host authentication failed");
    }

    const SessionKeys keys = derive_keys(
        client_private, server_public, hello, server_frame.payload);

    Bytes finished;
    append_u8(finished, 0xc1U);
    append_string(finished, request.vm_id.value);
    append_u64(finished, request.generation);
    append_string(finished, request.ticket.id);
    append_bytes(finished, keys.transcript);
    const Bytes sealed_finished = seal_record(
        keys.client_to_target, keys.transcript, MessageType::client_finished,
        0, finished);
    send_frame(channel, MessageType::client_finished, sealed_finished);

    const Frame ready_frame = receive_frame(channel);
    expect_type(ready_frame, MessageType::ready);
    const Bytes ready_plaintext =
        open_record(keys.target_to_client, keys.transcript, MessageType::ready,
                    0, ready_frame.payload);
    Decoder ready_decoder(ready_plaintext);
    if (ready_decoder.u8() != 0xd1U ||
        ready_decoder.string(kMaxIdentitySize) != request.vm_id.value ||
        ready_decoder.u64() != request.generation ||
        ready_decoder.string(kMaxTicketIdSize) != request.ticket.id) {
      fail(ErrorCode::authentication_failed,
           "target session confirmation does not match this migration");
    }
    const Digest finished_echo =
        digest_fixed(ready_decoder.fixed(32));
    ready_decoder.finish();
    if (!detail::constant_time_equal(finished_echo,
                                     detail::sha256(finished))) {
      fail(ErrorCode::authentication_failed,
           "target did not confirm the source key exchange");
    }

    std::uint64_t sequence = 1;
    for (const MemoryPage& page : request.pages) {
      Bytes plaintext;
      append_u64(plaintext, page.index);
      append_u64(plaintext, page.guest_address);
      append_sized_bytes(plaintext, page.data);
      const Digest checksum =
          compute_page_checksum(request.vm_id, request.generation, page);
      append_bytes(plaintext, checksum);
      const Bytes sealed = seal_record(
          keys.client_to_target, keys.transcript, MessageType::page, sequence,
          plaintext);
      send_frame(channel, MessageType::page, sealed);
      ++sequence;
    }

    Bytes commit;
    append_u64(commit, request.pages.size());
    append_bytes(commit, manifest);
    const Bytes sealed_commit = seal_record(
        keys.client_to_target, keys.transcript, MessageType::commit, sequence,
        commit);
    send_frame(channel, MessageType::commit, sealed_commit);

    const Frame complete_frame = receive_frame(channel);
    expect_type(complete_frame, MessageType::complete);
    const Bytes complete_plaintext = open_record(
        keys.target_to_client, keys.transcript, MessageType::complete, 1,
        complete_frame.payload);
    Decoder complete_decoder(complete_plaintext);
    if (complete_decoder.u8() != 0xe1U ||
        complete_decoder.string(kMaxIdentitySize) != request.vm_id.value ||
        complete_decoder.u64() != request.generation ||
        complete_decoder.u64() != request.pages.size()) {
      fail(ErrorCode::completion_mismatch,
           "target completion confirmation does not match this migration");
    }
    const Digest acknowledged_manifest =
        digest_fixed(complete_decoder.fixed(32));
    complete_decoder.finish();
    if (!detail::constant_time_equal(acknowledged_manifest, manifest)) {
      fail(ErrorCode::manifest_mismatch,
           "target acknowledged a different memory manifest");
    }

    result.error = ErrorCode::none;
    result.manifest_checksum = manifest;
    return result;
  } catch (const ProtocolFailure& error) {
    result.error = error.code;
    result.detail = error.detail;
  } catch (const std::invalid_argument& error) {
    result.error = ErrorCode::invalid_request;
    result.detail = error.what();
  } catch (const std::exception& error) {
    result.error = ErrorCode::internal_error;
    result.detail = error.what();
  }
  return result;
}

struct MigrationTarget::Impl {
  Impl(TargetConfig value, std::shared_ptr<TicketAuthority> authority)
      : config(std::move(value)), tickets(std::move(authority)) {
    validate_target_config(config);
    if (!tickets) {
      throw std::invalid_argument("migration target requires a ticket authority");
    }
  }

  TargetConfig config;
  std::shared_ptr<TicketAuthority> tickets;
};

MigrationTarget::MigrationTarget(TargetConfig config,
                                 std::shared_ptr<TicketAuthority> tickets)
    : impl_(std::make_unique<Impl>(std::move(config), std::move(tickets))) {}
MigrationTarget::~MigrationTarget() = default;
MigrationTarget::MigrationTarget(MigrationTarget&&) noexcept = default;
MigrationTarget& MigrationTarget::operator=(MigrationTarget&&) noexcept =
    default;

TargetMigrationResult MigrationTarget::accept(MigrationChannel& channel) {
  TargetMigrationResult result;
  try {
    const TargetConfig& config = impl_->config;
    const Frame hello_frame = receive_frame(channel);
    expect_type(hello_frame, MessageType::client_hello);
    if (hello_frame.payload.size() < 64) {
      fail(ErrorCode::malformed_message, "truncated client hello");
    }
    const std::span<const Byte> hello_payload = hello_frame.payload;
    const Bytes hello_base(
        hello_payload.begin(),
        hello_payload.end() - static_cast<std::ptrdiff_t>(64));
    const Digest received_source_proof =
        digest_fixed(hello_payload.subspan(hello_payload.size() - 64, 32));
    const Digest received_ticket_proof =
        digest_fixed(hello_payload.last(32));

    Decoder hello_decoder(hello_base);
    if (hello_decoder.u8() != kProtocolVersion) {
      fail(ErrorCode::malformed_message, "client hello version mismatch");
    }
    const std::string source_id =
        hello_decoder.string(kMaxIdentitySize);
    const std::string target_id =
        hello_decoder.string(kMaxIdentitySize);
    const std::string vm_id = hello_decoder.string(kMaxIdentitySize);
    const std::uint64_t generation = hello_decoder.u64();
    const std::string ticket_id =
        hello_decoder.string(kMaxTicketIdSize);
    const Endpoint requested_endpoint = hello_decoder.endpoint();
    const Digest client_nonce = digest_fixed(hello_decoder.fixed(32));
    const std::array<Byte, 32> client_public =
        digest_fixed(hello_decoder.fixed(32));
    hello_decoder.finish();

    if (source_id != config.source_id.value) {
      fail(ErrorCode::authentication_failed,
           "client hello source identity is not trusted");
    }
    if (target_id != config.target_id.value) {
      fail(ErrorCode::authentication_failed,
           "client hello is addressed to a different target");
    }
    if (requested_endpoint != config.endpoint) {
      fail(ErrorCode::endpoint_mismatch,
           "migration request attempted to rewrite the configured target address");
    }
    const Digest expected_source_proof =
        source_proof(config.source_auth_key, hello_base);
    if (!detail::constant_time_equal(received_source_proof,
                                     expected_source_proof)) {
      fail(ErrorCode::authentication_failed,
           "source host authentication failed");
    }

    const TicketBinding binding{config.source_id, config.target_id,
                                VmId{vm_id}, generation};
    auto ticket = TicketAuthorityAccess::find(*impl_->tickets, ticket_id);
    if (!ticket) {
      fail(ErrorCode::ticket_unknown, "migration ticket is not recognized");
    }
    if (!ticket_binding_matches(ticket->binding, binding)) {
      fail(ErrorCode::binding_mismatch,
           "migration ticket is bound to different session metadata");
    }
    if (ticket->consumed) {
      fail(ErrorCode::ticket_consumed,
           "migration ticket has already been consumed");
    }
    const Digest expected_ticket_proof =
        ticket_proof(ticket->secret, hello_base, received_source_proof);
    if (!detail::constant_time_equal(received_ticket_proof,
                                     expected_ticket_proof)) {
      fail(ErrorCode::authentication_failed,
           "migration ticket proof failed");
    }
    if (!TicketAuthorityAccess::consume(*impl_->tickets, ticket_id, binding)) {
      fail(ErrorCode::ticket_consumed,
           "migration ticket was consumed by a concurrent session");
    }

    const std::array<Byte, 32> server_private = random_array<32>();
    const std::array<Byte, 32> server_public =
        detail::x25519(server_private, kX25519Basepoint);
    const std::array<Byte, 32> server_nonce = random_array<32>();
    const Bytes server_base =
        server_hello_base(config.target_id, config.endpoint, client_nonce,
                          server_nonce, server_public, hello_payload);
    const Digest server_auth = target_proof(config.target_auth_key, server_base);
    Bytes server_hello = server_base;
    append_bytes(server_hello, server_auth);
    send_frame(channel, MessageType::server_hello, server_hello);

    const SessionKeys keys = derive_keys(
        server_private, client_public, hello_payload, server_hello);

    const Frame finished_frame = receive_frame(channel);
    expect_type(finished_frame, MessageType::client_finished);
    const Bytes finished_plaintext =
        open_record(keys.client_to_target, keys.transcript,
                    MessageType::client_finished, 0, finished_frame.payload);
    Decoder finished_decoder(finished_plaintext);
    if (finished_decoder.u8() != 0xc1U ||
        finished_decoder.string(kMaxIdentitySize) != vm_id ||
        finished_decoder.u64() != generation ||
        finished_decoder.string(kMaxTicketIdSize) != ticket_id) {
      fail(ErrorCode::binding_mismatch,
           "source key confirmation session metadata mismatch");
    }
    const Digest received_transcript =
        digest_fixed(finished_decoder.fixed(32));
    finished_decoder.finish();
    if (!detail::constant_time_equal(received_transcript, keys.transcript)) {
      fail(ErrorCode::authentication_failed,
           "source key confirmation transcript mismatch");
    }

    Bytes ready;
    append_u8(ready, 0xd1U);
    append_string(ready, vm_id);
    append_u64(ready, generation);
    append_string(ready, ticket_id);
    append_bytes(ready, detail::sha256(finished_plaintext));
    const Bytes sealed_ready =
        seal_record(keys.target_to_client, keys.transcript, MessageType::ready,
                    0, ready);
    send_frame(channel, MessageType::ready, sealed_ready);

    std::vector<MemoryPage> staged;
    std::unordered_set<std::uint64_t> addresses;
    std::uint64_t total_bytes = 0;
    std::uint64_t expected_sequence = 1;
    Digest expected_manifest{};

    for (;;) {
      const Frame record_frame = receive_frame(channel);
      if (record_frame.type == MessageType::commit) {
        const Bytes commit_plaintext =
            open_record(keys.client_to_target, keys.transcript,
                        MessageType::commit, expected_sequence,
                        record_frame.payload);
        Decoder commit_decoder(commit_plaintext);
        const std::uint64_t page_count = commit_decoder.u64();
        expected_manifest =
            digest_fixed(commit_decoder.fixed(32));
        commit_decoder.finish();
        if (page_count != staged.size()) {
          fail(ErrorCode::manifest_mismatch,
               "target did not receive the complete page set");
        }
        const Digest actual_manifest = compute_manifest_checksum(
            VmId{vm_id}, generation, staged);
        if (!detail::constant_time_equal(expected_manifest,
                                         actual_manifest)) {
          fail(ErrorCode::manifest_mismatch,
               "memory image manifest verification failed");
        }
        break;
      }
      if (record_frame.type != MessageType::page) {
        fail(ErrorCode::malformed_message,
             "unexpected message while receiving memory pages");
      }

      const Bytes page_plaintext = open_record(
          keys.client_to_target, keys.transcript, MessageType::page,
          expected_sequence, record_frame.payload);
      ++expected_sequence;
      Decoder page_decoder(page_plaintext);
      const std::uint64_t index = page_decoder.u64();
      const std::uint64_t guest_address = page_decoder.u64();
      const std::span<const Byte> data =
          page_decoder.sized_bytes(config.max_page_bytes);
      const Digest received_checksum =
          digest_fixed(page_decoder.fixed(32));
      page_decoder.finish();

      if (index != staged.size()) {
        fail(ErrorCode::page_validation_failed,
             "memory page is missing, duplicated, or out of order");
      }
      if (staged.size() >= config.max_pages) {
        fail(ErrorCode::page_validation_failed,
             "memory image exceeds the configured page count limit");
      }
      if (data.size() > config.max_page_bytes) {
        fail(ErrorCode::page_validation_failed,
             "memory page exceeds the configured size limit");
      }
      if (data.size() >
          std::numeric_limits<std::uint64_t>::max() - total_bytes) {
        fail(ErrorCode::page_validation_failed,
             "memory image size overflows uint64");
      }
      total_bytes += data.size();
      if (total_bytes > config.max_total_bytes) {
        fail(ErrorCode::page_validation_failed,
             "memory image exceeds the configured total size limit");
      }
      if (!addresses.insert(guest_address).second) {
        fail(ErrorCode::page_validation_failed,
             "duplicate guest page address");
      }
      MemoryPage page;
      page.index = index;
      page.guest_address = guest_address;
      page.data.assign(data.begin(), data.end());
      const Digest expected_checksum =
          compute_page_checksum(VmId{vm_id}, generation, page);
      if (!detail::constant_time_equal(received_checksum,
                                       expected_checksum)) {
        fail(ErrorCode::page_validation_failed,
             "memory page checksum verification failed");
      }
      staged.push_back(std::move(page));
    }

    Bytes complete;
    append_u8(complete, 0xe1U);
    append_string(complete, vm_id);
    append_u64(complete, generation);
    append_u64(complete, staged.size());
    append_bytes(complete, expected_manifest);
    const Bytes sealed_complete =
        seal_record(keys.target_to_client, keys.transcript,
                    MessageType::complete, 1, complete);
    send_frame(channel, MessageType::complete, sealed_complete);

    MemoryImage image;
    image.vm_id = VmId{vm_id};
    image.generation = generation;
    image.manifest_checksum = expected_manifest;
    image.pages = std::move(staged);
    result.error = ErrorCode::none;
    result.committed = std::move(image);
    return result;
  } catch (const ProtocolFailure& error) {
    result.error = error.code;
    result.detail = error.detail;
  } catch (const std::invalid_argument& error) {
    result.error = ErrorCode::invalid_config;
    result.detail = error.what();
  } catch (const std::exception& error) {
    result.error = ErrorCode::internal_error;
    result.detail = error.what();
  }
  return result;
}

}  // namespace netsec

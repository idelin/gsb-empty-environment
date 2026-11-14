#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <chrono>
#include <cstring>
#include <limits>
#include <map>
#include <mutex>
#include <random>
#include <sstream>
#include <utility>

namespace netsec {
namespace {

constexpr std::size_t kMaxFieldSize = 1U << 20U;
constexpr std::size_t kMaxAuditEvents = 8192U;

std::span<const std::uint8_t> as_span(std::string_view text) {
  return {reinterpret_cast<const std::uint8_t*>(text.data()), text.size()};
}

Bytes as_bytes(std::string_view text) {
  return Bytes(as_span(text).begin(), as_span(text).end());
}

std::string lowercase_ascii(std::string_view text) {
  std::string out;
  out.reserve(text.size());
  for (char ch : text) {
    out.push_back(static_cast<char>(
        std::tolower(static_cast<unsigned char>(ch))));
  }
  return out;
}

bool valid_hex(std::string_view text, std::size_t expected_bytes = 0) {
  if (text.size() % 2U != 0U) {
    return false;
  }
  if (expected_bytes != 0U && text.size() != expected_bytes * 2U) {
    return false;
  }
  for (char ch : text) {
    if (std::isxdigit(static_cast<unsigned char>(ch)) == 0) {
      return false;
    }
  }
  return true;
}

bool constant_time_equal(std::span<const std::uint8_t> lhs,
                         std::span<const std::uint8_t> rhs) {
  if (lhs.size() != rhs.size()) {
    return false;
  }
  std::uint8_t diff = 0;
  for (std::size_t i = 0; i < lhs.size(); ++i) {
    diff = static_cast<std::uint8_t>(diff | (lhs[i] ^ rhs[i]));
  }
  return diff == 0;
}

void secure_zero(std::span<std::uint8_t> bytes) noexcept {
  volatile std::uint8_t* ptr = bytes.data();
  for (std::size_t i = 0; i < bytes.size(); ++i) {
    ptr[i] = 0;
  }
}

void put_u8(Bytes& out, std::uint8_t value) {
  out.push_back(value);
}

void put_u16(Bytes& out, std::uint16_t value) {
  out.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xFFU));
  out.push_back(static_cast<std::uint8_t>(value & 0xFFU));
}

void put_u32(Bytes& out, std::uint32_t value) {
  for (int shift = 24; shift >= 0; shift -= 8) {
    out.push_back(static_cast<std::uint8_t>((value >> shift) & 0xFFU));
  }
}

void put_u64(Bytes& out, std::uint64_t value) {
  for (int shift = 56; shift >= 0; shift -= 8) {
    out.push_back(static_cast<std::uint8_t>((value >> shift) & 0xFFU));
  }
}

void put_i64(Bytes& out, std::int64_t value) {
  put_u64(out, static_cast<std::uint64_t>(value));
}

void put_raw(Bytes& out, std::span<const std::uint8_t> value) {
  out.insert(out.end(), value.begin(), value.end());
}

void put_string(Bytes& out, std::string_view value) {
  if (value.size() > std::numeric_limits<std::uint32_t>::max()) {
    put_u32(out, 0);
    return;
  }
  put_u32(out, static_cast<std::uint32_t>(value.size()));
  put_raw(out, as_span(value));
}

class Reader {
 public:
  explicit Reader(std::span<const std::uint8_t> data) : data_(data) {}

  bool u8(std::uint8_t& value) {
    if (remaining() < 1U) {
      return false;
    }
    value = data_[pos_++];
    return true;
  }

  bool u16(std::uint16_t& value) {
    if (remaining() < 2U) {
      return false;
    }
    value = static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(data_[pos_]) << 8U) |
        static_cast<std::uint16_t>(data_[pos_ + 1U]));
    pos_ += 2U;
    return true;
  }

  bool u32(std::uint32_t& value) {
    if (remaining() < 4U) {
      return false;
    }
    value = 0;
    for (int i = 0; i < 4; ++i) {
      value = (value << 8U) | static_cast<std::uint32_t>(data_[pos_ + static_cast<std::size_t>(i)]);
    }
    pos_ += 4U;
    return true;
  }

  bool u64(std::uint64_t& value) {
    if (remaining() < 8U) {
      return false;
    }
    value = 0;
    for (int i = 0; i < 8; ++i) {
      value = (value << 8U) | static_cast<std::uint64_t>(data_[pos_ + static_cast<std::size_t>(i)]);
    }
    pos_ += 8U;
    return true;
  }

  bool i64(std::int64_t& value) {
    std::uint64_t raw = 0;
    if (!u64(raw)) {
      return false;
    }
    value = static_cast<std::int64_t>(raw);
    return true;
  }

  bool string(std::string& value) {
    std::uint32_t length = 0;
    if (!u32(length)) {
      return false;
    }
    if (length > kMaxFieldSize || static_cast<std::size_t>(length) > remaining()) {
      return false;
    }
    value.assign(reinterpret_cast<const char*>(data_.data() + pos_), length);
    pos_ += length;
    return true;
  }

  bool expect(std::string_view magic) {
    if (remaining() < magic.size()) {
      return false;
    }
    if (std::memcmp(data_.data() + pos_, magic.data(), magic.size()) != 0) {
      return false;
    }
    pos_ += magic.size();
    return true;
  }

  bool at_end() const { return pos_ == data_.size(); }

 private:
  std::size_t remaining() const { return data_.size() - pos_; }

  std::span<const std::uint8_t> data_;
  std::size_t pos_{0};
};

constexpr std::array<std::uint32_t, 64> kSha256Constants = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
    0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
    0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
    0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
    0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
    0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
    0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
    0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

std::uint32_t rotr32(std::uint32_t value, unsigned count) {
  return (value >> count) | (value << (32U - count));
}

class Sha256 {
 public:
  Sha256() = default;

  void update(std::span<const std::uint8_t> data) {
    total_bytes_ += data.size();
    std::size_t offset = 0;
    while (offset < data.size()) {
      const std::size_t take = std::min(64U - buffer_len_, data.size() - offset);
      std::memcpy(buffer_.data() + buffer_len_, data.data() + offset, take);
      buffer_len_ += take;
      offset += take;
      if (buffer_len_ == 64U) {
        transform(buffer_.data());
        buffer_len_ = 0;
      }
    }
  }

  std::array<std::uint8_t, 32> finish() {
    const std::uint64_t total_bits = total_bytes_ * 8U;
    buffer_[buffer_len_++] = 0x80U;
    if (buffer_len_ > 56U) {
      std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_len_),
                buffer_.end(), 0U);
      transform(buffer_.data());
      buffer_len_ = 0;
    }
    std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_len_),
              buffer_.begin() + 56, 0U);
    for (int i = 0; i < 8; ++i) {
      buffer_[56U + static_cast<std::size_t>(i)] =
          static_cast<std::uint8_t>((total_bits >> (56 - i * 8)) & 0xFFU);
    }
    transform(buffer_.data());

    std::array<std::uint8_t, 32> digest{};
    for (std::size_t i = 0; i < state_.size(); ++i) {
      digest[i * 4U] = static_cast<std::uint8_t>((state_[i] >> 24U) & 0xFFU);
      digest[i * 4U + 1U] =
          static_cast<std::uint8_t>((state_[i] >> 16U) & 0xFFU);
      digest[i * 4U + 2U] =
          static_cast<std::uint8_t>((state_[i] >> 8U) & 0xFFU);
      digest[i * 4U + 3U] = static_cast<std::uint8_t>(state_[i] & 0xFFU);
    }
    return digest;
  }

 private:
  static std::uint32_t load_be32(const std::uint8_t* data) {
    return (static_cast<std::uint32_t>(data[0]) << 24U) |
           (static_cast<std::uint32_t>(data[1]) << 16U) |
           (static_cast<std::uint32_t>(data[2]) << 8U) |
           static_cast<std::uint32_t>(data[3]);
  }

  void transform(const std::uint8_t* block) {
    std::array<std::uint32_t, 64> words{};
    for (std::size_t i = 0; i < 16; ++i) {
      words[i] = load_be32(block + i * 4U);
    }
    for (std::size_t i = 16; i < 64; ++i) {
      const std::uint32_t s0 = rotr32(words[i - 15U], 7U) ^
                               rotr32(words[i - 15U], 18U) ^
                               (words[i - 15U] >> 3U);
      const std::uint32_t s1 = rotr32(words[i - 2U], 17U) ^
                               rotr32(words[i - 2U], 19U) ^
                               (words[i - 2U] >> 10U);
      words[i] = words[i - 16U] + s0 + words[i - 7U] + s1;
    }

    std::uint32_t a = state_[0];
    std::uint32_t b = state_[1];
    std::uint32_t c = state_[2];
    std::uint32_t d = state_[3];
    std::uint32_t e = state_[4];
    std::uint32_t f = state_[5];
    std::uint32_t g = state_[6];
    std::uint32_t h = state_[7];

    for (std::size_t i = 0; i < 64; ++i) {
      const std::uint32_t sum1 =
          rotr32(e, 6U) ^ rotr32(e, 11U) ^ rotr32(e, 25U);
      const std::uint32_t choose = (e & f) ^ ((~e) & g);
      const std::uint32_t temp1 =
          h + sum1 + choose + kSha256Constants[i] + words[i];
      const std::uint32_t sum0 =
          rotr32(a, 2U) ^ rotr32(a, 13U) ^ rotr32(a, 22U);
      const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t temp2 = sum0 + majority;

      h = g;
      g = f;
      f = e;
      e = d + temp1;
      d = c;
      c = b;
      b = a;
      a = temp1 + temp2;
    }

    state_[0] += a;
    state_[1] += b;
    state_[2] += c;
    state_[3] += d;
    state_[4] += e;
    state_[5] += f;
    state_[6] += g;
    state_[7] += h;
  }

  std::array<std::uint32_t, 8> state_{
      0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
      0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};
  std::array<std::uint8_t, 64> buffer_{};
  std::size_t buffer_len_{0};
  std::uint64_t total_bytes_{0};
};

std::array<std::uint8_t, 32> sha256(std::span<const std::uint8_t> data) {
  Sha256 hash;
  hash.update(data);
  return hash.finish();
}

std::array<std::uint8_t, 32> hmac_sha256(
    std::span<const std::uint8_t> key, std::span<const std::uint8_t> data) {
  std::array<std::uint8_t, 64> normalized{};
  std::array<std::uint8_t, 32> hashed_key{};
  std::span<const std::uint8_t> use_key = key;
  if (key.size() > normalized.size()) {
    hashed_key = sha256(key);
    use_key = hashed_key;
  }

  std::array<std::uint8_t, 64> inner_pad{};
  std::array<std::uint8_t, 64> outer_pad{};
  for (std::size_t i = 0; i < normalized.size(); ++i) {
    normalized[i] = i < use_key.size() ? use_key[i] : 0U;
    inner_pad[i] = static_cast<std::uint8_t>(normalized[i] ^ 0x36U);
    outer_pad[i] = static_cast<std::uint8_t>(normalized[i] ^ 0x5cU);
  }

  Sha256 inner;
  inner.update(inner_pad);
  inner.update(data);
  const auto inner_digest = inner.finish();

  Sha256 outer;
  outer.update(outer_pad);
  outer.update(inner_digest);
  return outer.finish();
}

Bytes hkdf_sha256(std::span<const std::uint8_t> ikm,
                  std::span<const std::uint8_t> salt,
                  std::span<const std::uint8_t> info, std::size_t out_length) {
  if (out_length > 255U * 32U) {
    return {};
  }
  const std::array<std::uint8_t, 64> zero_salt{};
  const auto prk = hmac_sha256(salt.empty() ? std::span<const std::uint8_t>(zero_salt)
                                            : salt,
                               ikm);
  Bytes output;
  output.reserve(out_length);
  Bytes previous;
  std::uint8_t counter = 1;
  while (output.size() < out_length) {
    Bytes message = previous;
    put_raw(message, info);
    message.push_back(counter++);
    const auto block = hmac_sha256(prk, message);
    const std::size_t take = std::min(block.size(), out_length - output.size());
    output.insert(output.end(), block.begin(),
                  block.begin() + static_cast<std::ptrdiff_t>(take));
    previous.assign(block.begin(), block.end());
  }
  secure_zero(previous);
  return output;
}

std::uint32_t load_le32(const std::uint8_t* data) {
  return static_cast<std::uint32_t>(data[0]) |
         (static_cast<std::uint32_t>(data[1]) << 8U) |
         (static_cast<std::uint32_t>(data[2]) << 16U) |
         (static_cast<std::uint32_t>(data[3]) << 24U);
}

void store_le32(std::uint8_t* data, std::uint32_t value) {
  data[0] = static_cast<std::uint8_t>(value & 0xFFU);
  data[1] = static_cast<std::uint8_t>((value >> 8U) & 0xFFU);
  data[2] = static_cast<std::uint8_t>((value >> 16U) & 0xFFU);
  data[3] = static_cast<std::uint8_t>((value >> 24U) & 0xFFU);
}

std::uint32_t rotl32(std::uint32_t value, unsigned count) {
  return (value << count) | (value >> (32U - count));
}

void quarter_round(std::uint32_t& a, std::uint32_t& b, std::uint32_t& c,
                   std::uint32_t& d) {
  a += b;
  d ^= a;
  d = rotl32(d, 16U);
  c += d;
  b ^= c;
  b = rotl32(b, 12U);
  a += b;
  d ^= a;
  d = rotl32(d, 8U);
  c += d;
  b ^= c;
  b = rotl32(b, 7U);
}

std::array<std::uint8_t, 64> chacha20_block(
    const std::array<std::uint32_t, 8>& key, std::uint32_t counter,
    const std::array<std::uint32_t, 3>& nonce) {
  std::array<std::uint32_t, 16> state{
      0x61707865U, 0x3320646eU, 0x79622d32U, 0x6b206574U,
      key[0],      key[1],      key[2],      key[3],
      key[4],      key[5],      key[6],      key[7],
      counter,     nonce[0],    nonce[1],    nonce[2]};
  std::array<std::uint32_t, 16> working = state;

  for (int round = 0; round < 10; ++round) {
    quarter_round(working[0], working[4], working[8], working[12]);
    quarter_round(working[1], working[5], working[9], working[13]);
    quarter_round(working[2], working[6], working[10], working[14]);
    quarter_round(working[3], working[7], working[11], working[15]);
    quarter_round(working[0], working[5], working[10], working[15]);
    quarter_round(working[1], working[6], working[11], working[12]);
    quarter_round(working[2], working[7], working[8], working[13]);
    quarter_round(working[3], working[4], working[9], working[14]);
  }

  std::array<std::uint8_t, 64> output{};
  for (std::size_t i = 0; i < working.size(); ++i) {
    store_le32(output.data() + i * 4U, working[i] + state[i]);
  }
  return output;
}

Bytes chacha20_xor(std::span<const std::uint8_t> key,
                   std::span<const std::uint8_t> nonce,
                   std::uint32_t initial_counter,
                   std::span<const std::uint8_t> input) {
  if (key.size() != 32U || nonce.size() != 12U) {
    return {};
  }
  std::array<std::uint32_t, 8> key_words{};
  std::array<std::uint32_t, 3> nonce_words{};
  for (std::size_t i = 0; i < key_words.size(); ++i) {
    key_words[i] = load_le32(key.data() + i * 4U);
  }
  for (std::size_t i = 0; i < nonce_words.size(); ++i) {
    nonce_words[i] = load_le32(nonce.data() + i * 4U);
  }

  Bytes output(input.size());
  std::uint32_t counter = initial_counter;
  for (std::size_t offset = 0; offset < input.size(); offset += 64U) {
    const auto block = chacha20_block(key_words, counter++, nonce_words);
    const std::size_t take = std::min<std::size_t>(64U, input.size() - offset);
    for (std::size_t i = 0; i < take; ++i) {
      output[offset + i] =
          static_cast<std::uint8_t>(input[offset + i] ^ block[i]);
    }
  }
  return output;
}

bool is_known_method(AuthMethod method) {
  return method == AuthMethod::RdpStandard || method == AuthMethod::Tls ||
         method == AuthMethod::CredSsp;
}

std::optional<std::vector<AuthMethod>> canonical_methods(
    const std::vector<AuthMethod>& methods) {
  std::vector<AuthMethod> out;
  out.reserve(methods.size());
  for (AuthMethod method : methods) {
    if (!is_known_method(method)) {
      return std::nullopt;
    }
    out.push_back(method);
  }
  std::sort(out.begin(), out.end());
  out.erase(std::unique(out.begin(), out.end()), out.end());
  return out;
}

bool contains_method(const std::vector<AuthMethod>& methods, AuthMethod method) {
  return std::find(methods.begin(), methods.end(), method) != methods.end();
}

bool same_methods(const std::vector<AuthMethod>& lhs,
                  const std::vector<AuthMethod>& rhs) {
  return lhs == rhs;
}

std::optional<std::uint32_t> parse_ipv4(std::string_view text) {
  const std::size_t colon = text.find(':');
  if (colon != std::string_view::npos) {
    if (text.find(':', colon + 1U) != std::string_view::npos) {
      return std::nullopt;
    }
    text = text.substr(0, colon);
  }
  std::uint32_t value = 0;
  int octets = 0;
  std::size_t start = 0;
  while (start <= text.size()) {
    const std::size_t dot = text.find('.', start);
    const std::size_t end = dot == std::string_view::npos ? text.size() : dot;
    if (end == start || end - start > 3U) {
      return std::nullopt;
    }
    int octet = 0;
    for (std::size_t i = start; i < end; ++i) {
      const char ch = text[i];
      if (ch < '0' || ch > '9') {
        return std::nullopt;
      }
      octet = octet * 10 + (ch - '0');
    }
    if (octet > 255) {
      return std::nullopt;
    }
    value = (value << 8U) | static_cast<std::uint32_t>(octet);
    ++octets;
    if (dot == std::string_view::npos) {
      break;
    }
    start = dot + 1U;
  }
  if (octets != 4) {
    return std::nullopt;
  }
  return value;
}

bool parse_cidr(std::string_view text, std::uint32_t& network,
                unsigned& prefix) {
  const std::size_t slash = text.find('/');
  if (slash == std::string_view::npos) {
    return false;
  }
  auto address = parse_ipv4(text.substr(0, slash));
  if (!address.has_value()) {
    return false;
  }
  const std::string_view prefix_text = text.substr(slash + 1U);
  if (prefix_text.empty() || prefix_text.size() > 2U) {
    return false;
  }
  unsigned parsed = 0;
  for (char ch : prefix_text) {
    if (ch < '0' || ch > '9') {
      return false;
    }
    parsed = parsed * 10U + static_cast<unsigned>(ch - '0');
  }
  if (parsed > 32U) {
    return false;
  }
  const std::uint32_t mask =
      parsed == 0U ? 0U : (0xFFFFFFFFU << (32U - parsed));
  network = *address & mask;
  prefix = parsed;
  return true;
}

Bytes binding_common_fields(const NegotiationBinding& binding) {
  Bytes out;
  put_string(out, binding.server_id);
  put_string(out, binding.policy_digest);
  put_string(out, binding.certificate_sha256);
  put_u16(out, static_cast<std::uint16_t>(binding.offered_methods.size()));
  for (AuthMethod method : binding.offered_methods) {
    put_u16(out, static_cast<std::uint16_t>(method));
  }
  put_u16(out, binding.requested_version.major);
  put_u16(out, binding.requested_version.minor);
  put_u16(out, binding.negotiated_version.major);
  put_u16(out, binding.negotiated_version.minor);
  put_u16(out, static_cast<std::uint16_t>(binding.method));
  put_string(out, binding.peer_address);
  put_string(out, binding.client_nonce);
  put_string(out, binding.server_nonce);
  put_string(out, binding.session_id);
  put_i64(out, binding.issued_at);
  put_i64(out, binding.expires_at);
  return out;
}

Bytes ticket_transcript(const NegotiationBinding& binding) {
  Bytes out = as_bytes("NETSEC-TICKET-TRANSCRIPT-V1");
  put_raw(out, binding_common_fields(binding));
  return out;
}

Bytes binding_transcript(const NegotiationBinding& binding) {
  Bytes out = as_bytes("NETSEC-BINDING-TRANSCRIPT-V1");
  put_raw(out, binding_common_fields(binding));
  put_string(out, binding.ticket);
  return out;
}

std::array<std::uint8_t, 32> negotiation_key(
    std::span<const std::uint8_t> transport_exporter) {
  const auto salt = as_bytes("NETSEC-NEGOTIATION-KDF-SALT-V1");
  const auto info = as_bytes("NETSEC-NEGOTIATION-KEY-V1");
  const Bytes material = hkdf_sha256(transport_exporter, salt, info, 32);
  std::array<std::uint8_t, 32> key{};
  if (material.size() == key.size()) {
    std::copy(material.begin(), material.end(), key.begin());
  }
  return key;
}

std::string binding_mac(const NegotiationBinding& binding,
                        std::span<const std::uint8_t> transport_exporter) {
  const auto key = negotiation_key(transport_exporter);
  const auto transcript = binding_transcript(binding);
  return to_hex(hmac_sha256(key, transcript));
}

bool binding_mac_matches(const NegotiationBinding& binding,
                         std::span<const std::uint8_t> transport_exporter) {
  if (transport_exporter.size() < 32U || !valid_hex(binding.mac, 32U)) {
    return false;
  }
  const auto expected = from_hex(binding_mac(binding, transport_exporter));
  const auto actual = from_hex(binding.mac);
  return expected.has_value() && actual.has_value() &&
         constant_time_equal(*expected, *actual);
}

std::string make_ticket(const NegotiationBinding& binding,
                        std::span<const std::uint8_t> transport_exporter,
                        std::int64_t ticket_expires_at) {
  const auto key = negotiation_key(transport_exporter);
  Bytes transcript = ticket_transcript(binding);
  put_i64(transcript, ticket_expires_at);
  return to_hex(hmac_sha256(key, transcript));
}

Bytes credential_aad(const NegotiationBinding& binding,
                     std::span<const std::uint8_t> nonce,
                     std::uint64_t sequence) {
  Bytes out = as_bytes("NETSEC-CREDENTIAL-AAD-V1");
  put_string(out, binding.session_id);
  put_string(out, binding.client_nonce);
  put_string(out, binding.server_nonce);
  put_u64(out, sequence);
  put_raw(out, nonce);
  return out;
}

Bytes credential_plaintext(const CredentialPayload& credentials) {
  Bytes out = as_bytes("NETSEC-CREDENTIAL-PLAINTEXT-V1");
  put_string(out, credentials.domain);
  put_string(out, credentials.username);
  put_string(out, credentials.password);
  return out;
}

bool parse_credential_plaintext(std::span<const std::uint8_t> plaintext,
                                CredentialPayload& credentials) {
  Reader reader(plaintext);
  if (!reader.expect("NETSEC-CREDENTIAL-PLAINTEXT-V1")) {
    return false;
  }
  if (!reader.string(credentials.domain) ||
      !reader.string(credentials.username) ||
      !reader.string(credentials.password) || !reader.at_end()) {
    return false;
  }
  return true;
}

std::optional<std::array<std::uint8_t, 32>> credential_key_material(
    const NegotiationBinding& binding,
    std::span<const std::uint8_t> transport_exporter,
    std::span<const std::uint8_t> nonce, bool mac_part) {
  if (transport_exporter.size() < 32U || nonce.size() != 12U ||
      !valid_hex(binding.mac, 32U)) {
    return std::nullopt;
  }
  const auto salt = from_hex(binding.mac);
  if (!salt.has_value()) {
    return std::nullopt;
  }
  Bytes info = as_bytes("NETSEC-CREDENTIAL-KDF-V1");
  put_string(info, binding.session_id);
  put_string(info, binding.client_nonce);
  put_string(info, binding.server_nonce);
  put_raw(info, nonce);
  const Bytes material = hkdf_sha256(transport_exporter, *salt, info, 64);
  if (material.size() != 64U) {
    return std::nullopt;
  }
  std::array<std::uint8_t, 32> key{};
  const std::size_t offset = mac_part ? 32U : 0U;
  std::copy_n(material.begin() + static_cast<std::ptrdiff_t>(offset), 32,
              key.begin());
  return key;
}

Bytes secure_random_bytes(std::size_t length) {
  Bytes out(length);
  std::random_device device;
  std::size_t offset = 0;
  while (offset < length) {
    const std::uint32_t value = static_cast<std::uint32_t>(device());
    for (int i = 0; i < 4 && offset < length; ++i) {
      out[offset++] = static_cast<std::uint8_t>((value >> (i * 8)) & 0xFFU);
    }
  }
  return out;
}

Bytes random_bytes(const RandomSource& source, std::size_t length) {
  if (source) {
    Bytes value = source(length);
    if (value.size() == length) {
      return value;
    }
  }
  return secure_random_bytes(length);
}

std::int64_t system_now_unix() {
  return std::chrono::duration_cast<std::chrono::seconds>(
             std::chrono::system_clock::now().time_since_epoch())
      .count();
}

std::int64_t current_time(const ClockSource& source) {
  return source ? source() : system_now_unix();
}

Status validate_policy(const ServerPolicy& policy) {
  if (policy.server_id.empty() || policy.policy_revision.empty()) {
    return Status::RejectedPolicy;
  }
  if (policy.minimum_version > policy.maximum_version) {
    return Status::RejectedPolicy;
  }
  if (policy.allowed_methods.empty()) {
    return Status::RejectedPolicy;
  }
  const auto methods = canonical_methods(policy.allowed_methods);
  if (!methods.has_value()) {
    return Status::RejectedPolicy;
  }
  if (policy.require_nla &&
      !contains_method(*methods, AuthMethod::CredSsp)) {
    return Status::RejectedPolicy;
  }
  if (policy.pinned_certificate_sha256.empty()) {
    return Status::RejectedPolicy;
  }
  for (const std::string& fingerprint : policy.pinned_certificate_sha256) {
    if (!valid_hex(fingerprint, 32U)) {
      return Status::RejectedPolicy;
    }
  }
  if (policy.binding_lifetime.count() <= 0 ||
      policy.ticket_lifetime.count() <= 0) {
    return Status::RejectedPolicy;
  }
  for (const NetworkRule& rule : policy.isolated_networks) {
    std::uint32_t network = 0;
    unsigned prefix = 0;
    if (!parse_cidr(rule.cidr, network, prefix)) {
      return Status::RejectedPolicy;
    }
  }
  return Status::Ok;
}

std::string exporter_fingerprint(std::span<const std::uint8_t> exporter) {
  Bytes material = as_bytes("NETSEC-EXPORTER-BINDING-V1");
  put_raw(material, exporter);
  return sha256_hex(material);
}

bool same_hex(std::string_view lhs, std::string_view rhs) {
  if (lhs.size() != rhs.size() || lhs.size() % 2U != 0U) {
    return false;
  }
  auto left = from_hex(lhs);
  auto right = from_hex(rhs);
  return left.has_value() && right.has_value() &&
         constant_time_equal(*left, *right);
}

struct SessionRecord {
  NegotiationBinding binding;
  std::string exporter_binding;
  std::int64_t ticket_expires_at{0};
  bool credentials_opened{false};
};

void push_audit(std::vector<AuditEvent>& log, AuditEventType type,
                Status status, AuthMethod method, std::string_view peer,
                std::string_view session, std::string_view detail,
                std::int64_t timestamp) {
  AuditEvent event;
  event.type = type;
  event.status = status;
  event.method = method;
  event.peer_address = std::string(peer);
  event.session_id = std::string(session);
  event.detail = std::string(detail);
  event.timestamp = timestamp;
  log.push_back(std::move(event));
}

}  // namespace

std::string ProtocolVersion::to_string() const {
  std::ostringstream stream;
  stream << major << '.' << minor;
  return stream.str();
}

bool is_network_level_auth(AuthMethod method) noexcept {
  return method == AuthMethod::CredSsp;
}

bool is_weak_auth(AuthMethod method) noexcept {
  return method == AuthMethod::RdpStandard || method == AuthMethod::Tls;
}

int auth_strength(AuthMethod method) noexcept {
  return static_cast<int>(method);
}

std::string_view to_string(AuthMethod method) noexcept {
  switch (method) {
    case AuthMethod::None:
      return "none";
    case AuthMethod::RdpStandard:
      return "rdp_standard";
    case AuthMethod::Tls:
      return "tls";
    case AuthMethod::CredSsp:
      return "credssp";
  }
  return "unknown";
}

std::string_view to_string(Status status) noexcept {
  switch (status) {
    case Status::Ok:
      return "ok";
    case Status::RejectedMalformed:
      return "rejected_malformed";
    case Status::RejectedPolicy:
      return "rejected_policy";
    case Status::RejectedServerIdentity:
      return "rejected_server_identity";
    case Status::RejectedCertificate:
      return "rejected_certificate";
    case Status::RejectedProtocolVersion:
      return "rejected_protocol_version";
    case Status::RejectedNoCommonMethod:
      return "rejected_no_common_method";
    case Status::RejectedNlaRequired:
      return "rejected_nla_required";
    case Status::RejectedDowngrade:
      return "rejected_downgrade";
    case Status::RejectedWeakAuthDisabled:
      return "rejected_weak_auth_disabled";
    case Status::RejectedWeakNetwork:
      return "rejected_weak_network";
    case Status::RejectedReplay:
      return "rejected_replay";
    case Status::RejectedExpired:
      return "rejected_expired";
    case Status::RejectedBinding:
      return "rejected_binding";
    case Status::RejectedSession:
      return "rejected_session";
    case Status::RejectedTicket:
      return "rejected_ticket";
    case Status::RejectedCredentials:
      return "rejected_credentials";
    case Status::RejectedTransport:
      return "rejected_transport";
  }
  return "unknown";
}

std::string to_hex(std::span<const std::uint8_t> bytes) {
  static constexpr char kDigits[] = "0123456789abcdef";
  std::string out;
  out.resize(bytes.size() * 2U);
  for (std::size_t i = 0; i < bytes.size(); ++i) {
    out[i * 2U] = kDigits[(bytes[i] >> 4U) & 0x0FU];
    out[i * 2U + 1U] = kDigits[bytes[i] & 0x0FU];
  }
  return out;
}

std::optional<Bytes> from_hex(std::string_view hex) {
  if (!valid_hex(hex)) {
    return std::nullopt;
  }
  Bytes out;
  out.reserve(hex.size() / 2U);
  auto nibble = [](char ch) -> std::uint8_t {
    if (ch >= '0' && ch <= '9') {
      return static_cast<std::uint8_t>(ch - '0');
    }
    return static_cast<std::uint8_t>(
        std::tolower(static_cast<unsigned char>(ch)) - 'a' + 10);
  };
  for (std::size_t i = 0; i < hex.size(); i += 2U) {
    out.push_back(static_cast<std::uint8_t>(
        (nibble(hex[i]) << 4U) | nibble(hex[i + 1U])));
  }
  return out;
}

std::string sha256_hex(std::span<const std::uint8_t> data) {
  return to_hex(sha256(data));
}

std::string fingerprint_certificate(std::span<const std::uint8_t> der) {
  return sha256_hex(der);
}

bool cidr_contains(std::string_view cidr, std::string_view ipv4_address) noexcept {
  std::uint32_t network = 0;
  unsigned prefix = 0;
  if (!parse_cidr(cidr, network, prefix)) {
    return false;
  }
  const auto address = parse_ipv4(ipv4_address);
  if (!address.has_value()) {
    return false;
  }
  const std::uint32_t mask =
      prefix == 0U ? 0U : (0xFFFFFFFFU << (32U - prefix));
  return (*address & mask) == network;
}

std::string policy_digest(const ServerPolicy& policy) {
  Bytes out = as_bytes("NETSEC-POLICY-DIGEST-V1");
  put_string(out, policy.server_id);
  put_string(out, policy.policy_revision);
  put_u16(out, policy.minimum_version.major);
  put_u16(out, policy.minimum_version.minor);
  put_u16(out, policy.maximum_version.major);
  put_u16(out, policy.maximum_version.minor);
  put_u8(out, policy.require_nla ? 1U : 0U);
  put_u8(out, policy.reject_downgrade ? 1U : 0U);
  put_u8(out, policy.allow_weak ? 1U : 0U);

  auto methods = policy.allowed_methods;
  std::sort(methods.begin(), methods.end());
  methods.erase(std::unique(methods.begin(), methods.end()), methods.end());
  put_u16(out, static_cast<std::uint16_t>(methods.size()));
  for (AuthMethod method : methods) {
    put_u16(out, static_cast<std::uint16_t>(method));
  }

  auto networks = policy.isolated_networks;
  std::sort(networks.begin(), networks.end(),
            [](const NetworkRule& lhs, const NetworkRule& rhs) {
              if (lhs.cidr != rhs.cidr) {
                return lhs.cidr < rhs.cidr;
              }
              return lhs.isolated < rhs.isolated;
            });
  put_u16(out, static_cast<std::uint16_t>(networks.size()));
  for (const NetworkRule& rule : networks) {
    put_string(out, rule.cidr);
    put_u8(out, rule.isolated ? 1U : 0U);
  }

  auto pins = policy.pinned_certificate_sha256;
  for (std::string& pin : pins) {
    pin = lowercase_ascii(pin);
  }
  std::sort(pins.begin(), pins.end());
  pins.erase(std::unique(pins.begin(), pins.end()), pins.end());
  put_u16(out, static_cast<std::uint16_t>(pins.size()));
  for (const std::string& pin : pins) {
    put_string(out, pin);
  }
  put_i64(out, static_cast<std::int64_t>(policy.binding_lifetime.count()));
  put_i64(out, static_cast<std::int64_t>(policy.ticket_lifetime.count()));
  return sha256_hex(out);
}

Status verify_binding(const NegotiationBinding& binding,
                      const ClientExpectations& expectations,
                      std::int64_t now_unix) {
  if (expectations.transport_exporter.size() < 32U ||
      !valid_hex(binding.mac, 32U) || binding.server_id.empty() ||
      binding.session_id.empty() || binding.method == AuthMethod::None ||
      !is_known_method(binding.method)) {
    return Status::RejectedBinding;
  }
  if (binding.server_id != expectations.server_id) {
    return Status::RejectedServerIdentity;
  }
  if (!same_hex(binding.policy_digest, expectations.policy_digest)) {
    return Status::RejectedPolicy;
  }
  if (!same_hex(binding.certificate_sha256,
                expectations.observed_certificate_sha256)) {
    return Status::RejectedCertificate;
  }
  const auto expected_methods = canonical_methods(expectations.offered_methods);
  if (!expected_methods.has_value() ||
      !same_methods(binding.offered_methods, *expected_methods)) {
    return Status::RejectedDowngrade;
  }
  if (binding.requested_version != expectations.offered_version ||
      binding.negotiated_version != expectations.offered_version) {
    return Status::RejectedProtocolVersion;
  }
  if (!same_hex(binding.client_nonce, expectations.client_nonce)) {
    return Status::RejectedBinding;
  }
  if (expectations.requires_nla &&
      !is_network_level_auth(binding.method)) {
    return Status::RejectedNlaRequired;
  }
  if (auth_strength(binding.method) < auth_strength(expectations.minimum_method)) {
    return Status::RejectedDowngrade;
  }
  if (binding.method == AuthMethod::CredSsp &&
      !valid_hex(binding.ticket, 32U)) {
    return Status::RejectedTicket;
  }
  if (is_weak_auth(binding.method) && !binding.ticket.empty()) {
    return Status::RejectedBinding;
  }
  const std::int64_t now = now_unix == 0 ? system_now_unix() : now_unix;
  if (binding.expires_at < now) {
    return Status::RejectedExpired;
  }
  if (!binding_mac_matches(binding, expectations.transport_exporter)) {
    return Status::RejectedBinding;
  }
  return Status::Ok;
}

CredentialSealResult seal_credentials(const NegotiationBinding& binding,
                                      std::span<const std::uint8_t> transport_exporter,
                                      const CredentialPayload& credentials,
                                      std::uint64_t sequence,
                                      std::int64_t now_unix) {
  CredentialSealResult result;
  if (transport_exporter.size() < 32U) {
    result.status = Status::RejectedTransport;
    result.detail = "transport exporter is too short";
    return result;
  }
  if (!binding_mac_matches(binding, transport_exporter)) {
    result.status = Status::RejectedBinding;
    result.detail = "negotiation binding MAC is invalid";
    return result;
  }
  const std::int64_t now = now_unix == 0 ? system_now_unix() : now_unix;
  if (binding.expires_at < now) {
    result.status = Status::RejectedExpired;
    result.detail = "negotiation binding has expired";
    return result;
  }

  Bytes nonce = secure_random_bytes(12);
  const auto encryption_key =
      credential_key_material(binding, transport_exporter, nonce, false);
  const auto mac_key =
      credential_key_material(binding, transport_exporter, nonce, true);
  if (!encryption_key.has_value() || !mac_key.has_value()) {
    result.status = Status::RejectedBinding;
    result.detail = "credential key derivation failed";
    return result;
  }

  Bytes plaintext = credential_plaintext(credentials);
  Bytes ciphertext =
      chacha20_xor(*encryption_key, nonce, 1U, plaintext);
  secure_zero(plaintext);

  Bytes authenticated = credential_aad(binding, nonce, sequence);
  put_raw(authenticated, ciphertext);
  const auto tag = hmac_sha256(*mac_key, authenticated);

  SealedCredentials envelope;
  envelope.session_id = binding.session_id;
  envelope.nonce = to_hex(nonce);
  envelope.ciphertext = to_hex(ciphertext);
  envelope.tag = to_hex(tag);
  envelope.sequence = sequence;
  result.status = Status::Ok;
  result.envelope = std::move(envelope);
  return result;
}

Bytes serialize_binding(const NegotiationBinding& binding) {
  Bytes out = as_bytes("NSB1");
  put_u16(out, 1U);
  put_string(out, binding.server_id);
  put_string(out, binding.policy_digest);
  put_string(out, binding.certificate_sha256);
  put_u16(out, static_cast<std::uint16_t>(binding.offered_methods.size()));
  for (AuthMethod method : binding.offered_methods) {
    put_u16(out, static_cast<std::uint16_t>(method));
  }
  put_u16(out, binding.requested_version.major);
  put_u16(out, binding.requested_version.minor);
  put_u16(out, binding.negotiated_version.major);
  put_u16(out, binding.negotiated_version.minor);
  put_u16(out, static_cast<std::uint16_t>(binding.method));
  put_string(out, binding.peer_address);
  put_string(out, binding.client_nonce);
  put_string(out, binding.server_nonce);
  put_string(out, binding.session_id);
  put_string(out, binding.ticket);
  put_i64(out, binding.issued_at);
  put_i64(out, binding.expires_at);
  put_string(out, binding.mac);
  return out;
}

std::optional<NegotiationBinding> parse_binding(
    std::span<const std::uint8_t> wire) {
  Reader reader(wire);
  if (!reader.expect("NSB1")) {
    return std::nullopt;
  }
  std::uint16_t version = 0;
  if (!reader.u16(version) || version != 1U) {
    return std::nullopt;
  }
  NegotiationBinding binding;
  if (!reader.string(binding.server_id) ||
      !reader.string(binding.policy_digest) ||
      !reader.string(binding.certificate_sha256)) {
    return std::nullopt;
  }
  std::uint16_t method_count = 0;
  if (!reader.u16(method_count) || method_count > 16U) {
    return std::nullopt;
  }
  for (std::uint16_t i = 0; i < method_count; ++i) {
    std::uint16_t raw = 0;
    if (!reader.u16(raw)) {
      return std::nullopt;
    }
    const auto method = static_cast<AuthMethod>(raw);
    if (!is_known_method(method)) {
      return std::nullopt;
    }
    binding.offered_methods.push_back(method);
  }
  if (!reader.u16(binding.requested_version.major) ||
      !reader.u16(binding.requested_version.minor) ||
      !reader.u16(binding.negotiated_version.major) ||
      !reader.u16(binding.negotiated_version.minor)) {
    return std::nullopt;
  }
  std::uint16_t raw_method = 0;
  if (!reader.u16(raw_method)) {
    return std::nullopt;
  }
  binding.method = static_cast<AuthMethod>(raw_method);
  if (binding.method != AuthMethod::None && !is_known_method(binding.method)) {
    return std::nullopt;
  }
  if (!reader.string(binding.peer_address) ||
      !reader.string(binding.client_nonce) ||
      !reader.string(binding.server_nonce) ||
      !reader.string(binding.session_id) ||
      !reader.string(binding.ticket) || !reader.i64(binding.issued_at) ||
      !reader.i64(binding.expires_at) || !reader.string(binding.mac) ||
      !reader.at_end()) {
    return std::nullopt;
  }
  return binding;
}

Bytes serialize_credentials(const SealedCredentials& envelope) {
  Bytes out = as_bytes("NSC1");
  put_u16(out, 1U);
  put_string(out, envelope.session_id);
  put_string(out, envelope.nonce);
  put_string(out, envelope.ciphertext);
  put_string(out, envelope.tag);
  put_u64(out, envelope.sequence);
  return out;
}

std::optional<SealedCredentials> parse_credentials(
    std::span<const std::uint8_t> wire) {
  Reader reader(wire);
  if (!reader.expect("NSC1")) {
    return std::nullopt;
  }
  std::uint16_t version = 0;
  if (!reader.u16(version) || version != 1U) {
    return std::nullopt;
  }
  SealedCredentials envelope;
  if (!reader.string(envelope.session_id) || !reader.string(envelope.nonce) ||
      !reader.string(envelope.ciphertext) || !reader.string(envelope.tag) ||
      !reader.u64(envelope.sequence) || !reader.at_end()) {
    return std::nullopt;
  }
  return envelope;
}

std::string_view to_string(AuditEventType type) noexcept {
  switch (type) {
    case AuditEventType::NegotiationAccepted:
      return "negotiation_accepted";
    case AuditEventType::NegotiationRejected:
      return "negotiation_rejected";
    case AuditEventType::WeakAuthAccepted:
      return "weak_auth_accepted";
    case AuditEventType::WeakAuthRejected:
      return "weak_auth_rejected";
    case AuditEventType::CredentialsAccepted:
      return "credentials_accepted";
    case AuditEventType::CredentialsRejected:
      return "credentials_rejected";
    case AuditEventType::TicketAccepted:
      return "ticket_accepted";
    case AuditEventType::TicketRejected:
      return "ticket_rejected";
    case AuditEventType::SessionResumed:
      return "session_resumed";
    case AuditEventType::ReplayRejected:
      return "replay_rejected";
  }
  return "unknown";
}

std::string format_audit_event(const AuditEvent& event) {
  std::ostringstream stream;
  stream << "ts=" << event.timestamp << " type=" << to_string(event.type)
         << " status=" << to_string(event.status)
         << " method=" << to_string(event.method)
         << " peer=" << event.peer_address
         << " session=" << event.session_id << " detail=" << event.detail;
  return stream.str();
}

struct Authenticator::Impl {
  ServerPolicy policy;
  Status policy_status{Status::RejectedPolicy};
  std::vector<AuthMethod> allowed_methods;
  std::vector<std::string> certificate_pins;
  RandomSource random;
  ClockSource now_unix;

  mutable std::mutex mutex;
  std::map<std::string, SessionRecord> sessions;
  std::map<std::string, std::int64_t> seen_nonces;
  std::vector<AuditEvent> audit;

  void audit_event(AuditEventType type, Status status, AuthMethod method,
                   std::string_view peer, std::string_view session,
                   std::string_view detail, std::int64_t timestamp) {
    if (audit.size() >= kMaxAuditEvents) {
      audit.erase(audit.begin());
    }
    push_audit(audit, type, status, method, peer, session, detail, timestamp);
  }
};

Authenticator::Authenticator(ServerPolicy policy, AuthenticatorOptions options)
    : impl_(std::make_unique<Impl>()) {
  impl_->policy_status = validate_policy(policy);
  impl_->policy = std::move(policy);
  impl_->random = std::move(options.random);
  impl_->now_unix = std::move(options.now_unix);
  if (const auto methods = canonical_methods(impl_->policy.allowed_methods);
      methods.has_value()) {
    impl_->allowed_methods = *methods;
  }
  impl_->certificate_pins.reserve(
      impl_->policy.pinned_certificate_sha256.size());
  for (const std::string& pin : impl_->policy.pinned_certificate_sha256) {
    impl_->certificate_pins.push_back(lowercase_ascii(pin));
  }
}

Authenticator::~Authenticator() = default;

Authenticator::Authenticator(Authenticator&&) noexcept = default;

Authenticator& Authenticator::operator=(Authenticator&&) noexcept = default;

const ServerPolicy& Authenticator::policy() const noexcept {
  return impl_->policy;
}

Status Authenticator::policy_status() const noexcept {
  return impl_->policy_status;
}

NegotiationResult Authenticator::negotiate(const ClientHello& hello,
                                           const TransportContext& transport) {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  const std::int64_t now = current_time(impl_->now_unix);
  auto reject = [&](Status status, AuthMethod method, std::string detail,
                    bool weak_rejection) {
    impl_->audit_event(AuditEventType::NegotiationRejected, status, method,
                       transport.peer_address, {}, detail, now);
    if (weak_rejection) {
      impl_->audit_event(AuditEventType::WeakAuthRejected, status, method,
                         transport.peer_address, {}, detail, now);
    }
    NegotiationResult result;
    result.status = status;
    result.detail = std::move(detail);
    return result;
  };

  if (impl_->policy_status != Status::Ok) {
    return reject(Status::RejectedPolicy, AuthMethod::None,
                  "server authentication policy is invalid", false);
  }
  if (transport.transport_exporter.size() < 32U) {
    return reject(Status::RejectedTransport, AuthMethod::None,
                  "transport keying material is missing or too short", false);
  }
  if (transport.presented_certificate.empty()) {
    return reject(Status::RejectedCertificate, AuthMethod::None,
                  "presented certificate is missing", false);
  }
  if (hello.server_id != impl_->policy.server_id) {
    return reject(Status::RejectedServerIdentity, AuthMethod::None,
                  "client requested a different server identity", false);
  }
  if (!valid_hex(hello.client_nonce) || hello.client_nonce.size() < 32U ||
      hello.client_nonce.size() > 256U) {
    return reject(Status::RejectedMalformed, AuthMethod::None,
                  "client nonce is missing or malformed", false);
  }
  const std::string normalized_nonce = lowercase_ascii(hello.client_nonce);
  if (hello.version < impl_->policy.minimum_version ||
      hello.version > impl_->policy.maximum_version) {
    return reject(Status::RejectedProtocolVersion, AuthMethod::None,
                  "requested protocol version is outside policy bounds", false);
  }

  for (auto it = impl_->sessions.begin(); it != impl_->sessions.end();) {
    const SessionRecord& record = it->second;
    if (record.binding.expires_at < now && record.ticket_expires_at < now) {
      it = impl_->sessions.erase(it);
    } else {
      ++it;
    }
  }
  const auto offered = canonical_methods(hello.offered_methods);
  if (!offered.has_value() || offered->empty()) {
    return reject(Status::RejectedMalformed, AuthMethod::None,
                  "offered authentication methods are malformed", false);
  }

  const std::string actual_fingerprint =
      fingerprint_certificate(transport.presented_certificate);
  const bool pinned = std::find(impl_->certificate_pins.begin(),
                                impl_->certificate_pins.end(),
                                actual_fingerprint) !=
                      impl_->certificate_pins.end();
  if (!pinned) {
    return reject(Status::RejectedCertificate, AuthMethod::None,
                  "presented certificate does not match the configured pin",
                  false);
  }
  if (!hello.expected_certificate_sha256.empty()) {
    if (!valid_hex(hello.expected_certificate_sha256, 32U) ||
        !same_hex(lowercase_ascii(hello.expected_certificate_sha256),
                  actual_fingerprint)) {
      return reject(Status::RejectedCertificate, AuthMethod::None,
                    "client observed a different certificate", false);
    }
  }

  for (auto it = impl_->seen_nonces.begin(); it != impl_->seen_nonces.end();) {
    if (it->second < now) {
      it = impl_->seen_nonces.erase(it);
    } else {
      ++it;
    }
  }
  if (impl_->seen_nonces.find(normalized_nonce) != impl_->seen_nonces.end()) {
    impl_->audit_event(AuditEventType::ReplayRejected, Status::RejectedReplay,
                       AuthMethod::None, transport.peer_address, {},
                       "client nonce was already used", now);
    return reject(Status::RejectedReplay, AuthMethod::None,
                  "client nonce was already used", false);
  }

  std::vector<AuthMethod> mutual;
  for (AuthMethod method : *offered) {
    if (contains_method(impl_->allowed_methods, method)) {
      mutual.push_back(method);
    }
  }
  if (mutual.empty()) {
    if (impl_->policy.require_nla || hello.requires_nla) {
      return reject(Status::RejectedNlaRequired, AuthMethod::None,
                    "no network-level authentication method is available",
                    false);
    }
    return reject(Status::RejectedNoCommonMethod, AuthMethod::None,
                  "no mutually supported authentication method", false);
  }

  AuthMethod selected = *std::max_element(
      mutual.begin(), mutual.end(),
      [](AuthMethod lhs, AuthMethod rhs) {
        return auth_strength(lhs) < auth_strength(rhs);
      });

  if (impl_->policy.require_nla &&
      !is_network_level_auth(selected)) {
    return reject(Status::RejectedNlaRequired, selected,
                  "network-level authentication is mandatory", false);
  }
  if (impl_->policy.reject_downgrade &&
      contains_method(*offered, AuthMethod::CredSsp) &&
      selected != AuthMethod::CredSsp) {
    return reject(Status::RejectedDowngrade, selected,
                  "server refused to downgrade a CredSSP-capable client",
                  false);
  }
  if (hello.requires_nla && !is_network_level_auth(selected)) {
    return reject(Status::RejectedNlaRequired, selected,
                  "client requires network-level authentication", false);
  }

  if (is_weak_auth(selected)) {
    if (!impl_->policy.allow_weak) {
      return reject(Status::RejectedWeakAuthDisabled, selected,
                    "weak authentication is disabled by policy", true);
    }
    bool isolated_network = false;
    for (const NetworkRule& rule : impl_->policy.isolated_networks) {
      if (rule.isolated && cidr_contains(rule.cidr, transport.peer_address)) {
        isolated_network = true;
        break;
      }
    }
    if (!isolated_network) {
      return reject(Status::RejectedWeakNetwork, selected,
                    "weak authentication is not allowed on this network", true);
    }
  }

  NegotiationBinding binding;
  binding.server_id = impl_->policy.server_id;
  binding.policy_digest = policy_digest(impl_->policy);
  binding.certificate_sha256 = actual_fingerprint;
  binding.offered_methods = *offered;
  binding.requested_version = hello.version;
  binding.negotiated_version = hello.version;
  binding.method = selected;
  binding.peer_address = transport.peer_address;
  binding.client_nonce = normalized_nonce;
  binding.server_nonce =
      to_hex(random_bytes(impl_->random, 32U));
  binding.session_id = to_hex(random_bytes(impl_->random, 16U));
  binding.issued_at = now;
  binding.expires_at = now + impl_->policy.binding_lifetime.count();

  const std::int64_t ticket_expires_at =
      now + impl_->policy.ticket_lifetime.count();
  if (selected == AuthMethod::CredSsp) {
    binding.ticket =
        make_ticket(binding, transport.transport_exporter, ticket_expires_at);
  }
  binding.mac = binding_mac(binding, transport.transport_exporter);

  SessionRecord record;
  record.binding = binding;
  record.exporter_binding =
      exporter_fingerprint(transport.transport_exporter);
  record.ticket_expires_at = ticket_expires_at;
  impl_->sessions[binding.session_id] = std::move(record);
  impl_->seen_nonces[normalized_nonce] =
      std::max(binding.expires_at, ticket_expires_at);

  impl_->audit_event(AuditEventType::NegotiationAccepted, Status::Ok, selected,
                     binding.peer_address, binding.session_id,
                     "authentication method negotiated", now);
  if (is_weak_auth(selected)) {
    impl_->audit_event(AuditEventType::WeakAuthAccepted, Status::Ok, selected,
                       binding.peer_address, binding.session_id,
                       "weak authentication accepted on isolated network", now);
  }

  NegotiationResult result;
  result.status = Status::Ok;
  result.binding = std::move(binding);
  return result;
}

bool Authenticator::verify_ticket(std::string_view ticket,
                                  std::string_view session_id,
                                  std::string_view certificate_sha256) const {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  const auto it = impl_->sessions.find(std::string(session_id));
  if (it == impl_->sessions.end()) {
    return false;
  }
  const SessionRecord& record = it->second;
  if (record.binding.method != AuthMethod::CredSsp ||
      record.binding.ticket.empty() || ticket.size() != 64U ||
      !same_hex(ticket, record.binding.ticket)) {
    return false;
  }
  if (!same_hex(record.binding.certificate_sha256, certificate_sha256)) {
    return false;
  }
  return current_time(impl_->now_unix) <= record.ticket_expires_at;
}

std::optional<SessionInfo> Authenticator::resume_session(
    std::string_view ticket, std::string_view session_id) {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  const std::int64_t now = current_time(impl_->now_unix);
  const auto it = impl_->sessions.find(std::string(session_id));
  if (it == impl_->sessions.end()) {
    impl_->audit_event(AuditEventType::TicketRejected, Status::RejectedSession,
                       AuthMethod::None, {}, session_id,
                       "session does not exist", now);
    return std::nullopt;
  }
  const SessionRecord& record = it->second;
  if (record.binding.method != AuthMethod::CredSsp ||
      record.binding.ticket.empty() || ticket.size() != 64U ||
      !same_hex(ticket, record.binding.ticket)) {
    impl_->audit_event(AuditEventType::TicketRejected, Status::RejectedTicket,
                       record.binding.method, record.binding.peer_address,
                       record.binding.session_id,
                       "ticket does not belong to this session", now);
    return std::nullopt;
  }
  if (now > record.ticket_expires_at) {
    impl_->audit_event(AuditEventType::TicketRejected, Status::RejectedExpired,
                       record.binding.method, record.binding.peer_address,
                       record.binding.session_id, "ticket has expired", now);
    return std::nullopt;
  }
  impl_->audit_event(AuditEventType::TicketAccepted, Status::Ok,
                     record.binding.method, record.binding.peer_address,
                     record.binding.session_id, "ticket accepted", now);
  impl_->audit_event(AuditEventType::SessionResumed, Status::Ok,
                     record.binding.method, record.binding.peer_address,
                     record.binding.session_id,
                     "CredSSP session resumed", now);
  SessionInfo info;
  info.session_id = record.binding.session_id;
  info.method = record.binding.method;
  info.version = record.binding.negotiated_version;
  info.certificate_sha256 = record.binding.certificate_sha256;
  info.expires_at = record.ticket_expires_at;
  return info;
}

CredentialOpenResult Authenticator::open_credentials(
    const NegotiationBinding& binding, const SealedCredentials& envelope,
    std::span<const std::uint8_t> transport_exporter) {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  const std::int64_t now = current_time(impl_->now_unix);
  auto reject = [&](Status status, std::string detail) {
    impl_->audit_event(AuditEventType::CredentialsRejected, status,
                       binding.method, binding.peer_address,
                       binding.session_id, detail, now);
    CredentialOpenResult result;
    result.status = status;
    result.detail = std::move(detail);
    return result;
  };

  if (transport_exporter.size() < 32U) {
    return reject(Status::RejectedTransport,
                  "transport keying material is missing or too short");
  }
  if (!binding_mac_matches(binding, transport_exporter)) {
    return reject(Status::RejectedBinding,
                  "negotiation binding MAC is invalid");
  }
  if (now > binding.expires_at) {
    return reject(Status::RejectedExpired,
                  "negotiation binding has expired");
  }
  const auto it = impl_->sessions.find(binding.session_id);
  if (it == impl_->sessions.end()) {
    return reject(Status::RejectedSession, "session does not exist");
  }
  SessionRecord& record = it->second;
  const std::string current_exporter =
      exporter_fingerprint(transport_exporter);
  if (!same_hex(record.exporter_binding, current_exporter) ||
      !same_hex(record.binding.mac, binding.mac) ||
      !same_hex(record.binding.certificate_sha256,
                binding.certificate_sha256) ||
      record.binding.method != binding.method ||
      record.binding.negotiated_version != binding.negotiated_version ||
      !same_hex(record.binding.client_nonce, binding.client_nonce)) {
    return reject(Status::RejectedSession,
                  "binding does not match the established session");
  }
  if (record.binding.method == AuthMethod::CredSsp &&
      (!same_hex(record.binding.ticket, binding.ticket) ||
       binding.ticket.empty())) {
    return reject(Status::RejectedTicket,
                  "CredSSP ticket does not match the session");
  }
  if (record.credentials_opened) {
    return reject(Status::RejectedReplay,
                  "credentials were already submitted for this session");
  }
  if (envelope.session_id != binding.session_id) {
    return reject(Status::RejectedSession,
                  "credential envelope is bound to another session");
  }
  if (!valid_hex(envelope.nonce, 12U) ||
      !valid_hex(envelope.tag, 32U) ||
      envelope.ciphertext.size() % 2U != 0U ||
      envelope.ciphertext.size() > kMaxFieldSize * 2U) {
    return reject(Status::RejectedCredentials,
                  "credential envelope is malformed");
  }
  const auto nonce = from_hex(envelope.nonce);
  const auto ciphertext = from_hex(envelope.ciphertext);
  const auto supplied_tag = from_hex(envelope.tag);
  if (!nonce.has_value() || !ciphertext.has_value() ||
      !supplied_tag.has_value()) {
    return reject(Status::RejectedCredentials,
                  "credential envelope encoding is invalid");
  }

  const auto encryption_key =
      credential_key_material(binding, transport_exporter, *nonce, false);
  const auto mac_key =
      credential_key_material(binding, transport_exporter, *nonce, true);
  if (!encryption_key.has_value() || !mac_key.has_value()) {
    return reject(Status::RejectedBinding,
                  "credential key derivation failed");
  }
  Bytes authenticated =
      credential_aad(binding, *nonce, envelope.sequence);
  put_raw(authenticated, *ciphertext);
  const auto expected_tag = hmac_sha256(*mac_key, authenticated);
  if (!constant_time_equal(*supplied_tag, expected_tag)) {
    return reject(Status::RejectedCredentials,
                  "credential envelope authentication failed");
  }

  Bytes plaintext =
      chacha20_xor(*encryption_key, *nonce, 1U, *ciphertext);
  CredentialPayload credentials;
  const bool parsed = parse_credential_plaintext(plaintext, credentials);
  secure_zero(plaintext);
  if (!parsed) {
    return reject(Status::RejectedCredentials,
                  "credential payload is malformed");
  }

  record.credentials_opened = true;
  impl_->audit_event(AuditEventType::CredentialsAccepted, Status::Ok,
                     binding.method, binding.peer_address,
                     binding.session_id,
                     "sealed credential envelope accepted", now);
  CredentialOpenResult result;
  result.status = Status::Ok;
  result.credentials = std::move(credentials);
  return result;
}

std::vector<AuditEvent> Authenticator::drain_audit_log() {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  std::vector<AuditEvent> events;
  events.swap(impl_->audit);
  return events;
}

}  // namespace netsec

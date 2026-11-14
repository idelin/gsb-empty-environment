#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <limits>
#include <sstream>
#include <utility>

namespace netsec {

ValidationResult::ValidationResult(Diagnostic diagnostic) {
  diagnostics.push_back(std::move(diagnostic));
}

void ValidationResult::add(ErrorCode code, std::string field,
                           std::string message) {
  diagnostics.push_back(
      Diagnostic{code, std::move(field), std::move(message)});
}

void ValidationResult::merge(const ValidationResult& other) {
  diagnostics.insert(diagnostics.end(), other.diagnostics.begin(),
                     other.diagnostics.end());
}

std::string ValidationResult::summary() const {
  if (ok()) {
    return "ok";
  }

  std::ostringstream output;
  for (std::size_t index = 0; index < diagnostics.size(); ++index) {
    if (index != 0) {
      output << "; ";
    }
    const auto& diagnostic = diagnostics[index];
    output << "[" << static_cast<unsigned>(diagnostic.code) << "]";
    if (!diagnostic.field.empty()) {
      output << " " << diagnostic.field << ":";
    }
    output << " " << diagnostic.message;
  }
  return output.str();
}

namespace {

[[nodiscard]] bool ascii_space(char value) {
  return value == ' ' || value == '\t' || value == '\r' || value == '\n' ||
         value == '\f' || value == '\v';
}

[[nodiscard]] bool ascii_control(char value) {
  const auto byte = static_cast<unsigned char>(value);
  return byte < 0x20u || byte == 0x7fu;
}

[[nodiscard]] std::string ascii_upper(std::string_view input) {
  std::string output(input);
  for (char& value : output) {
    if (value >= 'a' && value <= 'z') {
      value = static_cast<char>(value - 'a' + 'A');
    }
  }
  return output;
}

[[nodiscard]] std::string ascii_lower(std::string_view input) {
  std::string output(input);
  for (char& value : output) {
    if (value >= 'A' && value <= 'Z') {
      value = static_cast<char>(value - 'A' + 'a');
    }
  }
  return output;
}

[[nodiscard]] bool equals_ci(std::string_view left, std::string_view right) {
  if (left.size() != right.size()) {
    return false;
  }
  for (std::size_t index = 0; index < left.size(); ++index) {
    char lhs = left[index];
    char rhs = right[index];
    if (lhs >= 'A' && lhs <= 'Z') {
      lhs = static_cast<char>(lhs - 'A' + 'a');
    }
    if (rhs >= 'A' && rhs <= 'Z') {
      rhs = static_cast<char>(rhs - 'A' + 'a');
    }
    if (lhs != rhs) {
      return false;
    }
  }
  return true;
}

[[nodiscard]] bool starts_with_ci(std::string_view value,
                                  std::string_view prefix) {
  return value.size() >= prefix.size() &&
         equals_ci(value.substr(0, prefix.size()), prefix);
}

[[nodiscard]] bool ends_with_ci(std::string_view value,
                                std::string_view suffix) {
  return value.size() >= suffix.size() &&
         equals_ci(value.substr(value.size() - suffix.size()), suffix);
}

[[nodiscard]] bool set_contains_ci(const std::set<std::string>& values,
                                   std::string_view candidate) {
  return std::any_of(values.begin(), values.end(),
                     [candidate](const std::string& value) {
                       return equals_ci(value, candidate);
                     });
}

[[nodiscard]] constexpr std::uint32_t rotate_right(std::uint32_t value,
                                                   unsigned count) {
  return (value >> count) | (value << (32u - count));
}

constexpr std::array<std::uint32_t, 64> sha256_constants{
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
    0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
    0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
    0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
    0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
    0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
    0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
    0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
    0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
    0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
    0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
    0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u};

void sha256_transform(std::array<std::uint32_t, 8>& state,
                      const std::uint8_t* block) {
  std::array<std::uint32_t, 64> words{};
  for (std::size_t index = 0; index < 16; ++index) {
    const std::size_t offset = index * 4;
    words[index] = (static_cast<std::uint32_t>(block[offset]) << 24u) |
                   (static_cast<std::uint32_t>(block[offset + 1]) << 16u) |
                   (static_cast<std::uint32_t>(block[offset + 2]) << 8u) |
                   static_cast<std::uint32_t>(block[offset + 3]);
  }
  for (std::size_t index = 16; index < 64; ++index) {
    const std::uint32_t s0 =
        rotate_right(words[index - 15], 7) ^
        rotate_right(words[index - 15], 18) ^ (words[index - 15] >> 3u);
    const std::uint32_t s1 =
        rotate_right(words[index - 2], 17) ^
        rotate_right(words[index - 2], 19) ^ (words[index - 2] >> 10u);
    words[index] = words[index - 16] + s0 + words[index - 7] + s1;
  }

  std::uint32_t a = state[0];
  std::uint32_t b = state[1];
  std::uint32_t c = state[2];
  std::uint32_t d = state[3];
  std::uint32_t e = state[4];
  std::uint32_t f = state[5];
  std::uint32_t g = state[6];
  std::uint32_t h = state[7];

  for (std::size_t index = 0; index < 64; ++index) {
    const std::uint32_t sum1 =
        rotate_right(e, 6) ^ rotate_right(e, 11) ^ rotate_right(e, 25);
    const std::uint32_t choice = (e & f) ^ ((~e) & g);
    const std::uint32_t temporary1 =
        h + sum1 + choice + sha256_constants[index] + words[index];
    const std::uint32_t sum0 =
        rotate_right(a, 2) ^ rotate_right(a, 13) ^ rotate_right(a, 22);
    const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
    const std::uint32_t temporary2 = sum0 + majority;

    h = g;
    g = f;
    f = e;
    e = d + temporary1;
    d = c;
    c = b;
    b = a;
    a = temporary1 + temporary2;
  }

  state[0] += a;
  state[1] += b;
  state[2] += c;
  state[3] += d;
  state[4] += e;
  state[5] += f;
  state[6] += g;
  state[7] += h;
}

class Sha256Hasher {
 public:
  void update(std::span<const std::uint8_t> data) {
    total_bytes_ += data.size();
    while (!data.empty()) {
      const std::size_t available = buffer_.size() - buffer_size_;
      const std::size_t take = std::min(available, data.size());
      std::copy_n(data.begin(), take, buffer_.begin() + buffer_size_);
      buffer_size_ += take;
      data = data.subspan(take);
      if (buffer_size_ == buffer_.size()) {
        sha256_transform(state_, buffer_.data());
        buffer_size_ = 0;
      }
    }
  }

  [[nodiscard]] std::array<std::uint8_t, 32> finish() {
    const std::uint64_t bit_length = total_bytes_ * 8u;
    buffer_[buffer_size_++] = 0x80u;
    if (buffer_size_ > 56) {
      std::fill(buffer_.begin() + buffer_size_, buffer_.end(), 0u);
      sha256_transform(state_, buffer_.data());
      buffer_size_ = 0;
    }
    std::fill(buffer_.begin() + buffer_size_, buffer_.begin() + 56, 0u);
    for (std::size_t index = 0; index < 8; ++index) {
      buffer_[56 + index] =
          static_cast<std::uint8_t>(bit_length >> (56u - index * 8u));
    }
    sha256_transform(state_, buffer_.data());

    std::array<std::uint8_t, 32> digest{};
    for (std::size_t word = 0; word < state_.size(); ++word) {
      digest[word * 4] = static_cast<std::uint8_t>(state_[word] >> 24u);
      digest[word * 4 + 1] =
          static_cast<std::uint8_t>(state_[word] >> 16u);
      digest[word * 4 + 2] =
          static_cast<std::uint8_t>(state_[word] >> 8u);
      digest[word * 4 + 3] = static_cast<std::uint8_t>(state_[word]);
    }
    return digest;
  }

 private:
  std::array<std::uint32_t, 8> state_{0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u,
                                      0xa54ff53au, 0x510e527fu, 0x9b05688cu,
                                      0x1f83d9abu, 0x5be0cd19u};
  std::array<std::uint8_t, 64> buffer_{};
  std::size_t buffer_size_{0};
  std::uint64_t total_bytes_{0};
};

[[nodiscard]] std::array<std::uint8_t, 32> sha256_digest(
    std::span<const std::uint8_t> data) {
  Sha256Hasher hasher;
  hasher.update(data);
  return hasher.finish();
}

[[nodiscard]] bool constant_time_equal(std::span<const std::uint8_t> left,
                                       std::span<const std::uint8_t> right) {
  if (left.size() != right.size()) {
    return false;
  }
  std::uint8_t difference = 0;
  for (std::size_t index = 0; index < left.size(); ++index) {
    difference =
        static_cast<std::uint8_t>(difference | (left[index] ^ right[index]));
  }
  return difference == 0;
}

class CanonicalWriter {
 public:
  void write_u8(std::uint8_t value) { bytes_.push_back(value); }

  void write_u32(std::uint32_t value) {
    for (std::size_t index = 0; index < 4; ++index) {
      bytes_.push_back(static_cast<std::uint8_t>(value >> (index * 8u)));
    }
  }

  void write_u64(std::uint64_t value) {
    for (std::size_t index = 0; index < 8; ++index) {
      bytes_.push_back(static_cast<std::uint8_t>(value >> (index * 8u)));
    }
  }

  void write_string(std::string_view value) {
    write_u64(value.size());
    bytes_.insert(bytes_.end(), value.begin(), value.end());
  }

  void write_blob(std::span<const std::uint8_t> value) {
    write_u64(value.size());
    bytes_.insert(bytes_.end(), value.begin(), value.end());
  }

  [[nodiscard]] std::vector<std::uint8_t> take() && {
    return std::move(bytes_);
  }

 private:
  std::vector<std::uint8_t> bytes_;
};

struct ParsedWindowsPath {
  bool valid{false};
  std::string root;
  std::vector<std::string> components;
  std::string full;
  std::string error;
};

[[nodiscard]] bool is_reserved_device_name(std::string_view component) {
  const std::size_t dot = component.find('.');
  const std::string base = ascii_upper(component.substr(
      0, dot == std::string_view::npos ? component.size() : dot));
  if (base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
      base == "CONIN$" || base == "CONOUT$" || base == "CLOCK$") {
    return true;
  }
  if (base.size() == 4 &&
      (base.substr(0, 3) == "COM" || base.substr(0, 3) == "LPT") &&
      base[3] >= '1' && base[3] <= '9') {
    return true;
  }
  return false;
}

[[nodiscard]] bool valid_path_component(std::string_view component,
                                        std::string& error) {
  if (component.empty()) {
    error = "empty path component";
    return false;
  }
  if (component.size() > 255) {
    error = "path component exceeds 255 characters";
    return false;
  }
  if (component.front() == ' ' || component.back() == ' ' ||
      component.back() == '.') {
    error = "path component has a leading/trailing space or trailing dot";
    return false;
  }
  for (char value : component) {
    if (ascii_control(value) || value == ':' || value == '"' || value == '<' ||
        value == '>' || value == '|' || value == '?' || value == '*') {
      error = "path component contains a forbidden character";
      return false;
    }
  }
  if (is_reserved_device_name(component)) {
    error = "path component is a reserved device name";
    return false;
  }
  return true;
}

[[nodiscard]] std::string join_path(std::string_view root,
                                    const std::vector<std::string>& components,
                                    std::size_t count) {
  std::string result(root);
  for (std::size_t index = 0; index < count; ++index) {
    if (result.empty() || result.back() != '\\') {
      result.push_back('\\');
    }
    result += components[index];
  }
  return result;
}

[[nodiscard]] ParsedWindowsPath parse_absolute_windows_path(
    std::string_view input) {
  ParsedWindowsPath parsed;
  if (input.empty()) {
    parsed.error = "path is empty";
    return parsed;
  }
  if (input.size() > 32767) {
    parsed.error = "path exceeds 32767 characters";
    return parsed;
  }

  std::string path(input);
  std::replace(path.begin(), path.end(), '/', '\\');
  for (char value : path) {
    if (ascii_control(value)) {
      parsed.error = "path contains a control character";
      return parsed;
    }
  }
  if (path.rfind("\\\\?\\", 0) == 0 || path.rfind("\\\\.\\", 0) == 0) {
    parsed.error = "device namespace paths are not accepted";
    return parsed;
  }

  std::string_view remainder;
  if (path.size() >= 2 && path[0] == '\\' && path[1] == '\\') {
    const std::size_t server_start = 2;
    const std::size_t server_end = path.find('\\', server_start);
    if (server_end == std::string::npos || server_end == server_start) {
      parsed.error = "UNC path has no server/share";
      return parsed;
    }
    const std::string_view server(path.data() + server_start,
                                  server_end - server_start);
    const std::size_t share_start = server_end + 1;
    if (share_start >= path.size()) {
      parsed.error = "UNC path has no share";
      return parsed;
    }
    const std::size_t share_end = path.find('\\', share_start);
    const std::size_t effective_share_end =
        share_end == std::string::npos ? path.size() : share_end;
    const std::string_view share(path.data() + share_start,
                                 effective_share_end - share_start);
    std::string component_error;
    if (!valid_path_component(server, component_error) ||
        !valid_path_component(share, component_error)) {
      parsed.error = "invalid UNC server/share: " + component_error;
      return parsed;
    }
    parsed.root = "\\\\" + std::string(server) + "\\" + std::string(share);
    remainder = share_end == std::string::npos
                    ? std::string_view{}
                    : std::string_view(path).substr(share_end + 1);
  } else if (path.size() >= 3 &&
             std::isalpha(static_cast<unsigned char>(path[0])) &&
             path[1] == ':' && path[2] == '\\') {
    char drive = path[0];
    if (drive >= 'a' && drive <= 'z') {
      drive = static_cast<char>(drive - 'a' + 'A');
    }
    parsed.root = std::string(1, drive) + ":\\";
    remainder = std::string_view(path).substr(3);
  } else {
    parsed.error = "path must be an absolute drive or UNC path";
    return parsed;
  }

  std::size_t position = 0;
  std::string component_error;
  while (position < remainder.size()) {
    const std::size_t separator = remainder.find('\\', position);
    const std::size_t end =
        separator == std::string_view::npos ? remainder.size() : separator;
    if (end == position) {
      parsed.error = "path contains an empty component";
      return parsed;
    }
    const std::string_view component =
        remainder.substr(position, end - position);
    if (component == ".") {
      // Collapse a current-directory component.
    } else if (component == "..") {
      if (parsed.components.empty()) {
        parsed.error = "path escapes above its volume root";
        return parsed;
      }
      parsed.components.pop_back();
    } else {
      if (!valid_path_component(component, component_error)) {
        parsed.error = component_error;
        return parsed;
      }
      parsed.components.emplace_back(component);
    }
    if (separator == std::string_view::npos) {
      break;
    }
    position = separator + 1;
  }

  parsed.full =
      join_path(parsed.root, parsed.components, parsed.components.size());
  parsed.valid = true;
  return parsed;
}

[[nodiscard]] std::string canonical_registry_key(std::string_view path) {
  std::string result(path);
  std::replace(result.begin(), result.end(), '/', '\\');
  while (!result.empty() && result.back() == '\\') {
    result.pop_back();
  }
  return ascii_lower(result);
}

[[nodiscard]] bool registry_key_is_under(std::string_view path,
                                         std::string_view root) {
  const std::string canonical_path = canonical_registry_key(path);
  const std::string canonical_root = canonical_registry_key(root);
  return canonical_path == canonical_root ||
         (canonical_path.size() > canonical_root.size() &&
          canonical_path.compare(0, canonical_root.size(), canonical_root) ==
              0 &&
          canonical_path[canonical_root.size()] == '\\');
}

[[nodiscard]] bool valid_service_identifier(std::string_view value,
                                            ValidationResult& result,
                                            std::string_view field,
                                            ErrorCode code,
                                            std::string_view noun) {
  if (value.empty()) {
    result.add(code, std::string(field), std::string(noun) + " is empty");
    return false;
  }
  if (value.size() > 256) {
    result.add(code, std::string(field),
               std::string(noun) + " exceeds 256 characters");
    return false;
  }
  if (value.front() == ' ' || value.back() == ' ') {
    result.add(code, std::string(field),
               std::string(noun) + " has a leading or trailing space");
    return false;
  }
  if (value == "." || value == "..") {
    result.add(code, std::string(field),
               std::string(noun) + " is a relative path token");
    return false;
  }
  for (char character : value) {
    if (ascii_control(character) || character == '\\' || character == '/' ||
        character == '"' || character == '|' || character == ':' ||
        character == '*' || character == '?' || character == '<' ||
        character == '>') {
      result.add(code, std::string(field),
                 std::string(noun) + " contains a forbidden character");
      return false;
    }
  }
  return true;
}

[[nodiscard]] bool valid_object_name(std::string_view value,
                                     ValidationResult& result) {
  if (value.empty()) {
    result.add(ErrorCode::invalid_object_name, "ObjectName",
               "ObjectName is empty; use an explicit LocalSystem or service "
               "account identity");
    return false;
  }
  if (value.size() > 256) {
    result.add(ErrorCode::invalid_object_name, "ObjectName",
               "ObjectName exceeds 256 characters");
    return false;
  }
  if (value.front() == ' ' || value.back() == ' ') {
    result.add(ErrorCode::invalid_object_name, "ObjectName",
               "ObjectName has a leading or trailing space");
    return false;
  }
  for (char character : value) {
    if (ascii_control(character) || character == '"' || character == '/' ||
        character == '|' || character == '%' || character == '*' ||
        character == '?' || character == '<' || character == '>') {
      result.add(ErrorCode::invalid_object_name, "ObjectName",
                 "ObjectName contains a forbidden character");
      return false;
    }
  }
  if (starts_with_ci(value, "S-1-")) {
    for (char character : value) {
      if (!((character >= '0' && character <= '9') || character == 'S' ||
            character == 's' || character == '-')) {
        result.add(ErrorCode::invalid_object_name, "ObjectName",
                   "ObjectName is not a valid SID");
        return false;
      }
    }
    return true;
  }
  if (value.find('\\') != std::string_view::npos) {
    const std::size_t separator = value.find('\\');
    if (value.find('\\', separator + 1) != std::string_view::npos ||
        separator == 0 || separator + 1 == value.size()) {
      result.add(ErrorCode::invalid_object_name, "ObjectName",
                 "ObjectName must contain at most one non-empty domain "
                 "separator");
      return false;
    }
  }
  return true;
}

[[nodiscard]] std::vector<std::string> ancestor_chain(
    const ParsedWindowsPath& path) {
  std::vector<std::string> chain;
  for (std::size_t count = path.components.size() + 1; count > 0; --count) {
    chain.push_back(join_path(path.root, path.components, count - 1));
  }
  return chain;
}

}  // namespace

std::vector<std::uint8_t> canonical_configuration_bytes(
    const ServiceConfiguration& configuration) {
  CanonicalWriter writer;
  writer.write_string("netsec.service-configuration.v1");
  writer.write_string(configuration.service_name);
  writer.write_string(configuration.image_path);
  writer.write_string(configuration.object_name);
  writer.write_u64(configuration.dependencies.size());
  for (const std::string& dependency : configuration.dependencies) {
    writer.write_string(dependency);
  }
  writer.write_u64(configuration.recovery_actions.size());
  for (const RecoveryAction& action : configuration.recovery_actions) {
    writer.write_u8(static_cast<std::uint8_t>(action.type));
    writer.write_u32(action.delay_milliseconds);
    writer.write_string(action.command);
  }
  return std::move(writer).take();
}

std::vector<std::uint8_t> signed_configuration_message(
    const SignedConfiguration& signed_configuration) {
  CanonicalWriter writer;
  writer.write_string("netsec.signed-service-configuration.v1");
  writer.write_string(signed_configuration.key_id);
  writer.write_u8(static_cast<std::uint8_t>(signed_configuration.algorithm));
  writer.write_blob(
      canonical_configuration_bytes(signed_configuration.payload));
  return std::move(writer).take();
}

std::vector<std::uint8_t> hmac_sha256(std::span<const std::uint8_t> key,
                                      std::span<const std::uint8_t> data) {
  std::array<std::uint8_t, 64> normalized_key{};
  if (key.size() > normalized_key.size()) {
    const auto digest = sha256_digest(key);
    std::copy(digest.begin(), digest.end(), normalized_key.begin());
  } else {
    std::copy(key.begin(), key.end(), normalized_key.begin());
  }

  std::array<std::uint8_t, 64> inner_pad{};
  std::array<std::uint8_t, 64> outer_pad{};
  for (std::size_t index = 0; index < normalized_key.size(); ++index) {
    inner_pad[index] =
        static_cast<std::uint8_t>(normalized_key[index] ^ 0x36u);
    outer_pad[index] =
        static_cast<std::uint8_t>(normalized_key[index] ^ 0x5cu);
  }

  Sha256Hasher inner;
  inner.update(inner_pad);
  inner.update(data);
  const auto inner_digest = inner.finish();

  Sha256Hasher outer;
  outer.update(outer_pad);
  outer.update(inner_digest);
  const auto outer_digest = outer.finish();
  return std::vector<std::uint8_t>(outer_digest.begin(), outer_digest.end());
}

std::string hex_encode(std::span<const std::uint8_t> bytes) {
  static constexpr char digits[] = "0123456789abcdef";
  std::string output;
  output.reserve(bytes.size() * 2);
  for (std::uint8_t byte : bytes) {
    output.push_back(digits[byte >> 4u]);
    output.push_back(digits[byte & 0x0fu]);
  }
  return output;
}

HmacSha256Verifier::HmacSha256Verifier(
    std::map<std::string, std::vector<std::uint8_t>> keys) {
  for (auto& entry : keys) {
    if (!entry.first.empty() && !entry.second.empty()) {
      keys_.emplace(entry.first, std::move(entry.second));
    }
  }
}

bool HmacSha256Verifier::add_key(std::string key_id,
                                 std::vector<std::uint8_t> key) {
  if (key_id.empty() || key.empty() || keys_.contains(key_id)) {
    return false;
  }
  keys_.emplace(std::move(key_id), std::move(key));
  return true;
}

bool HmacSha256Verifier::verify(
    std::string_view key_id, SignatureAlgorithm algorithm,
    std::span<const std::uint8_t> message,
    std::span<const std::uint8_t> signature) const {
  if (algorithm != SignatureAlgorithm::hmac_sha256 || signature.size() != 32) {
    return false;
  }
  const auto found = keys_.find(std::string(key_id));
  if (found == keys_.end()) {
    return false;
  }
  const auto expected = hmac_sha256(found->second, message);
  return constant_time_equal(expected, signature);
}

HmacSha256Signer::HmacSha256Signer(std::string key_id,
                                   std::vector<std::uint8_t> key)
    : key_id_(std::move(key_id)), key_(std::move(key)) {}

SignedConfiguration HmacSha256Signer::sign(
    ServiceConfiguration payload) const {
  SignedConfiguration signed_configuration;
  signed_configuration.key_id = key_id_;
  signed_configuration.algorithm = SignatureAlgorithm::hmac_sha256;
  signed_configuration.payload = std::move(payload);
  signed_configuration.signature =
      hmac_sha256(key_, signed_configuration_message(signed_configuration));
  return signed_configuration;
}

SecurityPolicy SecurityPolicy::windows_service_defaults() {
  SecurityPolicy policy;
  for (std::string_view sid : {sids::local_system, sids::builtin_administrators,
                               sids::trusted_installer}) {
    policy.privileged_owners.emplace(sid);
    policy.privileged_writers.emplace(sid);
  }
  policy.trusted_directories = {
      "C:\\Windows\\System32", "C:\\Windows\\SysWOW64", "C:\\Program Files",
      "C:\\Program Files (x86)"};
  return policy;
}

ValidationResult validate_security_descriptor(
    const SecurityDescriptor& descriptor, const SecurityPolicy& policy,
    std::string_view location, SecurityObject object) {
  ValidationResult result;
  const std::string field(location);

  if (descriptor.owner.empty()) {
    result.add(ErrorCode::acl_untrusted_owner, field,
               "security descriptor has no owner SID");
  } else if (!set_contains_ci(policy.privileged_owners, descriptor.owner)) {
    result.add(ErrorCode::acl_untrusted_owner, field,
               "owner is not a privileged principal: " + descriptor.owner);
  }

  if (!descriptor.dacl_present) {
    result.add(ErrorCode::acl_missing, field,
               "DACL is absent (a null DACL grants everyone full control)");
    return result;
  }
  const bool require_protected =
      object == SecurityObject::file_system
          ? policy.require_protected_file_dacl
          : policy.require_protected_dacl;
  if (require_protected && !descriptor.dacl_protected) {
    result.add(ErrorCode::acl_unprotected, field,
               "DACL is not protected from parent inheritance");
  }

  const bool owner_is_privileged =
      !descriptor.owner.empty() &&
      set_contains_ci(policy.privileged_owners, descriptor.owner);

  for (const AccessControlEntry& entry : descriptor.entries) {
    if (entry.kind != AccessControlEntry::Kind::allow ||
        (entry.rights & policy.dangerous_write_rights) == 0) {
      continue;
    }
    if (set_contains_ci(policy.privileged_writers, entry.principal)) {
      continue;
    }
    if (equals_ci(entry.principal, sids::creator_owner) &&
        owner_is_privileged) {
      continue;
    }
    result.add(ErrorCode::acl_untrusted_writer, field,
               "non-privileged principal has write rights: " +
                   (entry.principal.empty() ? std::string("<empty>")
                                            : entry.principal) +
                   " mask=" + std::to_string(entry.rights));
  }
  return result;
}

NormalizedPath normalize_windows_path(std::string_view path) {
  const ParsedWindowsPath parsed = parse_absolute_windows_path(path);
  NormalizedPath result;
  result.valid = parsed.valid;
  result.value = parsed.full;
  result.error = parsed.error;
  return result;
}

bool path_is_within_root(std::string_view path, std::string_view root,
                         bool case_insensitive) {
  const ParsedWindowsPath parsed_path = parse_absolute_windows_path(path);
  const ParsedWindowsPath parsed_root = parse_absolute_windows_path(root);
  if (!parsed_path.valid || !parsed_root.valid) {
    return false;
  }

  const std::string& candidate = parsed_path.full;
  const std::string& trusted = parsed_root.full;
  if (case_insensitive) {
    if (equals_ci(candidate, trusted)) {
      return true;
    }
    if (!trusted.empty() && trusted.back() == '\\') {
      return starts_with_ci(candidate, trusted);
    }
    if (candidate.size() <= trusted.size() ||
        !starts_with_ci(candidate, trusted)) {
      return false;
    }
    return candidate[trusted.size()] == '\\';
  }

  if (candidate == trusted) {
    return true;
  }
  if (!trusted.empty() && trusted.back() == '\\') {
    return candidate.rfind(trusted, 0) == 0;
  }
  return candidate.size() > trusted.size() &&
         candidate.rfind(trusted, 0) == 0 &&
         candidate[trusted.size()] == '\\';
}

ParsedCommandLine parse_windows_command_line(std::string_view command_line) {
  ParsedCommandLine result;
  if (command_line.empty()) {
    result.error = "command line is empty";
    return result;
  }
  for (char value : command_line) {
    if (value == '\0') {
      result.error = "command line contains a NUL byte";
      return result;
    }
  }

  std::string token;
  bool in_quotes = false;
  bool token_started = false;

  auto flush = [&]() {
    if (token_started) {
      result.argv.push_back(token);
      token.clear();
      token_started = false;
    }
  };

  std::size_t index = 0;
  while (index < command_line.size()) {
    const char value = command_line[index];
    if (value == '\\') {
      std::size_t count = 0;
      std::size_t cursor = index;
      while (cursor < command_line.size() && command_line[cursor] == '\\') {
        ++count;
        ++cursor;
      }
      if (cursor < command_line.size() && command_line[cursor] == '"') {
        token.append(count / 2, '\\');
        if (count % 2 != 0) {
          token.push_back('"');
          ++cursor;
        }
        index = cursor;
        token_started = true;
      } else {
        token.append(count, '\\');
        index = cursor;
        token_started = true;
      }
    } else if (value == '"') {
      in_quotes = !in_quotes;
      token_started = true;
      ++index;
    } else if (!in_quotes && ascii_space(value)) {
      flush();
      ++index;
    } else {
      token.push_back(value);
      token_started = true;
      ++index;
    }
  }

  if (in_quotes) {
    result.error = "command line has an unterminated quote";
    return result;
  }
  flush();
  if (result.argv.empty()) {
    result.error = "command line has no executable";
    return result;
  }
  result.valid = true;
  return result;
}

std::string service_registry_key(std::string_view service_name) {
  return std::string(service_registry_root) + std::string(service_name);
}

ConfigurationIntegrity::ConfigurationIntegrity(
    std::shared_ptr<const SignatureVerifier> verifier,
    std::shared_ptr<ServiceRegistry> registry,
    std::shared_ptr<const FileSystemInspector> files, SecurityPolicy policy)
    : verifier_(std::move(verifier)),
      registry_(std::move(registry)),
      files_(std::move(files)),
      policy_(std::move(policy)) {}

ValidationResult ConfigurationIntegrity::validate_program_path(
    std::string_view command_line, std::string_view field) const {
  ValidationResult result;
  if (command_line.empty()) {
    result.add(ErrorCode::invalid_image_path, std::string(field),
               "program command line is empty");
    return result;
  }

  const ParsedCommandLine parsed = parse_windows_command_line(command_line);
  if (!parsed.valid) {
    result.add(ErrorCode::malformed_command_line, std::string(field),
               parsed.error);
    return result;
  }
  if (parsed.argv.empty() || parsed.argv.front().empty()) {
    result.add(ErrorCode::invalid_image_path, std::string(field),
               "empty executable name");
    return result;
  }

  const std::string& executable_text = parsed.argv.front();
  if (executable_text.find('%') != std::string::npos) {
    result.add(ErrorCode::invalid_image_path, std::string(field),
               "environment-expanded executable paths are not accepted");
    return result;
  }
  if (executable_text.find('"') != std::string::npos) {
    result.add(ErrorCode::invalid_image_path, std::string(field),
               "executable path contains an embedded quote");
    return result;
  }

  const ParsedWindowsPath executable_path =
      parse_absolute_windows_path(executable_text);
  if (!executable_path.valid) {
    result.add(ErrorCode::invalid_image_path, std::string(field),
               executable_path.error);
    return result;
  }
  if (!ends_with_ci(executable_path.full, ".exe")) {
    result.add(ErrorCode::invalid_image_path, std::string(field),
               "executable must have an .exe extension and be fully quoted if "
               "its path contains spaces");
    return result;
  }

  std::string matched_root;
  for (const std::string& trusted_root : policy_.trusted_directories) {
    if (path_is_within_root(executable_path.full, trusted_root,
                            policy_.case_insensitive_paths)) {
      const NormalizedPath normalized_root =
          normalize_windows_path(trusted_root);
      if (normalized_root.valid && normalized_root.value.size() > matched_root.size()) {
        matched_root = normalized_root.value;
      }
    }
  }
  if (matched_root.empty()) {
    result.add(ErrorCode::path_outside_trusted_directory, std::string(field),
               "executable is outside every trusted directory: " +
                   executable_path.full);
    return result;
  }

  if (!files_) {
    result.add(ErrorCode::internal_error, std::string(field),
               "no filesystem inspector is configured");
    return result;
  }
  const PathSnapshot observed = files_->inspect(executable_path.full);
  if (!observed.exists) {
    result.add(ErrorCode::path_not_found, std::string(field),
               "executable does not exist: " + executable_path.full);
    return result;
  }
  if (!observed.is_regular_file) {
    result.add(ErrorCode::path_not_regular_file, std::string(field),
               "executable is not a regular file: " + executable_path.full);
  }
  if (observed.traverses_reparse_point) {
    result.add(ErrorCode::path_reparse_point, std::string(field),
               "executable path traverses a reparse point");
  }

  const ParsedWindowsPath canonical =
      parse_absolute_windows_path(observed.canonical_path);
  if (!canonical.valid) {
    result.add(ErrorCode::path_reparse_point, std::string(field),
               "filesystem inspector returned an invalid canonical path");
    return result;
  }
  if (!equals_ci(canonical.full, executable_path.full)) {
    result.add(ErrorCode::path_reparse_point, std::string(field),
               "executable resolves to a different path: " + canonical.full);
  }
  if (!path_is_within_root(canonical.full, matched_root,
                           policy_.case_insensitive_paths)) {
    result.add(ErrorCode::path_outside_trusted_directory, std::string(field),
               "canonical executable path left the trusted directory");
  }

  const std::vector<std::string> complete_chain =
      ancestor_chain(executable_path);
  std::size_t trusted_index = complete_chain.size();
  for (std::size_t index = 0; index < complete_chain.size(); ++index) {
    if (equals_ci(complete_chain[index], matched_root)) {
      trusted_index = index;
      break;
    }
  }
  if (trusted_index == complete_chain.size()) {
    result.add(ErrorCode::path_component_unverified, std::string(field),
               "could not locate the trusted directory in the path chain");
  }

  for (std::size_t index = 0; index < complete_chain.size(); ++index) {
    const auto found = std::find_if(
        observed.components.begin(), observed.components.end(),
        [&complete_chain, index](const FileSystemComponent& component) {
          const NormalizedPath normalized =
              normalize_windows_path(component.path);
          return normalized.valid &&
                 equals_ci(normalized.value, complete_chain[index]);
        });
    if (found == observed.components.end()) {
      result.add(ErrorCode::path_component_unverified, std::string(field),
                 "filesystem inspector omitted ACL information for " +
                     complete_chain[index]);
    }
  }

  for (const FileSystemComponent& component : observed.components) {
    const NormalizedPath normalized =
        normalize_windows_path(component.path);
    if (!normalized.valid) {
      result.add(ErrorCode::path_component_unverified, std::string(field),
                 "filesystem component has an invalid path: " +
                     component.path);
      continue;
    }
    if (component.is_reparse_point) {
      result.add(ErrorCode::path_reparse_point, std::string(field),
                 "path component is a reparse point: " + normalized.value);
    }
    const ValidationResult acl = validate_security_descriptor(
        component.security, policy_, "file:" + normalized.value,
        SecurityObject::file_system);
    for (const Diagnostic& diagnostic : acl.diagnostics) {
      ErrorCode code = diagnostic.code;
      if (code == ErrorCode::acl_untrusted_writer ||
          code == ErrorCode::acl_missing ||
          code == ErrorCode::acl_unprotected ||
          code == ErrorCode::acl_untrusted_owner) {
        code = ErrorCode::path_user_writable;
      }
      result.add(code, diagnostic.field,
                 "path component is not privileged-only: " +
                     diagnostic.message);
    }
  }
  return result;
}

ValidationResult ConfigurationIntegrity::validate_configuration(
    const SignedConfiguration& signed_configuration) const {
  ValidationResult result;
  if (!verifier_) {
    result.add(ErrorCode::internal_error, "signature",
               "no signature verifier is configured");
    return result;
  }
  if (signed_configuration.key_id.empty() ||
      signed_configuration.key_id.size() > 128 ||
      std::any_of(signed_configuration.key_id.begin(),
                  signed_configuration.key_id.end(), ascii_control)) {
    result.add(ErrorCode::invalid_signature, "signature.key_id",
               "signing key id is empty, too long, or malformed");
    return result;
  }
  if (signed_configuration.signature.empty()) {
    result.add(ErrorCode::invalid_signature, "signature",
               "detached signature is missing");
    return result;
  }

  // Bound attacker-controlled allocations before serialising for signature
  // verification.  These limits are deliberately generous; the semantic
  // checks below report the precise policy violation for a valid signature.
  const ServiceConfiguration& bounded = signed_configuration.payload;
  const bool oversized =
      bounded.service_name.size() > 1024 || bounded.image_path.size() > 32767 ||
      bounded.object_name.size() > 1024 ||
      bounded.dependencies.size() > 4096 ||
      bounded.recovery_actions.size() > 4096;
  bool oversized_nested = false;
  for (const std::string& dependency : bounded.dependencies) {
    oversized_nested = oversized_nested || dependency.size() > 1024;
  }
  for (const RecoveryAction& action : bounded.recovery_actions) {
    oversized_nested =
        oversized_nested || action.command.size() > 32767;
  }
  if (oversized || oversized_nested) {
    result.add(ErrorCode::malformed_configuration, "configuration",
               "configuration exceeds bounded field sizes");
    return result;
  }

  if (!verifier_->verify(
          signed_configuration.key_id, signed_configuration.algorithm,
          signed_configuration_message(signed_configuration),
          signed_configuration.signature)) {
    result.add(ErrorCode::invalid_signature, "signature",
               "configuration signature was rejected");
    return result;
  }

  const ServiceConfiguration& configuration = signed_configuration.payload;
  if (!valid_service_identifier(configuration.service_name, result,
                                "service_name",
                                ErrorCode::invalid_service_name,
                                "service name")) {
    return result;
  }
  if (configuration.image_path.empty()) {
    result.add(ErrorCode::invalid_image_path, "ImagePath",
               "ImagePath is empty");
  } else {
    result.merge(validate_program_path(configuration.image_path, "ImagePath"));
  }

  (void)valid_object_name(configuration.object_name, result);

  if (configuration.dependencies.size() > policy_.max_dependencies) {
    result.add(ErrorCode::invalid_dependency, "DependOnService",
               "too many dependencies");
  }
  for (std::size_t index = 0; index < configuration.dependencies.size();
       ++index) {
    (void)valid_service_identifier(
        configuration.dependencies[index], result,
        "DependOnService[" + std::to_string(index) + "]",
        ErrorCode::invalid_dependency, "dependency name");
  }

  if (configuration.recovery_actions.size() > policy_.max_recovery_actions) {
    result.add(ErrorCode::invalid_recovery_action, "FailureActions",
               "too many recovery actions");
  }
  constexpr std::uint32_t kMaximumDelayMilliseconds =
      7u * 24u * 60u * 60u * 1000u;
  for (std::size_t index = 0; index < configuration.recovery_actions.size();
       ++index) {
    const RecoveryAction& action = configuration.recovery_actions[index];
    const std::string field =
        "FailureActions[" + std::to_string(index) + "]";
    if (action.delay_milliseconds > kMaximumDelayMilliseconds) {
      result.add(ErrorCode::invalid_recovery_action, field,
                 "recovery delay exceeds seven days");
    }
    switch (action.type) {
      case RecoveryActionType::none:
      case RecoveryActionType::restart_service:
      case RecoveryActionType::restart_computer:
        if (!action.command.empty()) {
          result.add(ErrorCode::invalid_recovery_action, field,
                     "this recovery action must not contain a command");
        }
        break;
      case RecoveryActionType::run_program:
        if (action.command.empty()) {
          result.add(ErrorCode::invalid_recovery_action, field,
                     "run_program requires a signed command");
        } else {
          result.merge(
              validate_program_path(action.command, field + ".command"));
        }
        break;
      default:
        result.add(ErrorCode::invalid_recovery_action, field,
                   "unknown recovery action type");
        break;
    }
  }
  return result;
}

ValidationResult ConfigurationIntegrity::validate_registry_acl(
    const RegistrySnapshot& snapshot, std::string_view expected_key) const {
  ValidationResult result;
  if (!snapshot.exists) {
    result.add(ErrorCode::registry_key_missing, std::string(expected_key),
               "service registry key does not exist");
    return result;
  }
  if (!equals_ci(snapshot.key_path, expected_key)) {
    result.add(ErrorCode::service_name_mismatch, "registry.key_path",
               "registry adapter returned an unexpected key: " +
                   snapshot.key_path);
  }
  if (snapshot.keys.empty()) {
    result.add(ErrorCode::acl_missing, std::string(expected_key),
               "registry adapter returned no key security descriptors");
    return result;
  }

  bool root_found = false;
  for (const RegistryKeyAcl& key : snapshot.keys) {
    if (!registry_key_is_under(key.path, expected_key)) {
      result.add(ErrorCode::registry_unexpected_key, key.path,
                 "security descriptor is outside the service key subtree");
      continue;
    }
    if (equals_ci(key.path, expected_key)) {
      root_found = true;
    }
    result.merge(validate_security_descriptor(
        key.security, policy_, "registry:" + key.path));
  }
  if (!root_found) {
    result.add(ErrorCode::acl_missing, std::string(expected_key),
               "the service key itself has no security descriptor");
  }
  return result;
}

ValidationResult ConfigurationIntegrity::audit(
    const SignedConfiguration& signed_configuration) const {
  ValidationResult result = validate_configuration(signed_configuration);
  if (result.failed()) {
    return result;
  }
  if (!registry_) {
    result.add(ErrorCode::internal_error, "registry",
               "no service registry adapter is configured");
    return result;
  }

  const std::string expected_key =
      service_registry_key(signed_configuration.payload.service_name);
  const RegistrySnapshot snapshot = registry_->snapshot(expected_key);
  if (!snapshot.exists) {
    result.add(ErrorCode::registry_key_missing, expected_key,
               "service registry key does not exist");
    return result;
  }

  result.merge(validate_registry_acl(snapshot, expected_key));
  if (!equals_ci(snapshot.key_path, expected_key)) {
    return result;
  }

  const ServiceConfiguration& expected = signed_configuration.payload;
  if (!snapshot.observed.image_path.has_value()) {
    result.add(ErrorCode::configuration_mismatch, "ImagePath",
               "ImagePath is missing from the registry");
  } else if (*snapshot.observed.image_path != expected.image_path) {
    result.add(ErrorCode::configuration_mismatch, "ImagePath",
               "registry ImagePath does not match the signed configuration");
  }

  if (!snapshot.observed.object_name.has_value()) {
    result.add(ErrorCode::configuration_mismatch, "ObjectName",
               "ObjectName is missing from the registry");
  } else if (*snapshot.observed.object_name != expected.object_name) {
    result.add(ErrorCode::configuration_mismatch, "ObjectName",
               "registry ObjectName does not match the signed configuration");
  }

  if (expected.dependencies.empty()) {
    if (snapshot.observed.dependencies.has_value() &&
        !snapshot.observed.dependencies->empty()) {
      result.add(ErrorCode::configuration_mismatch, "DependOnService",
                 "registry has dependencies that are absent from the signed "
                 "configuration");
    }
  } else if (!snapshot.observed.dependencies.has_value() ||
             *snapshot.observed.dependencies != expected.dependencies) {
    result.add(ErrorCode::configuration_mismatch, "DependOnService",
               "registry dependencies do not match the signed configuration");
  }

  if (expected.recovery_actions.empty()) {
    if (snapshot.observed.recovery_actions.has_value() &&
        !snapshot.observed.recovery_actions->empty()) {
      result.add(ErrorCode::configuration_mismatch, "FailureActions",
                 "registry has recovery actions that are absent from the "
                 "signed configuration");
    }
  } else if (!snapshot.observed.recovery_actions.has_value() ||
             *snapshot.observed.recovery_actions !=
                 expected.recovery_actions) {
    result.add(ErrorCode::configuration_mismatch, "FailureActions",
               "registry recovery actions do not match the signed "
               "configuration");
  }
  return result;
}

ValidationResult ConfigurationIntegrity::write_then_audit(
    const SignedConfiguration& signed_configuration, bool require_existing,
    bool preaudit_acl) const {
  ValidationResult result = validate_configuration(signed_configuration);
  if (result.failed()) {
    return result;
  }
  if (!registry_ || !files_) {
    result.add(ErrorCode::internal_error, "component",
               "registry and filesystem adapters are required for writes");
    return result;
  }

  const std::string expected_key =
      service_registry_key(signed_configuration.payload.service_name);
  const RegistrySnapshot before = registry_->snapshot(expected_key);
  if (require_existing && !before.exists) {
    result.add(ErrorCode::registry_key_missing, expected_key,
               "update requires an existing service key");
    return result;
  }
  if (before.exists && !equals_ci(before.key_path, expected_key)) {
    result.add(ErrorCode::service_name_mismatch, "registry.key_path",
               "registry adapter returned an unexpected key");
    return result;
  }

  if (preaudit_acl) {
    result.merge(validate_registry_acl(before, expected_key));
    if (result.failed()) {
      return result;
    }
  }

  if (before.exists) {
    const ValidationResult hardened =
        registry_->enforce_acl(expected_key, policy_);
    if (hardened.failed()) {
      result.merge(hardened);
      return result;
    }
  }

  const ValidationResult written = registry_->write_configuration(
      expected_key, signed_configuration.payload, policy_);
  if (written.failed()) {
    result.merge(written);
    return result;
  }

  const ValidationResult hardened =
      registry_->enforce_acl(expected_key, policy_);
  if (hardened.failed()) {
    result.merge(hardened);
    return result;
  }

  ValidationResult post = audit(signed_configuration);
  if (post.failed()) {
    post.add(ErrorCode::postcondition_failed, expected_key,
             "post-write integrity audit failed");
  }
  return post;
}

ValidationResult ConfigurationIntegrity::install(
    const SignedConfiguration& signed_configuration) const {
  return write_then_audit(signed_configuration, false, false);
}

ValidationResult ConfigurationIntegrity::update(
    const SignedConfiguration& signed_configuration) const {
  return write_then_audit(signed_configuration, true, true);
}

ValidationResult ConfigurationIntegrity::repair(
    const SignedConfiguration& signed_configuration) const {
  return write_then_audit(signed_configuration, false, false);
}

ConfigurationIntegrity::LaunchDecision ConfigurationIntegrity::prepare_launch(
    const SignedConfiguration& signed_configuration) const {
  LaunchDecision decision;
  decision.status = audit(signed_configuration);
  if (decision.status.failed()) {
    return decision;
  }

  // The audit above proved that the registry value and the signed value are
  // identical and that the file/ACL state is still acceptable.  Build argv
  // from signed content and use the normalized executable path.
  const ParsedCommandLine parsed =
      parse_windows_command_line(signed_configuration.payload.image_path);
  if (!parsed.valid || parsed.argv.empty()) {
    decision.status.add(ErrorCode::internal_error, "ImagePath",
                        "audited command line could not be parsed");
    return decision;
  }
  const ParsedWindowsPath executable =
      parse_absolute_windows_path(parsed.argv.front());
  if (!executable.valid) {
    decision.status.add(ErrorCode::internal_error, "ImagePath",
                        "audited executable path could not be normalized");
    return decision;
  }

  LaunchPlan plan;
  plan.service_name = signed_configuration.payload.service_name;
  plan.executable = executable.full;
  plan.argv = parsed.argv;
  plan.argv.front() = executable.full;
  decision.plan = std::move(plan);
  return decision;
}

}  // namespace netsec

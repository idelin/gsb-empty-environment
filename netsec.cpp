#include "netsec.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <string>
#include <utility>

namespace netsec {

namespace {

constexpr std::array<std::uint32_t, 64> kSha256Constants = {
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
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
};

std::uint32_t rotate_right(std::uint32_t value, unsigned amount) noexcept {
  return (value >> amount) | (value << (32u - amount));
}

void sha256_compress(std::array<std::uint32_t, 8>& hash,
                     const std::uint8_t* block) noexcept {
  std::array<std::uint32_t, 64> words{};
  for (std::size_t i = 0; i < 16; ++i) {
    const std::size_t offset = i * 4;
    words[i] = (static_cast<std::uint32_t>(block[offset]) << 24u) |
               (static_cast<std::uint32_t>(block[offset + 1]) << 16u) |
               (static_cast<std::uint32_t>(block[offset + 2]) << 8u) |
               static_cast<std::uint32_t>(block[offset + 3]);
  }
  for (std::size_t i = 16; i < 64; ++i) {
    const std::uint32_t s0 = rotate_right(words[i - 15], 7) ^
                             rotate_right(words[i - 15], 18) ^
                             (words[i - 15] >> 3u);
    const std::uint32_t s1 = rotate_right(words[i - 2], 17) ^
                             rotate_right(words[i - 2], 19) ^
                             (words[i - 2] >> 10u);
    words[i] = words[i - 16] + s0 + words[i - 7] + s1;
  }

  std::uint32_t a = hash[0];
  std::uint32_t b = hash[1];
  std::uint32_t c = hash[2];
  std::uint32_t d = hash[3];
  std::uint32_t e = hash[4];
  std::uint32_t f = hash[5];
  std::uint32_t g = hash[6];
  std::uint32_t h = hash[7];

  for (std::size_t i = 0; i < 64; ++i) {
    const std::uint32_t sum1 = rotate_right(e, 6) ^ rotate_right(e, 11) ^
                               rotate_right(e, 25);
    const std::uint32_t choose = (e & f) ^ (~e & g);
    const std::uint32_t temp1 =
        h + sum1 + choose + kSha256Constants[i] + words[i];
    const std::uint32_t sum0 = rotate_right(a, 2) ^ rotate_right(a, 13) ^
                               rotate_right(a, 22);
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

  hash[0] += a;
  hash[1] += b;
  hash[2] += c;
  hash[3] += d;
  hash[4] += e;
  hash[5] += f;
  hash[6] += g;
  hash[7] += h;
}

std::array<std::uint8_t, 32> sha256_bytes(std::string_view input) {
  std::array<std::uint32_t, 8> hash = {
      0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
      0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u,
  };

  const std::uint64_t bit_length =
      static_cast<std::uint64_t>(input.size()) * 8u;
  std::string padded(input);
  padded.push_back(static_cast<char>(0x80));
  while (padded.size() % 64 != 56) {
    padded.push_back(static_cast<char>(0x00));
  }
  for (int shift = 56; shift >= 0; shift -= 8) {
    padded.push_back(
        static_cast<char>((bit_length >> static_cast<unsigned>(shift)) & 0xffu));
  }

  for (std::size_t offset = 0; offset < padded.size(); offset += 64) {
    sha256_compress(
        hash, reinterpret_cast<const std::uint8_t*>(padded.data() + offset));
  }

  std::array<std::uint8_t, 32> digest{};
  for (std::size_t i = 0; i < hash.size(); ++i) {
    digest[i * 4] = static_cast<std::uint8_t>(hash[i] >> 24u);
    digest[i * 4 + 1] = static_cast<std::uint8_t>(hash[i] >> 16u);
    digest[i * 4 + 2] = static_cast<std::uint8_t>(hash[i] >> 8u);
    digest[i * 4 + 3] = static_cast<std::uint8_t>(hash[i]);
  }
  return digest;
}

void append_u64(std::string& output, std::uint64_t value) {
  for (int shift = 56; shift >= 0; shift -= 8) {
    output.push_back(
        static_cast<char>((value >> static_cast<unsigned>(shift)) & 0xffu));
  }
}

void append_field(std::string& output, std::string_view value) {
  append_u64(output, static_cast<std::uint64_t>(value.size()));
  output.append(value.data(), value.size());
}

}  // namespace

std::string_view to_string(TransactionState state) noexcept {
  switch (state) {
    case TransactionState::Pending:
      return "pending";
    case TransactionState::Issued:
      return "issued";
    case TransactionState::Completed:
      return "completed";
    case TransactionState::Rejected:
      return "rejected";
    case TransactionState::Expired:
      return "expired";
    case TransactionState::Revoked:
      return "revoked";
  }
  return "unknown";
}

std::string_view to_string(ResultCode code) noexcept {
  switch (code) {
    case ResultCode::Created:
      return "created";
    case ResultCode::Applied:
      return "applied";
    case ResultCode::Duplicate:
      return "duplicate";
    case ResultCode::NotFound:
      return "not_found";
    case ResultCode::Conflict:
      return "conflict";
    case ResultCode::InvalidRequest:
      return "invalid_request";
    case ResultCode::NonceReused:
      return "nonce_reused";
    case ResultCode::GenerationMismatch:
      return "generation_mismatch";
    case ResultCode::InvalidState:
      return "invalid_state";
    case ResultCode::Expired:
      return "expired";
    case ResultCode::Rejected:
      return "rejected";
    case ResultCode::Revoked:
      return "revoked";
  }
  return "unknown";
}

std::string sha256_hex(std::string_view data) {
  static constexpr char kHex[] = "0123456789abcdef";
  const std::array<std::uint8_t, 32> digest = sha256_bytes(data);
  std::string output(64, '\0');
  for (std::size_t i = 0; i < digest.size(); ++i) {
    output[i * 2] = kHex[digest[i] >> 4u];
    output[i * 2 + 1] = kHex[digest[i] & 0x0fu];
  }
  return output;
}

std::string compute_request_digest(std::string_view device_id,
                                   std::string_view nonce,
                                   std::string_view request_payload) {
  std::string canonical;
  canonical.reserve(device_id.size() + nonce.size() + request_payload.size() +
                    80);
  canonical.append("netsec.request-digest.v1");
  append_field(canonical, device_id);
  append_field(canonical, nonce);
  append_field(canonical, request_payload);
  return sha256_hex(canonical);
}

TransactionRef make_ref(const RegistrationRequest& request) {
  return request;
}

RegistrationTransactionStore::RegistrationTransactionStore(
    Generation key_generation, Generation template_generation, ClockFn clock)
    : key_generation_(key_generation),
      template_generation_(template_generation),
      clock_(std::move(clock)) {}

bool RegistrationTransactionStore::valid_binding(
    const TransactionBinding& binding) noexcept {
  return !binding.transaction_id.empty() && !binding.device_id.empty() &&
         !binding.nonce.empty() && !binding.request_digest.empty() &&
         binding.not_before < binding.not_after;
}

bool RegistrationTransactionStore::binding_matches(
    const TransactionBinding& lhs,
    const TransactionBinding& rhs) noexcept {
  return lhs.transaction_id == rhs.transaction_id &&
         lhs.device_id == rhs.device_id && lhs.nonce == rhs.nonce &&
         lhs.request_digest == rhs.request_digest &&
         lhs.not_before == rhs.not_before && lhs.not_after == rhs.not_after &&
         lhs.key_generation == rhs.key_generation &&
         lhs.template_generation == rhs.template_generation;
}

bool RegistrationTransactionStore::active_generation_matches(
    const Record& record, Generation key_generation,
    Generation template_generation) noexcept {
  return record.binding.key_generation == key_generation &&
         record.binding.template_generation == template_generation;
}

bool RegistrationTransactionStore::expire_if_due(Record& record,
                                                 TimePoint now) noexcept {
  if (record.state != TransactionState::Pending) {
    return false;
  }
  if (now < record.binding.not_after) {
    return false;
  }
  record.state = TransactionState::Expired;
  record.reason = "transaction time window elapsed";
  record.terminal_at = now;
  return true;
}

TransactionResult RegistrationTransactionStore::result_for(
    ResultCode code, const Record& record, bool state_changed,
    std::string reason) {
  return TransactionResult{code,
                           record.state,
                           record.binding.transaction_id,
                           record.certificate_fingerprint,
                           std::move(reason),
                           state_changed};
}

TransactionResult RegistrationTransactionStore::error_for(
    ResultCode code, std::string transaction_id, std::string reason) {
  return TransactionResult{code, std::nullopt, std::move(transaction_id), {},
                           std::move(reason), false};
}

TransactionSnapshot RegistrationTransactionStore::snapshot_for(
    const Record& record) {
  return TransactionSnapshot{record.binding,
                             record.state,
                             record.certificate_fingerprint,
                             record.reason,
                             record.created_at,
                             record.issued_at,
                             record.completed_at,
                             record.terminal_at};
}

TimePoint RegistrationTransactionStore::current_time() const {
  return clock_ ? clock_() : Clock::now();
}

TransactionResult RegistrationTransactionStore::begin(
    const RegistrationRequest& request) {
  return begin(request, current_time());
}

TransactionResult RegistrationTransactionStore::begin(
    const RegistrationRequest& request, TimePoint now) {
  if (!valid_binding(request)) {
    return error_for(ResultCode::InvalidRequest, request.transaction_id,
                     "invalid transaction binding");
  }

  std::lock_guard<std::mutex> lock(mutex_);
  const auto existing = transactions_.find(request.transaction_id);
  if (existing != transactions_.end()) {
    Record& record = existing->second;
    const bool changed = expire_if_due(record, now);
    if (!binding_matches(record.binding, request)) {
      return result_for(
          ResultCode::Conflict, record, changed,
          "transaction identifier is already bound to a different request");
    }
    switch (record.state) {
      case TransactionState::Expired:
        return result_for(ResultCode::Expired, record, changed, record.reason);
      case TransactionState::Rejected:
        return result_for(ResultCode::Rejected, record, false, record.reason);
      case TransactionState::Revoked:
        return result_for(ResultCode::Revoked, record, false, record.reason);
      case TransactionState::Pending:
      case TransactionState::Issued:
      case TransactionState::Completed:
        return result_for(ResultCode::Duplicate, record, false, record.reason);
    }
  }

  if (now < request.not_before) {
    return error_for(ResultCode::InvalidRequest, request.transaction_id,
                     "transaction is not yet valid");
  }
  if (request.key_generation != key_generation_ ||
      request.template_generation != template_generation_) {
    return error_for(ResultCode::GenerationMismatch, request.transaction_id,
                     "key or template generation is not current");
  }
  if (nonce_index_.find(request.nonce) != nonce_index_.end()) {
    return error_for(ResultCode::NonceReused, request.transaction_id,
                     "nonce has already been used");
  }

  Record record;
  record.binding = request;
  record.created_at = now;
  record.reason = "transaction created";
  if (now >= request.not_after) {
    record.state = TransactionState::Expired;
    record.reason = "request arrived outside the transaction time window";
    record.terminal_at = now;
  }

  transactions_.emplace(request.transaction_id, record);
  nonce_index_.emplace(request.nonce, request.transaction_id);

  if (record.state == TransactionState::Expired) {
    return result_for(ResultCode::Expired, record, true, record.reason);
  }
  return result_for(ResultCode::Created, record, true, record.reason);
}

TransactionResult RegistrationTransactionStore::issue(const IssueRequest& request) {
  return issue(request, current_time());
}

TransactionResult RegistrationTransactionStore::issue(const IssueRequest& request,
                                                      TimePoint now) {
  if (!valid_binding(request.transaction) ||
      request.certificate_fingerprint.empty()) {
    return error_for(ResultCode::InvalidRequest,
                     request.transaction.transaction_id,
                     "invalid issue request");
  }

  std::lock_guard<std::mutex> lock(mutex_);
  const auto found = transactions_.find(request.transaction.transaction_id);
  if (found == transactions_.end()) {
    return error_for(ResultCode::NotFound,
                     request.transaction.transaction_id,
                     "transaction does not exist");
  }

  Record& record = found->second;
  const bool changed = expire_if_due(record, now);
  if (!binding_matches(record.binding, request.transaction)) {
    return result_for(ResultCode::Conflict, record, changed,
                      "issue request does not match the bound transaction");
  }
  if (now < record.binding.not_before) {
    return result_for(ResultCode::InvalidRequest, record, false,
                      "transaction is not yet valid");
  }

  switch (record.state) {
    case TransactionState::Expired:
      return result_for(ResultCode::Expired, record, changed, record.reason);
    case TransactionState::Rejected:
      return result_for(ResultCode::Rejected, record, false, record.reason);
    case TransactionState::Revoked:
      if (record.certificate_fingerprint ==
          request.certificate_fingerprint) {
        return result_for(ResultCode::Revoked, record, false, record.reason);
      }
      return result_for(ResultCode::Conflict, record, false,
                        "certificate does not match the revoked transaction");
    case TransactionState::Completed:
    case TransactionState::Issued:
      if (record.certificate_fingerprint ==
          request.certificate_fingerprint) {
        return result_for(ResultCode::Duplicate, record, false, record.reason);
      }
      return result_for(ResultCode::Conflict, record, false,
                        "certificate does not match the issued result");
    case TransactionState::Pending:
      break;
  }

  if (!active_generation_matches(record, key_generation_,
                                 template_generation_)) {
    return result_for(ResultCode::GenerationMismatch, record, false,
                      "key or template generation is not current");
  }

  record.state = TransactionState::Issued;
  record.certificate_fingerprint = request.certificate_fingerprint;
  record.issued_at = now;
  record.reason = "certificate issued";
  return result_for(ResultCode::Applied, record, true, record.reason);
}

TransactionResult RegistrationTransactionStore::complete(
    const CompleteRequest& request) {
  return complete(request, current_time());
}

TransactionResult RegistrationTransactionStore::complete(
    const CompleteRequest& request, TimePoint now) {
  if (!valid_binding(request.transaction) ||
      request.certificate_fingerprint.empty()) {
    return error_for(ResultCode::InvalidRequest,
                     request.transaction.transaction_id,
                     "invalid complete request");
  }

  std::lock_guard<std::mutex> lock(mutex_);
  const auto found = transactions_.find(request.transaction.transaction_id);
  if (found == transactions_.end()) {
    return error_for(ResultCode::NotFound,
                     request.transaction.transaction_id,
                     "transaction does not exist");
  }

  Record& record = found->second;
  const bool changed = expire_if_due(record, now);
  if (!binding_matches(record.binding, request.transaction)) {
    return result_for(ResultCode::Conflict, record, changed,
                      "complete request does not match the bound transaction");
  }
  if (now < record.binding.not_before) {
    return result_for(ResultCode::InvalidRequest, record, false,
                      "transaction is not yet valid");
  }

  switch (record.state) {
    case TransactionState::Expired:
      return result_for(ResultCode::Expired, record, changed, record.reason);
    case TransactionState::Rejected:
      return result_for(ResultCode::Rejected, record, false, record.reason);
    case TransactionState::Revoked:
      if (record.certificate_fingerprint ==
          request.certificate_fingerprint) {
        return result_for(ResultCode::Revoked, record, false, record.reason);
      }
      return result_for(ResultCode::Conflict, record, false,
                        "certificate does not match the revoked transaction");
    case TransactionState::Pending:
      return result_for(ResultCode::InvalidState, record, false,
                        "transaction has not been issued");
    case TransactionState::Completed:
      if (record.certificate_fingerprint ==
          request.certificate_fingerprint) {
        return result_for(ResultCode::Duplicate, record, false, record.reason);
      }
      return result_for(ResultCode::Conflict, record, false,
                        "certificate does not match the completed transaction");
    case TransactionState::Issued:
      break;
  }

  if (record.certificate_fingerprint != request.certificate_fingerprint) {
    return result_for(ResultCode::Conflict, record, false,
                      "certificate does not match the issued result");
  }
  if (!active_generation_matches(record, key_generation_,
                                 template_generation_)) {
    return result_for(ResultCode::GenerationMismatch, record, false,
                      "key or template generation is not current");
  }

  record.state = TransactionState::Completed;
  record.completed_at = now;
  record.reason = "registration completed";
  return result_for(ResultCode::Applied, record, true, record.reason);
}

TransactionResult RegistrationTransactionStore::reject(
    const RejectRequest& request) {
  return reject(request, current_time());
}

TransactionResult RegistrationTransactionStore::reject(
    const RejectRequest& request, TimePoint now) {
  if (!valid_binding(request.transaction)) {
    return error_for(ResultCode::InvalidRequest,
                     request.transaction.transaction_id,
                     "invalid reject request");
  }

  std::lock_guard<std::mutex> lock(mutex_);
  const auto found = transactions_.find(request.transaction.transaction_id);
  if (found == transactions_.end()) {
    return error_for(ResultCode::NotFound,
                     request.transaction.transaction_id,
                     "transaction does not exist");
  }

  Record& record = found->second;
  const bool changed = expire_if_due(record, now);
  if (!binding_matches(record.binding, request.transaction)) {
    return result_for(ResultCode::Conflict, record, changed,
                      "reject request does not match the bound transaction");
  }
  if (now < record.binding.not_before) {
    return result_for(ResultCode::InvalidRequest, record, false,
                      "transaction is not yet valid");
  }

  switch (record.state) {
    case TransactionState::Expired:
      return result_for(ResultCode::Expired, record, changed, record.reason);
    case TransactionState::Rejected:
      return result_for(ResultCode::Rejected, record, false, record.reason);
    case TransactionState::Revoked:
      return result_for(ResultCode::Revoked, record, false, record.reason);
    case TransactionState::Issued:
    case TransactionState::Completed:
      return result_for(ResultCode::InvalidState, record, false,
                        "issued certificates must be revoked, not rejected");
    case TransactionState::Pending:
      break;
  }

  record.state = TransactionState::Rejected;
  record.reason =
      request.reason.empty() ? "registration rejected" : request.reason;
  record.terminal_at = now;
  return result_for(ResultCode::Applied, record, true, record.reason);
}

TransactionResult RegistrationTransactionStore::revoke(
    const RevokeRequest& request) {
  return revoke(request, current_time());
}

TransactionResult RegistrationTransactionStore::revoke(
    const RevokeRequest& request, TimePoint now) {
  if (!valid_binding(request.transaction) ||
      request.certificate_fingerprint.empty()) {
    return error_for(ResultCode::InvalidRequest,
                     request.transaction.transaction_id,
                     "invalid revoke request");
  }

  std::lock_guard<std::mutex> lock(mutex_);
  const auto found = transactions_.find(request.transaction.transaction_id);
  if (found == transactions_.end()) {
    return error_for(ResultCode::NotFound,
                     request.transaction.transaction_id,
                     "transaction does not exist");
  }

  Record& record = found->second;
  const bool changed = expire_if_due(record, now);
  if (!binding_matches(record.binding, request.transaction)) {
    return result_for(ResultCode::Conflict, record, changed,
                      "revoke request does not match the bound transaction");
  }
  if (now < record.binding.not_before) {
    return result_for(ResultCode::InvalidRequest, record, false,
                      "transaction is not yet valid");
  }

  switch (record.state) {
    case TransactionState::Expired:
      return result_for(ResultCode::Expired, record, changed, record.reason);
    case TransactionState::Rejected:
      return result_for(ResultCode::Rejected, record, false, record.reason);
    case TransactionState::Revoked:
      if (record.certificate_fingerprint ==
          request.certificate_fingerprint) {
        return result_for(ResultCode::Revoked, record, false, record.reason);
      }
      return result_for(ResultCode::Conflict, record, false,
                        "certificate does not match the revoked transaction");
    case TransactionState::Pending:
      return result_for(ResultCode::InvalidState, record, false,
                        "pending transactions must be rejected, not revoked");
    case TransactionState::Issued:
    case TransactionState::Completed:
      break;
  }

  if (record.certificate_fingerprint != request.certificate_fingerprint) {
    return result_for(ResultCode::Conflict, record, false,
                      "certificate does not match the issued result");
  }

  record.state = TransactionState::Revoked;
  record.reason =
      request.reason.empty() ? "certificate revoked" : request.reason;
  record.terminal_at = now;
  return result_for(ResultCode::Applied, record, true, record.reason);
}

std::vector<TransactionResult> RegistrationTransactionStore::expire_due() {
  return expire_due(current_time());
}

std::vector<TransactionResult> RegistrationTransactionStore::expire_due(
    TimePoint now) {
  std::lock_guard<std::mutex> lock(mutex_);
  std::vector<TransactionResult> expired;
  for (auto& entry : transactions_) {
    Record& record = entry.second;
    if (expire_if_due(record, now)) {
      expired.push_back(
          result_for(ResultCode::Expired, record, true, record.reason));
    }
  }
  return expired;
}

std::optional<TransactionSnapshot> RegistrationTransactionStore::find(
    std::string_view transaction_id) const {
  std::lock_guard<std::mutex> lock(mutex_);
  const auto found = transactions_.find(transaction_id);
  if (found == transactions_.end()) {
    return std::nullopt;
  }
  return snapshot_for(found->second);
}

std::optional<TransactionSnapshot> RegistrationTransactionStore::find(
    std::string_view transaction_id, TimePoint now) const {
  std::lock_guard<std::mutex> lock(mutex_);
  const auto found = transactions_.find(transaction_id);
  if (found == transactions_.end()) {
    return std::nullopt;
  }
  expire_if_due(found->second, now);
  return snapshot_for(found->second);
}

bool RegistrationTransactionStore::advance_key_generation(Generation next) {
  std::lock_guard<std::mutex> lock(mutex_);
  if (next <= key_generation_) {
    return false;
  }
  key_generation_ = next;
  return true;
}

bool RegistrationTransactionStore::advance_template_generation(Generation next) {
  std::lock_guard<std::mutex> lock(mutex_);
  if (next <= template_generation_) {
    return false;
  }
  template_generation_ = next;
  return true;
}

Generation RegistrationTransactionStore::key_generation() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return key_generation_;
}

Generation RegistrationTransactionStore::template_generation() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return template_generation_;
}

std::size_t RegistrationTransactionStore::size() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return transactions_.size();
}

}  // namespace netsec

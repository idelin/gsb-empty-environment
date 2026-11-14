#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Clock = std::chrono::system_clock;
using TimePoint = Clock::time_point;
using Generation = std::uint64_t;
using ClockFn = std::function<TimePoint()>;

enum class TransactionState : std::uint8_t {
  Pending,
  Issued,
  Completed,
  Rejected,
  Expired,
  Revoked,
};

enum class ResultCode : std::uint8_t {
  Created,
  Applied,
  Duplicate,
  NotFound,
  Conflict,
  InvalidRequest,
  NonceReused,
  GenerationMismatch,
  InvalidState,
  Expired,
  Rejected,
  Revoked,
};

std::string_view to_string(TransactionState state) noexcept;
std::string_view to_string(ResultCode code) noexcept;

struct TransactionBinding {
  std::string transaction_id;
  std::string device_id;
  std::string nonce;
  std::string request_digest;
  TimePoint not_before{};
  TimePoint not_after{};
  Generation key_generation = 0;
  Generation template_generation = 0;
};

using RegistrationRequest = TransactionBinding;
using TransactionRef = TransactionBinding;

TransactionRef make_ref(const RegistrationRequest& request);

struct TransactionSnapshot {
  TransactionBinding binding;
  TransactionState state = TransactionState::Pending;
  std::string certificate_fingerprint;
  std::string reason;
  TimePoint created_at{};
  std::optional<TimePoint> issued_at;
  std::optional<TimePoint> completed_at;
  std::optional<TimePoint> terminal_at;
};

struct TransactionResult {
  ResultCode code = ResultCode::NotFound;
  std::optional<TransactionState> state;
  std::string transaction_id;
  std::string certificate_fingerprint;
  std::string reason;
  bool state_changed = false;

  bool ok() const noexcept {
    return code == ResultCode::Created || code == ResultCode::Applied ||
           code == ResultCode::Duplicate;
  }

  bool duplicate() const noexcept { return code == ResultCode::Duplicate; }
};

struct IssueRequest {
  TransactionRef transaction;
  std::string certificate_fingerprint;
};

struct CompleteRequest {
  TransactionRef transaction;
  std::string certificate_fingerprint;
};

struct RejectRequest {
  TransactionRef transaction;
  std::string reason;
};

struct RevokeRequest {
  TransactionRef transaction;
  std::string certificate_fingerprint;
  std::string reason;
};

std::string sha256_hex(std::string_view data);
std::string compute_request_digest(std::string_view device_id,
                                   std::string_view nonce,
                                   std::string_view request_payload);

class RegistrationTransactionStore {
 public:
  explicit RegistrationTransactionStore(Generation key_generation = 1,
                                        Generation template_generation = 1,
                                        ClockFn clock = {});

  TransactionResult begin(const RegistrationRequest& request);
  TransactionResult begin(const RegistrationRequest& request, TimePoint now);

  TransactionResult issue(const IssueRequest& request);
  TransactionResult issue(const IssueRequest& request, TimePoint now);

  TransactionResult complete(const CompleteRequest& request);
  TransactionResult complete(const CompleteRequest& request, TimePoint now);

  TransactionResult reject(const RejectRequest& request);
  TransactionResult reject(const RejectRequest& request, TimePoint now);

  TransactionResult revoke(const RevokeRequest& request);
  TransactionResult revoke(const RevokeRequest& request, TimePoint now);

  std::vector<TransactionResult> expire_due();
  std::vector<TransactionResult> expire_due(TimePoint now);

  std::optional<TransactionSnapshot> find(std::string_view transaction_id) const;
  std::optional<TransactionSnapshot> find(std::string_view transaction_id,
                                          TimePoint now) const;

  bool advance_key_generation(Generation next);
  bool advance_template_generation(Generation next);

  Generation key_generation() const;
  Generation template_generation() const;
  std::size_t size() const;

 private:
  struct Record {
    TransactionBinding binding;
    TransactionState state = TransactionState::Pending;
    std::string certificate_fingerprint;
    std::string reason;
    TimePoint created_at{};
    std::optional<TimePoint> issued_at;
    std::optional<TimePoint> completed_at;
    std::optional<TimePoint> terminal_at;
  };

  static TransactionResult result_for(ResultCode code, const Record& record,
                                      bool state_changed, std::string reason);
  static TransactionResult error_for(ResultCode code, std::string transaction_id,
                                     std::string reason);
  static bool valid_binding(const TransactionBinding& binding) noexcept;
  static bool binding_matches(const TransactionBinding& lhs,
                              const TransactionBinding& rhs) noexcept;
  static TransactionSnapshot snapshot_for(const Record& record);
  static bool active_generation_matches(const Record& record,
                                        Generation key_generation,
                                        Generation template_generation) noexcept;

  static bool expire_if_due(Record& record, TimePoint now) noexcept;
  TimePoint current_time() const;

  mutable std::mutex mutex_;
  mutable std::map<std::string, Record, std::less<>> transactions_;
  std::map<std::string, std::string, std::less<>> nonce_index_;
  Generation key_generation_;
  Generation template_generation_;
  ClockFn clock_;
};

}  // namespace netsec

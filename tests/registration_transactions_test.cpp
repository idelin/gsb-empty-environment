#include "netsec.hpp"

#include <chrono>
#include <iostream>
#include <string>
#include <string_view>

namespace {

int failures = 0;

void check(bool condition, const char* expression, const char* file, int line) {
  if (condition) {
    return;
  }
  std::cerr << file << ':' << line << ": check failed: " << expression << '\n';
  ++failures;
}

#define CHECK(expression) check((expression), #expression, __FILE__, __LINE__)

netsec::TimePoint at_seconds(long long seconds) {
  return netsec::TimePoint{std::chrono::seconds{seconds}};
}

struct ManualClock {
  netsec::TimePoint now;

  netsec::TimePoint operator()() const noexcept { return now; }
};

netsec::RegistrationRequest make_request(
    std::string transaction_id, std::string device_id, std::string nonce,
    netsec::TimePoint start, netsec::Generation key_generation = 1,
    netsec::Generation template_generation = 1,
    std::string payload = "csr-payload",
    std::chrono::seconds window = std::chrono::seconds(60)) {
  const std::string digest =
      netsec::compute_request_digest(device_id, nonce, payload);
  return netsec::RegistrationRequest{
      std::move(transaction_id),
      std::move(device_id),
      std::move(nonce),
      digest,
      start,
      start + window,
      key_generation,
      template_generation,
  };
}

netsec::IssueRequest issue_request(const netsec::RegistrationRequest& request,
                                   std::string certificate) {
  return netsec::IssueRequest{request, std::move(certificate)};
}

netsec::CompleteRequest complete_request(
    const netsec::RegistrationRequest& request, std::string certificate) {
  return netsec::CompleteRequest{request, std::move(certificate)};
}

netsec::RevokeRequest revoke_request(const netsec::RegistrationRequest& request,
                                     std::string certificate,
                                     std::string reason) {
  return netsec::RevokeRequest{request, std::move(certificate),
                               std::move(reason)};
}

bool has_state(const netsec::TransactionResult& result,
               netsec::TransactionState state) {
  return result.state.has_value() && *result.state == state;
}

void test_sha256_and_request_digest() {
  CHECK(netsec::sha256_hex("") ==
        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
  CHECK(netsec::sha256_hex("abc") ==
        "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  CHECK(netsec::sha256_hex(
            "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq") ==
        "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1");

  const std::string first =
      netsec::compute_request_digest("device-a", "nonce-a", "csr-a");
  const std::string second =
      netsec::compute_request_digest("device-a", "nonce-a", "csr-b");
  CHECK(first.size() == 64);
  CHECK(second.size() == 64);
  CHECK(first != second);
}

void test_legal_registration_is_idempotent() {
  const netsec::TimePoint base = at_seconds(1000);
  ManualClock clock{base};
  netsec::RegistrationTransactionStore store(1, 1,
                                             [&clock] { return clock.now; });
  const netsec::RegistrationRequest request =
      make_request("tx-legal", "device-legal", "nonce-legal", base);

  const netsec::TransactionResult created = store.begin(request, base);
  CHECK(created.code == netsec::ResultCode::Created);
  CHECK(has_state(created, netsec::TransactionState::Pending));
  CHECK(created.state_changed);
  CHECK(created.transaction_id == "tx-legal");

  const netsec::TransactionResult duplicate_begin =
      store.begin(request, base + std::chrono::seconds(1));
  CHECK(duplicate_begin.code == netsec::ResultCode::Duplicate);
  CHECK(has_state(duplicate_begin, netsec::TransactionState::Pending));
  CHECK(!duplicate_begin.state_changed);
  CHECK(duplicate_begin.reason == created.reason);

  const netsec::TransactionResult issued =
      store.issue(issue_request(request, "cert-legal"), base + std::chrono::seconds(2));
  CHECK(issued.code == netsec::ResultCode::Applied);
  CHECK(has_state(issued, netsec::TransactionState::Issued));
  CHECK(issued.certificate_fingerprint == "cert-legal");
  CHECK(issued.state_changed);

  const netsec::TransactionResult duplicate_issue =
      store.issue(issue_request(request, "cert-legal"),
                  base + std::chrono::seconds(3));
  CHECK(duplicate_issue.code == netsec::ResultCode::Duplicate);
  CHECK(has_state(duplicate_issue, netsec::TransactionState::Issued));
  CHECK(duplicate_issue.certificate_fingerprint == "cert-legal");
  CHECK(!duplicate_issue.state_changed);
  CHECK(duplicate_issue.reason == issued.reason);

  const netsec::TransactionResult completed =
      store.complete(complete_request(request, "cert-legal"),
                     base + std::chrono::seconds(4));
  CHECK(completed.code == netsec::ResultCode::Applied);
  CHECK(has_state(completed, netsec::TransactionState::Completed));
  CHECK(completed.certificate_fingerprint == "cert-legal");
  CHECK(completed.state_changed);

  const netsec::TransactionResult duplicate_complete =
      store.complete(complete_request(request, "cert-legal"),
                     base + std::chrono::seconds(5));
  CHECK(duplicate_complete.code == netsec::ResultCode::Duplicate);
  CHECK(has_state(duplicate_complete, netsec::TransactionState::Completed));
  CHECK(duplicate_complete.certificate_fingerprint == "cert-legal");
  CHECK(!duplicate_complete.state_changed);
  CHECK(duplicate_complete.reason == completed.reason);

  const auto snapshot = store.find("tx-legal");
  CHECK(snapshot.has_value());
  CHECK(snapshot->state == netsec::TransactionState::Completed);
  CHECK(snapshot->certificate_fingerprint == "cert-legal");
  CHECK(snapshot->issued_at.has_value());
  CHECK(snapshot->completed_at.has_value());
  CHECK(!snapshot->terminal_at.has_value());
}

void test_replayed_requests_do_not_change_completed_transaction() {
  const netsec::TimePoint base = at_seconds(2000);
  netsec::RegistrationTransactionStore store(1, 1);
  const netsec::RegistrationRequest request =
      make_request("tx-replay", "device-replay", "nonce-replay", base);

  CHECK(store.begin(request, base).code == netsec::ResultCode::Created);
  CHECK(store.issue(issue_request(request, "cert-original"),
                    base + std::chrono::seconds(1))
            .code == netsec::ResultCode::Applied);
  CHECK(store.complete(complete_request(request, "cert-original"),
                       base + std::chrono::seconds(2))
            .code == netsec::ResultCode::Applied);

  const auto before = store.find("tx-replay");
  CHECK(before.has_value());
  CHECK(before->state == netsec::TransactionState::Completed);
  CHECK(before->certificate_fingerprint == "cert-original");

  const netsec::TransactionResult replayed_begin =
      store.begin(request, base + std::chrono::seconds(30));
  CHECK(replayed_begin.code == netsec::ResultCode::Duplicate);
  CHECK(has_state(replayed_begin, netsec::TransactionState::Completed));
  CHECK(replayed_begin.certificate_fingerprint == "cert-original");
  CHECK(!replayed_begin.state_changed);

  const netsec::TransactionResult attacker_issue =
      store.issue(issue_request(request, "cert-attacker"),
                  base + std::chrono::seconds(31));
  CHECK(attacker_issue.code == netsec::ResultCode::Conflict);
  CHECK(has_state(attacker_issue, netsec::TransactionState::Completed));
  CHECK(attacker_issue.certificate_fingerprint == "cert-original");
  CHECK(!attacker_issue.state_changed);

  const netsec::TransactionResult attacker_complete =
      store.complete(complete_request(request, "cert-attacker"),
                     base + std::chrono::seconds(32));
  CHECK(attacker_complete.code == netsec::ResultCode::Conflict);
  CHECK(has_state(attacker_complete, netsec::TransactionState::Completed));
  CHECK(attacker_complete.certificate_fingerprint == "cert-original");
  CHECK(!attacker_complete.state_changed);

  const netsec::TransactionResult replayed_reject =
      store.reject(netsec::RejectRequest{request, "replay"},
                   base + std::chrono::seconds(33));
  CHECK(replayed_reject.code == netsec::ResultCode::InvalidState);
  CHECK(has_state(replayed_reject, netsec::TransactionState::Completed));
  CHECK(!replayed_reject.state_changed);

  const netsec::TransactionResult duplicate_issue_after_complete =
      store.issue(issue_request(request, "cert-original"),
                  base + std::chrono::seconds(34));
  CHECK(duplicate_issue_after_complete.code == netsec::ResultCode::Duplicate);
  CHECK(has_state(duplicate_issue_after_complete,
                  netsec::TransactionState::Completed));
  CHECK(duplicate_issue_after_complete.certificate_fingerprint ==
        "cert-original");
  CHECK(!duplicate_issue_after_complete.state_changed);

  const auto after = store.find("tx-replay");
  CHECK(after.has_value());
  CHECK(after->state == netsec::TransactionState::Completed);
  CHECK(after->certificate_fingerprint == "cert-original");
  CHECK(after->reason == before->reason);
}

void test_transaction_substitution_is_rejected() {
  const netsec::TimePoint base = at_seconds(3000);
  netsec::RegistrationTransactionStore store(1, 1);
  const netsec::RegistrationRequest original =
      make_request("tx-swap", "device-swap", "nonce-swap", base);

  CHECK(store.begin(original, base).code == netsec::ResultCode::Created);
  const auto before = store.find("tx-swap");
  CHECK(before.has_value());
  CHECK(before->binding.device_id == "device-swap");
  CHECK(before->binding.nonce == "nonce-swap");

  netsec::RegistrationRequest swapped = original;
  swapped.device_id = "device-attacker";
  swapped.nonce = "nonce-substitute";
  swapped.request_digest = netsec::compute_request_digest(
      swapped.device_id, swapped.nonce, "attacker-csr");

  const netsec::TransactionResult replaced = store.begin(swapped, base);
  CHECK(replaced.code == netsec::ResultCode::Conflict);
  CHECK(has_state(replaced, netsec::TransactionState::Pending));
  CHECK(!replaced.state_changed);

  netsec::RegistrationRequest device_swapped = original;
  device_swapped.device_id = "device-attacker";
  device_swapped.request_digest = netsec::compute_request_digest(
      device_swapped.device_id, device_swapped.nonce, "attacker-csr");
  CHECK(store.begin(device_swapped, base).code ==
        netsec::ResultCode::Conflict);

  netsec::RegistrationRequest changed_digest = original;
  changed_digest.request_digest = netsec::compute_request_digest(
      original.device_id, original.nonce, "different-csr");
  CHECK(store.begin(changed_digest, base).code == netsec::ResultCode::Conflict);

  netsec::RegistrationRequest changed_window = original;
  changed_window.not_after = original.not_after + std::chrono::seconds(30);
  CHECK(store.begin(changed_window, base).code == netsec::ResultCode::Conflict);

  netsec::RegistrationRequest changed_generation = original;
  changed_generation.key_generation = 2;
  CHECK(store.begin(changed_generation, base).code ==
        netsec::ResultCode::Conflict);

  const netsec::TransactionResult altered_issue =
      store.issue(issue_request(changed_window, "cert-swap"),
                  base + std::chrono::seconds(1));
  CHECK(altered_issue.code == netsec::ResultCode::Conflict);
  CHECK(has_state(altered_issue, netsec::TransactionState::Pending));
  CHECK(!altered_issue.state_changed);

  const auto after = store.find("tx-swap");
  CHECK(after.has_value());
  CHECK(after->binding.device_id == "device-swap");
  CHECK(after->binding.nonce == "nonce-swap");
  CHECK(after->binding.request_digest == original.request_digest);
  CHECK(after->binding.not_after == original.not_after);
  CHECK(after->state == netsec::TransactionState::Pending);

  const netsec::RegistrationRequest fresh =
      make_request("tx-fresh", "device-fresh", "nonce-substitute", base);
  CHECK(store.begin(fresh, base).code == netsec::ResultCode::Created);
}

void test_nonce_reuse_is_rejected() {
  const netsec::TimePoint base = at_seconds(4000);
  netsec::RegistrationTransactionStore store(1, 1);
  const netsec::RegistrationRequest first =
      make_request("tx-nonce-1", "device-nonce", "nonce-shared", base);
  CHECK(store.begin(first, base).code == netsec::ResultCode::Created);

  const netsec::RegistrationRequest second =
      make_request("tx-nonce-2", "device-nonce", "nonce-shared", base);
  const netsec::TransactionResult reused = store.begin(second, base);
  CHECK(reused.code == netsec::ResultCode::NonceReused);
  CHECK(!reused.state.has_value());
  CHECK(!reused.state_changed);
  CHECK(!store.find("tx-nonce-2").has_value());
  CHECK(store.find("tx-nonce-1").has_value());

  const netsec::RegistrationRequest other_device =
      make_request("tx-nonce-3", "device-other", "nonce-shared", base);
  CHECK(store.begin(other_device, base).code ==
        netsec::ResultCode::NonceReused);

  CHECK(store.expire_due(base + std::chrono::seconds(61)).size() == 1);
  const auto expired = store.find("tx-nonce-1");
  CHECK(expired.has_value());
  CHECK(expired->state == netsec::TransactionState::Expired);

  const netsec::RegistrationRequest after_expiry =
      make_request("tx-nonce-4", "device-nonce", "nonce-shared", base);
  CHECK(store.begin(after_expiry, base + std::chrono::seconds(61)).code ==
        netsec::ResultCode::NonceReused);

  const netsec::TransactionResult duplicate_same_transaction =
      store.begin(first, base + std::chrono::seconds(61));
  CHECK(duplicate_same_transaction.code == netsec::ResultCode::Expired);
  CHECK(has_state(duplicate_same_transaction, netsec::TransactionState::Expired));
  CHECK(!duplicate_same_transaction.state_changed);
}

void test_timeout_turns_pending_into_terminal_expired() {
  const netsec::TimePoint base = at_seconds(5000);
  netsec::RegistrationTransactionStore store(1, 1);
  const netsec::RegistrationRequest request =
      make_request("tx-timeout", "device-timeout", "nonce-timeout", base);

  CHECK(store.begin(request, base).code == netsec::ResultCode::Created);
  const auto before = store.find("tx-timeout");
  CHECK(before.has_value());
  CHECK(before->state == netsec::TransactionState::Pending);

  const netsec::TransactionResult late_begin =
      store.begin(request, base + std::chrono::seconds(61));
  CHECK(late_begin.code == netsec::ResultCode::Expired);
  CHECK(has_state(late_begin, netsec::TransactionState::Expired));
  CHECK(late_begin.state_changed);
  CHECK(late_begin.certificate_fingerprint.empty());
  CHECK(!late_begin.reason.empty());

  const netsec::TransactionResult late_issue =
      store.issue(issue_request(request, "cert-late"),
                  base + std::chrono::seconds(61));
  CHECK(late_issue.code == netsec::ResultCode::Expired);
  CHECK(has_state(late_issue, netsec::TransactionState::Expired));
  CHECK(late_issue.certificate_fingerprint.empty());
  CHECK(!late_issue.state_changed);

  const netsec::TransactionResult late_complete =
      store.complete(complete_request(request, "cert-late"),
                     base + std::chrono::seconds(62));
  CHECK(late_complete.code == netsec::ResultCode::Expired);
  CHECK(has_state(late_complete, netsec::TransactionState::Expired));
  CHECK(!late_complete.state_changed);

  const netsec::TransactionResult late_reject =
      store.reject(netsec::RejectRequest{request, "late"},
                   base + std::chrono::seconds(63));
  CHECK(late_reject.code == netsec::ResultCode::Expired);
  CHECK(has_state(late_reject, netsec::TransactionState::Expired));
  CHECK(!late_reject.state_changed);

  const netsec::TransactionResult late_revoke =
      store.revoke(revoke_request(request, "cert-late", "late"),
                   base + std::chrono::seconds(64));
  CHECK(late_revoke.code == netsec::ResultCode::Expired);
  CHECK(has_state(late_revoke, netsec::TransactionState::Expired));
  CHECK(!late_revoke.state_changed);

  const netsec::TransactionResult replayed =
      store.begin(request, base + std::chrono::seconds(200));
  CHECK(replayed.code == netsec::ResultCode::Expired);
  CHECK(has_state(replayed, netsec::TransactionState::Expired));
  CHECK(!replayed.state_changed);

  const auto after = store.find("tx-timeout");
  CHECK(after.has_value());
  CHECK(after->state == netsec::TransactionState::Expired);
  CHECK(after->certificate_fingerprint.empty());
  CHECK(after->terminal_at.has_value());
  CHECK(after->reason == before->reason ||
        after->reason == "transaction time window elapsed");
}

void test_explicit_expiry_and_late_arrival() {
  const netsec::TimePoint base = at_seconds(6000);
  netsec::RegistrationTransactionStore store(1, 1);

  const netsec::RegistrationRequest pending =
      make_request("tx-expire", "device-expire", "nonce-expire", base);
  CHECK(store.begin(pending, base).code == netsec::ResultCode::Created);
  const netsec::RegistrationRequest pending_due =
      make_request("tx-expire-due", "device-expire", "nonce-expire-due", base);
  CHECK(store.begin(pending_due, base).code == netsec::ResultCode::Created);

  const auto inspected =
      store.find("tx-expire", base + std::chrono::seconds(61));
  CHECK(inspected.has_value());
  CHECK(inspected->state == netsec::TransactionState::Expired);

  const std::vector<netsec::TransactionResult> expired =
      store.expire_due(base + std::chrono::seconds(61));
  CHECK(expired.size() == 1);
  CHECK(expired.front().code == netsec::ResultCode::Expired);
  CHECK(has_state(expired.front(), netsec::TransactionState::Expired));
  CHECK(expired.front().state_changed);
  CHECK(store.expire_due(base + std::chrono::seconds(62)).empty());

  const netsec::RegistrationRequest late =
      make_request("tx-late", "device-late", "nonce-late", base);
  const netsec::TransactionResult late_begin =
      store.begin(late, base + std::chrono::seconds(61));
  CHECK(late_begin.code == netsec::ResultCode::Expired);
  CHECK(has_state(late_begin, netsec::TransactionState::Expired));
  CHECK(late_begin.state_changed);
  CHECK(late_begin.certificate_fingerprint.empty());

  const netsec::RegistrationRequest reuse_late_nonce =
      make_request("tx-late-2", "device-late", "nonce-late", base);
  CHECK(store.begin(reuse_late_nonce, base + std::chrono::seconds(61)).code ==
        netsec::ResultCode::NonceReused);

  const netsec::RegistrationRequest future =
      make_request("tx-future", "device-future", "nonce-future",
                   base + std::chrono::seconds(120));
  const netsec::TransactionResult too_early = store.begin(future, base);
  CHECK(too_early.code == netsec::ResultCode::InvalidRequest);
  CHECK(!too_early.state.has_value());
  CHECK(!too_early.state_changed);
  CHECK(!store.find("tx-future").has_value());
}

void test_key_and_template_generations_are_enforced() {
  const netsec::TimePoint base = at_seconds(7000);
  netsec::RegistrationTransactionStore store(1, 1);

  const netsec::RegistrationRequest current =
      make_request("tx-gen", "device-gen", "nonce-gen", base, 1, 1);
  CHECK(store.begin(current, base).code == netsec::ResultCode::Created);
  CHECK(store.advance_key_generation(2));

  const netsec::TransactionResult stale_issue =
      store.issue(issue_request(current, "cert-gen"),
                  base + std::chrono::seconds(1));
  CHECK(stale_issue.code == netsec::ResultCode::GenerationMismatch);
  CHECK(has_state(stale_issue, netsec::TransactionState::Pending));
  CHECK(stale_issue.certificate_fingerprint.empty());
  CHECK(!stale_issue.state_changed);

  CHECK(!store.advance_key_generation(1));
  CHECK(!store.advance_key_generation(2));

  const netsec::RegistrationRequest new_key_generation =
      make_request("tx-gen-2", "device-gen", "nonce-gen-2", base, 2, 1);
  CHECK(store.begin(new_key_generation, base + std::chrono::seconds(2))
            .code == netsec::ResultCode::Created);

  const netsec::RegistrationRequest stale_new =
      make_request("tx-gen-stale", "device-gen", "nonce-gen-stale", base, 1, 1);
  const netsec::TransactionResult stale_begin = store.begin(stale_new, base);
  CHECK(stale_begin.code == netsec::ResultCode::GenerationMismatch);
  CHECK(!stale_begin.state.has_value());
  CHECK(!store.find("tx-gen-stale").has_value());

  const netsec::RegistrationRequest corrected_nonce =
      make_request("tx-gen-correct", "device-gen", "nonce-gen-stale", base, 2, 1);
  CHECK(store.begin(corrected_nonce, base).code ==
        netsec::ResultCode::Created);

  netsec::RegistrationTransactionStore template_store(1, 1);
  const netsec::RegistrationRequest template_request =
      make_request("tx-template", "device-template", "nonce-template", base);
  CHECK(template_store.begin(template_request, base).code ==
        netsec::ResultCode::Created);
  CHECK(template_store.advance_template_generation(2));
  const netsec::TransactionResult template_mismatch =
      template_store.issue(issue_request(template_request, "cert-template"),
                           base + std::chrono::seconds(1));
  CHECK(template_mismatch.code == netsec::ResultCode::GenerationMismatch);
  CHECK(has_state(template_mismatch, netsec::TransactionState::Pending));

  const netsec::RegistrationRequest template_new =
      make_request("tx-template-2", "device-template", "nonce-template-2",
                   base, 1, 2);
  CHECK(template_store.begin(template_new, base).code ==
        netsec::ResultCode::Created);

  netsec::RegistrationTransactionStore rotate_store(1, 1);
  const netsec::RegistrationRequest rotate_request =
      make_request("tx-rotate", "device-rotate", "nonce-rotate", base);
  CHECK(rotate_store.begin(rotate_request, base).code ==
        netsec::ResultCode::Created);
  CHECK(rotate_store.issue(issue_request(rotate_request, "cert-rotate"),
                           base + std::chrono::seconds(1))
            .code == netsec::ResultCode::Applied);
  CHECK(rotate_store.advance_key_generation(2));
  const netsec::TransactionResult stale_complete =
      rotate_store.complete(complete_request(rotate_request, "cert-rotate"),
                            base + std::chrono::seconds(2));
  CHECK(stale_complete.code == netsec::ResultCode::GenerationMismatch);
  CHECK(has_state(stale_complete, netsec::TransactionState::Issued));
  CHECK(!stale_complete.state_changed);
  const netsec::TransactionResult stale_revoke =
      rotate_store.revoke(revoke_request(rotate_request, "cert-rotate",
                                         "key rotated"));
  CHECK(stale_revoke.code == netsec::ResultCode::Applied);
  CHECK(has_state(stale_revoke, netsec::TransactionState::Revoked));
}

void test_rejection_and_revocation_are_terminal() {
  const netsec::TimePoint base = at_seconds(8000);
  netsec::RegistrationTransactionStore store(1, 1);

  const netsec::RegistrationRequest rejected_request =
      make_request("tx-reject", "device-reject", "nonce-reject", base);
  CHECK(store.begin(rejected_request, base).code == netsec::ResultCode::Created);
  const netsec::TransactionResult rejected =
      store.reject(netsec::RejectRequest{rejected_request, "policy denied"},
                   base + std::chrono::seconds(1));
  CHECK(rejected.code == netsec::ResultCode::Applied);
  CHECK(has_state(rejected, netsec::TransactionState::Rejected));
  CHECK(rejected.state_changed);
  CHECK(rejected.reason == "policy denied");

  const netsec::TransactionResult replayed_reject =
      store.reject(netsec::RejectRequest{rejected_request, "other reason"},
                   base + std::chrono::seconds(2));
  CHECK(replayed_reject.code == netsec::ResultCode::Rejected);
  CHECK(has_state(replayed_reject, netsec::TransactionState::Rejected));
  CHECK(replayed_reject.reason == "policy denied");
  CHECK(!replayed_reject.state_changed);

  CHECK(store.begin(rejected_request, base + std::chrono::seconds(3)).code ==
        netsec::ResultCode::Rejected);
  CHECK(store.issue(issue_request(rejected_request, "cert-reject"),
                    base + std::chrono::seconds(4))
            .code == netsec::ResultCode::Rejected);
  CHECK(store.complete(complete_request(rejected_request, "cert-reject"),
                       base + std::chrono::seconds(5))
            .code == netsec::ResultCode::Rejected);
  CHECK(store.revoke(revoke_request(rejected_request, "cert-reject", "revoke"),
                     base + std::chrono::seconds(6))
            .code == netsec::ResultCode::Rejected);

  const netsec::RegistrationRequest revoked_request =
      make_request("tx-revoke", "device-revoke", "nonce-revoke", base);
  CHECK(store.begin(revoked_request, base).code == netsec::ResultCode::Created);
  CHECK(store.issue(issue_request(revoked_request, "cert-revoke"),
                    base + std::chrono::seconds(1))
            .code == netsec::ResultCode::Applied);
  CHECK(store.complete(complete_request(revoked_request, "cert-revoke"),
                       base + std::chrono::seconds(2))
            .code == netsec::ResultCode::Applied);

  const netsec::TransactionResult revoked =
      store.revoke(revoke_request(revoked_request, "cert-revoke", "compromise"),
                   base + std::chrono::seconds(3));
  CHECK(revoked.code == netsec::ResultCode::Applied);
  CHECK(has_state(revoked, netsec::TransactionState::Revoked));
  CHECK(revoked.state_changed);
  CHECK(revoked.certificate_fingerprint == "cert-revoke");

  const netsec::TransactionResult replayed_revoke =
      store.revoke(revoke_request(revoked_request, "cert-revoke", "compromise"),
                   base + std::chrono::seconds(4));
  CHECK(replayed_revoke.code == netsec::ResultCode::Revoked);
  CHECK(has_state(replayed_revoke, netsec::TransactionState::Revoked));
  CHECK(replayed_revoke.reason == "compromise");
  CHECK(!replayed_revoke.state_changed);

  const netsec::TransactionResult wrong_certificate_revoke =
      store.revoke(revoke_request(revoked_request, "cert-attacker", "compromise"),
                   base + std::chrono::seconds(5));
  CHECK(wrong_certificate_revoke.code == netsec::ResultCode::Conflict);
  CHECK(has_state(wrong_certificate_revoke, netsec::TransactionState::Revoked));
  CHECK(!wrong_certificate_revoke.state_changed);

  const netsec::TransactionResult replayed_begin =
      store.begin(revoked_request, base + std::chrono::seconds(6));
  CHECK(replayed_begin.code == netsec::ResultCode::Revoked);
  CHECK(has_state(replayed_begin, netsec::TransactionState::Revoked));
  CHECK(replayed_begin.certificate_fingerprint == "cert-revoke");
  CHECK(!replayed_begin.state_changed);

  const netsec::TransactionResult replayed_issue =
      store.issue(issue_request(revoked_request, "cert-attacker"),
                  base + std::chrono::seconds(7));
  CHECK(replayed_issue.code == netsec::ResultCode::Conflict);
  CHECK(has_state(replayed_issue, netsec::TransactionState::Revoked));
  CHECK(replayed_issue.certificate_fingerprint == "cert-revoke");
  CHECK(!replayed_issue.state_changed);

  const netsec::TransactionResult replayed_same_issue =
      store.issue(issue_request(revoked_request, "cert-revoke"),
                  base + std::chrono::seconds(7));
  CHECK(replayed_same_issue.code == netsec::ResultCode::Revoked);
  CHECK(has_state(replayed_same_issue, netsec::TransactionState::Revoked));
  CHECK(replayed_same_issue.certificate_fingerprint == "cert-revoke");
  CHECK(!replayed_same_issue.state_changed);

  const netsec::RegistrationRequest next_request =
      make_request("tx-revoke-next", "device-revoke", "nonce-revoke-next", base);
  CHECK(store.begin(next_request, base).code == netsec::ResultCode::Created);
  CHECK(store.issue(issue_request(next_request, "cert-next"),
                    base + std::chrono::seconds(1))
            .code == netsec::ResultCode::Applied);
  CHECK(store.complete(complete_request(next_request, "cert-next"),
                       base + std::chrono::seconds(2))
            .code == netsec::ResultCode::Applied);

  CHECK(store.revoke(revoke_request(revoked_request, "cert-revoke", "old replay"),
                     base + std::chrono::seconds(8))
            .code == netsec::ResultCode::Revoked);
  const auto next = store.find("tx-revoke-next");
  CHECK(next.has_value());
  CHECK(next->state == netsec::TransactionState::Completed);
  CHECK(next->certificate_fingerprint == "cert-next");

  const netsec::RegistrationRequest pending =
      make_request("tx-revoke-pending", "device-pending", "nonce-pending", base);
  CHECK(store.begin(pending, base).code == netsec::ResultCode::Created);
  const netsec::TransactionResult revoke_pending =
      store.revoke(revoke_request(pending, "cert-pending", "too early"),
                   base + std::chrono::seconds(1));
  CHECK(revoke_pending.code == netsec::ResultCode::InvalidState);
  CHECK(has_state(revoke_pending, netsec::TransactionState::Pending));
  CHECK(!revoke_pending.state_changed);
}

void test_injected_clock_auto_expiry() {
  const netsec::TimePoint base = at_seconds(9000);
  ManualClock clock{base};
  netsec::RegistrationTransactionStore store(1, 1,
                                             [&clock] { return clock.now; });
  const netsec::RegistrationRequest request =
      make_request("tx-clock", "device-clock", "nonce-clock", base);

  CHECK(store.begin(request).code == netsec::ResultCode::Created);
  clock.now += std::chrono::seconds(61);
  const std::vector<netsec::TransactionResult> expired = store.expire_due();
  CHECK(expired.size() == 1);
  CHECK(expired.front().code == netsec::ResultCode::Expired);
  const netsec::TransactionResult replayed = store.begin(request);
  CHECK(replayed.code == netsec::ResultCode::Expired);
  CHECK(has_state(replayed, netsec::TransactionState::Expired));
  CHECK(!replayed.state_changed);
}

void run(const char* name, void (*test)()) {
  const int before = failures;
  test();
  if (failures == before) {
    std::cout << "PASS " << name << '\n';
    return;
  }
  std::cout << "FAIL " << name << '\n';
}

}  // namespace

int main() {
  run("sha256_and_request_digest", test_sha256_and_request_digest);
  run("legal_registration_is_idempotent",
      test_legal_registration_is_idempotent);
  run("replayed_requests_do_not_change_state",
      test_replayed_requests_do_not_change_completed_transaction);
  run("transaction_substitution_is_rejected",
      test_transaction_substitution_is_rejected);
  run("nonce_reuse_is_rejected", test_nonce_reuse_is_rejected);
  run("timeout_expires_pending_transaction",
      test_timeout_turns_pending_into_terminal_expired);
  run("explicit_expiry_and_late_arrival",
      test_explicit_expiry_and_late_arrival);
  run("key_and_template_generation_checks",
      test_key_and_template_generations_are_enforced);
  run("rejection_and_revocation_are_terminal",
      test_rejection_and_revocation_are_terminal);
  run("injected_clock_auto_expiry", test_injected_clock_auto_expiry);

  if (failures != 0) {
    std::cerr << failures << " check(s) failed\n";
    return 1;
  }
  std::cout << "all tests passed\n";
  return 0;
}

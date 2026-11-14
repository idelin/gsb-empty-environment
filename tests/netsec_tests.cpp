#include "netsec.hpp"

#include <atomic>
#include <barrier>
#include <chrono>
#include <iostream>
#include <string>
#include <thread>
#include <vector>

namespace {

using namespace netsec;
using namespace std::chrono_literals;

#define CHECK(condition)                                                     \
  do {                                                                       \
    if (!(condition)) {                                                      \
      std::cerr << __FILE__ << ':' << __LINE__ << ": CHECK failed: "         \
                << #condition << '\n';                                       \
      return false;                                                          \
    }                                                                        \
  } while (false)

ConnectionIdentity alice(ConnectionGeneration generation = 1) {
  return ConnectionIdentity{"alice", "192.0.2.10", 0, generation};
}

ConnectionIdentity bob() {
  return ConnectionIdentity{"bob", "198.51.100.40", 1, 1};
}

bool test_legitimate_connection_and_one_time_use() {
  const AccessTime base = AccessTime{} + std::chrono::hours(24);
  AccessCredentialStore store;

  const IssuanceResult issued = store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(issued.ok());
  CHECK(issued.credential->valid());
  CHECK(issued.credential->capabilities().contains(Capability::ViewDisplay));
  CHECK(!issued.credential->capabilities().contains(Capability::Clipboard));
  CHECK(!issued.credential->capabilities().contains(Capability::FileTransfer));

  const RedemptionResult redeemed =
      store.redeem(issued.credential->token(), alice(), base + 2s);
  CHECK(redeemed.ok());
  CHECK(redeemed.grant->valid());
  CHECK(redeemed.grant->allows(Capability::ViewDisplay));
  CHECK(redeemed.grant->allows(Capability::ViewDisplay, 0));
  CHECK(!redeemed.grant->allows(Capability::ClipboardRead));
  CHECK(!redeemed.grant->allows(Capability::ClipboardWrite));
  CHECK(!redeemed.grant->allows(Capability::FileTransfer));
  CHECK(!redeemed.grant->allows(Capability::ViewDisplay, 1));

  CHECK(store.authorize(*redeemed.grant, Capability::ViewDisplay, 0, base + 2s));
  CHECK(!store.authorize(*redeemed.grant, Capability::ClipboardRead, 0,
                         base + 2s));
  CHECK(!store.authorize(*redeemed.grant, Capability::FileTransfer, 0,
                         base + 2s));
  CHECK(store.authorize(*redeemed.grant, Capability::ViewDisplay, 0, base + 2s)
            .status == CredentialStatus::Ok);

  const RedemptionResult replay =
      store.redeem(issued.credential->token(), alice(), base + 3s);
  CHECK(!replay.ok());
  CHECK(replay.status == CredentialStatus::AlreadyUsed);
  CHECK(!replay.grant.has_value());
  CHECK(store.authorize(*redeemed.grant, Capability::ViewDisplay, 0, base + 3s));
  return true;
}

bool test_copied_cookie_is_rejected_from_another_host() {
  const AccessTime base = AccessTime{} + std::chrono::hours(48);
  AccessCredentialStore store;
  const IssuanceResult issued = store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(issued.ok());

  ConnectionIdentity attacker = alice();
  attacker.client_address = "203.0.113.99";
  const RedemptionResult attack =
      store.redeem(issued.credential->token(), attacker, base + 1s);
  CHECK(!attack.ok());
  CHECK(attack.status == CredentialStatus::AddressMismatch);
  CHECK(!attack.grant.has_value());

  const RedemptionResult different_user =
      store.redeem(issued.credential->token(), bob(), base + 1s);
  CHECK(!different_user.ok());
  CHECK(different_user.status == CredentialStatus::UserMismatch);

  // The failed off-path attempt must not burn the legitimate user's cookie.
  const RedemptionResult legitimate =
      store.redeem(issued.credential->token(), alice(), base + 2s);
  CHECK(legitimate.ok());
  CHECK(legitimate.grant->allows(Capability::ViewDisplay, 0));
  return true;
}

bool test_user_and_display_substitution_are_rejected() {
  const AccessTime base = AccessTime{} + std::chrono::hours(72);
  AccessCredentialStore store;
  const IssuanceResult issued = store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(issued.ok());

  ConnectionIdentity wrong_user = alice();
  wrong_user.user = "bob";
  const RedemptionResult user_attack =
      store.redeem(issued.credential->token(), wrong_user, base + 1s);
  CHECK(!user_attack.ok());
  CHECK(user_attack.status == CredentialStatus::UserMismatch);

  ConnectionIdentity wrong_display = alice();
  wrong_display.display = 1;
  const RedemptionResult display_attack =
      store.redeem(issued.credential->token(), wrong_display, base + 1s);
  CHECK(!display_attack.ok());
  CHECK(display_attack.status == CredentialStatus::DisplayMismatch);
  CHECK(!display_attack.grant.has_value());

  const RedemptionResult legitimate =
      store.redeem(issued.credential->token(), alice(), base + 2s);
  CHECK(legitimate.ok());
  CHECK(!store.authorize(*legitimate.grant, Capability::ViewDisplay, 1,
                         base + 2s));
  CHECK(store.authorize(*legitimate.grant, Capability::ViewDisplay, 1,
                        base + 2s)
            .status == CredentialStatus::DisplayMismatch);
  return true;
}

bool test_expiration_is_enforced_at_the_boundary() {
  const AccessTime base = AccessTime{} + std::chrono::hours(96);
  AccessCredentialStore store;
  const IssuanceResult issued = store.issue(alice(), CredentialOptions{5s}, base);
  CHECK(issued.ok());
  CHECK(issued.credential->expires_at() == base + 5s);

  const RedemptionResult expired =
      store.redeem(issued.credential->token(), alice(), base + 5s);
  CHECK(!expired.ok());
  CHECK(expired.status == CredentialStatus::Expired);
  CHECK(!expired.grant.has_value());

  const RedemptionResult later =
      store.redeem(issued.credential->token(), alice(), base + 6s);
  CHECK(!later.ok());
  CHECK(later.status == CredentialStatus::Expired);
  return true;
}

bool test_session_is_bound_to_current_connection_generation() {
  const AccessTime base = AccessTime{} + std::chrono::hours(120);
  AccessCredentialStore store;
  const ConnectionGeneration first_generation = store.next_generation(
      "alice", "192.0.2.10", 0);
  CHECK(first_generation == 1);

  const IssuanceResult first =
      store.issue(alice(first_generation), CredentialOptions{30s}, base);
  CHECK(first.ok());
  const IssuanceResult stale_cookie =
      store.issue(alice(first_generation), CredentialOptions{30s}, base + 1s);
  CHECK(stale_cookie.ok());
  const RedemptionResult first_session =
      store.redeem(first.credential->token(), alice(first_generation), base + 1s);
  CHECK(first_session.ok());

  const ConnectionGeneration second_generation = store.next_generation(
      "alice", "192.0.2.10", 0);
  CHECK(second_generation == 2);
  const IssuanceResult second =
      store.issue(alice(second_generation), CredentialOptions{30s}, base + 2s);
  CHECK(second.ok());

  // An old unconsumed cookie is stale even when its binding is replayed
  // exactly, because the proxy has established a newer generation.
  const RedemptionResult stale =
      store.redeem(stale_cookie.credential->token(), alice(first_generation),
                   base + 3s);
  CHECK(!stale.ok());
  CHECK(stale.status == CredentialStatus::StaleGeneration);

  const RedemptionResult mismatched =
      store.redeem(stale_cookie.credential->token(), alice(second_generation),
                   base + 3s);
  CHECK(!mismatched.ok());
  CHECK(mismatched.status == CredentialStatus::GenerationMismatch);

  const RedemptionResult current =
      store.redeem(second.credential->token(), alice(second_generation), base + 4s);
  CHECK(current.ok());
  CHECK(store.authorize(*current.grant, Capability::ViewDisplay, 0, base + 5s));

  const ConnectionGeneration third_generation = store.next_generation(
      "alice", "192.0.2.10", 0);
  CHECK(third_generation == 3);
  const AuthorizationResult old_session =
      store.authorize(*current.grant, Capability::ViewDisplay, 0, base + 5s);
  CHECK(!old_session.ok());
  CHECK(old_session.status == CredentialStatus::StaleGeneration);
  return true;
}

bool test_display_allowlist_limits_access() {
  const AccessTime base = AccessTime{} + std::chrono::hours(144);
  AccessCredentialStore::Policy policy;
  policy.allowed_displays = {0};
  AccessCredentialStore store(policy);

  const IssuanceResult denied =
      store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(denied.ok());

  ConnectionIdentity display_one = alice();
  display_one.display = 1;
  const IssuanceResult forbidden =
      store.issue(display_one, CredentialOptions{30s}, base + 1s);
  CHECK(!forbidden.ok());
  CHECK(forbidden.status == CredentialStatus::DisplayNotAllowed);

  const RedemptionResult legit =
      store.redeem(denied.credential->token(), alice(), base + 2s);
  CHECK(legit.ok());
  CHECK(!store.authorize(*legit.grant, Capability::ViewDisplay, 1, base + 2s));
  return true;
}

bool test_clipboard_and_file_transfer_need_separate_tickets() {
  const AccessTime base = AccessTime{} + std::chrono::hours(168);
  AccessCredentialStore store;
  const IssuanceResult display =
      store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(display.ok());
  const RedemptionResult session =
      store.redeem(display.credential->token(), alice(), base + 1s);
  CHECK(session.ok());

  CHECK(!session.grant->allows(Capability::ClipboardRead));
  CHECK(!session.grant->allows(Capability::ClipboardWrite));
  CHECK(!session.grant->allows(Capability::FileTransfer));
  CHECK(!store.authorize(*session.grant, Capability::ClipboardRead, 0,
                         base + 1s));
  CHECK(!store.authorize(*session.grant, Capability::FileTransfer, 0,
                         base + 1s));

  const IssuanceResult clipboard = store.issue_capability(
      *session.grant, Capability::ClipboardRead, 1h, base + 2s);
  CHECK(clipboard.ok());
  CHECK(clipboard.credential->expires_at() <= session.grant->expires_at());
  CHECK(!clipboard.credential->capabilities().contains(Capability::ViewDisplay));
  CHECK(clipboard.credential->capabilities().contains(Capability::ClipboardRead));

  ConnectionIdentity attacker = alice();
  attacker.client_address = "203.0.113.50";
  const RedemptionResult copied_clipboard =
      store.redeem(clipboard.credential->token(), attacker, base + 3s);
  CHECK(!copied_clipboard.ok());
  CHECK(copied_clipboard.status == CredentialStatus::AddressMismatch);

  const RedemptionResult clipboard_session =
      store.redeem(clipboard.credential->token(), alice(), base + 3s);
  CHECK(clipboard_session.ok());
  CHECK(clipboard_session.grant->allows(Capability::ClipboardRead));
  CHECK(!clipboard_session.grant->allows(Capability::ClipboardWrite));
  CHECK(!clipboard_session.grant->allows(Capability::FileTransfer));
  CHECK(!clipboard_session.grant->allows(Capability::ViewDisplay));
  CHECK(store.authorize(*clipboard_session.grant, Capability::ClipboardRead, 0,
                        base + 3s));

  const IssuanceResult chained = store.issue_capability(
      *clipboard_session.grant, Capability::FileTransfer, 30s, base + 3s);
  CHECK(!chained.ok());
  CHECK(chained.status == CredentialStatus::CapabilityDenied);

  const IssuanceResult file = store.issue_capability(
      *session.grant, Capability::FileTransfer, 30s, base + 4s);
  CHECK(file.ok());
  const RedemptionResult file_session =
      store.redeem(file.credential->token(), alice(), base + 5s);
  CHECK(file_session.ok());
  CHECK(file_session.grant->allows(Capability::FileTransfer));
  CHECK(!file_session.grant->allows(Capability::ClipboardRead));
  CHECK(store.authorize(*file_session.grant, Capability::FileTransfer, 0,
                        base + 5s));

  const IssuanceResult orphaned = store.issue_capability(
      *session.grant, Capability::ClipboardWrite, 10s, base + 6s);
  CHECK(orphaned.ok());
  CHECK(store.close(*session.grant));
  const RedemptionResult revoked =
      store.redeem(orphaned.credential->token(), alice(), base + 7s);
  CHECK(!revoked.ok());
  CHECK(revoked.status == CredentialStatus::Revoked);
  return true;
}

bool test_ending_connection_invalidates_all_access() {
  const AccessTime base = AccessTime{} + std::chrono::hours(180);
  AccessCredentialStore store;
  const IssuanceResult redeemed =
      store.issue(alice(), CredentialOptions{30s}, base);
  const IssuanceResult pending =
      store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(redeemed.ok());
  CHECK(pending.ok());

  const RedemptionResult session =
      store.redeem(redeemed.credential->token(), alice(), base + 1s);
  CHECK(session.ok());
  const IssuanceResult clipboard = store.issue_capability(
      *session.grant, Capability::ClipboardRead, 10s, base + 2s);
  CHECK(clipboard.ok());

  CHECK(store.end_connection("alice", "192.0.2.10", 0, 1));
  CHECK(store.current_generation("alice", "192.0.2.10", 0) == 2);
  CHECK(!store.end_connection("alice", "192.0.2.10", 0, 1));

  const AuthorizationResult live =
      store.authorize(*session.grant, Capability::ViewDisplay, 0, base + 3s);
  CHECK(!live.ok());

  const RedemptionResult pending_access =
      store.redeem(pending.credential->token(), alice(), base + 3s);
  CHECK(!pending_access.ok());
  CHECK(pending_access.status == CredentialStatus::Revoked);

  const RedemptionResult clipboard_access =
      store.redeem(clipboard.credential->token(), alice(), base + 3s);
  CHECK(!clipboard_access.ok());
  CHECK(clipboard_access.status == CredentialStatus::Revoked);
  return true;
}

bool test_concurrent_redeem_has_exactly_one_winner() {
  const AccessTime base = AccessTime{} + std::chrono::hours(192);
  AccessCredentialStore store;
  const IssuanceResult issued = store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(issued.ok());

  constexpr std::size_t kThreads = 8;
  std::barrier start(kThreads);
  std::atomic<std::size_t> successes{0};
  std::atomic<std::size_t> already_used{0};
  std::atomic<std::size_t> unexpected{0};
  std::vector<std::thread> threads;
  threads.reserve(kThreads);

  for (std::size_t i = 0; i < kThreads; ++i) {
    threads.emplace_back([&] {
      start.arrive_and_wait();
      const RedemptionResult result =
          store.redeem(issued.credential->token(), alice(), base + 1s);
      if (result.ok()) {
        ++successes;
      } else if (result.status == CredentialStatus::AlreadyUsed) {
        ++already_used;
      } else {
        ++unexpected;
      }
    });
  }
  for (std::thread& thread : threads) {
    thread.join();
  }

  CHECK(successes.load() == 1);
  CHECK(already_used.load() == kThreads - 1);
  CHECK(unexpected.load() == 0);
  return true;
}

bool test_opaque_unique_tokens_and_revocation() {
  const AccessTime base = AccessTime{} + std::chrono::hours(216);
  AccessCredentialStore store;
  const IssuanceResult first = store.issue(alice(), CredentialOptions{30s}, base);
  const IssuanceResult second =
      store.issue(alice(), CredentialOptions{30s}, base + 1s);
  CHECK(first.ok());
  CHECK(second.ok());
  CHECK(first.credential->token() != second.credential->token());
  CHECK(first.credential->token().find("alice") == std::string::npos);
  CHECK(first.credential->token().find("192.0.2.10") == std::string::npos);

  CHECK(store.revoke(first.credential->token()));
  const RedemptionResult revoked =
      store.redeem(first.credential->token(), alice(), base + 2s);
  CHECK(!revoked.ok());
  CHECK(revoked.status == CredentialStatus::Revoked);

  const RedemptionResult live =
      store.redeem(second.credential->token(), alice(), base + 3s);
  CHECK(live.ok());
  CHECK(store.close(*live.grant));
  const AuthorizationResult closed =
      store.authorize(*live.grant, Capability::ViewDisplay, 0, base + 3s);
  CHECK(!closed.ok());
  CHECK(closed.status == CredentialStatus::SessionRevoked);
  return true;
}

bool test_short_default_and_policy_ttl_cap() {
  const AccessTime base = AccessTime{} + std::chrono::hours(240);
  AccessCredentialStore::Policy policy;
  policy.max_ttl = 2s;
  AccessCredentialStore store(policy);
  const IssuanceResult issued =
      store.issue(alice(), CredentialOptions{30s}, base);
  CHECK(issued.ok());
  CHECK(issued.credential->expires_at() == base + 2s);
  CHECK(!store.redeem(issued.credential->token(), alice(), base + 2s).ok());
  return true;
}

bool test_clock_injection_and_bad_input() {
  AccessTime now = AccessTime{} + std::chrono::hours(264);
  AccessCredentialStore store(AccessCredentialStore::Policy{},
                              [&now] { return now; });
  const IssuanceResult issued = store.issue(alice(), CredentialOptions{10s});
  CHECK(issued.ok());
  const RedemptionResult redeemed =
      store.redeem(issued.credential->token(), alice());
  CHECK(redeemed.ok());
  CHECK(redeemed.grant->allows(Capability::ViewDisplay, 0, now));

  now += 11s;
  CHECK(!redeemed.grant->allows(Capability::ViewDisplay, 0, now));
  CHECK(!store.authorize(*redeemed.grant, Capability::ViewDisplay, 0));

  const IssuanceResult empty_user =
      store.issue("", "192.0.2.10", 0, 1, 10s, now);
  CHECK(!empty_user.ok());
  CHECK(empty_user.status == CredentialStatus::InvalidRequest);

  const IssuanceResult zero_generation =
      store.issue("alice", "192.0.2.10", 0, 0, 10s, now);
  CHECK(!zero_generation.ok());
  CHECK(zero_generation.status == CredentialStatus::InvalidRequest);

  const RedemptionResult malformed = store.redeem("not-a-cookie", alice(), now);
  CHECK(!malformed.ok());
  CHECK(malformed.status == CredentialStatus::InvalidCookie);
  return true;
}

}  // namespace

int main() {
  struct TestCase {
    const char* name;
    bool (*run)();
  };
  const TestCase tests[] = {
      {"legitimate connection and one-time use",
       test_legitimate_connection_and_one_time_use},
      {"copied cookie from another host",
       test_copied_cookie_is_rejected_from_another_host},
      {"user and display substitution",
       test_user_and_display_substitution_are_rejected},
      {"expiration boundary", test_expiration_is_enforced_at_the_boundary},
      {"connection generation binding",
       test_session_is_bound_to_current_connection_generation},
      {"display allowlist", test_display_allowlist_limits_access},
      {"clipboard and file transfer separation",
       test_clipboard_and_file_transfer_need_separate_tickets},
      {"connection end invalidates access",
       test_ending_connection_invalidates_all_access},
      {"concurrent one-time redemption",
       test_concurrent_redeem_has_exactly_one_winner},
      {"opaque tokens and revocation", test_opaque_unique_tokens_and_revocation},
      {"short TTL policy", test_short_default_and_policy_ttl_cap},
      {"clock injection and bad input",
       test_clock_injection_and_bad_input},
  };

  std::size_t failures = 0;
  for (const TestCase& test : tests) {
    if (!test.run()) {
      std::cerr << "FAILED: " << test.name << '\n';
      ++failures;
    } else {
      std::cout << "PASSED: " << test.name << '\n';
    }
  }

  if (failures != 0) {
    std::cerr << failures << " test(s) failed\n";
    return 1;
  }
  std::cout << "All tests passed\n";
  return 0;
}

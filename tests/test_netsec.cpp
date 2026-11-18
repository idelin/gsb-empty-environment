#include <atomic>
#include <cstdint>
#include <iostream>
#include <memory>
#include <string>
#include <thread>
#include <vector>

#include "netsec.hpp"

namespace {

using netsec::AuditRecord;
using netsec::AuditRing;
using netsec::Decision;
using netsec::FileKey;
using netsec::HookEvent;
using netsec::LsmDispatcher;
using netsec::Policy;

int g_failures = 0;

template <typename E, typename A>
void check_eq(const char* scenario, const char* what, const E& expected,
              const A& actual) {
  if (expected == actual) {
    std::cout << "  PASS " << scenario << ": " << what << " expected="
              << expected << " actual=" << actual << "\n";
  } else {
    std::cout << "  FAIL " << scenario << ": " << what << " expected="
              << expected << " actual=" << actual << "\n";
    ++g_failures;
  }
}

void check_decision(const char* scenario, const char* what, Decision expected,
                    Decision actual) {
  if (expected == actual) {
    std::cout << "  PASS " << scenario << ": " << what << " expected="
              << netsec::to_string(expected)
              << " actual=" << netsec::to_string(actual) << "\n";
  } else {
    std::cout << "  FAIL " << scenario << ": " << what << " expected="
              << netsec::to_string(expected)
              << " actual=" << netsec::to_string(actual) << "\n";
    ++g_failures;
  }
}

std::shared_ptr<const Policy> makePolicy(std::uint64_t generation,
                                        std::vector<FileKey> protected_files) {
  auto p = std::make_shared<Policy>();
  p->generation = generation;
  for (const auto& k : protected_files) p->protected_files.insert(k);
  return p;
}

// The guard program: denies exactly when the policy protects (dev, ino).
// It deliberately ignores the path hint.
LsmDispatcher::ProgramList guardPrograms() {
  return {{"inode_guard", [](const HookEvent& ev, const Policy& pol) {
             return pol.protects(ev.key) ? Decision::Deny : Decision::Allow;
           }}};
}

// 1. Hardlink alias: same inode reached through a different path must still
//    be denied; the alias path only shows up in the audit log.
void test_hardlink_alias() {
  const char* s = "hardlink-alias";
  AuditRing ring(16);
  LsmDispatcher disp(guardPrograms(), makePolicy(1, {{8, 42}}), ring);

  auto real = disp.hook({{8, 42}, "/etc/secret"});
  auto alias = disp.hook({{8, 42}, "/var/tmp/.hardlink_alias"});
  auto same_path_other_inode = disp.hook({{8, 43}, "/etc/secret"});

  check_decision(s, "open via real path", Decision::Deny, real.decision);
  check_decision(s, "open via hardlink alias", Decision::Deny, alias.decision);
  check_decision(s, "same path, other inode", Decision::Allow,
                 same_path_other_inode.decision);
  check_eq(s, "alias path kept in audit log", std::string("/var/tmp/.hardlink_alias"),
           alias.path_hint);
  check_eq(s, "alias audit inode", (std::uint64_t)42, alias.key.ino);
  check_eq(s, "alias first deny program", 0, alias.first_deny_program);
}

// 2. Rename then access: renaming a protected file does not evade the deny,
//    and the audit record carries the post-rename path hint.
void test_rename() {
  const char* s = "rename";
  AuditRing ring(16);
  LsmDispatcher disp(guardPrograms(), makePolicy(1, {{9, 100}}), ring);

  auto before = disp.hook({{9, 100}, "/data/old_name"});
  // simulated rename(2): inode unchanged, path changes
  auto after = disp.hook({{9, 100}, "/data/renamed"});

  check_decision(s, "open before rename", Decision::Deny, before.decision);
  check_decision(s, "open after rename", Decision::Deny, after.decision);
  check_eq(s, "post-rename path in audit log", std::string("/data/renamed"),
           after.path_hint);
  check_eq(s, "inode stable across rename", before.key.ino, after.key.ino);
}

// 3. Concurrent mount-namespace switching: many threads open the same inode
//    through different per-namespace paths. Every call is denied and the
//    audit accounting (records + lost) matches the number of hook calls.
void test_concurrent_namespaces() {
  const char* s = "concurrent-ns";
  constexpr int kThreads = 8;
  constexpr int kCallsPerThread = 250;
  constexpr std::uint64_t kTotal = kThreads * kCallsPerThread;
  AuditRing ring(64);
  LsmDispatcher disp(guardPrograms(), makePolicy(1, {{7, 777}}), ring);

  std::atomic<std::uint64_t> denies{0};
  std::vector<std::thread> threads;
  for (int t = 0; t < kThreads; ++t) {
    threads.emplace_back([&, t] {
      const std::string path = "/ns" + std::to_string(t) + "/secret";
      for (int i = 0; i < kCallsPerThread; ++i) {
        auto rec = disp.hook({{7, 777}, path});
        if (rec.decision == Decision::Deny) denies.fetch_add(1);
      }
    });
  }
  for (auto& th : threads) th.join();

  check_eq(s, "all calls denied", kTotal, denies.load());

  // Drain: every stored record accounts for itself plus the drops that
  // happened before it; the pending lost counter covers the rest.
  std::uint64_t accounted = ring.lost();
  std::uint64_t records = 0;
  bool content_ok = true;
  AuditRecord rec;
  while (ring.take(rec)) {
    accounted += 1 + rec.lost_before;
    ++records;
    if (rec.decision != Decision::Deny || rec.key.ino != 777 ||
        rec.generation != 1 || rec.first_deny_program != 0) {
      content_ok = false;
    }
  }
  check_eq(s, "records+lost == hook calls", kTotal, accounted);
  check_eq(s, "stored records fit capacity", ring.capacity(), records);
  check_eq(s, "record content consistent", true, content_ok);
}

// 4. Multi-program mount order: programs run in order, the first deny is
//    final, and programs after the denying one never run.
void test_program_order() {
  const char* s = "program-order";
  AuditRing ring(16);

  int ran1 = 0, ran2 = 0, ran3 = 0;
  auto counting = [](int* ran, Decision d) {
    return [ran, d](const HookEvent&, const Policy&) {
      ++*ran;
      return d;
    };
  };
  LsmDispatcher::ProgramList programs = {
      {"p1", counting(&ran1, Decision::Allow)},
      {"p2", counting(&ran2, Decision::Deny)},
      {"p3", counting(&ran3, Decision::Allow)},
  };
  LsmDispatcher disp(programs, makePolicy(1, {}), ring);

  auto rec = disp.hook({{1, 1}, "/x"});
  check_decision(s, "final decision is first deny", Decision::Deny, rec.decision);
  check_eq(s, "first denying program index", 1, rec.first_deny_program);
  check_eq(s, "p1 executed", 1, ran1);
  check_eq(s, "p2 executed", 1, ran2);
  check_eq(s, "p3 not executed after deny", 0, ran3);

  // When nobody denies, all programs run and the call is allowed.
  LsmDispatcher::ProgramList allow_programs = {
      {"p1", counting(&ran1, Decision::Allow)},
      {"p2", counting(&ran2, Decision::Allow)},
      {"p3", counting(&ran3, Decision::Allow)},
  };
  LsmDispatcher disp2(allow_programs, makePolicy(1, {}), ring);
  auto rec2 = disp2.hook({{1, 1}, "/x"});
  check_decision(s, "all-allow decision", Decision::Allow, rec2.decision);
  check_eq(s, "no denying program", -1, rec2.first_deny_program);
  check_eq(s, "p3 executed when allowed", 1, ran3);
}

// 5. Ring buffer full: policy denies are still enforced, drops are counted,
//    and the next successfully stored record carries the lost count.
void test_buffer_full() {
  const char* s = "buffer-full";
  AuditRing ring(4);
  LsmDispatcher disp(guardPrograms(), makePolicy(1, {{3, 30}}), ring);

  int denies = 0;
  for (int i = 0; i < 6; ++i) {
    if (disp.hook({{3, 30}, "/protected"}).decision == Decision::Deny) ++denies;
  }
  check_eq(s, "deny enforced while buffer full", 6, denies);
  check_eq(s, "records stored", (std::size_t)4, ring.size());
  check_eq(s, "lost count", (std::uint64_t)2, ring.lost());

  AuditRecord rec;
  bool no_lost_yet = true;
  while (ring.take(rec)) {
    if (rec.lost_before != 0) no_lost_yet = false;
  }
  check_eq(s, "no lost reported before overflow records drained", true,
           no_lost_yet);

  auto next = disp.hook({{3, 30}, "/protected"});
  check_eq(s, "next stored record carries lost count", (std::uint64_t)2,
           next.lost_before);
  check_eq(s, "lost counter reset after being carried", (std::uint64_t)0,
           ring.lost());
}

// 6. Policy generation switch: an in-flight call started under the old
//    generation is decided by the old policy and tagged with its generation.
void test_generation_switch() {
  const char* s = "generation-switch";
  AuditRing ring(16);
  LsmDispatcher disp(guardPrograms(), makePolicy(1, {{5, 50}}), ring);

  auto in_flight = disp.begin({{5, 50}, "/protected"});  // pins generation 1
  disp.install_policy(makePolicy(2, {{5, 60}}));          // switch mid-flight

  auto old_call = disp.complete(in_flight);
  check_decision(s, "in-flight call uses old policy", Decision::Deny,
                 old_call.decision);
  check_eq(s, "in-flight audit generation", (std::uint64_t)1,
           old_call.generation);

  auto new_allow = disp.hook({{5, 50}, "/protected"});
  check_decision(s, "new call uses new policy", Decision::Allow,
                 new_allow.decision);
  check_eq(s, "new audit generation", (std::uint64_t)2, new_allow.generation);

  auto new_deny = disp.hook({{5, 60}, "/newly_protected"});
  check_decision(s, "new policy denies its own files", Decision::Deny,
                 new_deny.decision);
  check_eq(s, "new deny generation", (std::uint64_t)2, new_deny.generation);
}

}  // namespace

int main() {
  test_hardlink_alias();
  test_rename();
  test_concurrent_namespaces();
  test_program_order();
  test_buffer_full();
  test_generation_switch();

  if (g_failures == 0) {
    std::cout << "ALL TESTS PASSED\n";
    return 0;
  }
  std::cout << g_failures << " CHECK(S) FAILED\n";
  return 1;
}

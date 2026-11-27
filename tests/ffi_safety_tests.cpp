#include "ffi_safety/ffi_safety.hpp"

#include <cstdio>
#include <functional>
#include <string>
#include <utility>
#include <vector>

namespace {

using ffi_safety::Action;
using ffi_safety::AdversarialSearcher;
using ffi_safety::Event;
using ffi_safety::EventType;
using ffi_safety::Handle;
using ffi_safety::Monitor;
using ffi_safety::SearchResult;
using ffi_safety::Verdict;
using ffi_safety::Violation;

int g_failures = 0;
int g_checks = 0;

void report_failure(const char* expr, const char* file, int line) {
  ++g_failures;
  std::printf("  FAIL %s:%d: %s\n", file, line, expr);
}

#define CHECK(cond)                                         \
  do {                                                      \
    ++g_checks;                                             \
    if (!(cond)) {                                          \
      report_failure(#cond, __FILE__, __LINE__);            \
    }                                                       \
  } while (0)

template <typename Lhs, typename Rhs>
void check_eq(const Lhs& lhs, const Rhs& rhs, const char* lhs_text,
              const char* rhs_text, const char* file, int line) {
  ++g_checks;
  if (!(lhs == rhs)) {
    ++g_failures;
    std::printf("  FAIL %s:%d: %s != %s\n", file, line, lhs_text, rhs_text);
  }
}

#define CHECK_EQ(lhs, rhs) \
  check_eq((lhs), (rhs), #lhs, #rhs, __FILE__, __LINE__)

using TestFn = std::function<void()>;

// --- (1) ownership and generation -----------------------------------------

void test_acquire_release_generation() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(7), Verdict::Ok);
  CHECK(monitor.region_alive(7));
  CHECK_EQ(monitor.region_generation(7), ffi_safety::Generation{1});
  CHECK_EQ(monitor.release(7), Verdict::Ok);
  CHECK(!monitor.region_alive(7));
  CHECK_EQ(monitor.region_generation(7), ffi_safety::Generation{2});
  CHECK_EQ(monitor.acquire_owned(7), Verdict::Ok);
  CHECK_EQ(monitor.region_generation(7), ffi_safety::Generation{3});
}

void test_double_acquire_and_release_are_ub() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(monitor.acquire_owned(1), Verdict::UB);
  CHECK_EQ(monitor.last_violation().code, Violation::RegionAlreadyLive);

  Monitor other;
  CHECK_EQ(other.release(42), Verdict::UB);
  CHECK_EQ(other.last_violation().code, Violation::UseAfterRelease);
  CHECK_EQ(other.release(42), Verdict::UB);
  CHECK_EQ(other.last_violation().code, Violation::TaintedSession);
}

// --- (1) Aliasing XOR Mutability ------------------------------------------

void test_shared_borrows_coexist() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  Handle h1 = monitor.borrow_shared(1);
  Handle h2 = monitor.borrow_shared(1);
  CHECK(h1.valid() && h2.valid());
  CHECK(h1 != h2);
  CHECK_EQ(monitor.deref(h1), Verdict::Ok);
  CHECK_EQ(monitor.deref(h2), Verdict::Ok);
  CHECK_EQ(monitor.return_borrow(h1), Verdict::Ok);
  CHECK_EQ(monitor.return_borrow(h2), Verdict::Ok);
  CHECK_EQ(monitor.release(1), Verdict::Ok);
}

void test_mut_borrow_is_exclusive() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  Handle m1 = monitor.borrow_mut(1);
  CHECK(m1.valid());
  CHECK_EQ(monitor.borrow_mut(1).valid(), false);
  CHECK_EQ(monitor.last_violation().code, Violation::AliasingViolation);

  Monitor shared_first;
  CHECK_EQ(shared_first.acquire_owned(1), Verdict::Ok);
  CHECK(shared_first.borrow_shared(1).valid());
  CHECK_EQ(shared_first.borrow_mut(1).valid(), false);
  CHECK_EQ(shared_first.last_violation().code,
           Violation::AliasingViolation);
}

void test_borrow_return_then_reborrow() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  Handle m1 = monitor.borrow_mut(1);
  {
    Monitor blocked = monitor.clone();
    CHECK_EQ(blocked.borrow_shared(1).valid(), false);
  }
  CHECK_EQ(monitor.return_borrow(m1), Verdict::Ok);
  Handle s1 = monitor.borrow_shared(1);
  Handle s2 = monitor.borrow_shared(1);
  CHECK(s1.valid() && s2.valid());
  {
    Monitor blocked = monitor.clone();
    CHECK_EQ(blocked.borrow_mut(1).valid(), false);
  }
  CHECK_EQ(monitor.return_borrow(s1), Verdict::Ok);
  CHECK_EQ(monitor.return_borrow(s2), Verdict::Ok);
  CHECK(monitor.borrow_mut(1).valid());
}

// --- (1) release forbids outstanding / post-release access ---------------

void test_release_with_active_borrows_blocked() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  Handle shared = monitor.borrow_shared(1);
  CHECK(shared.valid());
  {
    Monitor blocked = monitor.clone();
    CHECK_EQ(blocked.release(1), Verdict::UB);
    CHECK_EQ(blocked.last_violation().code,
             Violation::ActiveBorrowsOnRelease);
  }
  CHECK(monitor.region_alive(1));
  CHECK_EQ(monitor.return_borrow(shared), Verdict::Ok);
  CHECK_EQ(monitor.release(1), Verdict::Ok);

  Monitor mutable_case;
  CHECK_EQ(mutable_case.acquire_owned(2), Verdict::Ok);
  Handle mutable_handle = mutable_case.borrow_mut(2);
  {
    Monitor blocked = mutable_case.clone();
    CHECK_EQ(blocked.release(2), Verdict::UB);
    CHECK_EQ(blocked.last_violation().code,
             Violation::ActiveBorrowsOnRelease);
  }
  CHECK_EQ(mutable_case.return_borrow(mutable_handle), Verdict::Ok);
}

void test_stale_handle_after_release_is_dangling() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  Handle loan = monitor.borrow_mut(1);
  CHECK_EQ(monitor.return_borrow(loan), Verdict::Ok);
  CHECK_EQ(monitor.release(1), Verdict::Ok);
  CHECK_EQ(monitor.deref(loan), Verdict::UB);
  CHECK_EQ(monitor.last_violation().code, Violation::DanglingHandle);
  CHECK_EQ(monitor.return_borrow(loan), Verdict::UB);
  CHECK_EQ(monitor.last_violation().code, Violation::TaintedSession);

  // Re-acquired region gets a fresh generation: old handles stay stale.
  Monitor reborn;
  CHECK_EQ(reborn.acquire_owned(1), Verdict::Ok);
  Handle old = reborn.borrow_shared(1);
  CHECK_EQ(reborn.return_borrow(old), Verdict::Ok);
  CHECK_EQ(reborn.release(1), Verdict::Ok);
  CHECK_EQ(reborn.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(reborn.deref(old), Verdict::UB);
  CHECK_EQ(reborn.last_violation().code, Violation::DanglingHandle);
}

void test_borrow_on_unknown_region_ub() {
  Monitor monitor;
  CHECK_EQ(monitor.borrow_shared(99).valid(), false);
  CHECK_EQ(monitor.last_violation().code, Violation::UnknownRegion);
  Monitor mutable_case;
  CHECK_EQ(mutable_case.borrow_mut(99).valid(), false);
  CHECK_EQ(mutable_case.last_violation().code, Violation::UnknownRegion);
}

// --- (2) callback re-entrancy ---------------------------------------------

void test_reentrant_mutation_blocked() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(monitor.begin_iteration(1), Verdict::Ok);
  CHECK_EQ(monitor.invoke_callback(100), Verdict::Ok);
  CHECK_EQ(monitor.borrow_mut(1).valid(), false);
  CHECK_EQ(monitor.last_violation().code, Violation::ReentrantMutation);

  Monitor release_case;
  CHECK_EQ(release_case.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(release_case.begin_iteration(1), Verdict::Ok);
  CHECK_EQ(release_case.invoke_callback(1), Verdict::Ok);
  CHECK_EQ(release_case.release(1), Verdict::UB);
  CHECK_EQ(release_case.last_violation().code,
           Violation::ReentrantMutation);
}

void test_mutation_without_callback_is_not_reentrant() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(monitor.begin_iteration(1), Verdict::Ok);
  CHECK(monitor.borrow_mut(1).valid());
  CHECK_EQ(monitor.end_iteration(1), Verdict::Ok);

  Monitor after_callback;
  CHECK_EQ(after_callback.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(after_callback.begin_iteration(1), Verdict::Ok);
  CHECK_EQ(after_callback.invoke_callback(1), Verdict::Ok);
  CHECK_EQ(after_callback.return_callback(), Verdict::Ok);
  CHECK(after_callback.borrow_mut(1).valid());
}

void test_multi_hop_callback_reentrancy() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(monitor.begin_iteration(1), Verdict::Ok);
  CHECK_EQ(monitor.invoke_callback(1), Verdict::Ok);
  CHECK_EQ(monitor.invoke_callback(2), Verdict::Ok);
  CHECK_EQ(monitor.invoke_callback(3), Verdict::Ok);
  CHECK_EQ(monitor.callback_depth(), std::uint32_t{3});
  // Three hops deep, the innermost plugin tries to mutate the host container.
  CHECK_EQ(monitor.borrow_mut(1).valid(), false);
  CHECK_EQ(monitor.last_violation().code, Violation::ReentrantMutation);
}

void test_callback_and_iteration_imbalance() {
  Monitor monitor;
  CHECK_EQ(monitor.return_callback(), Verdict::UB);
  CHECK_EQ(monitor.last_violation().code,
           Violation::CallbackStackImbalance);

  Monitor iteration;
  CHECK_EQ(iteration.acquire_owned(1), Verdict::Ok);
  {
    Monitor unbalanced = iteration.clone();
    CHECK_EQ(unbalanced.end_iteration(1), Verdict::UB);
    CHECK_EQ(unbalanced.last_violation().code,
             Violation::IterationImbalance);
  }
  CHECK_EQ(iteration.begin_iteration(1), Verdict::Ok);
  {
    Monitor nested = iteration.clone();
    CHECK_EQ(nested.begin_iteration(1), Verdict::UB);
    CHECK_EQ(nested.last_violation().code,
             Violation::IterationImbalance);
  }
}

void test_check_closed_detects_leaks() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  Handle loan = monitor.borrow_shared(1);
  CHECK(loan.valid());
  CHECK_EQ(monitor.check_closed(), Verdict::UB);
  CHECK_EQ(monitor.last_violation().code, Violation::LeakedBorrows);

  Monitor clean;
  CHECK_EQ(clean.acquire_owned(1), Verdict::Ok);
  Handle loan2 = clean.borrow_mut(1);
  CHECK_EQ(clean.return_borrow(loan2), Verdict::Ok);
  CHECK_EQ(clean.release(1), Verdict::Ok);
  CHECK_EQ(clean.check_closed(), Verdict::Ok);

  Monitor open_callback;
  CHECK_EQ(open_callback.invoke_callback(1), Verdict::Ok);
  CHECK_EQ(open_callback.check_closed(), Verdict::UB);
  CHECK_EQ(open_callback.last_violation().code,
           Violation::CallbackStackImbalance);
}

// --- (3) adversarial searcher, determinism and replay ---------------------

void test_search_is_deterministic() {
  AdversarialSearcher searcher;
  SearchResult first = searcher.search_dangling_or_alias();
  for (int run = 0; run < 5; ++run) {
    SearchResult again = searcher.search_dangling_or_alias();
    CHECK(again.found == first.found);
    CHECK(again.violation == first.violation);
    CHECK(again.actions == first.actions);
    CHECK(again.explored_states == first.explored_states);
  }
}

void test_searcher_finds_minimal_alias_sequence() {
  AdversarialSearcher searcher;
  SearchResult result = searcher.find_first(
      Violation::AliasingViolation,
      AdversarialSearcher::single_region_alphabet(1));
  CHECK(result.found);
  CHECK_EQ(result.violation, Violation::AliasingViolation);
  CHECK_EQ(result.actions.size(), std::size_t{3});
  CHECK_EQ(result.actions[0].kind, Action::Kind::Acquire);
  CHECK_EQ(result.actions[1].kind, Action::Kind::BorrowShared);
  CHECK_EQ(result.actions[2].kind, Action::Kind::BorrowMut);

  // Replaying the witness on a fresh monitor reproduces the same UB.
  Monitor replay;
  for (const Event& event : result.events) {
    replay.feed(event);
  }
  CHECK(replay.tainted());
  CHECK_EQ(replay.last_violation().code, Violation::AliasingViolation);
  CHECK_EQ(replay.trace().size(), result.events.size());
}

void test_searcher_finds_minimal_reentrancy_sequence() {
  AdversarialSearcher searcher;
  SearchResult result = searcher.search_reentrancy(1);
  CHECK(result.found);
  CHECK_EQ(result.violation, Violation::ReentrantMutation);
  CHECK_EQ(result.actions.size(), std::size_t{4});
  CHECK_EQ(result.actions[0].kind, Action::Kind::Acquire);
  CHECK_EQ(result.actions[1].kind, Action::Kind::BeginIteration);
  CHECK_EQ(result.actions[2].kind, Action::Kind::InvokeCallback);
  CHECK_EQ(result.actions[3].kind, Action::Kind::BorrowMut);

  Monitor replay;
  for (const Event& event : result.events) {
    replay.feed(event);
  }
  CHECK_EQ(replay.last_violation().code, Violation::ReentrantMutation);
}

void test_searcher_find_all_violation_classes() {
  AdversarialSearcher searcher;
  const std::vector<Violation> targets = {
      Violation::AliasingViolation,
      Violation::UseAfterRelease,
      Violation::ActiveBorrowsOnRelease,
      Violation::DanglingHandle,
  };
  std::vector<SearchResult> results = searcher.find_all(
      AdversarialSearcher::single_region_alphabet(1), targets);
  CHECK_EQ(results.size(), targets.size());
  std::size_t alias_length = 0;
  for (std::size_t idx = 0; idx < results.size(); ++idx) {
    CHECK(results[idx].found);
    CHECK_EQ(results[idx].violation, targets[idx]);
    CHECK(results[idx].actions.size() <=
          static_cast<std::size_t>(AdversarialSearcher::kDefaultMaxDepth));
    if (targets[idx] == Violation::AliasingViolation) {
      alias_length = results[idx].actions.size();
    }
  }
  // The true dangling-reference witness needs a stale generation, so it is at
  // least as long as the direct aliasing witness and longer than depth 2.
  CHECK(results[3].actions.size() >= alias_length);
  // acquire, borrow, return, release, then deref the stale handle.
  CHECK_EQ(results[3].actions.size(), std::size_t{5});
}

void test_searcher_depth_bound_is_respected() {
  AdversarialSearcher searcher;
  // A four-step budget cannot witness a dangling handle, which needs a
  // released generation before the offending stale-handle dereference.
  SearchResult shallow = searcher.find_first(
      Violation::DanglingHandle,
      AdversarialSearcher::single_region_alphabet(1), 4);
  CHECK(!shallow.found);

  SearchResult deep = searcher.find_first(
      Violation::DanglingHandle,
      AdversarialSearcher::single_region_alphabet(1), 6);
  CHECK(deep.found);
  CHECK_EQ(deep.violation, Violation::DanglingHandle);
  CHECK_EQ(deep.actions.size(), std::size_t{5});
  CHECK_EQ(deep.actions[4].kind, Action::Kind::DerefIssuedHandle);
}

void test_region_isolation_and_state_key() {
  Monitor monitor;
  CHECK_EQ(monitor.acquire_owned(1), Verdict::Ok);
  CHECK_EQ(monitor.acquire_owned(2), Verdict::Ok);
  Handle loan_on_one = monitor.borrow_mut(1);
  CHECK(loan_on_one.valid());
  // Region 2 has no loans, so releasing it is legal.
  CHECK_EQ(monitor.release(2), Verdict::Ok);
  CHECK(monitor.region_alive(1));
  CHECK(!monitor.region_alive(2));
  CHECK_EQ(monitor.deref(loan_on_one), Verdict::Ok);

  Monitor copy = monitor.clone();
  CHECK_EQ(copy.state_key(), monitor.state_key());
  CHECK_EQ(copy.deref(loan_on_one), Verdict::Ok);
}

void test_event_feed_matches_typed_api() {
  const std::vector<Event> events = {
      Event{EventType::AcquireOwned, 1, Handle{}, 0},
      Event{EventType::BorrowShared, 1, Handle{}, 0},
      Event{EventType::BorrowMut, 1, Handle{}, 0},
  };
  Monitor monitor;
  CHECK_EQ(monitor.feed(events[0]), Verdict::Ok);
  CHECK_EQ(monitor.feed(events[1]), Verdict::Ok);
  CHECK_EQ(monitor.feed(events[2]), Verdict::UB);
  CHECK_EQ(monitor.last_violation().code, Violation::AliasingViolation);
  // The successful shared loan's handle is recorded in the trace.
  CHECK(monitor.trace()[1].handle.valid());
}

}  // namespace

int main() {
  struct TestCase {
    const char* name;
    TestFn fn;
  };
  const std::vector<TestCase> tests = {
      {"acquire/release generation", test_acquire_release_generation},
      {"double acquire/release is UB",
       test_double_acquire_and_release_are_ub},
      {"shared borrows coexist", test_shared_borrows_coexist},
      {"mutable borrow is exclusive", test_mut_borrow_is_exclusive},
      {"borrow return then reborrow", test_borrow_return_then_reborrow},
      {"release with active borrows blocked",
       test_release_with_active_borrows_blocked},
      {"stale handle after release dangling",
       test_stale_handle_after_release_is_dangling},
      {"borrow on unknown region UB", test_borrow_on_unknown_region_ub},
      {"reentrant mutation blocked", test_reentrant_mutation_blocked},
      {"mutation without callback allowed",
       test_mutation_without_callback_is_not_reentrant},
      {"multi-hop callback reentrancy",
       test_multi_hop_callback_reentrancy},
      {"callback/iteration imbalance", test_callback_and_iteration_imbalance},
      {"check_closed detects leaks", test_check_closed_detects_leaks},
      {"search is deterministic", test_search_is_deterministic},
      {"minimal alias sequence", test_searcher_finds_minimal_alias_sequence},
      {"minimal reentrancy sequence",
       test_searcher_finds_minimal_reentrancy_sequence},
      {"find_all violation classes", test_searcher_find_all_violation_classes},
      {"search depth bound respected",
       test_searcher_depth_bound_is_respected},
      {"region isolation and state key",
       test_region_isolation_and_state_key},
      {"event feed matches typed API", test_event_feed_matches_typed_api},
  };

  int passed = 0;
  for (const TestCase& test : tests) {
    int before = g_failures;
    test.fn();
    if (g_failures == before) {
      ++passed;
      std::printf("[PASS] %s\n", test.name);
    } else {
      std::printf("[FAIL] %s\n", test.name);
    }
  }

  std::printf("\n%d/%d test cases passed, %d assertions, %d failures\n",
              passed, static_cast<int>(tests.size()), g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}

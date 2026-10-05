// Tests for the explicit batch cycle collector.
//
// Every scenario cross-checks the destruction log against the expected
// survivor set: a node may be destroyed iff it was unreachable from the
// active roots at the begin boundary of the cycle.

#include "gc/gc.hpp"

#include <iostream>
#include <set>
#include <string>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    if (!(cond)) {                                                         \
      ++g_failures;                                                        \
      std::cerr << "CHECK failed: " #cond " (line " << __LINE__ << ")\n";  \
    }                                                                      \
  } while (0)

struct TestNode : gc::Node {
  gc::Ptr<TestNode> next{this};
  gc::Ptr<TestNode> other{this};
};

std::set<std::string> logNames(const gc::Collector& collector) {
  std::set<std::string> names;
  for (const auto& entry : collector.destructorLog().entries()) {
    names.insert(entry.name);
  }
  return names;
}

// An unreachable cycle kept alive only by its own strong edges is
// reclaimed by the batch collector.
void unreachable_cycle_is_collected() {
  gc::Collector c;
  auto* a = c.create<TestNode>("A");
  auto* b = c.create<TestNode>("B");
  auto* d = c.create<TestNode>("D");
  a->next = b;
  b->next = d;
  d->next = a;  // 3-cycle, no roots

  CHECK(c.collect() == 3);
  CHECK(c.nodeCount() == 0);
  CHECK(logNames(c) == std::set<std::string>({"A", "B", "D"}));
}

// A self-loop is the smallest possible cycle.
void self_loop_is_collected() {
  gc::Collector c;
  auto* a = c.create<TestNode>("A");
  a->next = a;
  CHECK(c.collect() == 1);
  CHECK(logNames(c) == std::set<std::string>({"A"}));
}

// A rooted cycle survives; a sibling garbage cycle is reclaimed.
void rooted_cycle_survives_garbage_does_not() {
  gc::Collector c;
  auto* a = c.create<TestNode>("A");
  auto* b = c.create<TestNode>("B");
  auto* x = c.create<TestNode>("X");
  auto* y = c.create<TestNode>("Y");
  a->next = b;
  b->next = a;  // rooted 2-cycle
  x->next = y;
  y->next = x;  // garbage 2-cycle

  gc::Root<TestNode> keep(c, a);
  auto ha = c.handleOf(a);
  auto hb = c.handleOf(b);

  CHECK(c.collect() == 2);
  CHECK(logNames(c) == std::set<std::string>({"X", "Y"}));
  CHECK(c.nodeCount() == 2);

  // Survivors are still readable and their strong edges are intact.
  CHECK(ha.get() == a);
  CHECK(hb.get() == b);
  CHECK(ha->next.get() == b);
  CHECK(hb->next.get() == a);

  // A second cycle is stable: nothing else is reclaimed.
  CHECK(c.collect() == 0);
}

// Removing the last root makes a formerly reachable cycle collectable.
void dropping_root_allows_collection() {
  gc::Collector c;
  auto* a = c.create<TestNode>("A");
  auto* b = c.create<TestNode>("B");
  a->next = b;
  b->next = a;
  auto ha = c.handleOf(a);

  {
    gc::Root<TestNode> keep(c, a);
    CHECK(c.collect() == 0);
    CHECK(ha.get() == a);
  }  // root set shrinks here

  CHECK(c.collect() == 2);
  CHECK(ha.get() == nullptr);
  CHECK(logNames(c) == std::set<std::string>({"A", "B"}));
}

// Reachability flows through chains, and a live node's edge to a garbage
// cycle rescues the whole cycle.
void reachability_is_transitive() {
  gc::Collector c;
  auto* head = c.create<TestNode>("head");
  auto* mid = c.create<TestNode>("mid");
  auto* tail = c.create<TestNode>("tail");
  auto* g1 = c.create<TestNode>("g1");
  auto* g2 = c.create<TestNode>("g2");
  head->next = mid;
  mid->next = tail;
  g1->next = g2;
  g2->next = g1;

  gc::Root<TestNode> keep(c, head);
  CHECK(c.collect() == 2);
  CHECK(logNames(c) == std::set<std::string>({"g1", "g2"}));

  // Rebuild the garbage cycle, then anchor it to the live graph.
  auto* h1 = c.create<TestNode>("h1");
  auto* h2 = c.create<TestNode>("h2");
  h1->next = h2;
  h2->next = h1;
  tail->other = h1;
  CHECK(c.collect() == 0);
  CHECK(c.nodeCount() == 5);
}

// Phase boundaries: between retire and finish a retired node still exists
// but cannot be read; it is destroyed (and logged) at the end boundary.
void retired_nodes_are_unreadable_until_end_boundary() {
  gc::Collector c;
  auto* g = c.create<TestNode>("G");
  auto h = c.handleOf(g);

  auto cycle = c.startCycle();
  CHECK(h.get() == g);  // alive before the mark phase runs

  CHECK(cycle.retireUnreachable() == 1);
  CHECK(h.get() == nullptr);         // retired: reads are blocked
  CHECK(c.quarantineCount() == 1);   // ... but not yet destroyed
  CHECK(logNames(c).empty());

  cycle.finish();  // end boundary destroys the quarantine
  CHECK(c.quarantineCount() == 0);
  CHECK(logNames(c) == std::set<std::string>({"G"}));
}

// Roots registered during an open cycle are deferred: they do not protect
// nodes in the in-flight cycle.
void roots_registered_during_cycle_are_deferred() {
  gc::Collector c;
  auto* g = c.create<TestNode>("G");
  auto h = c.handleOf(g);

  auto cycle = c.startCycle();
  gc::Root<TestNode> late(c, g);  // goes to the pending set
  CHECK(cycle.retireUnreachable() == 1);
  CHECK(late.get() == nullptr);   // too late: G was unreachable at snapshot
  cycle.finish();
  CHECK(h.get() == nullptr);
  CHECK(logNames(c) == std::set<std::string>({"G"}));
}

// A deferred root becomes active at the next begin boundary and protects
// its node from then on.
void deferred_root_takes_effect_next_cycle() {
  gc::Collector c;
  auto* p = c.create<TestNode>("P");
  auto hp = c.handleOf(p);

  gc::Root<TestNode> first(c, p);
  gc::Root<TestNode> second;
  {
    auto cycle = c.startCycle();
    second = gc::Root<TestNode>(c, p);  // pending until next begin boundary
    CHECK(cycle.retireUnreachable() == 0);
    cycle.finish();
  }

  first.reset();  // only the deferred root remains
  CHECK(c.collect() == 0);  // adopted at this cycle's begin boundary
  CHECK(hp.get() == p);
  CHECK(logNames(c).empty());
}

// Nodes created while a cycle is open are allocated-live: they survive the
// in-flight cycle but are fair game for the next one.
void nodes_created_during_cycle_survive_it() {
  gc::Collector c;
  auto cycle = c.startCycle();
  c.create<TestNode>("N");
  CHECK(cycle.retireUnreachable() == 0);
  cycle.finish();

  CHECK(c.nodeCount() == 1);
  CHECK(c.collect() == 1);
  CHECK(logNames(c) == std::set<std::string>({"N"}));
}

// Full-graph scenario: verify the survivor set against the destruction log.
void destructor_log_verifies_survivor_set() {
  gc::Collector c;
  auto* a = c.create<TestNode>("A");
  auto* b = c.create<TestNode>("B");
  auto* d = c.create<TestNode>("C");
  auto* x = c.create<TestNode>("X");
  auto* y = c.create<TestNode>("Y");
  auto* z = c.create<TestNode>("Z");
  a->next = b;
  b->next = d;
  d->next = a;  // rooted 3-cycle
  x->next = y;
  y->next = z;
  z->next = x;  // garbage 3-cycle

  gc::Root<TestNode> keep(c, a);
  // Handles must be acquired while the nodes are still alive.
  auto ha = c.handleOf(a);
  auto hb = c.handleOf(b);
  auto hd = c.handleOf(d);
  auto hx = c.handleOf(x);
  auto hy = c.handleOf(y);
  auto hz = c.handleOf(z);

  CHECK(c.collect() == 3);

  const std::set<std::string> expectedGarbage{"X", "Y", "Z"};
  const std::set<std::string> expectedSurvivors{"A", "B", "C"};
  CHECK(logNames(c) == expectedGarbage);

  // Every survivor is readable and absent from the log; every garbage node
  // is unreadable and present in the log.
  CHECK(ha.get() == a);
  CHECK(hb.get() == b);
  CHECK(hd.get() == d);
  CHECK(hx.get() == nullptr);
  CHECK(hy.get() == nullptr);
  CHECK(hz.get() == nullptr);
  for (const auto& name : expectedSurvivors) {
    CHECK(!c.destructorLog().contains(name));
  }
  CHECK(c.nodeCount() == expectedSurvivors.size());
}

// Root copies count as independent roots; the node survives until the last
// copy is gone.
void root_copies_are_independent() {
  gc::Collector c;
  auto* a = c.create<TestNode>("A");
  auto ha = c.handleOf(a);
  {
    gc::Root<TestNode> r1(c, a);
    {
      gc::Root<TestNode> r2 = r1;
      CHECK(c.collect() == 0);
    }
    CHECK(c.collect() == 0);
    CHECK(ha.get() == a);
  }
  CHECK(c.collect() == 1);
  CHECK(ha.get() == nullptr);
}

struct TestCase {
  const char* name;
  void (*fn)();
};

}  // namespace

int main() {
  const TestCase tests[] = {
      {"unreachable_cycle_is_collected", &unreachable_cycle_is_collected},
      {"self_loop_is_collected", &self_loop_is_collected},
      {"rooted_cycle_survives_garbage_does_not", &rooted_cycle_survives_garbage_does_not},
      {"dropping_root_allows_collection", &dropping_root_allows_collection},
      {"reachability_is_transitive", &reachability_is_transitive},
      {"retired_nodes_are_unreadable_until_end_boundary", &retired_nodes_are_unreadable_until_end_boundary},
      {"roots_registered_during_cycle_are_deferred", &roots_registered_during_cycle_are_deferred},
      {"deferred_root_takes_effect_next_cycle", &deferred_root_takes_effect_next_cycle},
      {"nodes_created_during_cycle_survive_it", &nodes_created_during_cycle_survive_it},
      {"destructor_log_verifies_survivor_set", &destructor_log_verifies_survivor_set},
      {"root_copies_are_independent", &root_copies_are_independent},
  };

  for (const auto& test : tests) {
    const int before = g_failures;
    test.fn();
    std::cout << (g_failures == before ? "PASS " : "FAIL ") << test.name << "\n";
  }

  if (g_failures != 0) {
    std::cerr << g_failures << " check(s) failed\n";
    return 1;
  }
  std::cout << "all tests passed\n";
  return 0;
}

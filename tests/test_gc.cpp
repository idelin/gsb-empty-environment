// Judge: a plain graph-reachability model (BFS from roots over an
// independent edge list). After every collect() the GC's live set and
// refcounts must match the oracle exactly.

#include "gc_graph.h"

#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <deque>
#include <map>
#include <random>
#include <set>
#include <string>
#include <vector>

using gcgraph::Graph;
using gcgraph::Object;

static int failures = 0;

#define CHECK(cond)                                                          \
  do {                                                                       \
    if (!(cond)) {                                                           \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);            \
      ++failures;                                                            \
    }                                                                        \
  } while (0)

// ---------------- Oracle: plain reachability over a separate model --------

struct Model {
  std::set<int> nodes;
  std::map<int, std::multiset<int>> edges;   // internal edges
  std::map<std::string, int> roots;          // external roots

  std::set<int> reachable() const {
    std::set<int> seen;
    std::deque<int> queue;
    for (const auto& kv : roots) {
      if (seen.insert(kv.second).second) queue.push_back(kv.second);
    }
    while (!queue.empty()) {
      int cur = queue.front();
      queue.pop_front();
      auto it = edges.find(cur);
      if (it == edges.end()) continue;
      for (int next : it->second) {
        if (seen.insert(next).second) queue.push_back(next);
      }
    }
    return seen;
  }

  // Expected refcount of a live node: roots naming it + incoming edges
  // from other *live* nodes (dead nodes' edges are removed on destroy).
  std::map<int, std::size_t> expectedRefCounts(const std::set<int>& live) const {
    std::map<int, std::size_t> counts;
    for (const auto& kv : roots) counts[kv.second]++;
    for (int src : live) {
      auto it = edges.find(src);
      if (it != edges.end())
        for (int dst : it->second) counts[dst]++;
    }
    return counts;
  }
};

static void checkAgainstOracle(Graph& g, const Model& m,
                               const std::vector<Object*>& handles) {
  std::set<int> want = m.reachable();
  std::set<int> got;
  for (Object* o : g.liveObjects()) got.insert(static_cast<int>(o->id()));
  CHECK(got == want);
  CHECK(g.size() == want.size());
  auto expected = m.expectedRefCounts(want);
  for (Object* o : g.liveObjects()) {
    int id = static_cast<int>(o->id());
    auto it = expected.find(id);
    CHECK(o->refCount() == (it == expected.end() ? 0 : it->second));
  }
  // Every handle must agree with the oracle about liveness.
  for (Object* h : handles) {
    if (h == nullptr) continue;
    int id = static_cast<int>(h->id());
    CHECK(g.isAlive(h) == (want.count(id) != 0));
  }
}

// ---------------- Scenario tests -----------------------------------------

// 1. Deleting a root must reclaim a pure reference cycle.
static void testRootRemovalReclaimsCycle() {
  Graph g;
  Object *a = g.create(), *b = g.create(), *c = g.create();
  g.addEdge(a, b);
  g.addEdge(b, c);
  g.addEdge(c, a);  // cycle: refcounts all 1, refcounting alone is stuck
  g.addRoot("r", a);
  CHECK(g.collect() == 0);
  CHECK(g.size() == 3);
  g.removeRoot("r");
  CHECK(g.collect() == 3);
  CHECK(g.size() == 0);
}

// 2. Shared subgraph survives while any root remains.
static void testSharedSubgraph() {
  Graph g;
  Object* shared = g.create();
  Object* leaf = g.create();
  g.addEdge(shared, leaf);
  g.addRoot("r1", shared);
  g.addRoot("r2", shared);
  g.removeRoot("r1");
  CHECK(g.collect() == 0);  // still rooted via r2
  CHECK(g.isAlive(shared) && g.isAlive(leaf));
  CHECK(shared->refCount() == 1);
  g.removeRoot("r2");
  CHECK(g.collect() == 2);
  CHECK(g.size() == 0);
}

// 3. Diamond sharing: D reachable via two paths; cutting one keeps it.
static void testDiamondSharedChild() {
  Graph g;
  Object *a = g.create(), *b = g.create(), *c = g.create(), *d = g.create();
  g.addRoot("r", a);
  g.addEdge(a, b);
  g.addEdge(a, c);
  g.addEdge(b, d);
  g.addEdge(c, d);
  CHECK(d->refCount() == 2);
  g.removeEdge(a, b);       // B unreachable; D still reachable via C
  CHECK(g.collect() == 1);
  CHECK(!g.isAlive(b));
  CHECK(g.isAlive(d));
  CHECK(d->refCount() == 1);
  g.removeEdge(a, c);
  CHECK(g.collect() == 2);  // C and D
  CHECK(g.size() == 1);     // only A
}

// 4. Edges of destroyed objects are removed: a dead cycle pointing into
//    the live set must drop its contribution to live refcounts.
static void testDestroyRemovesOutgoingEdges() {
  Graph g;
  Object* live = g.create();
  Object *x = g.create(), *y = g.create();
  g.addRoot("r", live);
  g.addEdge(x, y);
  g.addEdge(y, x);     // dead cycle
  g.addEdge(x, live);  // dead -> live edge
  CHECK(live->refCount() == 2);
  CHECK(g.collect() == 2);
  CHECK(g.isAlive(live));
  CHECK(live->refCount() == 1);  // x's edge was removed at destruction
  CHECK(live->edges().empty());
}

// 5. Long chains: destruction must be iterative, not recursive.
static void testLongChainNoStackOverflow() {
  const int kChain = 1000000;
  {
    Graph g;
    Object* prev = g.create();
    g.addRoot("head", prev);
    for (int i = 1; i < kChain; ++i) {
      Object* next = g.create();
      g.addEdge(prev, next);
      prev = next;
    }
    g.removeRoot("head");
    CHECK(g.collect() == static_cast<std::size_t>(kChain));
    CHECK(g.size() == 0);
  }
  // Graph destructor on a huge live graph must also be flat.
  {
    Graph g;
    Object* prev = g.create();
    for (int i = 1; i < kChain; ++i) {
      Object* next = g.create();
      g.addEdge(prev, next);
      prev = next;
    }
  }
  CHECK(true);  // reached without crashing
}

// 6. Randomized fuzz against the oracle.
static void testFuzzAgainstOracle() {
  std::mt19937 rng(20261006);
  Graph g;
  Model m;
  std::vector<Object*> handles;  // may hold dangling pointers; id() only
  std::vector<Object*> liveHandles;

  auto pickLive = [&](std::mt19937& r) -> Object* {
    if (liveHandles.empty()) return nullptr;
    return liveHandles[r() % liveHandles.size()];
  };

  int rootSeq = 0;
  for (int step = 0; step < 60000; ++step) {
    int op = rng() % 100;
    if (op < 25 || liveHandles.empty()) {
      Object* o = g.create();
      handles.push_back(o);
      liveHandles.push_back(o);
      m.nodes.insert(static_cast<int>(o->id()));
    } else if (op < 50) {
      Object* a = pickLive(rng);
      Object* b = pickLive(rng);
      if (a && b) {
        g.addEdge(a, b);
        m.edges[static_cast<int>(a->id())].insert(static_cast<int>(b->id()));
      }
    } else if (op < 65) {
      Object* a = pickLive(rng);
      if (a && !a->edges().empty()) {
        Object* b = a->edges()[rng() % a->edges().size()];
        g.removeEdge(a, b);
        auto& ms = m.edges[static_cast<int>(a->id())];
        ms.erase(ms.find(static_cast<int>(b->id())));
      }
    } else if (op < 80) {
      Object* a = pickLive(rng);
      if (a) {
        std::string name = "root" + std::to_string(rootSeq++);
        g.addRoot(name, a);
        m.roots[name] = static_cast<int>(a->id());
      }
    } else if (op < 90) {
      if (!m.roots.empty()) {
        auto it = m.roots.begin();
        std::advance(it, rng() % m.roots.size());
        g.removeRoot(it->first);
        m.roots.erase(it);
      }
    } else {
      g.collect();
      // Rebuild live handle list from what actually survived.
      liveHandles.clear();
      for (Object* h : handles) {
        if (h && g.isAlive(h)) liveHandles.push_back(h);
      }
      // Prune oracle state to live nodes; dead nodes' edges are gone.
      std::set<int> live = m.reachable();
      std::set<int> aliveIds;
      for (Object* h : liveHandles) aliveIds.insert(static_cast<int>(h->id()));
      CHECK(live == aliveIds);
      for (auto it = m.nodes.begin(); it != m.nodes.end();) {
        if (aliveIds.count(*it) == 0) it = m.nodes.erase(it);
        else ++it;
      }
      for (auto it = m.edges.begin(); it != m.edges.end();) {
        if (aliveIds.count(it->first) == 0) {
          it = m.edges.erase(it);
        } else {
          for (auto e = it->second.begin(); e != it->second.end();) {
            if (aliveIds.count(*e) == 0) e = it->second.erase(e);
            else ++e;
          }
          ++it;
        }
      }
      checkAgainstOracle(g, m, handles);
    }
  }
  g.collect();
  checkAgainstOracle(g, m, handles);
}

int main() {
  testRootRemovalReclaimsCycle();
  testSharedSubgraph();
  testDiamondSharedChild();
  testDestroyRemovesOutgoingEdges();
  testLongChainNoStackOverflow();
  testFuzzAgainstOracle();

  if (failures == 0) {
    std::printf("ALL TESTS PASSED\n");
    return 0;
  }
  std::printf("%d CHECK(S) FAILED\n", failures);
  return 1;
}

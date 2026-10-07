// Tests for the reference-counted object graph with explicit cycle
// collection. The judge ("oracle") is a plain structural graph model:
// reachability from external roots decides what may survive collect(),
// and structurally derived reference counts decide what the eager
// zero-count reclamation must have destroyed already.

#include "object_graph.h"

#include <cstdlib>
#include <deque>
#include <iostream>
#include <map>
#include <random>
#include <set>
#include <unordered_map>
#include <vector>

using objgraph::Graph;
using objgraph::Object;

static int failures = 0;

#define CHECK(cond)                                                       \
    do {                                                                  \
        if (!(cond)) {                                                    \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": "   \
                      << #cond << "\n";                                   \
            ++failures;                                                   \
        }                                                                 \
    } while (0)

// ---------------------------------------------------------------------------
// Oracle: a plain graph model with no knowledge of the implementation.
// Reference counts are derived structurally (roots + incoming edges).
// ---------------------------------------------------------------------------
struct Model {
    std::set<int> objs;
    std::set<int> roots;
    std::map<int, std::set<int>> out;
    std::unordered_map<int, int> rc;  // structural ref count per object
    // Objects whose count has never risen above zero. Eager reclamation
    // only fires on a 1 -> 0 transition, so these survive until collect().
    std::set<int> floating;

    void addObj(int o) {
        objs.insert(o);
        rc[o] = 0;
        floating.insert(o);
    }
    void addRoot(int o) {
        roots.insert(o);
        ++rc[o];
        floating.erase(o);
    }
    void removeRoot(int o) {
        roots.erase(o);
        --rc[o];
    }
    void addEdge(int a, int b) {
        out[a].insert(b);
        ++rc[b];
        floating.erase(b);
    }
    void removeEdge(int a, int b) {
        out[a].erase(b);
        --rc[b];
    }
    void eraseObj(int o) {
        objs.erase(o);
        roots.erase(o);
        auto it = out.find(o);
        if (it != out.end()) {
            for (int c : it->second) --rc[c];
            out.erase(it);
        }
        rc.erase(o);
        floating.erase(o);
    }

    int refCountOf(int o) const { return rc.at(o); }

    // Plain BFS reachability from the external roots.
    std::set<int> reachable() const {
        std::set<int> seen;
        std::deque<int> queue(roots.begin(), roots.end());
        while (!queue.empty()) {
            int o = queue.front();
            queue.pop_front();
            if (!seen.insert(o).second) continue;
            auto it = out.find(o);
            if (it != out.end())
                for (int c : it->second) queue.push_back(c);
        }
        return seen;
    }

    // Fixpoint of "remove every non-floating object with a zero structural
    // ref count": exactly what eager zero-count reclamation must leave.
    std::set<int> expectedLive() const {
        std::unordered_map<int, int> counts = rc;
        std::set<int> live = objs;
        std::deque<int> queue;
        for (int o : objs)
            if (counts[o] == 0 && !floating.count(o)) queue.push_back(o);
        while (!queue.empty()) {
            int victim = queue.front();
            queue.pop_front();
            if (!live.erase(victim)) continue;
            auto it = out.find(victim);
            if (it != out.end()) {
                for (int c : it->second)
                    if (live.count(c) && --counts[c] == 0) queue.push_back(c);
            }
        }
        return live;
    }
};

static std::set<Object::Id> liveIdSet(const Graph& g) {
    std::vector<Object::Id> ids = g.liveIds();
    return std::set<Object::Id>(ids.begin(), ids.end());
}

// ---------------------------------------------------------------------------
// Deterministic cases
// ---------------------------------------------------------------------------

// Deleting the last root of a cycle: objects survive (counts stay positive)
// until collect() reclaims the whole unreachable ring.
static void test_root_removal_and_cycle() {
    Graph g;
    Object* a = g.create();
    Object* b = g.create();
    Object* c = g.create();
    g.addEdge(a, b);
    g.addEdge(b, c);
    g.addEdge(c, a);
    g.addRoot(a);

    CHECK(g.liveCount() == 3);
    g.removeRoot(a);
    CHECK(g.liveCount() == 3);  // cycle keeps counts positive
    CHECK(g.collect() == 3);
    CHECK(g.liveCount() == 0);
}

// Shared subgraph: two parents share a child; losing one parent must not
// release the shared part.
static void test_shared_subgraph() {
    Graph g;
    Object* a = g.create();
    Object* b = g.create();
    Object* shared = g.create();
    Object* leaf = g.create();
    g.addEdge(a, shared);
    g.addEdge(b, shared);
    g.addEdge(shared, leaf);
    g.addRoot(a);
    g.addRoot(b);

    g.removeRoot(a);  // a dies eagerly; edge a->shared removed on destruction
    CHECK(!g.contains(a));
    CHECK(g.contains(b));
    CHECK(g.contains(shared));
    CHECK(g.contains(leaf));
    CHECK(shared->refCount() == 1);  // only b->shared remains

    g.removeRoot(b);  // cascades iteratively through the shared subgraph
    CHECK(g.liveCount() == 0);
}

// Edge removal during destruction: destroying an object must remove its
// outgoing edges so children referenced elsewhere stay alive.
static void test_edge_removal_on_destruction() {
    Graph g;
    Object* r = g.create();
    Object* mid = g.create();
    Object* keep = g.create();
    Object* anchor = g.create();
    g.addEdge(r, mid);
    g.addEdge(mid, keep);
    g.addEdge(anchor, keep);
    g.addRoot(r);
    g.addRoot(anchor);

    g.removeRoot(r);  // r and mid die; mid's destruction removes mid->keep
    CHECK(!g.contains(r));
    CHECK(!g.contains(mid));
    CHECK(g.contains(keep));
    CHECK(g.contains(anchor));
    CHECK(keep->refCount() == 1);  // only anchor->keep remains
    CHECK(g.collect() == 0);       // nothing unreachable left
}

// Self-loop is a 1-node cycle: unreachable after root removal, reclaimed by
// collect() only.
static void test_self_loop() {
    Graph g;
    Object* o = g.create();
    g.addEdge(o, o);
    g.addRoot(o);
    g.removeRoot(o);
    CHECK(g.contains(o));  // self-edge keeps the count at 1
    CHECK(g.collect() == 1);
    CHECK(g.liveCount() == 0);
}

// Unrooted newborns are reclaimed by collect(); rooted ones survive.
static void test_collect_keeps_reachable() {
    Graph g;
    Object* root = g.create();
    Object* child = g.create();
    Object* floating = g.create();
    g.addRoot(root);
    g.addEdge(root, child);
    CHECK(g.collect() == 1);  // only the floating object
    CHECK(g.contains(root));
    CHECK(g.contains(child));
    CHECK(!g.contains(floating));
}

// Deep chains and giant cycles must be torn down iteratively: recursive
// destructor chains of this depth would overflow the stack.
static void test_deep_chain_and_big_cycle() {
    const std::size_t depth = 1000000;
    {
        Graph g;
        Object* head = g.create();
        g.addRoot(head);
        Object* prev = head;
        for (std::size_t i = 1; i < depth; ++i) {
            Object* next = g.create();
            g.addEdge(prev, next);
            prev = next;
        }
        g.removeRoot(head);
        CHECK(g.liveCount() == 0);  // iterative cascade, no recursion
    }
    {
        Graph g;
        Object* head = g.create();
        Object* prev = head;
        for (std::size_t i = 1; i < depth; ++i) {
            Object* next = g.create();
            g.addEdge(prev, next);
            prev = next;
        }
        g.addEdge(prev, head);  // close the ring: every count stays 1
        CHECK(g.collect() == depth);
        CHECK(g.liveCount() == 0);
    }
}

// Graph destruction itself must be iterative even with deep structure left.
static void test_graph_destructor_iterative() {
    const std::size_t depth = 500000;
    Graph* g = new Graph();
    Object* head = g->create();
    g->addRoot(head);
    Object* prev = head;
    for (std::size_t i = 1; i < depth; ++i) {
        Object* next = g->create();
        g->addEdge(prev, next);
        prev = next;
    }
    delete g;  // would recurse ~500k frames if teardown were recursive
    CHECK(true);
}

// ---------------------------------------------------------------------------
// Randomized stress test against the oracle model.
// ---------------------------------------------------------------------------
static void verify(const Graph& g, const Model& m, bool after_collect,
                   const std::vector<Object*>& ptrs) {
    std::set<Object::Id> live = liveIdSet(g);
    std::set<int> expected = after_collect ? m.reachable() : m.expectedLive();
    std::set<Object::Id> expected_ids(expected.begin(), expected.end());
    CHECK(live == expected_ids);
    CHECK(g.liveCount() == expected_ids.size());
    // Reachable objects must always be alive, regardless of collect().
    std::set<int> reach = m.reachable();
    for (int id : reach) CHECK(live.count(static_cast<Object::Id>(id)));
    // Structural ref counts must match on live objects.
    for (int id : expected) {
        const Object* o = ptrs[static_cast<std::size_t>(id)];
        CHECK(o->refCount() == static_cast<std::size_t>(m.refCountOf(id)));
    }
}

static void test_randomized_against_model() {
    std::mt19937 rng(20261007);
    for (int trial = 0; trial < 20; ++trial) {
        Graph g;
        Model m;
        std::vector<Object*> ptrs;

        for (int step = 0; step < 4000; ++step) {
            std::vector<int> model_objs(m.objs.begin(), m.objs.end());
            auto pick = [&]() -> int {
                if (model_objs.empty()) return -1;
                return model_objs[rng() % model_objs.size()];
            };
            int op = static_cast<int>(rng() % 7);
            bool collected = false;
            switch (op) {
                case 0: {  // create + root
                    Object* o = g.create();
                    g.addRoot(o);
                    int id = static_cast<int>(o->id());
                    ptrs.push_back(o);
                    m.addObj(id);
                    m.addRoot(id);
                    break;
                }
                case 1: {  // add edge between two live objects
                    int a = pick();
                    int b = pick();
                    if (a < 0 || b < 0 || m.out[a].count(b)) break;
                    g.addEdge(ptrs[a], ptrs[b]);
                    m.addEdge(a, b);
                    break;
                }
                case 2: {  // remove an existing edge
                    int a = pick();
                    if (a < 0 || m.out[a].empty()) break;
                    auto it = m.out[a].begin();
                    std::advance(it, rng() % m.out[a].size());
                    int b = *it;
                    g.removeEdge(ptrs[a], ptrs[b]);
                    m.removeEdge(a, b);
                    break;
                }
                case 3: {  // add root
                    int a = pick();
                    if (a < 0 || m.roots.count(a)) break;
                    g.addRoot(ptrs[a]);
                    m.addRoot(a);
                    break;
                }
                case 4: {  // remove root
                    if (m.roots.empty()) break;
                    auto it = m.roots.begin();
                    std::advance(it, rng() % m.roots.size());
                    int a = *it;
                    g.removeRoot(ptrs[a]);
                    m.removeRoot(a);
                    break;
                }
                case 5: {  // collect
                    std::size_t reclaimed = g.collect();
                    std::set<int> reach = m.reachable();
                    std::vector<int> dead;
                    for (int o : m.objs)
                        if (!reach.count(o)) dead.push_back(o);
                    CHECK(reclaimed == dead.size());
                    for (int o : dead) m.eraseObj(o);
                    collected = true;
                    break;
                }
                case 6: {  // create unrooted (floating) object
                    Object* o = g.create();
                    int id = static_cast<int>(o->id());
                    ptrs.push_back(o);
                    m.addObj(id);
                    break;
                }
            }
            // Sync the model with eager zero-count reclamation.
            std::set<int> expected_live = m.expectedLive();
            std::vector<int> gone;
            for (int o : m.objs)
                if (!expected_live.count(o)) gone.push_back(o);
            for (int o : gone) m.eraseObj(o);
            verify(g, m, collected, ptrs);
        }
    }
}

int main() {
    test_root_removal_and_cycle();
    test_shared_subgraph();
    test_edge_removal_on_destruction();
    test_self_loop();
    test_collect_keeps_reachable();
    test_deep_chain_and_big_cycle();
    test_graph_destructor_iterative();
    test_randomized_against_model();

    if (failures == 0) {
        std::cout << "All tests passed.\n";
        return EXIT_SUCCESS;
    }
    std::cerr << failures << " check(s) failed.\n";
    return EXIT_FAILURE;
}

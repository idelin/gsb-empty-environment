// Assert-based test harness for the gc cycle collector.
#include "gc/gc.hpp"

#include <cstdio>
#include <set>
#include <string>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                            \
    do {                                                                       \
        if (!(cond)) {                                                         \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);        \
            ++g_failures;                                                      \
        }                                                                      \
    } while (0)

std::set<std::string> to_set(const gc::destruction_log& log) {
    return {log.begin(), log.end()};
}

std::set<std::string> names_of(const std::vector<std::shared_ptr<gc::Node>>& nodes) {
    std::set<std::string> names;
    for (const auto& n : nodes) {
        names.insert(n->name());
    }
    return names;
}

// A cycle with no roots is reclaimed in one batch.
void unreferenced_cycle_is_reclaimed() {
    gc::Collector gc;
    {
        auto a = gc.make_node("a");
        auto b = gc.make_node("b");
        auto c = gc.make_node("c");
        a->link(b);
        b->link(c);
        c->link(a); // strong cycle: refcount alone can never free this
    }               // external handles gone; the cycle still self-sustains

    CHECK(gc.log().empty());
    CHECK(gc.live_set().size() == 3);

    const std::size_t reclaimed = gc.collect();

    CHECK(reclaimed == 3);
    CHECK((to_set(gc.log()) == std::set<std::string>{"a", "b", "c"}));
    CHECK(gc.live_set().empty());
    CHECK(gc.phase() == gc::Phase::Idle);
}

// A cycle reachable from a root survives collection.
void rooted_cycle_survives() {
    gc::Collector gc;
    auto a = gc.make_node("a");
    auto b = gc.make_node("b");
    a->link(b);
    b->link(a);
    gc.add_root(a);

    const std::size_t reclaimed = gc.collect();

    CHECK(reclaimed == 0);
    CHECK(gc.log().empty());
    CHECK((names_of(gc.live_set()) == std::set<std::string>{"a", "b"}));
    CHECK(a->read() == 0); // still readable
}

// Removing the last root makes the cycle collectable in the next epoch.
void root_removal_allows_reclaim() {
    gc::Collector gc;
    auto a = gc.make_node("a");
    auto b = gc.make_node("b");
    a->link(b);
    b->link(a);
    gc.add_root(a);

    CHECK(gc.collect() == 0);
    gc.remove_root(a);
    a.reset(); // drop our handles so destruction can complete
    b.reset();
    CHECK(gc.collect() == 2);
    CHECK((to_set(gc.log()) == std::set<std::string>{"a", "b"}));
}

// Roots registered during the Mark phase are deferred: they do not rescue
// nodes in the current epoch, and cannot resurrect a retired node later.
void roots_registered_during_collection_are_deferred() {
    gc::Collector gc;
    auto doomed = gc.make_node("doomed");
    auto saved = gc.make_node("saved");
    doomed->link(doomed); // self-cycle
    gc.add_root(saved);

    bool hook_saw_mark_phase = false;
    gc.collect([&](gc::Collector& c) {
        hook_saw_mark_phase = (c.phase() == gc::Phase::Mark);
        c.add_root(doomed); // deferred: too late for this epoch
    });

    CHECK(hook_saw_mark_phase);
    // `doomed` was unreachable at the root snapshot, so it was retired even
    // though a root was registered mid-collection.
    CHECK(doomed->retired());
    CHECK(to_set(gc.log()).count("doomed") == 0); // still held by our handle
    CHECK(!gc.is_root(doomed)); // retired nodes are not re-admitted as roots

    doomed.reset(); // drop the last strong reference
    CHECK((to_set(gc.log()) == std::set<std::string>{"doomed"}));
    CHECK((names_of(gc.live_set()) == std::set<std::string>{"saved"}));
}

// A deferred root for a *reachable* node takes effect after the epoch.
void deferred_root_applies_next_epoch() {
    gc::Collector gc;
    auto root = gc.make_node("root");
    auto child = gc.make_node("child");
    root->link(child);
    gc.add_root(root);

    gc.collect([&](gc::Collector& c) { c.add_root(child); });
    CHECK(gc.is_root(child)); // effective after collection finished

    gc.remove_root(root);
    root.reset(); // `root` is not in a cycle: refcounting destroys it here
    CHECK((to_set(gc.log()) == std::set<std::string>{"root"}));
    CHECK(gc.collect() == 0); // nothing else to reclaim
    CHECK((names_of(gc.live_set()) == std::set<std::string>{"child"}));
}

// Reading a retired node throws instead of touching a torn-down object.
void retired_nodes_reject_reads() {
    gc::Collector gc;
    auto a = gc.make_node("a");
    auto b = gc.make_node("b");
    a->link(b);
    b->link(a);
    a->write(42);

    gc.collect(); // a and b are retired; our handles keep them alive

    CHECK(a->retired());
    bool threw = false;
    try {
        (void)a->read();
    } catch (const gc::retired_access_error&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        a->write(1);
    } catch (const gc::retired_access_error&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        gc.add_root(a); // resurrecting a retired node is rejected
    } catch (const gc::retired_access_error&) {
        threw = true;
    }
    CHECK(threw);

    // Nothing was destroyed yet: the external handles still hold strong refs.
    CHECK(gc.log().empty());
}

// Mixed graph: the destruction log and the live set partition the node set.
void destruction_log_verifies_live_set() {
    gc::Collector gc;

    // Live component: root -> mid -> leaf, plus a rooted cycle.
    auto root = gc.make_node("root");
    auto mid = gc.make_node("mid");
    auto leaf = gc.make_node("leaf");
    auto cyc1 = gc.make_node("cyc1");
    auto cyc2 = gc.make_node("cyc2");
    root->link(mid);
    mid->link(leaf);
    leaf->link(cyc1);
    cyc1->link(cyc2);
    cyc2->link(cyc1);
    gc.add_root(root);

    // Garbage: an isolated 3-cycle plus a chain hanging off it.
    const std::set<std::string> expected_live{"root", "mid", "leaf", "cyc1", "cyc2"};
    const std::set<std::string> expected_dead{"g1", "g2", "g3", "g4"};
    {
        auto g1 = gc.make_node("g1");
        auto g2 = gc.make_node("g2");
        auto g3 = gc.make_node("g3");
        auto g4 = gc.make_node("g4");
        g1->link(g2);
        g2->link(g3);
        g3->link(g1);
        g3->link(g4);
    } // handles gone; the cycle keeps the whole component alive

    const std::size_t reclaimed = gc.collect();

    CHECK(reclaimed == expected_dead.size());
    CHECK(to_set(gc.log()) == expected_dead);
    CHECK(names_of(gc.live_set()) == expected_live);

    // Log and live set partition everything that was ever created.
    std::set<std::string> all = expected_live;
    all.insert(expected_dead.begin(), expected_dead.end());
    std::set<std::string> observed = to_set(gc.log());
    const auto live = names_of(gc.live_set());
    observed.insert(live.begin(), live.end());
    CHECK(observed == all);
}

// Dead nodes may point into the live set; sweeping must not harm the live set.
void garbage_edges_into_live_set_are_cut_cleanly() {
    gc::Collector gc;
    auto live = gc.make_node("live");
    live->write(7);
    gc.add_root(live);
    {
        auto dead1 = gc.make_node("dead1");
        auto dead2 = gc.make_node("dead2");
        dead1->link(dead2);
        dead2->link(dead1); // garbage cycle keeps itself alive
        dead2->link(live);  // garbage -> live edge
    }

    CHECK(gc.collect() == 2);
    CHECK((to_set(gc.log()) == std::set<std::string>{"dead1", "dead2"}));
    CHECK(live->read() == 7);
    CHECK((names_of(gc.live_set()) == std::set<std::string>{"live"}));
}

// Reentrant collection from inside the mark hook is rejected.
void reentrant_collect_is_rejected() {
    gc::Collector gc;
    gc.make_node("x");
    bool threw = false;
    gc.collect([&](gc::Collector& c) {
        try {
            c.collect();
        } catch (const std::logic_error&) {
            threw = true;
        }
    });
    CHECK(threw);
    CHECK(gc.phase() == gc::Phase::Idle);
}

} // namespace

int main() {
    unreferenced_cycle_is_reclaimed();
    rooted_cycle_survives();
    root_removal_allows_reclaim();
    roots_registered_during_collection_are_deferred();
    deferred_root_applies_next_epoch();
    retired_nodes_reject_reads();
    destruction_log_verifies_live_set();
    garbage_edges_into_live_set_are_cut_cleanly();
    reentrant_collect_is_rejected();

    if (g_failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", g_failures);
    return 1;
}

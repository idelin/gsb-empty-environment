#include "netsec.hpp"

#include <chrono>
#include <iostream>

namespace {

using namespace std::chrono_literals;

netsec::Credentials credentials() {
    return netsec::Credentials{1000, 1000, 777, "probe"};
}

}  // namespace

int main() {
    netsec::LifecycleOptions options;
    options.quarantine = 25ms;
    netsec::NodeLifecycle lifecycle(options);
    const auto session = lifecycle.open_session(42);

    auto a = lifecycle.lookup(session, 2, "object-a", 4, {1, "a"});
    auto b = lifecycle.lookup(session, 3, "object-b", 4, {1, "b"});
    if (!a.ok() || !b.ok()) {
        return 2;
    }
    const netsec::NodeRef old_a{session, a.entry->nodeid,
                                a.entry->generation};
    const netsec::NodeRef old_b{session, b.entry->nodeid,
                                b.entry->generation};

    // A legal cache hit is served while the inode identity and cache epoch
    // still match.
    if (!lifecycle.cache_hit(old_a, a.entry->cache_epoch).hit) {
        return 3;
    }

    // The backend is delayed while FORGET arrives.  The in-flight unique keeps
    // the node id pinned even though nlookup has reached zero.
    auto delayed =
        lifecycle.receive_request(old_a, 100, credentials());
    if (!delayed.ok() || !delayed.request->before_side_effect().ok) {
        return 4;
    }
    lifecycle.forget(session, old_a.nodeid, 1);

    // RENAME_EXCHANGE changes only directory-entry bindings.
    const auto exchanged = lifecycle.rename_exchange(old_a, old_b);
    if (!exchanged.ok() || exchanged.first->stable_object_id != "object-a" ||
        exchanged.first->generation != old_a.generation) {
        return 5;
    }

    auto blocked =
        lifecycle.lookup(session, old_a.nodeid, "object-c", 5, {1, "c"});
    if (blocked.status != netsec::LookupStatus::nodeid_busy) {
        return 6;
    }

    (void)delayed.request->complete();
    lifecycle.advance(1s);

    auto reused =
        lifecycle.lookup(session, old_a.nodeid, "object-c", 5, {1, "c"});
    if (!reused.ok()) {
        return 7;
    }
    const netsec::NodeRef current{session, reused.entry->nodeid,
                                  reused.entry->generation};

    // Deliberately replay the old generation after the node id has been
    // retargeted.  This must be refused without changing the new node.
    lifecycle.clear_identity_conflicts();
    auto stale = lifecycle.receive_request(old_a, 150, credentials());
    if (!stale.conflict ||
        stale.conflict->kind != netsec::ConflictKind::nodeid_reused) {
        return 8;
    }

    // INTERRUPT settles the old ticket only.  The numeric unique can be reused
    // as a new ticket without a late old completion touching it.
    auto interrupted =
        lifecycle.receive_request(current, 200, credentials());
    if (!interrupted.ok() || !lifecycle.interrupt(session, 200)) {
        return 9;
    }
    auto replacement =
        lifecycle.receive_request(current, 200, credentials());
    if (!replacement.ok()) {
        return 10;
    }
    (void)interrupted.request->complete();
    if (!replacement.request->validate().ok) {
        return 11;
    }
    (void)replacement.request->complete();

    // A reconnect invalidates all old-session requests and lets the new
    // session reuse the same numeric unique safely.
    auto before_restart =
        lifecycle.receive_request(current, 300, credentials());
    const auto restarted = lifecycle.restart_session(session);
    auto after_restart_lookup =
        lifecycle.lookup(restarted, 2, "object-c", 6, {1, "c"});
    if (!after_restart_lookup.ok()) {
        return 12;
    }
    auto after_restart = lifecycle.receive_request(
        netsec::NodeRef{restarted, after_restart_lookup.entry->nodeid,
                        after_restart_lookup.entry->generation},
        300, credentials());
    if (!after_restart.ok() ||
        before_restart.request->validate().conflict !=
            netsec::ConflictKind::request_aborted) {
        return 13;
    }

    // Cache invalidation preserves inode identity and the stale cache epoch is
    // rejected from this point on.
    auto invalidated = lifecycle.notify_inval_inode(
        netsec::NodeRef{restarted, after_restart_lookup.entry->nodeid,
                        after_restart_lookup.entry->generation});
    if (!invalidated.ok() ||
        lifecycle
            .cache_hit(netsec::NodeRef{
                           restarted, after_restart_lookup.entry->nodeid,
                           after_restart_lookup.entry->generation},
                       after_restart_lookup.entry->cache_epoch)
            .hit) {
        return 14;
    }
    (void)invalidated.notification->complete();
    (void)after_restart.request->complete();

    const auto first = lifecycle.first_identity_conflict();
    if (!first) {
        return 15;
    }
    std::cout << first->to_string() << '\n';
    return 0;
}

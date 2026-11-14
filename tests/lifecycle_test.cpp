#include "netsec.hpp"

#include <chrono>
#include <cstdlib>
#include <iostream>
#include <string>
#include <utility>
#include <vector>

namespace {

using namespace std::chrono_literals;

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (condition) {
        return;
    }
    std::cerr << "FAILED line " << line << ": " << expression << '\n';
    ++failures;
}

#define CHECK(expression) check((expression), #expression, __LINE__)

netsec::Credentials credentials() {
    return netsec::Credentials{1000, 1000, 4242, "sandbox"};
}

netsec::LookupResult lookup_or_fail(netsec::NodeLifecycle& lifecycle,
                                    netsec::SessionKey session,
                                    netsec::NodeId nodeid,
                                    std::string object,
                                    netsec::NameKey name = {}) {
    auto outcome = lifecycle.lookup(session, nodeid, std::move(object), 7,
                                    std::move(name));
    CHECK(outcome.ok());
    if (!outcome.entry) {
        std::abort();
    }
    return *outcome.entry;
}

void forget_blocks_reuse_until_original_unique_settles() {
    netsec::LifecycleOptions options;
    options.quarantine = 50ms;
    netsec::NodeLifecycle lifecycle(options);
    const auto session = lifecycle.open_session(9);

    const auto old_node =
        lookup_or_fail(lifecycle, session, 2, "object-a", {1, "a"});
    auto old_request = lifecycle.receive_request(
        netsec::NodeRef{session, old_node.nodeid, old_node.generation}, 100,
        credentials());
    CHECK(old_request.ok());
    CHECK(old_request.request->validate().ok);

    const auto forgotten = lifecycle.forget(session, old_node.nodeid, 1);
    CHECK(forgotten.applied == 1);
    CHECK(forgotten.ignored == 0);

    const auto retired = lifecycle.node(
        netsec::NodeRef{session, old_node.nodeid, old_node.generation});
    CHECK(retired.has_value());
    CHECK(retired->retired);
    CHECK(retired->in_flight_requests == 1);

    lifecycle.advance(1s);
    auto blocked = lifecycle.lookup(session, old_node.nodeid, "object-b", 8,
                                    {1, "b"});
    CHECK(!blocked.ok());
    CHECK(blocked.status == netsec::LookupStatus::nodeid_busy);
    CHECK(blocked.conflict.has_value());

    const auto settled = old_request.request->complete();
    CHECK(settled.status == netsec::CompletionStatus::settled);
    CHECK(settled.may_reply);

    auto reused = lifecycle.lookup(session, old_node.nodeid, "object-b", 8,
                                   {1, "b"});
    CHECK(reused.ok());
    CHECK(reused.entry.has_value());
    CHECK(reused.entry->generation > old_node.generation);
    CHECK(reused.entry->stable_object_id == "object-b");
    CHECK(reused.entry->nodeid_reused);

    const auto duplicate = old_request.request->complete();
    CHECK(duplicate.status ==
          netsec::CompletionStatus::ignored_already_settled);
}

void interrupt_and_reused_unique_have_distinct_tickets() {
    netsec::NodeLifecycle lifecycle;
    const auto session = lifecycle.open_session(10);
    const auto node =
        lookup_or_fail(lifecycle, session, 2, "object-a", {1, "a"});
    const netsec::NodeRef ref{session, node.nodeid, node.generation};

    auto first =
        lifecycle.receive_request(ref, 777, credentials());
    CHECK(first.ok());
    const auto first_ticket = first.request->ticket();
    CHECK(lifecycle.interrupt(session, 777));
    CHECK(first.request->validate().conflict ==
          netsec::ConflictKind::request_interrupted);

    auto second =
        lifecycle.receive_request(ref, 777, credentials());
    CHECK(second.ok());
    CHECK(second.request->ticket() != first_ticket);

    // A delayed interrupt for the old incarnation must not fall through to
    // the active ticket with the same numeric unique.
    CHECK(!lifecycle.interrupt(session, 777));
    CHECK(second.request->validate().ok);

    const auto late_first = first.request->complete();
    CHECK(late_first.status ==
          netsec::CompletionStatus::ignored_interrupted);
    CHECK(!late_first.may_reply);
    CHECK(second.request->validate().ok);

    const auto second_result = second.request->before_side_effect();
    CHECK(second_result.ok);
    const auto second_completion = second.request->complete();
    CHECK(second_completion.status == netsec::CompletionStatus::settled);
}

void rename_exchange_moves_names_but_not_identity() {
    netsec::NodeLifecycle lifecycle;
    const auto session = lifecycle.open_session(11);
    const auto left =
        lookup_or_fail(lifecycle, session, 2, "left-object", {1, "left"});
    const auto right =
        lookup_or_fail(lifecycle, session, 3, "right-object", {1, "right"});
    const netsec::NodeRef left_ref{session, left.nodeid, left.generation};
    const netsec::NodeRef right_ref{session, right.nodeid, right.generation};

    netsec::RequestOptions name_sensitive;
    name_sensitive.require_name_unchanged = true;
    auto queued = lifecycle.receive_request(left_ref, 50, credentials(),
                                            name_sensitive);
    CHECK(queued.ok());

    const auto renamed =
        lifecycle.rename_exchange(left_ref, right_ref);
    CHECK(renamed.ok());
    CHECK(renamed.first->stable_object_id == "left-object");
    CHECK(renamed.first->generation == left.generation);
    CHECK(renamed.first->name.name == "right");
    CHECK(renamed.second->stable_object_id == "right-object");
    CHECK(renamed.second->generation == right.generation);
    CHECK(renamed.second->name.name == "left");

    const auto left_name = lifecycle.resolve_name(session, {1, "left"});
    const auto right_name = lifecycle.resolve_name(session, {1, "right"});
    CHECK(left_name.has_value());
    CHECK(right_name.has_value());
    CHECK(left_name->stable_object_id == "right-object");
    CHECK(right_name->stable_object_id == "left-object");

    const auto stale_name_check = queued.request->validate();
    CHECK(!stale_name_check.ok);
    CHECK(stale_name_check.conflict == netsec::ConflictKind::name_changed);
}

void unlink_keeps_open_handle_identity_alive() {
    netsec::NodeLifecycle lifecycle;
    const auto session = lifecycle.open_session(12);
    const auto node =
        lookup_or_fail(lifecycle, session, 2, "unlinked-object", {1, "gone"});
    const netsec::NodeRef ref{session, node.nodeid, node.generation};

    auto opened = lifecycle.open_handle(ref);
    CHECK(opened.ok());
    CHECK(opened.handle.has_value());

    const auto removed = lifecycle.unlink(session, {1, "gone"}, ref);
    CHECK(removed.ok());
    CHECK(!removed.node->name_live);
    CHECK(removed.node->nlink == 0);
    CHECK(removed.node->stable_object_id == "unlinked-object");

    CHECK(lifecycle.forget(session, node.nodeid, 1).applied == 1);
    lifecycle.advance(1s);

    auto blocked = lifecycle.lookup(session, node.nodeid, "new-object", 8,
                                    {1, "new"});
    CHECK(blocked.status == netsec::LookupStatus::nodeid_busy);

    auto via_handle =
        lifecycle.receive_request(*opened.handle, 900, credentials());
    CHECK(via_handle.ok());
    CHECK(via_handle.request->validate().ok);
    CHECK(via_handle.request->before_side_effect().ok);
    CHECK(via_handle.request->complete().status ==
          netsec::CompletionStatus::settled);

    CHECK(opened.handle->close());
    auto reused = lifecycle.lookup(session, node.nodeid, "new-object", 8,
                                   {1, "new"});
    CHECK(reused.ok());
    CHECK(reused.entry->generation > node.generation);
}

void notification_invalidates_cache_and_pins_identity() {
    netsec::NodeLifecycle lifecycle;
    const auto session = lifecycle.open_session(13);
    const auto node =
        lookup_or_fail(lifecycle, session, 2, "cached-object", {1, "cache"});
    const netsec::NodeRef ref{session, node.nodeid, node.generation};

    auto hit = lifecycle.cache_hit(ref, node.cache_epoch);
    CHECK(hit.hit);

    auto notification = lifecycle.notify_inval_inode(ref);
    CHECK(notification.ok());
    CHECK(notification.notification.has_value());

    hit = lifecycle.cache_hit(ref, node.cache_epoch);
    CHECK(!hit.hit);
    const auto after = lifecycle.node(ref);
    CHECK(after->generation == node.generation);
    CHECK(after->stable_object_id == "cached-object");
    CHECK(!after->cache_valid);
    CHECK(after->notification_refs == 1);

    CHECK(lifecycle.forget(session, node.nodeid, 1).applied == 1);
    lifecycle.advance(1s);
    auto blocked = lifecycle.lookup(session, node.nodeid, "next-object", 9,
                                    {1, "next"});
    CHECK(blocked.status == netsec::LookupStatus::nodeid_busy);

    CHECK(notification.notification->complete());
    auto reused = lifecycle.lookup(session, node.nodeid, "next-object", 9,
                                   {1, "next"});
    CHECK(reused.ok());
}

void connection_restart_cuts_old_requests_and_uniques() {
    netsec::NodeLifecycle lifecycle;
    const auto old_session = lifecycle.open_session(14);
    const auto old_node =
        lookup_or_fail(lifecycle, old_session, 2, "old-session-object",
                       {1, "old"});
    auto old_request = lifecycle.receive_request(
        netsec::NodeRef{old_session, old_node.nodeid, old_node.generation},
        1234, credentials());
    CHECK(old_request.ok());
    CHECK(old_request.request->before_side_effect().ok);

    const auto new_session = lifecycle.restart_session(old_session);
    CHECK(new_session.mount == old_session.mount);
    CHECK(new_session.epoch > old_session.epoch);
    CHECK(!lifecycle.session_active(old_session));
    CHECK(old_request.request->validate().conflict ==
          netsec::ConflictKind::request_aborted);

    const auto new_node = lookup_or_fail(lifecycle, new_session, 2,
                                         "new-session-object", {1, "new"});
    auto new_request = lifecycle.receive_request(
        netsec::NodeRef{new_session, new_node.nodeid, new_node.generation},
        1234, credentials());
    CHECK(new_request.ok());
    CHECK(new_request.request->validate().ok);

    const auto old_completion = old_request.request->complete();
    CHECK(old_completion.status ==
          netsec::CompletionStatus::ignored_aborted);
    CHECK(!old_completion.may_reply);
    CHECK(new_request.request->validate().ok);
    CHECK(new_request.request->complete().may_reply);
}

void side_effect_gate_rechecks_policy_and_credentials() {
    bool credentials_allowed = true;
    netsec::LifecycleOptions options;
    options.credential_validator =
        [&credentials_allowed](const netsec::Credentials&) {
            return credentials_allowed;
        };
    netsec::NodeLifecycle lifecycle(options);
    const auto session = lifecycle.open_session(16);
    auto node = lookup_or_fail(lifecycle, session, 2, "policy-object",
                               {1, "policy"});
    const netsec::NodeRef ref{session, node.nodeid, node.generation};

    auto request = lifecycle.receive_request(ref, 600, credentials());
    CHECK(request.ok());
    CHECK(request.request->validate().ok);

    // A newer policy generation bound to the same inode identity invalidates
    // the old authorization snapshot before any side effect runs.
    auto policy_updated =
        lifecycle.lookup(session, node.nodeid, "policy-object", 11,
                         {1, "policy"});
    CHECK(policy_updated.ok());
    const auto policy_check = request.request->before_side_effect();
    CHECK(!policy_check.ok);
    CHECK(policy_check.conflict ==
          netsec::ConflictKind::policy_generation_changed);

    auto current = lifecycle.receive_request(ref, 601, credentials());
    CHECK(current.ok());
    CHECK(current.request->before_side_effect().ok);

    credentials_allowed = false;
    auto second = lifecycle.receive_request(ref, 602, credentials());
    CHECK(second.status == netsec::RequestStatus::credentials_rejected);
    CHECK(second.conflict.has_value());
    CHECK(second.conflict->kind ==
          netsec::ConflictKind::credential_rejected);

    // An already received request is rechecked again at the side-effect gate.
    credentials_allowed = true;
    auto third = lifecycle.receive_request(ref, 603, credentials());
    CHECK(third.ok());
    credentials_allowed = false;
    const auto credential_check = third.request->before_side_effect();
    CHECK(!credential_check.ok);
    CHECK(credential_check.conflict ==
          netsec::ConflictKind::credential_rejected);
}

void batch_forget_and_first_identity_conflict() {
    netsec::LifecycleOptions options;
    options.quarantine = 10ms;
    netsec::NodeLifecycle lifecycle(options);
    const auto session = lifecycle.open_session(15);
    const auto a = lookup_or_fail(lifecycle, session, 2, "a", {1, "a"});
    const auto b = lookup_or_fail(lifecycle, session, 3, "b", {1, "b"});
    CHECK(!lifecycle.first_identity_conflict().has_value());

    const auto result = lifecycle.batch_forget(
        session, {{a.nodeid, 1}, {b.nodeid, 1}});
    CHECK(result.applied == 2);
    CHECK(result.ignored == 0);
    CHECK(lifecycle.node(netsec::NodeRef{session, a.nodeid, a.generation})
              ->kernel_lookup_refs == 0);
    CHECK(!lifecycle.first_identity_conflict().has_value());

    lifecycle.advance(1s);
    const auto replacement =
        lookup_or_fail(lifecycle, session, 2, "replacement", {1, "replacement"});
    CHECK(replacement.generation > a.generation);

    auto stale = lifecycle.receive_request(
        netsec::NodeRef{session, a.nodeid, a.generation}, 55, credentials());
    CHECK(!stale.ok());
    CHECK(stale.status == netsec::RequestStatus::stale_generation);
    CHECK(stale.conflict.has_value());
    CHECK(stale.conflict->kind == netsec::ConflictKind::nodeid_reused);
    CHECK(stale.conflict->actual_object_id == "replacement");

    const auto first = lifecycle.first_identity_conflict();
    CHECK(first.has_value());
    if (first) {
        CHECK(first->sequence == stale.conflict->sequence);
        CHECK(first->kind == netsec::ConflictKind::nodeid_reused);
        CHECK(first->to_string().find("nodeid-reused") != std::string::npos);
    }
}

}  // namespace

int main() {
    forget_blocks_reuse_until_original_unique_settles();
    interrupt_and_reused_unique_have_distinct_tickets();
    rename_exchange_moves_names_but_not_identity();
    unlink_keeps_open_handle_identity_alive();
    notification_invalidates_cache_and_pins_identity();
    connection_restart_cuts_old_requests_and_uniques();
    side_effect_gate_rechecks_policy_and_credentials();
    batch_forget_and_first_identity_conflict();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all lifecycle tests passed\n";
    return 0;
}

#include "borrow/borrow.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

using namespace borrow;

namespace {

int g_checks = 0;
int g_failures = 0;

void report(bool ok, const char* expr, const char* file, int line,
            const std::string& extra = {}) {
    ++g_checks;
    if (ok) return;
    ++g_failures;
    std::cerr << file << ":" << line << ": CHECK failed: " << expr;
    if (!extra.empty()) std::cerr << "  [" << extra << "]";
    std::cerr << "\n";
}

#define CHECK(expr) \
    report(static_cast<bool>(expr), #expr, __FILE__, __LINE__)
#define CHECK_MSG(expr, msg) \
    report(static_cast<bool>(expr), #expr, __FILE__, __LINE__, (msg))

const ObjectRef kObj1{1, 0};

HistoryProblem gap_problem() {
    HistoryProblem p;
    p.add_object(1, 8, 0);

    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
        Event::enter(10, 11),
        Event::enter(10, 12),
    };
    p.add_segment(s0);

    Segment gap;
    gap.id = 2;
    gap.summary = "same-gap";
    gap.frame = 11;
    gap.object = 1;
    gap.range = Range(0, 4);
    gap.categories = {"noop", "shared"};
    p.add_gap(gap);
    return p;
}

HistoryQuery write_query(FrameId frame, Range range) {
    HistoryQuery q;
    q.frame = frame;
    q.object = 1;
    q.range = range;
    q.kind = QueryKind::Write;
    return q;
}

void test_intervals() {
    IntervalSet s;
    s.add(Range(0, 4));
    s.add(Range(4, 8));
    CHECK(s.ranges().size() == 1);
    CHECK((s.ranges()[0] == Range(0, 8)));
    CHECK(s.covers(Range(0, 8)));
    CHECK(s.intersects(Range(7, 9)));

    s.subtract(Range(2, 6));
    CHECK((s == IntervalSet::from(Range(0, 2))
                        .union_with(IntervalSet::from(Range(6, 8)))));
    CHECK(!s.covers(Range(0, 8)));

    s.add(Range(1, 3));
    CHECK((s.ranges().size() == 2));
    CHECK((s.ranges()[0] == Range(0, 3)));
    CHECK((s.ranges()[1] == Range(6, 8)));
    CHECK(s.measure() == 5);

    IntervalSet t = IntervalSet::from(Range(0, 10));
    t.intersect(Range(4, 12));
    CHECK((t == IntervalSet::from(Range(4, 10))));
    CHECK(t.covers(IntervalSet::from(Range(5, 6))));
}

void test_sequential_return() {
    Authority a;
    a.define_object(1, 8);
    FrameId first = a.enter();
    CHECK(a.acquire(first, kObj1, Range(0, 4), BorrowMode::Exclusive));
    CHECK(a.leave(first) == Status::Ok);

    FrameId second = a.enter();
    CHECK(a.acquire(second, kObj1, Range(0, 4), BorrowMode::Exclusive));
    CHECK(a.can_access(second, kObj1, Range(0, 4), AccessKind::Write).allowed);
}

void test_reentrant_chain() {
    Authority a;
    a.define_object(1, 8);
    FrameId parent = a.enter();
    CHECK(a.acquire(parent, kObj1, Range(0, 8), BorrowMode::Exclusive));

    // The child is entered before the parent returns.
    FrameId child = a.enter(parent);
    CHECK(a.has_frame(child));
    CHECK(a.can_access(child, kObj1, Range(0, 8), AccessKind::Write).allowed);
    CHECK(a.acquire(child, kObj1, Range(0, 4), BorrowMode::Shared));

    // The child's shared lease narrows both itself and its parent.
    CHECK(a.can_access(child, kObj1, Range(0, 4), AccessKind::Read).allowed);
    CHECK(!a.can_access(child, kObj1, Range(0, 4), AccessKind::Write).allowed);
    CHECK(!a.can_access(parent, kObj1, Range(0, 4), AccessKind::Write).allowed);
    CHECK(a.can_access(parent, kObj1, Range(4, 8), AccessKind::Write).allowed);

    // Child returns before the parent.
    CHECK(a.leave(child) == Status::Ok);
    CHECK(a.can_access(parent, kObj1, Range(0, 4), AccessKind::Write).allowed);
    CHECK(a.leave(parent) == Status::Ok);
}

void test_shared_and_exclusive_chain() {
    Authority a;
    a.define_object(1, 8);
    FrameId parent = a.enter();
    CHECK(a.acquire(parent, kObj1, Range(0, 8), BorrowMode::Shared));

    FrameId child = a.enter(parent);
    CHECK(a.can_access(child, kObj1, Range(0, 8), AccessKind::Read).allowed);
    CHECK(!a.can_access(child, kObj1, Range(0, 8), AccessKind::Write).allowed);
    CHECK(a.can_borrow(child, kObj1, Range(0, 4), BorrowMode::Shared).allowed);
    CHECK(!a.can_borrow(child, kObj1, Range(0, 4), BorrowMode::Exclusive).allowed);
    CHECK(a.acquire(child, kObj1, Range(0, 4), BorrowMode::Shared));

    FrameId sibling = a.enter(parent);
    CHECK(a.can_access(sibling, kObj1, Range(0, 4), AccessKind::Read).allowed);
    CHECK(!a.can_access(sibling, kObj1, Range(0, 4), AccessKind::Write).allowed);
    CHECK(a.leave(parent) == Status::HasLiveChildren);

    CHECK(a.cancel(child) == Status::Ok);
    CHECK(a.leave(parent) == Status::HasLiveChildren);
    CHECK(a.cancel(sibling) == Status::Ok);
    CHECK(a.leave(parent) == Status::Ok);

    // The root may also take a lease; descendants inherit its restriction.
    Authority rooted;
    ObjectRef ref2 = rooted.define_object(2, 4);
    FrameId child2 = rooted.enter();
    CHECK(rooted.acquire(kRootFrame, ref2, Range(0, 4), BorrowMode::Shared));
    CHECK(rooted.can_access(child2, ref2, Range(0, 4), AccessKind::Read).allowed);
    CHECK(!rooted.can_access(child2, ref2, Range(0, 4), AccessKind::Write).allowed);
    CHECK(!rooted.can_access(kRootFrame, ref2, Range(0, 4), AccessKind::Write).allowed);
}

void test_partial_overlap_is_conservative() {
    Authority a;
    a.define_object(1, 8);
    FrameId parent = a.enter();
    FrameId child = a.enter(parent);
    CHECK(a.acquire(parent, kObj1, Range(0, 8), BorrowMode::Exclusive));
    CHECK(a.acquire(child, kObj1, Range(2, 6), BorrowMode::Shared));

    // A whole-range query that straddles the shared lease is conservatively
    // rejected, while ranges wholly outside it remain writable.
    CHECK(!a.can_access(child, kObj1, Range(0, 8), AccessKind::Write).allowed);
    CHECK(a.can_access(child, kObj1, Range(0, 2), AccessKind::Write).allowed);
    CHECK(!a.can_access(child, kObj1, Range(5, 8), AccessKind::Write).allowed);
    CHECK(a.can_access(child, kObj1, Range(6, 8), AccessKind::Write).allowed);
    CHECK(!a.can_borrow(child, kObj1, Range(0, 8), BorrowMode::Exclusive).allowed);
}

void test_suspend_resume_cancel() {
    Authority a;
    a.define_object(1, 8);
    FrameId parent = a.enter();
    CHECK(a.acquire(parent, kObj1, Range(0, 8), BorrowMode::Exclusive));

    FrameId left = a.enter(parent);
    CHECK(a.acquire(left, kObj1, Range(0, 4), BorrowMode::Shared));
    SuspendResult left_ticket = a.suspend(left);
    CHECK(left_ticket);
    CHECK(a.frame(left)->state == FrameState::Suspended);
    StackCertificate suspended_stack = a.stack_certificate();
    CHECK(suspended_stack.tickets.size() == 1);
    CHECK(suspended_stack.tickets[0].frame == left);
    CHECK(suspended_stack.tickets[0].valid);
    CHECK(suspended_stack.verify(a));
    auto listed_state = [&](FrameId id) {
        for (const FrameInfo& info : a.frames()) {
            if (info.id == id) return info.state;
        }
        return FrameState::Cancelled;
    };
    CHECK(listed_state(left) == FrameState::Suspended);

    FrameId right = a.enter(parent);
    CHECK(a.acquire(right, kObj1, Range(4, 8), BorrowMode::Shared));
    SuspendResult right_ticket = a.suspend(right);
    CHECK(right_ticket);

    FrameId observer = a.enter(parent);
    CHECK(!a.can_access(observer, kObj1, Range(0, 4), AccessKind::Write).allowed);
    CHECK(!a.can_access(observer, kObj1, Range(4, 8), AccessKind::Write).allowed);
    CHECK(a.can_access(observer, kObj1, Range(0, 8), AccessKind::Read).allowed);

    // Cancelling one suspended sibling does not revoke the other ticket.
    CHECK(a.cancel(left) == Status::Ok);
    CHECK(!a.ticket_valid(left_ticket.ticket));
    CHECK(!a.stack_certificate().tickets[0].valid);
    CHECK(a.ticket_valid(right_ticket.ticket));
    ResumeResult resumed = a.resume(right_ticket.ticket);
    CHECK(resumed);
    CHECK(a.frame(right)->state == FrameState::Active);
    CHECK(listed_state(right) == FrameState::Active);
    CHECK(!a.can_access(observer, kObj1, Range(4, 8), AccessKind::Write).allowed);
    CHECK(a.can_access(observer, kObj1, Range(0, 4), AccessKind::Write).allowed);
    CHECK(a.resume(right_ticket.ticket).status == Status::TicketUsed);

    // Cancellation removes the frame and its actual descendants, but not the
    // parent or unrelated siblings.
    FrameId doomed = a.enter(parent);
    FrameId descendant = a.enter(doomed);
    CHECK(a.acquire(doomed, kObj1, Range(0, 2), BorrowMode::Exclusive));
    CHECK(a.acquire(descendant, kObj1, Range(0, 1), BorrowMode::Shared));
    CHECK(a.cancel(doomed) == Status::Ok);
    CHECK(a.frame(doomed)->state == FrameState::Cancelled);
    CHECK(a.frame(descendant)->state == FrameState::Cancelled);
    CHECK(a.can_access(observer, kObj1, Range(0, 2), AccessKind::Write).allowed);

    // A suspended frame cannot execute a return query.
    FrameId paused = a.enter(parent);
    CHECK(a.acquire(paused, kObj1, Range(2, 3), BorrowMode::Shared));
    SuspendResult paused_ticket = a.suspend(paused);
    CHECK(paused_ticket);
    CHECK(a.evaluate(paused, kObj1, Range(2, 3), QueryKind::Return).decision ==
          Decision::Forbid);

    // Cancelling an ancestor does not rewrite an already returned child.
    FrameId returned_child = a.enter(parent);
    CHECK(a.leave(returned_child) == Status::Ok);
    CHECK(a.cancel(parent) == Status::Ok);
    CHECK(a.frame(returned_child)->state == FrameState::Returned);
}

void test_branch_tickets() {
    Authority a;
    a.define_object(1, 8);
    FrameId parent = a.enter();

    // A same-branch continuation chain restores in caller-to-callee order.
    FrameId chain_parent = a.enter(parent);
    FrameId chain_child = a.enter(chain_parent);
    SuspendResult chain_child_ticket = a.suspend(chain_child);
    SuspendResult chain_parent_ticket = a.suspend(chain_parent);
    CHECK(chain_child_ticket);
    CHECK(chain_parent_ticket);
    std::vector<FrameId> resumed;
    CHECK(a.resume_chain({chain_parent_ticket.ticket, chain_child_ticket.ticket},
                         &resumed) == Status::Ok);
    CHECK((resumed == std::vector<FrameId>{chain_parent, chain_child}));

    // A ticket from branch 77 and a ticket from branch 79 cannot be spliced
    // into one restored call chain.
    FrameId branch_a = a.enter_branch(parent, 77);
    FrameId branch_b = a.enter_branch(branch_a, 79);
    SuspendResult b_ticket = a.suspend(branch_b);
    SuspendResult a_ticket = a.suspend(branch_a);
    CHECK(a_ticket);
    CHECK(b_ticket);
    CHECK(a.resume_chain({a_ticket.ticket, b_ticket.ticket}) ==
          Status::BranchMismatch);
    CHECK(a.ticket_valid(a_ticket.ticket));
    CHECK(a.ticket_valid(b_ticket.ticket));

    // Nor can a ticket be transplanted onto another frame.
    CHECK(a.resume_as(a_ticket.ticket, branch_b).status ==
          Status::BranchMismatch);

    // Restoring one branch's own lineage is still allowed.
    CHECK(a.resume_chain({a_ticket.ticket}) == Status::Ok);
    CHECK(a.ticket_valid(b_ticket.ticket));
    ResumeResult child_resume = a.resume(b_ticket.ticket);
    CHECK(child_resume);
}

void test_move_and_partial_release() {
    Authority a;
    ObjectRef ref = a.define_object(1, 8);
    FrameId owner = a.enter();
    FrameId observer = a.enter();
    CHECK(a.acquire(owner, ref, Range(0, 8), BorrowMode::Exclusive));
    CHECK(!a.can_access(observer, ref, Range(0, 4), AccessKind::Write).allowed);

    CHECK(a.release(owner, ref, Range(4, 8)) == Status::Ok);
    CHECK(!a.can_access(observer, ref, Range(0, 4), AccessKind::Write).allowed);
    CHECK(a.can_access(observer, ref, Range(4, 8), AccessKind::Write).allowed);

    QueryReport half = a.evaluate(owner, ref, Range(0, 8), QueryKind::Return);
    CHECK(half.decision == Decision::Undecided);
    CHECK((half.allowed == IntervalSet::from(Range(0, 4))));
    CHECK((half.forbidden == IntervalSet::from(Range(4, 8))));

    auto moved = a.move_object(1);
    CHECK(moved.has_value());
    CHECK(moved->generation == 1);
    CHECK(!a.is_current(ref));
    CHECK(a.can_access(owner, ref, Range(0, 4), AccessKind::Read).status ==
          Status::StaleGeneration);
    CHECK(a.can_access(owner, *moved, Range(0, 4), AccessKind::Write).allowed);

    // A release may cut a hole through the middle of a lease.
    ObjectRef ref2 = a.define_object(2, 16);
    FrameId cutter = a.enter();
    CHECK(a.acquire(cutter, ref2, Range(0, 8), BorrowMode::Exclusive));
    CHECK(a.release(cutter, ref2, Range(3, 5)) == Status::Ok);
    CHECK((a.borrowed_intervals(cutter, ref2) ==
           IntervalSet::from(Range(0, 3))
               .union_with(IntervalSet::from(Range(5, 8)))));
}

void test_certificates() {
    Authority a;
    ObjectRef ref = a.define_object(1, 8);
    FrameId owner = a.enter();
    FrameId child = a.enter(owner);
    CHECK(a.acquire(owner, ref, Range(0, 8), BorrowMode::Exclusive));
    CHECK(a.acquire(child, ref, Range(2, 6), BorrowMode::Shared));

    QueryCertificate cert =
        a.certificate(owner, ref, Range(0, 8), QueryKind::Write);
    CHECK(cert.verify(a));
    CHECK(cert.decision == Decision::Undecided);
    CHECK((cert.allowed == IntervalSet::from(Range(0, 2))
                              .union_with(IntervalSet::from(Range(6, 8)))));
    CHECK((cert.forbidden == IntervalSet::from(Range(2, 6))));
    const std::string json = cert.to_json();
    CHECK(json.find("\"decision\":\"undecided\"") != std::string::npos);
    CHECK(json.find("\"allowed\":[[0,2],[6,8]]") != std::string::npos);
    CHECK(json.find("\"stack\":") != std::string::npos);

    QueryCertificate tampered = cert;
    tampered.decision = Decision::Allow;
    CHECK(!tampered.verify(a));

    StackCertificate stack = a.stack_certificate();
    CHECK(stack.verify(a));
    StackCertificate bad_stack = stack;
    bad_stack.digest ^= 1;
    CHECK(!bad_stack.verify(a));
}

void test_history_allow_forbid() {
    HistoryProblem p;
    p.add_object(1, 8, 0);
    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
        Event::enter(kRootFrame, 11),
    };
    p.add_segment(s0);

    HistoryCertificate owner =
        p.solve(10, 1, Range(0, 8), QueryKind::Write);
    CHECK(owner.decision == Decision::Allow);
    CHECK(owner.compatible_histories == 1);
    CHECK(owner.verify(p));

    HistoryCertificate sibling =
        p.solve(11, 1, Range(0, 8), QueryKind::Write);
    CHECK(sibling.decision == Decision::Forbid);
    CHECK(sibling.verify(p));
}

void test_history_gap_refinement() {
    HistoryProblem p = gap_problem();

    HistoryCertificate before = p.solve(write_query(12, Range(0, 4)));
    CHECK(before.decision == Decision::Undecided);
    CHECK(before.compatible_histories == 2);
    CHECK(!before.inconsistent);
    CHECK(!before.truncated);
    CHECK((before.undecided == IntervalSet::from(Range(0, 4))));
    CHECK(before.verify(p));

    // Every witness has exactly one summary choice, so both gaps sharing the
    // summary are interpreted the same way.
    for (const HistoryWitness& w : before.witnesses) {
        if (!w.valid) continue;
        CHECK(w.choices.size() == 1);
        CHECK(w.choices[0].summary == "same-gap");
        CHECK(w.choices[0].category == "noop" ||
              w.choices[0].category == "shared");
    }

    p.fix_summary("same-gap", "shared");
    HistoryCertificate shared = p.solve(write_query(12, Range(0, 4)));
    CHECK(shared.decision == Decision::Forbid);
    CHECK(shared.compatible_histories == 1);
    CHECK(shared.verify(p));

    p.fix_summary("same-gap", "noop");
    HistoryCertificate noop = p.solve(write_query(12, Range(0, 4)));
    CHECK(noop.decision == Decision::Allow);
    CHECK(noop.compatible_histories == 1);
    CHECK(noop.verify(p));
    const std::string json = noop.to_json();
    CHECK(json.find("\"decision\":\"allow\"") != std::string::npos);
    CHECK(json.find("\"common_allowed\":[[0,4]]") != std::string::npos);
    CHECK(json.find("\"choices\":[{\"summary\":\"same-gap\"") !=
          std::string::npos);

    HistoryCertificate bad = noop;
    bad.decision = Decision::Forbid;
    CHECK(!bad.verify(p));

    HistoryProblem limited = gap_problem();
    limited.options().max_interpretations = 1;
    HistoryCertificate truncated = limited.solve(write_query(12, Range(0, 4)));
    CHECK(truncated.truncated);
    CHECK(truncated.decision == Decision::Undecided);
    CHECK(truncated.verify(limited));

    HistoryProblem filled = gap_problem();
    CHECK(filled.fill_gap(2, {Event::shared(11, 1, Range(0, 4))}));
    HistoryCertificate direct = filled.solve(write_query(12, Range(0, 4)));
    CHECK(direct.decision == Decision::Forbid);
    CHECK(direct.verify(filled));
}

void test_history_same_summary_consistency() {
    HistoryProblem p;
    p.add_object(1, 8, 0);
    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
        Event::enter(10, 11),
        Event::enter(10, 12),
        Event::enter(10, 13),
    };
    p.add_segment(s0);

    Segment g1;
    g1.id = 2;
    g1.summary = "shared-summary";
    g1.frame = 11;
    g1.object = 1;
    g1.range = Range(0, 4);
    g1.categories = {"noop", "shared"};
    p.add_gap(g1);

    Segment g2;
    g2.id = 3;
    g2.summary = "shared-summary";
    g2.frame = 12;
    g2.object = 1;
    g2.range = Range(0, 4);
    g2.categories = {"noop", "shared"};
    p.add_gap(g2);

    HistoryCertificate c = p.solve(write_query(13, Range(0, 4)));
    CHECK(c.decision == Decision::Undecided);
    CHECK(c.compatible_histories == 2);
    CHECK(c.verify(p));

    // A third gap with the same summary narrows the shared interpretation to
    // shared, making the decision definite.
    Segment g3;
    g3.id = 4;
    g3.summary = "shared-summary";
    g3.frame = 11;
    g3.object = 1;
    g3.range = Range(0, 4);
    g3.categories = {"shared"};
    p.add_gap(g3);

    HistoryCertificate narrowed = p.solve(write_query(13, Range(0, 4)));
    CHECK(narrowed.decision == Decision::Forbid);
    CHECK(narrowed.compatible_histories == 1);
    CHECK(narrowed.verify(p));
}

void test_history_move_and_stale_generation() {
    HistoryProblem p;
    p.add_object(1, 8, 0);
    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
    };
    p.add_segment(s0);

    Segment gap;
    gap.id = 2;
    gap.summary = "move-or-not";
    gap.frame = 10;
    gap.object = 1;
    gap.range = Range(0, 8);
    gap.categories = {"noop", "move"};
    p.add_gap(gap);

    HistoryQuery old_gen;
    old_gen.frame = 10;
    old_gen.object = 1;
    old_gen.generation = 0;
    old_gen.range = Range(0, 8);
    old_gen.kind = QueryKind::Read;

    HistoryCertificate undecided = p.solve(old_gen);
    CHECK(undecided.decision == Decision::Undecided);
    CHECK(undecided.compatible_histories == 2);
    CHECK(undecided.verify(p));

    p.fix_summary("move-or-not", "move");
    HistoryCertificate moved = p.solve(old_gen);
    CHECK(moved.decision == Decision::Forbid);
    CHECK(moved.verify(p));

    p.fix_summary("move-or-not", "noop");
    HistoryCertificate unmoved = p.solve(old_gen);
    CHECK(unmoved.decision == Decision::Allow);
    CHECK(unmoved.verify(p));
}

void test_gap_generation_context() {
    auto base = []() {
        HistoryProblem p;
        p.add_object(1, 8, 0);
        Segment s0;
        s0.id = 1;
        s0.events = {
            Event::enter(kRootFrame, 10),
            Event::exclusive(10, 1, Range(0, 8)),
            Event::move(10, 1, Range(0, 8)),
        };
        p.add_segment(s0);
        return p;
    };

    HistoryProblem fresh = base();
    Segment gap;
    gap.id = 2;
    gap.summary = "versioned-gap";
    gap.frame = 10;
    gap.object = 1;
    gap.generation = 1;
    gap.range = Range(0, 4);
    gap.categories = {"read"};
    fresh.add_gap(gap);

    HistoryQuery new_gen;
    new_gen.frame = 10;
    new_gen.object = 1;
    new_gen.generation = 1;
    new_gen.range = Range(0, 4);
    new_gen.kind = QueryKind::Read;
    HistoryCertificate good = fresh.solve(new_gen);
    CHECK(good.decision == Decision::Allow);
    CHECK(good.verify(fresh));

    HistoryProblem stale = base();
    Segment stale_gap = gap;
    stale_gap.generation = 0;  // the gap names a generation that has moved
    stale.add_gap(stale_gap);
    HistoryCertificate bad = stale.solve(new_gen);
    CHECK(bad.inconsistent);
    CHECK(bad.verify(stale));
}

void test_history_versions_and_stack_digest() {
    {
        HistoryProblem p;
        p.add_object(1, 8, 0);
        Segment s0;
        s0.id = 1;
        s0.events = {
            Event::enter(kRootFrame, 10),
            Event::exclusive(10, 1, Range(0, 4)),
        };
        s0.objects = {ObjectVersion{1, 1}};  // actual generation is still 0
        p.add_segment(s0);

        HistoryCertificate c = p.solve(10, 1, Range(0, 4), QueryKind::Read);
        CHECK(c.inconsistent);
        CHECK(c.compatible_histories == 0);
        CHECK(c.verify(p));
    }

    {
        Authority replay;
        replay.define_object(1, 8, 0);
        FrameId id = kNoFrame;
        CHECK(replay.enter_into(&id, kRootFrame, 10, std::nullopt) == Status::Ok);
        CHECK(replay.acquire(10, kObj1, Range(0, 4), BorrowMode::Exclusive));
        const std::string digest = replay.stack_certificate().summary;

        HistoryProblem p;
        p.add_object(1, 8, 0);
        Segment s0;
        s0.id = 1;
        s0.events = {
            Event::enter(kRootFrame, 10),
            Event::exclusive(10, 1, Range(0, 4)),
        };
        s0.objects = {ObjectVersion{1, 0}};
        s0.expected_stack_digest = digest;
        p.add_segment(s0);

        HistoryCertificate good =
            p.solve(10, 1, Range(0, 4), QueryKind::Read);
        CHECK(!good.inconsistent);
        CHECK(good.decision == Decision::Allow);
        CHECK(good.verify(p));

        // There is no mutable segment accessor, so build the mismatch as a
        // fresh problem with the same events and a deliberately wrong digest.
        HistoryProblem bad;
        bad.add_object(1, 8, 0);
        Segment bs;
        bs.id = 1;
        bs.events = s0.events;
        bs.objects = {ObjectVersion{1, 0}};
        bs.expected_stack_digest = "0000000000000000";
        bad.add_segment(bs);
        HistoryCertificate mismatch =
            bad.solve(10, 1, Range(0, 4), QueryKind::Read);
        CHECK(mismatch.inconsistent);
        CHECK(mismatch.verify(bad));
    }
}

void test_history_partial_return() {
    HistoryProblem p;
    p.add_object(1, 8, 0);
    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
    };
    p.add_segment(s0);

    HistoryCertificate full =
        p.solve(10, 1, Range(0, 8), QueryKind::Return);
    CHECK(full.decision == Decision::Allow);

    Segment release;
    release.id = 2;
    release.events = {Event::release(10, 1, Range(0, 4))};
    p.add_segment(release);

    HistoryCertificate half =
        p.solve(10, 1, Range(0, 8), QueryKind::Return);
    CHECK(half.decision == Decision::Undecided);
    CHECK((half.common_allowed == IntervalSet::from(Range(4, 8))));
    CHECK((half.common_forbidden == IntervalSet::from(Range(0, 4))));
    CHECK(half.verify(p));
}

void test_history_query_after_segment() {
    HistoryProblem p;
    p.add_object(1, 8, 0);

    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
    };
    p.add_segment(s0);

    Segment s1;
    s1.id = 2;
    s1.events = {Event::cancel(10)};
    p.add_segment(s1);

    HistoryQuery at_setup;
    at_setup.frame = 10;
    at_setup.object = 1;
    at_setup.range = Range(0, 8);
    at_setup.kind = QueryKind::Read;
    at_setup.after_segment = 1;
    HistoryCertificate before_cancel = p.solve(at_setup);
    CHECK(before_cancel.decision == Decision::Allow);
    CHECK(before_cancel.verify(p));

    at_setup.after_segment = 2;
    HistoryCertificate after_cancel = p.solve(at_setup);
    CHECK(after_cancel.decision == Decision::Forbid);
    CHECK(after_cancel.verify(p));

    at_setup.after_segment = 99;
    HistoryCertificate missing_point = p.solve(at_setup);
    CHECK(missing_point.inconsistent);
    CHECK(missing_point.verify(p));

    // Gaps after the query point do not affect a point-in-time query.
    Segment future_gap;
    future_gap.id = 3;
    future_gap.summary = "future";
    future_gap.frame = 11;
    future_gap.object = 1;
    future_gap.range = Range(0, 4);
    future_gap.categories = {"noop", "shared"};
    p.add_gap(future_gap);
    at_setup.after_segment = 1;
    HistoryCertificate before_future = p.solve(at_setup);
    CHECK(before_future.decision == Decision::Allow);
    CHECK(before_future.interpretations == 1);
    CHECK(before_future.verify(p));
}

void test_byte_reentry() {
    Authority a;
    ByteMemory memory(a);
    ObjectRef ref = memory.create(1, 8);
    FrameId parent = a.enter();
    CHECK(a.acquire(parent, ref, Range(0, 8), BorrowMode::Exclusive));
    std::vector<std::uint8_t> bytes = {10, 11, 12, 13, 14, 15, 16, 17};
    CHECK(memory.write(parent, ref, Range(0, 8), bytes));

    FrameId child = a.enter(parent);
    ByteSpanResult inherited = memory.read(child, ref, Range(0, 8));
    CHECK(inherited);
    CHECK((inherited.bytes == bytes));

    CHECK(a.acquire(child, ref, Range(0, 4), BorrowMode::Shared));
    CHECK(!memory.write_byte(child, ref, 0, 99));
    CHECK(!memory.write_byte(parent, ref, 0, 99));
    CHECK(memory.write_byte(parent, ref, 7, 99));
    CHECK(memory.read_byte(child, ref, 7).value == 99);

    CHECK(a.leave(child) == Status::Ok);
    CHECK(memory.write_byte(parent, ref, 0, 77));
}

void test_byte_move_stale_generation() {
    Authority a;
    ByteMemory memory(a);
    ObjectRef old_ref = memory.create(1, 8);
    FrameId owner = a.enter();
    CHECK(a.acquire(owner, old_ref, Range(0, 8), BorrowMode::Exclusive));
    std::vector<std::uint8_t> bytes = {1, 2, 3, 4, 5, 6, 7, 8};
    CHECK(memory.write(owner, old_ref, Range(0, 8), bytes));

    FrameId child = a.enter(owner);
    CHECK(a.acquire(child, old_ref, Range(0, 4), BorrowMode::Shared));
    ByteMoveResult blocked = memory.move(owner, old_ref);
    CHECK(!blocked);
    CHECK(blocked.status == Status::Conflict);

    CHECK(a.cancel(child) == Status::Ok);
    ByteMoveResult moved = memory.move(owner, old_ref);
    CHECK(moved);
    CHECK(moved.ref.generation == 1);
    CHECK(memory.read_byte(owner, old_ref, 0).status ==
          Status::StaleGeneration);
    ByteSpanResult fresh = memory.read(owner, moved.ref, Range(0, 8));
    CHECK(fresh);
    CHECK((fresh.bytes == bytes));
}

void test_byte_sibling_cancel() {
    Authority a;
    ByteMemory memory(a);
    ObjectRef ref = memory.create(1, 8);
    FrameId parent = a.enter();
    CHECK(a.acquire(parent, ref, Range(0, 8), BorrowMode::Exclusive));
    std::vector<std::uint8_t> bytes = {0, 0, 0, 0, 0, 0, 0, 0};
    CHECK(memory.write(parent, ref, Range(0, 8), bytes));

    FrameId left = a.enter(parent);
    FrameId right = a.enter(parent);
    CHECK(a.acquire(left, ref, Range(0, 4), BorrowMode::Shared));
    CHECK(a.acquire(right, ref, Range(4, 8), BorrowMode::Shared));

    CHECK(memory.read_byte(left, ref, 0));
    CHECK(memory.read_byte(right, ref, 4));
    CHECK(a.cancel(left) == Status::Ok);
    CHECK(memory.read_byte(left, ref, 0).status == Status::Cancelled);
    CHECK(memory.read_byte(right, ref, 4));

    CHECK(memory.write_byte(parent, ref, 0, 42));
    CHECK(!memory.write_byte(parent, ref, 4, 43));
    CHECK(a.cancel(right) == Status::Ok);
    CHECK(memory.write_byte(parent, ref, 4, 43));
}

void test_byte_half_return() {
    Authority a;
    ByteMemory memory(a);
    ObjectRef ref = memory.create(1, 8);
    FrameId owner = a.enter();
    FrameId observer = a.enter();
    CHECK(a.acquire(owner, ref, Range(0, 8), BorrowMode::Exclusive));
    std::vector<std::uint8_t> bytes = {1, 2, 3, 4, 5, 6, 7, 8};
    CHECK(memory.write(owner, ref, Range(0, 8), bytes));

    CHECK(a.release(owner, ref, Range(4, 8)) == Status::Ok);
    CHECK((memory.accessible_bytes(observer, ref, AccessKind::Write, Range(0, 8)) ==
           IntervalSet::from(Range(4, 8))));
    CHECK(memory.write_byte(observer, ref, 4, 40));
    CHECK(!memory.write_byte(observer, ref, 0, 10));
    CHECK(memory.first_forbidden_byte(observer, ref, AccessKind::Write,
                                      Range(0, 8)) == 0);

    QueryReport half = a.evaluate(owner, ref, Range(0, 8), QueryKind::Return);
    CHECK(half.decision == Decision::Undecided);
    CHECK((half.allowed == IntervalSet::from(Range(0, 4))));

    CHECK(a.release(owner, ref, Range(0, 4)) == Status::Ok);
    CHECK(memory.write_byte(observer, ref, 0, 10));
}

void test_byte_gap_refinement() {
    HistoryProblem p = gap_problem();

    HistoryCertificate before = p.solve(write_query(12, Range(0, 4)));
    CHECK(before.decision == Decision::Undecided);

    const HistoryWitness* allow_witness = nullptr;
    const HistoryWitness* forbid_witness = nullptr;
    for (const HistoryWitness& w : before.witnesses) {
        if (!w.valid) continue;
        if (w.outcome == Decision::Allow) allow_witness = &w;
        if (w.outcome == Decision::Forbid) forbid_witness = &w;
    }
    CHECK(allow_witness != nullptr);
    CHECK(forbid_witness != nullptr);

    auto check_materialized = [](const HistoryWitness& witness,
                                 bool expected_allowed) {
        HistoryProblem p2 = gap_problem();
        MaterializeResult state = p2.materialize(witness.choices);
        CHECK(state);
        Authority authority = state.authority;
        ByteMemory memory(authority);
        ObjectRef ref = memory.create(1, 8);
        std::vector<std::uint8_t> bytes = {9, 9, 9, 9};
        ByteWriteResult write = memory.write(12, ref, Range(0, 4), bytes);
        CHECK(write.status == (expected_allowed ? Status::Ok : Status::Conflict));
    };

    if (allow_witness != nullptr) check_materialized(*allow_witness, true);
    if (forbid_witness != nullptr) check_materialized(*forbid_witness, false);

    p.fix_summary("same-gap", "noop");
    CHECK(p.solve(write_query(12, Range(0, 4))).decision == Decision::Allow);
    p.fix_summary("same-gap", "shared");
    CHECK(p.solve(write_query(12, Range(0, 4))).decision == Decision::Forbid);
}

void test_history_no_gaps_and_certificate_digest() {
    HistoryProblem p;
    p.add_object(1, 4, 0);
    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::shared(10, 1, Range(0, 4)),
    };
    p.add_segment(s0);

    HistoryCertificate c = p.solve(10, 1, Range(0, 4), QueryKind::Read);
    CHECK(c.decision == Decision::Allow);
    CHECK(c.interpretations == 1);
    CHECK(c.compatible_histories == 1);
    CHECK(c.verify(p));

    HistoryCertificate tampered = c;
    tampered.common_allowed.add(Range(0, 4));
    tampered.common_forbidden.add(Range(0, 4));
    CHECK(!tampered.verify(p));
}

void test_custom_category_and_gap_context() {
    HistoryProblem p;
    p.add_object(1, 8, 0);
    p.register_category(Category{
        "read-then-write",
        "custom host category",
        [](const Segment& s) {
            return std::vector<Event>{
                Event::read(s.frame, s.object, s.range),
                Event::write(s.frame, s.object, s.range),
            };
        }});

    Segment s0;
    s0.id = 1;
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
    };
    p.add_segment(s0);

    Segment gap;
    gap.id = 2;
    gap.summary = "custom";
    gap.frame = 10;
    gap.object = 1;
    gap.range = Range(0, 4);
    gap.categories = {"read-then-write", "noop"};
    p.add_gap(gap);

    HistoryCertificate c = p.solve(10, 1, Range(0, 8), QueryKind::Write);
    CHECK(c.decision == Decision::Allow);
    CHECK(c.verify(p));

    p.fix_summary("custom", "read-then-write");
    CHECK(p.solve(10, 1, Range(0, 8), QueryKind::Write).decision ==
          Decision::Allow);
}

}  // namespace

int main() {
    test_intervals();
    test_sequential_return();
    test_reentrant_chain();
    test_shared_and_exclusive_chain();
    test_partial_overlap_is_conservative();
    test_suspend_resume_cancel();
    test_branch_tickets();
    test_move_and_partial_release();
    test_certificates();
    test_history_allow_forbid();
    test_history_gap_refinement();
    test_history_same_summary_consistency();
    test_history_move_and_stale_generation();
    test_gap_generation_context();
    test_history_versions_and_stack_digest();
    test_history_partial_return();
    test_history_query_after_segment();
    test_byte_reentry();
    test_byte_move_stale_generation();
    test_byte_sibling_cancel();
    test_byte_half_return();
    test_byte_gap_refinement();
    test_history_no_gaps_and_certificate_digest();
    test_custom_category_and_gap_context();

    if (g_failures == 0) {
        std::cout << "borrow tests: " << g_checks << " checks passed\n";
        return 0;
    }
    std::cerr << "borrow tests: " << g_failures << " of " << g_checks
              << " checks failed\n";
    return 1;
}

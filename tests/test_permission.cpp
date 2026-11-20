#include "index_reclaim/permission.hpp"
#include "index_reclaim/reference.hpp"

#include <algorithm>
#include <array>
#include <atomic>
#include <cstdint>
#include <iostream>
#include <limits>
#include <sstream>
#include <string>
#include <thread>
#include <vector>

namespace {

using index_reclaim::Authority;
using index_reclaim::BatchId;
using index_reclaim::ClaimResult;
using index_reclaim::ObjectId;
using index_reclaim::ReaderStatus;
using index_reclaim::ReferenceDecision;
using index_reclaim::ScanSession;
using index_reclaim::ThreadToken;

int g_failures = 0;

#define REQUIRE(condition)                                                   \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ":" << __LINE__                         \
                      << ": requirement failed: " #condition << '\n';        \
            ++g_failures;                                                    \
            return false;                                                    \
        }                                                                    \
    } while (false)

template <typename T>
void sort_unique(std::vector<T>& values) {
    std::sort(values.begin(), values.end());
    values.erase(std::unique(values.begin(), values.end()), values.end());
}

template <typename T>
bool same_set(std::vector<T> lhs, std::vector<T> rhs) {
    sort_unique(lhs);
    sort_unique(rhs);
    return lhs == rhs;
}

bool contains_id(const std::vector<BatchId>& ids, BatchId id) {
    return std::find(ids.begin(), ids.end(), id) != ids.end();
}

bool contains_object(const std::vector<ObjectId>& objects,
                     const ObjectId& object) {
    return std::find(objects.begin(), objects.end(), object) != objects.end();
}

struct ClaimSummary {
    std::vector<BatchId> permitted_batches;
    std::vector<BatchId> deferred_batches;
    std::vector<BatchId> protected_batches;
    std::vector<BatchId> already_issued_batches;
    std::vector<ObjectId> permitted;
    std::vector<ObjectId> deferred;
    std::vector<std::size_t> first_invalidated_shards;
    bool incomplete = false;
    int rounds = 0;
};

template <typename T>
void append_values(std::vector<T>& destination,
                   const std::vector<T>& source) {
    destination.insert(destination.end(), source.begin(), source.end());
}

void append_objects(std::vector<ObjectId>& destination,
                    const std::vector<ObjectId>& source) {
    destination.insert(destination.end(), source.begin(), source.end());
}

ClaimSummary claim_with_rescan(Authority& authority,
                               ScanSession session) {
    ClaimSummary summary;

    for (int round = 0; round < 16; ++round) {
        ++summary.rounds;
        ClaimResult result = authority.claim(session);

        if (round == 0) {
            summary.first_invalidated_shards =
                result.invalidated_shards;
        }

        append_values(summary.permitted_batches,
                      result.permitted_batches);
        append_objects(summary.permitted, result.permitted);
        append_values(summary.already_issued_batches,
                      result.already_issued_batches);

        if (!result.needs_rescan()) {
            summary.deferred_batches = result.deferred_batches;
            summary.deferred = result.deferred;
            summary.protected_batches = result.protected_batches;
            sort_unique(summary.permitted_batches);
            return summary;
        }

        std::vector<std::size_t> to_rescan;
        append_values(to_rescan, result.invalidated_shards);
        append_values(to_rescan, result.stale_shards);
        append_values(to_rescan, result.missing_shards);
        sort_unique(to_rescan);

        if (to_rescan.empty()) {
            summary.incomplete = true;
            return summary;
        }

        for (std::size_t shard : to_rescan) {
            if (!authority.scan_shard(session, shard)) {
                summary.incomplete = true;
                return summary;
            }
        }
    }

    summary.incomplete = true;
    return summary;
}

ClaimSummary drain_all(Authority& authority) {
    ScanSession session = authority.begin_scan();
    for (std::size_t shard = 0; shard < authority.shard_count(); ++shard) {
        if (!authority.scan_shard(session, shard)) {
            ClaimSummary summary;
            summary.incomplete = true;
            return summary;
        }
    }
    return claim_with_rescan(authority, session);
}

bool compare_decision(const ClaimSummary& actual,
                      const ReferenceDecision& expected,
                      const std::string& context) {
    bool ok = !actual.incomplete &&
              same_set(actual.permitted_batches,
                       expected.eligible_batches) &&
              same_set(actual.deferred_batches,
                       expected.deferred_batches) &&
              same_set(actual.permitted, expected.eligible) &&
              same_set(actual.deferred, expected.deferred);
    if (!ok) {
        std::cerr << "reference mismatch: " << context << '\n';
        std::cerr << "  actual permits:";
        for (BatchId id : actual.permitted_batches) {
            std::cerr << ' ' << id;
        }
        std::cerr << "\n  expected permits:";
        for (BatchId id : expected.eligible_batches) {
            std::cerr << ' ' << id;
        }
        std::cerr << "\n  actual deferrals:";
        for (BatchId id : actual.deferred_batches) {
            std::cerr << ' ' << id;
        }
        std::cerr << "\n  expected deferrals:";
        for (BatchId id : expected.deferred_batches) {
            std::cerr << ' ' << id;
        }
        std::cerr << '\n';
        ++g_failures;
    }
    return ok;
}

bool scan_all(Authority& authority, ScanSession& session) {
    for (std::size_t shard = 0; shard < authority.shard_count(); ++shard) {
        if (!authority.scan_shard(session, shard)) {
            return false;
        }
    }
    return true;
}

bool test_generation_nonwrapping() {
    index_reclaim::GenerationSequence sequence(
        std::numeric_limits<index_reclaim::Generation>::max() - 1U);

    REQUIRE(sequence.next() ==
            std::numeric_limits<index_reclaim::Generation>::max() - 1U);

    bool threw = false;
    try {
        (void)sequence.next();
    } catch (const std::overflow_error&) {
        threw = true;
    }
    REQUIRE(threw);

    threw = false;
    try {
        (void)index_reclaim::GenerationSequence::checked_next(
            std::numeric_limits<index_reclaim::Generation>::max());
    } catch (const std::overflow_error&) {
        threw = true;
    }
    REQUIRE(threw);
    return true;
}

bool test_same_address_generation_change() {
    Authority authority(4, 2);
    const ObjectId generation1{0x1000, 1};
    const ObjectId generation2{0x1000, 2};

    const auto root1 = authority.publish_root(generation1);
    const ThreadToken reader = authority.register_thread();

    const auto attempt = authority.begin_read(reader);
    REQUIRE(attempt.published());
    auto guard = authority.finish_read(attempt);
    REQUIRE(guard.valid());
    REQUIRE(guard.object() == generation1);

    // Same address, new generation.  The old reader keeps protecting
    // generation 1, which must not block generation 2.
    const auto root2 = authority.publish_root(generation2);
    const BatchId old_batch =
        authority.retire(generation1, root2.publication);
    REQUIRE(root2.publication > root1.publication);

    const auto root3 = authority.publish_root({0x1000, 3});
    const BatchId new_batch =
        authority.retire(generation2, root3.publication);

    const ReferenceDecision before = index_reclaim::reference_rescan_all(authority);
    const ClaimSummary first = drain_all(authority);
    REQUIRE(compare_decision(first, before, "same-address generations"));
    REQUIRE(contains_id(first.permitted_batches, new_batch));
    REQUIRE(contains_id(first.deferred_batches, old_batch));
    REQUIRE(contains_id(first.protected_batches, old_batch));
    REQUIRE(contains_object(first.permitted, generation2));
    REQUIRE(!contains_object(first.permitted, generation1));

    guard.release();
    REQUIRE(!guard.valid());

    const ReferenceDecision after_release =
        index_reclaim::reference_rescan_all(authority);
    const ClaimSummary second = drain_all(authority);
    REQUIRE(compare_decision(second, after_release,
                             "same-address generations after release"));
    REQUIRE(contains_id(second.permitted_batches, old_batch));
    REQUIRE(contains_object(second.permitted, generation1));
    return true;
}

bool test_unpublished_reader_does_not_block() {
    Authority authority(3, 2);
    const ObjectId old_object{0x2000, 10};
    const ObjectId new_object{0x2000, 11};

    (void)authority.publish_root(old_object);
    const ThreadToken reader = authority.register_thread();

    // This is the critical interleaving: the reader has observed the old root
    // but has not published any hazard yet.
    const auto first_read = authority.read_root();
    REQUIRE(first_read.object == old_object);

    const auto new_root = authority.publish_root(new_object);
    const BatchId batch =
        authority.retire(old_object, new_root.publication);

    const ReferenceDecision reference =
        index_reclaim::reference_rescan_all(authority);
    const ClaimSummary decision = drain_all(authority);
    REQUIRE(compare_decision(decision, reference,
                             "unpublished reader"));
    REQUIRE(contains_id(decision.permitted_batches, batch));
    REQUIRE(contains_object(decision.permitted, old_object));

    // Publishing later is harmless: the mandated root reread sees the new
    // generation and the slot is removed without dereferencing the old object.
    const auto late_attempt =
        authority.publish_protection(reader, first_read);
    REQUIRE(late_attempt.published());
    auto late_guard = authority.finish_read(late_attempt);
    REQUIRE(!late_guard.valid());
    REQUIRE(late_guard.status() == ReaderStatus::RootChanged);

    const auto snapshot = authority.snapshot_for_reference();
    REQUIRE(snapshot.live_threads.size() == 1);
    REQUIRE(!snapshot.live_threads.front().occupied);
    return true;
}

bool test_guard_tracks_slot_overwrite() {
    Authority authority(2, 1);
    const ObjectId object{0x2800, 1};
    (void)authority.publish_root(object);
    const ThreadToken reader = authority.register_thread();

    auto first = authority.finish_read(authority.begin_read(reader));
    REQUIRE(first.valid());
    REQUIRE(first.object() == object);

    auto second = authority.finish_read(authority.begin_read(reader));
    REQUIRE(second.valid());
    REQUIRE(!first.valid());

    first.release();
    REQUIRE(second.valid());
    second.release();
    REQUIRE(!second.valid());
    return true;
}

bool test_registration_invalidates_only_affected_shard() {
    // Six slots, three shards, two slots per shard.
    Authority authority(6, 3);
    const ThreadToken t0 = authority.register_thread();
    const ThreadToken t1 = authority.register_thread();
    const ThreadToken t2 = authority.register_thread();
    REQUIRE(t0.index == 0);
    REQUIRE(t1.index == 1);
    REQUIRE(t2.index == 2);

    const ObjectId object{0x3000, 1};
    (void)authority.publish_root(object);
    const auto removed = authority.publish_root({0x3000, 2});
    const BatchId batch = authority.retire(object, removed.publication);

    ScanSession session = authority.begin_scan();
    REQUIRE(scan_all(authority, session));

    // This new thread lands in shard 1, whose evidence has already been
    // captured.  Shards 0 and 2 remain untouched.
    const ThreadToken t3 = authority.register_thread();
    REQUIRE(t3.index == 3);
    REQUIRE(authority.shard_for(t3.index) == 1);

    ClaimResult invalid = authority.claim(session);
    REQUIRE(invalid.permits.empty());
    REQUIRE(invalid.invalidated_shards ==
            std::vector<std::size_t>{1});
    REQUIRE(invalid.stale_shards.empty());
    REQUIRE(invalid.missing_shards.empty());
    REQUIRE(contains_id(invalid.deferred_batches, batch));
    REQUIRE(session.invalidated(1));
    REQUIRE(!session.invalidated(0));
    REQUIRE(!session.invalidated(2));

    REQUIRE(authority.scan_shard(session, 1));
    const ReferenceDecision reference =
        index_reclaim::reference_rescan_all(authority);
    ClaimResult granted = authority.claim(session);
    ClaimSummary summary;
    summary.permitted_batches = granted.permitted_batches;
    summary.deferred_batches = granted.deferred_batches;
    summary.permitted = granted.permitted;
    summary.deferred = granted.deferred;
    REQUIRE(compare_decision(summary, reference,
                             "registration rescan"));
    REQUIRE(contains_id(granted.permitted_batches, batch));
    REQUIRE(!granted.needs_rescan());
    return true;
}

bool test_slot_version_change_invalidates_only_affected_shard() {
    Authority authority(4, 2);
    const ThreadToken reader = authority.register_thread();
    const ThreadToken other0 = authority.register_thread();
    const ThreadToken other1 = authority.register_thread();
    REQUIRE(reader.index == 0);
    REQUIRE(other0.index == 1);
    REQUIRE(other1.index == 2);
    REQUIRE(authority.shard_for(reader.index) == 0);
    REQUIRE(authority.shard_for(other1.index) == 1);

    const ObjectId object{0x3600, 1};
    (void)authority.publish_root(object);
    const auto removed = authority.publish_root({0x3600, 2});
    const BatchId batch = authority.retire(object, removed.publication);

    ScanSession session = authority.begin_scan();
    REQUIRE(scan_all(authority, session));

    // Publish and release in shard 0.  The claim sees no live protection, but
    // the slot version changed after scanning, so only shard 0 is invalid.
    auto attempt = authority.begin_read(reader);
    auto guard = authority.finish_read(attempt);
    REQUIRE(guard.valid());
    guard.release();

    ClaimResult changed = authority.claim(session);
    REQUIRE(changed.permits.empty());
    REQUIRE(changed.invalidated_shards ==
            std::vector<std::size_t>{0});
    REQUIRE(contains_id(changed.deferred_batches, batch));
    REQUIRE(!session.invalidated(1));

    REQUIRE(authority.scan_shard(session, 0));
    ClaimResult granted = authority.claim(session);
    REQUIRE(!granted.needs_rescan());
    REQUIRE(contains_id(granted.permitted_batches, batch));
    return true;
}

bool test_registration_in_unscanned_shard_is_covered() {
    Authority authority(4, 2);
    const ThreadToken t0 = authority.register_thread();
    REQUIRE(t0.index == 0);

    const ObjectId object{0x3500, 1};
    (void)authority.publish_root(object);
    const auto removed = authority.publish_root({0x3500, 2});
    const BatchId batch = authority.retire(object, removed.publication);

    ScanSession session = authority.begin_scan();
    REQUIRE(authority.scan_shard(session, 0));

    const ThreadToken t2 = authority.register_thread();
    REQUIRE(t2.index == 1);
    REQUIRE(authority.shard_for(t2.index) == 0);

    // Shard 0 must be rescanned because the new thread landed in it.
    const auto first = claim_with_rescan(authority, session);
    REQUIRE(!first.incomplete);
    REQUIRE(contains_id(first.permitted_batches, batch));

    Authority other(4, 2);
    const ThreadToken u0 = other.register_thread();
    REQUIRE(u0.index == 0);
    (void)other.publish_root(object);
    const auto other_removed = other.publish_root({0x3500, 2});
    const BatchId other_batch =
        other.retire(object, other_removed.publication);

    ScanSession other_session = other.begin_scan();
    REQUIRE(other.scan_shard(other_session, 0));
    const ThreadToken u1 = other.register_thread();
    REQUIRE(u1.index == 1);
    REQUIRE(other.shard_for(u1.index) == 0);

    // A fresh full scan after registration must include u1's empty slot.
    ScanSession full = other.begin_scan();
    REQUIRE(scan_all(other, full));
    ClaimResult result = other.claim(full);
    REQUIRE(result.invalidated_shards.empty());
    REQUIRE(!result.needs_rescan());
    REQUIRE(contains_id(result.permitted_batches, other_batch));
    return true;
}

bool test_empty_shards_are_still_scanned() {
    Authority authority(2, 4);
    const ThreadToken reader = authority.register_thread();
    const ObjectId object{0x3800, 1};
    (void)authority.publish_root(object);
    const auto removed = authority.publish_root({0x3800, 2});
    const BatchId batch = authority.retire(object, removed.publication);
    (void)reader;

    ScanSession session = authority.begin_scan();
    REQUIRE(scan_all(authority, session));
    ClaimResult result = authority.claim(session);
    REQUIRE(result.missing_shards.empty());
    REQUIRE(result.invalidated_shards.empty());
    REQUIRE(contains_id(result.permitted_batches, batch));
    return true;
}

bool test_thread_id_reuse_does_not_inherit_evidence() {
    Authority authority(4, 2);
    const ThreadToken old_thread = authority.register_thread();
    const ThreadToken other = authority.register_thread();
    REQUIRE(old_thread.index == 0);
    REQUIRE(other.index == 1);

    const ObjectId old_object{0x4000, 1};
    (void)authority.publish_root(old_object);
    const auto old_attempt = authority.begin_read(old_thread);
    auto old_guard = authority.finish_read(old_attempt);
    REQUIRE(old_guard.valid());

    const auto new_root = authority.publish_root({0x4000, 2});
    const BatchId batch =
        authority.retire(old_object, new_root.publication);

    ScanSession session = authority.begin_scan();
    REQUIRE(scan_all(authority, session));
    ClaimResult protected_result = authority.claim(session);
    REQUIRE(contains_id(protected_result.deferred_batches, batch));
    REQUIRE(protected_result.permits.empty());

    authority.unregister_thread(old_thread);
    REQUIRE(!authority.thread_live(old_thread));

    const ThreadToken replacement = authority.register_thread();
    REQUIRE(replacement.index == old_thread.index);
    REQUIRE(replacement.incarnation != old_thread.incarnation);

    ClaimResult stale = authority.claim(session);
    REQUIRE(stale.permits.empty());
    REQUIRE(stale.invalidated_shards ==
            std::vector<std::size_t>{authority.shard_for(old_thread.index)});
    REQUIRE(contains_id(stale.deferred_batches, batch));
    REQUIRE(session.invalidated(
        authority.shard_for(old_thread.index)));

    REQUIRE(authority.scan_shard(
        session, authority.shard_for(old_thread.index)));
    const ReferenceDecision reference =
        index_reclaim::reference_rescan_all(authority);
    ClaimResult granted = authority.claim(session);
    ClaimSummary summary;
    summary.permitted_batches = granted.permitted_batches;
    summary.deferred_batches = granted.deferred_batches;
    summary.permitted = granted.permitted;
    summary.deferred = granted.deferred;
    REQUIRE(compare_decision(summary, reference,
                             "thread id reuse"));
    REQUIRE(contains_id(granted.permitted_batches, batch));

    // The old guard must not clear a slot now owned by the new incarnation.
    const auto new_attempt = authority.begin_read(replacement);
    auto new_guard = authority.finish_read(new_attempt);
    REQUIRE(new_guard.valid());
    const auto before_release = authority.snapshot_for_reference();
    old_guard.release();
    const auto after_release = authority.snapshot_for_reference();
    REQUIRE(before_release.live_threads.size() ==
            after_release.live_threads.size());
    for (const auto& slot : after_release.live_threads) {
        if (slot.thread_index == replacement.index) {
            REQUIRE(slot.occupied);
            REQUIRE(slot.object == new_root.object);
        }
    }
    return true;
}

bool test_two_reclaimers_race_for_one_permit() {
    constexpr int kRounds = 32;

    for (int round = 0; round < kRounds; ++round) {
        Authority authority(3, 2);
        const ThreadToken reader = authority.register_thread();
        (void)reader;
        const ObjectId object{0x5000, static_cast<index_reclaim::Generation>(
                                         round + 1)};
        (void)authority.publish_root(object);
        const auto removed = authority.publish_root(
            {0x5000, static_cast<index_reclaim::Generation>(round + 1000)});
        const BatchId batch =
            authority.retire(object, removed.publication);

        std::array<ScanSession, 2> sessions{
            authority.begin_scan(), authority.begin_scan()};
        REQUIRE(scan_all(authority, sessions[0]));
        REQUIRE(scan_all(authority, sessions[1]));

        std::array<ClaimResult, 2> results;
        std::atomic<int> ready{0};
        std::atomic<bool> go{false};

        auto worker = [&](int index) {
            ready.fetch_add(1, std::memory_order_acq_rel);
            while (!go.load(std::memory_order_acquire)) {
                std::this_thread::yield();
            }
            results[static_cast<std::size_t>(index)] =
                authority.claim(sessions[static_cast<std::size_t>(index)]);
        };

        std::thread first(worker, 0);
        std::thread second(worker, 1);
        while (ready.load(std::memory_order_acquire) != 2) {
            std::this_thread::yield();
        }
        go.store(true, std::memory_order_release);
        first.join();
        second.join();

        int permit_count = 0;
        int already_claimed_count = 0;
        for (const ClaimResult& result : results) {
            permit_count += static_cast<int>(result.permits.size());
            if (contains_id(result.already_issued_batches, batch)) {
                ++already_claimed_count;
            }
        }
        REQUIRE(permit_count == 1);
        REQUIRE(already_claimed_count == 1);

        const ReferenceDecision after =
            index_reclaim::reference_rescan_all(authority);
        REQUIRE(after.eligible_batches.empty());
        REQUIRE(after.deferred_batches.empty());
    }
    return true;
}

bool test_permit_value_is_single_use() {
    Authority authority(2, 1);
    const ObjectId object{0x5800, 1};
    (void)authority.publish_root(object);
    const auto removed = authority.publish_root({0x5800, 2});
    (void)authority.retire(object, removed.publication);

    ScanSession session = authority.begin_scan();
    REQUIRE(scan_all(authority, session));
    ClaimResult first = authority.claim(session);
    REQUIRE(first.permits.size() == 1);
    REQUIRE(first.permitted == std::vector<ObjectId>{object});

    auto permit = std::move(first.permits.front());
    REQUIRE(permit.valid());
    REQUIRE(permit.consume());
    REQUIRE(!permit.consume());
    REQUIRE(!permit.valid());

    ClaimResult second = authority.claim(session);
    REQUIRE(second.permits.empty());
    REQUIRE(second.permitted.empty());
    return true;
}

enum class TraceKind {
    ReaderReadRoot,
    ReaderPublish,
    ReaderFinish,
    ReaderRelease,
    HostPublishNewRoot,
    HostRetire,
    ReclaimerScan0,
    ReclaimerScan1,
    ReclaimerClaim,
};

const char* trace_name(TraceKind kind) {
    switch (kind) {
        case TraceKind::ReaderReadRoot:
            return "reader:read";
        case TraceKind::ReaderPublish:
            return "reader:publish";
        case TraceKind::ReaderFinish:
            return "reader:finish";
        case TraceKind::ReaderRelease:
            return "reader:release";
        case TraceKind::HostPublishNewRoot:
            return "host:new-root";
        case TraceKind::HostRetire:
            return "host:retire";
        case TraceKind::ReclaimerScan0:
            return "reclaimer:scan0";
        case TraceKind::ReclaimerScan1:
            return "reclaimer:scan1";
        case TraceKind::ReclaimerClaim:
            return "reclaimer:claim";
    }
    return "unknown";
}

struct InterleavingStats {
    std::size_t traces = 0;
    std::size_t claims = 0;
    std::size_t permitted = 0;
    std::size_t deferred = 0;
    std::size_t permits_issued = 0;
};

void run_trace(const std::vector<TraceKind>& trace,
               bool& ok,
               InterleavingStats& stats) {
    Authority authority(4, 2);
    const ObjectId old_object{0x6000, 1};
    const ObjectId new_object{0x6000, 2};

    const auto initial_root = authority.publish_root(old_object);
    (void)initial_root;
    const ThreadToken reader = authority.register_thread();

    ScanSession session = authority.begin_scan();
    index_reclaim::RootSnapshot first_read{};
    index_reclaim::ReaderAttempt attempt{};
    index_reclaim::ReadGuard guard;
    index_reclaim::RootSnapshot new_root{};
    BatchId batch = 0;

    for (TraceKind kind : trace) {
        switch (kind) {
            case TraceKind::ReaderReadRoot:
                first_read = authority.read_root();
                break;
            case TraceKind::ReaderPublish:
                attempt = authority.publish_protection(reader, first_read);
                if (!attempt.published()) {
                    ok = false;
                }
                break;
            case TraceKind::ReaderFinish:
                guard = authority.finish_read(attempt);
                break;
            case TraceKind::ReaderRelease:
                guard.release();
                break;
            case TraceKind::HostPublishNewRoot:
                new_root = authority.publish_root(new_object);
                break;
            case TraceKind::HostRetire:
                batch = authority.retire(old_object,
                                         new_root.publication);
                break;
            case TraceKind::ReclaimerScan0:
                if (!authority.scan_shard(session, 0)) {
                    ok = false;
                }
                break;
            case TraceKind::ReclaimerScan1:
                if (!authority.scan_shard(session, 1)) {
                    ok = false;
                }
                break;
            case TraceKind::ReclaimerClaim: {
                ++stats.claims;
                const ReferenceDecision expected =
                    index_reclaim::reference_rescan_all(authority);
                const ClaimSummary actual =
                    claim_with_rescan(authority, session);

                std::ostringstream context;
                context << "interleaving";
                for (TraceKind step : trace) {
                    context << ' ' << trace_name(step);
                }
                if (!compare_decision(actual, expected, context.str())) {
                    ok = false;
                }
                stats.permitted += actual.permitted_batches.size();
                stats.deferred += actual.deferred_batches.size();
                break;
            }
        }
    }

    if (batch != 0) {
        // A batch can only disappear through a permit; nothing else mutates it.
        const auto snapshots = authority.batch_snapshot();
        for (const auto& snapshot : snapshots) {
            if (snapshot.id == batch && snapshot.issued) {
                ++stats.permits_issued;
            }
        }
    }
}

void enumerate_traces(const std::array<std::vector<TraceKind>, 3>& queues,
                      std::array<std::size_t, 3>& positions,
                      std::vector<TraceKind>& trace,
                      bool& ok,
                      InterleavingStats& stats) {
    const bool done = positions[0] == queues[0].size() &&
                      positions[1] == queues[1].size() &&
                      positions[2] == queues[2].size();
    if (done) {
        ++stats.traces;
        run_trace(trace, ok, stats);
        return;
    }

    for (std::size_t queue = 0; queue < queues.size(); ++queue) {
        if (positions[queue] == queues[queue].size()) {
            continue;
        }
        trace.push_back(queues[queue][positions[queue]]);
        ++positions[queue];
        enumerate_traces(queues, positions, trace, ok, stats);
        --positions[queue];
        trace.pop_back();
    }
}

bool test_reference_against_small_interleavings() {
    std::array<std::vector<TraceKind>, 3> queues{{
        {
            TraceKind::ReaderReadRoot,
            TraceKind::ReaderPublish,
            TraceKind::ReaderFinish,
            TraceKind::ReaderRelease,
        },
        {
            TraceKind::HostPublishNewRoot,
            TraceKind::HostRetire,
        },
        {
            TraceKind::ReclaimerScan0,
            TraceKind::ReclaimerScan1,
            TraceKind::ReclaimerClaim,
        },
    }};

    std::array<std::size_t, 3> positions{{0, 0, 0}};
    std::vector<TraceKind> trace;
    InterleavingStats stats;
    bool ok = true;
    enumerate_traces(queues, positions, trace, ok, stats);

    std::cout << "interleavings: " << stats.traces
              << ", claims: " << stats.claims
              << ", permits: " << stats.permitted
              << ", deferrals: " << stats.deferred << '\n';

    REQUIRE(stats.traces == 1260);
    REQUIRE(stats.claims > 300);
    REQUIRE(stats.permitted > 0);
    REQUIRE(stats.deferred > 0);
    REQUIRE(stats.permits_issued == stats.permitted);
    REQUIRE(ok);
    return true;
}

}  // namespace

int main() {
    bool ok = true;

    ok = test_generation_nonwrapping() && ok;
    ok = test_same_address_generation_change() && ok;
    ok = test_unpublished_reader_does_not_block() && ok;
    ok = test_guard_tracks_slot_overwrite() && ok;
    ok = test_registration_invalidates_only_affected_shard() && ok;
    ok = test_slot_version_change_invalidates_only_affected_shard() && ok;
    ok = test_registration_in_unscanned_shard_is_covered() && ok;
    ok = test_empty_shards_are_still_scanned() && ok;
    ok = test_thread_id_reuse_does_not_inherit_evidence() && ok;
    ok = test_two_reclaimers_race_for_one_permit() && ok;
    ok = test_permit_value_is_single_use() && ok;
    ok = test_reference_against_small_interleavings() && ok;

    if (!ok || g_failures != 0) {
        std::cerr << g_failures << " failure(s)\n";
        return 1;
    }

    std::cout << "all tests passed\n";
    return 0;
}

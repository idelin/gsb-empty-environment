#ifndef INDEX_RECLAIM_PERMISSION_HPP
#define INDEX_RECLAIM_PERMISSION_HPP

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <memory>
#include <vector>

namespace index_reclaim {

using Address = std::uint64_t;
using Generation = std::uint64_t;
using Publication = std::uint64_t;
using SlotVersion = std::uint64_t;
using BatchId = std::uint64_t;

// Object identity never consists of an address alone.  Reusing an address is
// represented by a new generation, and the generation counter is checked for
// overflow instead of being allowed to wrap.
struct ObjectId {
    Address address = 0;
    Generation generation = 0;
};

inline bool operator==(const ObjectId& lhs, const ObjectId& rhs) noexcept {
    return lhs.address == rhs.address && lhs.generation == rhs.generation;
}

inline bool operator!=(const ObjectId& lhs, const ObjectId& rhs) noexcept {
    return !(lhs == rhs);
}

inline bool operator<(const ObjectId& lhs, const ObjectId& rhs) noexcept {
    return lhs.address < rhs.address ||
           (lhs.address == rhs.address && lhs.generation < rhs.generation);
}

struct ObjectIdLess {
    bool operator()(const ObjectId& lhs, const ObjectId& rhs) const noexcept {
        return lhs < rhs;
    }
};

// publication is the sequence number assigned when this root value became
// visible.  It is deliberately part of the root snapshot, although object
// identity itself is only (address, generation).
struct RootSnapshot {
    ObjectId object{};
    Publication publication = 0;
};

class GenerationSequence {
public:
    explicit GenerationSequence(Generation first = 1) noexcept;

    // Returns the current generation and advances the sequence.  Throws
    // std::overflow_error rather than wrapping at UINT64_MAX.
    Generation next();

    // Utility for hosts that keep their generation in their own object record.
    static Generation checked_next(Generation current);

private:
    std::atomic<Generation> next_;
};

enum class ReaderStatus {
    Ok,
    InvalidAttempt,
    ThreadNotLive,
    SlotOverwritten,
    RootChanged,
};

struct ThreadToken {
    static constexpr std::uint32_t kInvalidIndex =
        std::numeric_limits<std::uint32_t>::max();

    std::uint32_t index = kInvalidIndex;
    std::uint64_t incarnation = 0;

    explicit operator bool() const noexcept {
        return index != kInvalidIndex && incarnation != 0;
    }
};

inline bool operator==(const ThreadToken& lhs, const ThreadToken& rhs) noexcept {
    return lhs.index == rhs.index && lhs.incarnation == rhs.incarnation;
}

inline bool operator!=(const ThreadToken& lhs, const ThreadToken& rhs) noexcept {
    return !(lhs == rhs);
}

namespace detail {
struct AuthorityState;
struct ScanSessionState;
}  // namespace detail

// A reader that has only read the root has not published protection yet.  A
// ReaderAttempt records the publication action; finish_protection() performs
// the mandatory reread.
struct ReaderAttempt {
    ReaderStatus status = ReaderStatus::InvalidAttempt;
    ThreadToken thread{};
    RootSnapshot observed{};
    SlotVersion slot_version = 0;

    bool published() const noexcept {
        return status == ReaderStatus::Ok;
    }

private:
    friend class Authority;
    std::shared_ptr<detail::AuthorityState> state_;
};

// The guard owns a published slot until release() or destruction.  The host
// may dereference object() only while valid() is true.
class ReadGuard {
public:
    ReadGuard() = default;
    ~ReadGuard();

    ReadGuard(ReadGuard&& other) noexcept;
    ReadGuard& operator=(ReadGuard&& other) noexcept;
    ReadGuard(const ReadGuard&) = delete;
    ReadGuard& operator=(const ReadGuard&) = delete;

    // Revalidates the slot version and owning thread incarnation on every
    // call.  A later publication through the same thread token therefore
    // makes this guard invalid even though the guard was valid at finish.
    bool valid() const noexcept;

    explicit operator bool() const noexcept {
        return valid();
    }

    ReaderStatus status() const noexcept {
        return status_;
    }

    const ObjectId& object() const noexcept {
        return object_;
    }

    ThreadToken thread() const noexcept {
        return thread_;
    }

    SlotVersion slot_version() const noexcept {
        return slot_version_;
    }

    // Clears the protection slot if it still contains exactly this guard's
    // publication.  It never clears a newer publication that overwrote it.
    void release() noexcept;

private:
    friend class Authority;

    std::shared_ptr<detail::AuthorityState> state_;
    ThreadToken thread_{};
    ObjectId object_{};
    SlotVersion slot_version_ = 0;
    ReaderStatus status_ = ReaderStatus::Ok;
};

class Permit {
public:
    Permit() = default;
    Permit(Permit&& other) noexcept;
    Permit& operator=(Permit&& other) noexcept;
    Permit(const Permit&) = delete;
    Permit& operator=(const Permit&) = delete;

    bool valid() const noexcept {
        return valid_;
    }

    explicit operator bool() const noexcept {
        return valid_;
    }

    // A permit is single-use.  The authority already guarantees that no second
    // permit for this batch can be issued; consume() additionally prevents the
    // host from accidentally processing the same permit value twice.
    bool consume() noexcept;

    BatchId batch_id() const noexcept {
        return batch_id_;
    }

    const std::vector<ObjectId>& objects() const noexcept {
        return objects_;
    }

private:
    friend class Authority;

    Permit(BatchId batch_id, std::vector<ObjectId> objects);

    BatchId batch_id_ = 0;
    std::vector<ObjectId> objects_;
    bool valid_ = false;
};

struct ClaimResult {
    ClaimResult() = default;
    ClaimResult(ClaimResult&&) noexcept = default;
    ClaimResult& operator=(ClaimResult&&) noexcept = default;
    ClaimResult(const ClaimResult&) = delete;
    ClaimResult& operator=(const ClaimResult&) = delete;

    std::vector<Permit> permits;
    std::vector<ObjectId> permitted;
    std::vector<ObjectId> deferred;

    std::vector<BatchId> permitted_batches;
    std::vector<BatchId> deferred_batches;
    std::vector<BatchId> protected_batches;
    std::vector<BatchId> already_issued_batches;

    // Shards whose scan evidence was invalidated by a registration or slot
    // version change between scanning and the atomic claim check.
    std::vector<std::size_t> invalidated_shards;

    // Evidence is valid, but it was taken before this batch's removal
    // publication.  Those shards must be scanned again for this removal.
    std::vector<std::size_t> stale_shards;

    // No evidence exists for these shards yet.
    std::vector<std::size_t> missing_shards;

    bool needs_rescan() const noexcept {
        return !invalidated_shards.empty() || !stale_shards.empty() ||
               !missing_shards.empty();
    }
};

class ScanSession {
public:
    ScanSession() = default;

    std::size_t shard_count() const noexcept;
    bool scanned(std::size_t shard) const noexcept;
    bool invalidated(std::size_t shard) const noexcept;

private:
    friend class Authority;

    explicit ScanSession(std::shared_ptr<detail::ScanSessionState> state) noexcept;

    std::shared_ptr<detail::ScanSessionState> state_;
};

struct ThreadSlotSnapshot {
    std::uint32_t thread_index = 0;
    std::uint64_t incarnation = 0;
    SlotVersion slot_version = 0;
    bool occupied = false;
    ObjectId object{};
};

struct BatchSnapshot {
    BatchId id = 0;
    std::vector<ObjectId> objects;
    Publication removal_publication = 0;
    bool issued = false;
};

struct AuthoritySnapshot {
    RootSnapshot root{};
    std::vector<ThreadSlotSnapshot> live_threads;
    std::vector<BatchSnapshot> batches;
};

// One authority instance models the root, thread registry, protection slots and
// retired batches of one synthetic index.  No pointer is ever dereferenced.
class Authority {
public:
    explicit Authority(std::size_t max_threads, std::size_t shard_count = 1);
    ~Authority();

    Authority(const Authority&) = delete;
    Authority& operator=(const Authority&) = delete;
    Authority(Authority&&) = delete;
    Authority& operator=(Authority&&) = delete;

    std::size_t max_threads() const noexcept;
    std::size_t shard_count() const noexcept;
    std::size_t shard_for(std::uint32_t thread_index) const;

    ThreadToken register_thread();

    // Thread exit clears the slot before making the index reusable.  A stale
    // token is ignored.
    void unregister_thread(ThreadToken token) noexcept;

    bool thread_live(ThreadToken token) const noexcept;

    RootSnapshot read_root() const noexcept;

    // Advances the root publication sequence by one.  The sequence is checked
    // for overflow and is never wrapped.
    RootSnapshot publish_root(ObjectId object);

    // Publishes protection for an already-read root snapshot.
    ReaderAttempt publish_protection(ThreadToken token,
                                     const RootSnapshot& observed);

    // Convenience form: read root, publish, then (in finish_read) reread.
    ReaderAttempt begin_read(ThreadToken token);

    // Performs the second root read and verifies that:
    //   * both reads identify the same (address, generation),
    //   * the publication slot still has the version installed by the reader,
    //   * the token still names a live thread incarnation.
    // On failure the slot is cleared if it still belongs to this attempt.
    ReadGuard finish_protection(const ReaderAttempt& attempt,
                                const RootSnapshot& reread) noexcept;

    ReadGuard finish_read(const ReaderAttempt& attempt) noexcept;

    // Retires objects after the root value carrying removal_publication has
    // been installed.  A batch is granted at most one permit.
    BatchId retire(const ObjectId& object, Publication removal_publication);
    BatchId retire(const std::vector<ObjectId>& objects,
                   Publication removal_publication);

    ScanSession begin_scan() const;

    // Scans one shard.  Returns false if the shard changed too often to obtain
    // a stable snapshot; the session records that fact and a later scan_shard
    // call can replace the evidence.
    bool scan_shard(ScanSession& session, std::size_t shard) const;

    // Validates only the shards represented by this session, invalidates only
    // evidence affected by registration/slot version changes, and atomically
    // passes each eligible batch through a one-shot CAS.
    ClaimResult claim(ScanSession& session) const;

    std::vector<BatchSnapshot> batch_snapshot() const;
    AuthoritySnapshot snapshot_for_reference() const;

private:
    std::shared_ptr<detail::AuthorityState> state_;
};

}  // namespace index_reclaim

#endif  // INDEX_RECLAIM_PERMISSION_HPP

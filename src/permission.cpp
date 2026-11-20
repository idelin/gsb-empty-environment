#include "index_reclaim/permission.hpp"

#include <algorithm>
#include <mutex>
#include <stdexcept>
#include <thread>
#include <utility>

namespace index_reclaim {
namespace {

template <typename T>
T nonwrapping_next(T current) {
    if (current == std::numeric_limits<T>::max()) {
        throw std::overflow_error("index_reclaim: sequence exhausted");
    }
    return static_cast<T>(current + 1);
}

void cpu_relax() {
    std::this_thread::yield();
}

template <typename T>
bool contains(const std::vector<T>& values, const T& value) {
    return std::find(values.begin(), values.end(), value) != values.end();
}

template <typename T>
void append_unique(std::vector<T>& values, const T& value) {
    if (!contains(values, value)) {
        values.push_back(value);
    }
}

}  // namespace

GenerationSequence::GenerationSequence(Generation first) noexcept
    : next_(first) {}

Generation GenerationSequence::next() {
    Generation current = next_.load(std::memory_order_acquire);
    for (;;) {
        const Generation following = checked_next(current);
        if (next_.compare_exchange_weak(current, following,
                                        std::memory_order_acq_rel,
                                        std::memory_order_acquire)) {
            return current;
        }
    }
}

Generation GenerationSequence::checked_next(Generation current) {
    return nonwrapping_next(current);
}

Permit::Permit(BatchId batch_id, std::vector<ObjectId> objects)
    : batch_id_(batch_id), objects_(std::move(objects)), valid_(true) {}

Permit::Permit(Permit&& other) noexcept
    : batch_id_(other.batch_id_),
      objects_(std::move(other.objects_)),
      valid_(other.valid_) {
    other.valid_ = false;
}

Permit& Permit::operator=(Permit&& other) noexcept {
    if (this != &other) {
        batch_id_ = other.batch_id_;
        objects_ = std::move(other.objects_);
        valid_ = other.valid_;
        other.valid_ = false;
    }
    return *this;
}

bool Permit::consume() noexcept {
    if (!valid_) {
        return false;
    }
    valid_ = false;
    return true;
}

namespace detail {

struct Slot {
    std::atomic<SlotVersion> version{0};
    std::atomic<std::uint8_t> occupied{0};
    std::atomic<Address> address{0};
    std::atomic<Generation> generation{0};

    struct Raw {
        SlotVersion version = 0;
        bool occupied = false;
        ObjectId object{};
    };

    Raw read() const noexcept {
        for (;;) {
            const SlotVersion before =
                version.load(std::memory_order_acquire);
            if ((before & 1U) != 0) {
                cpu_relax();
                continue;
            }

            Raw raw;
            raw.occupied =
                occupied.load(std::memory_order_acquire) != 0;
            raw.object.address =
                address.load(std::memory_order_acquire);
            raw.object.generation =
                generation.load(std::memory_order_acquire);
            const SlotVersion after =
                version.load(std::memory_order_acquire);

            if (before == after) {
                raw.version = before;
                return raw;
            }
        }
    }

    void write(const ObjectId& object) noexcept {
        SlotVersion current = version.load(std::memory_order_relaxed);
        for (;;) {
            while ((current & 1U) != 0) {
                cpu_relax();
                current = version.load(std::memory_order_relaxed);
            }
            if (version.compare_exchange_weak(
                    current, current + 1, std::memory_order_acq_rel,
                    std::memory_order_relaxed)) {
                break;
            }
        }

        address.store(object.address, std::memory_order_relaxed);
        generation.store(object.generation, std::memory_order_relaxed);
        occupied.store(1, std::memory_order_relaxed);
        version.store(current + 2, std::memory_order_release);
    }

    void clear() noexcept {
        SlotVersion current = version.load(std::memory_order_relaxed);
        for (;;) {
            while ((current & 1U) != 0) {
                cpu_relax();
                current = version.load(std::memory_order_relaxed);
            }
            if (version.compare_exchange_weak(
                    current, current + 1, std::memory_order_acq_rel,
                    std::memory_order_relaxed)) {
                break;
            }
        }

        occupied.store(0, std::memory_order_relaxed);
        address.store(0, std::memory_order_relaxed);
        generation.store(0, std::memory_order_relaxed);
        version.store(current + 2, std::memory_order_release);
    }

    bool clear_if_matches(const ObjectId& expected_object,
                          SlotVersion expected_version) noexcept {
        SlotVersion current = version.load(std::memory_order_relaxed);
        if ((current & 1U) != 0 || current != expected_version) {
            return false;
        }
        if (!version.compare_exchange_strong(
                current, current + 1, std::memory_order_acq_rel,
                std::memory_order_relaxed)) {
            return false;
        }

        const bool matches =
            occupied.load(std::memory_order_relaxed) != 0 &&
            address.load(std::memory_order_relaxed) ==
                expected_object.address &&
            generation.load(std::memory_order_relaxed) ==
                expected_object.generation;

        if (matches) {
            occupied.store(0, std::memory_order_relaxed);
            address.store(0, std::memory_order_relaxed);
            generation.store(0, std::memory_order_relaxed);
        }
        version.store(current + 2, std::memory_order_release);
        return matches;
    }
};

struct ThreadRecord {
    std::atomic<std::uint64_t> incarnation{0};
};

struct ShardControl {
    // Even values are stable snapshots; a lifecycle operation increments this
    // once before and once after changing its thread records and slot.
    std::atomic<std::uint64_t> registration_version{0};
};

struct BatchRecord {
    BatchRecord(BatchId batch_id, std::vector<ObjectId> batch_objects,
                Publication removal)
        : id(batch_id),
          objects(std::move(batch_objects)),
          removal_publication(removal) {}

    BatchId id;
    std::vector<ObjectId> objects;
    Publication removal_publication;
    std::atomic<std::uint8_t> issued{0};
};

struct SlotObservation {
    std::uint32_t thread_index = 0;
    std::uint64_t incarnation = 0;
    SlotVersion slot_version = 0;
    bool occupied = false;
    ObjectId object{};
};

struct ShardEvidence {
    bool present = false;
    bool invalid = false;
    std::uint64_t registration_version = 0;
    Publication scan_publication = 0;
    std::vector<SlotObservation> observations;
};

struct AuthorityState {
    AuthorityState(std::size_t thread_count, std::size_t shards)
        : max_threads(thread_count),
          shard_count(shards),
          threads_per_shard(thread_count / shards +
                            (thread_count % shards != 0 ? 1U : 0U)),
          slots(thread_count),
          records(thread_count),
          shards(shards) {}

    std::size_t max_threads;
    std::size_t shard_count;
    std::size_t threads_per_shard;

    std::vector<Slot> slots;
    std::vector<ThreadRecord> records;
    std::vector<ShardControl> shards;

    mutable std::mutex lifecycle_mutex;
    std::uint64_t next_incarnation = 1;

    mutable std::mutex root_write_mutex;
    mutable std::atomic<std::uint64_t> root_version{0};
    std::atomic<Address> root_address{0};
    std::atomic<Generation> root_generation{0};
    std::atomic<Publication> root_publication{0};

    mutable std::mutex batches_mutex;
    std::vector<std::shared_ptr<BatchRecord>> batches;
    BatchId next_batch_id = 1;

    std::size_t shard_for(std::uint32_t thread_index) const {
        if (threads_per_shard == 0) {
            return 0;
        }
        const std::size_t shard =
            static_cast<std::size_t>(thread_index) / threads_per_shard;
        return std::min(shard, shard_count - 1U);
    }

    std::size_t shard_begin(std::size_t shard) const {
        return shard * threads_per_shard;
    }

    std::size_t shard_end(std::size_t shard) const {
        const std::size_t begin = shard_begin(shard);
        return std::min(begin + threads_per_shard, max_threads);
    }

    RootSnapshot read_root() const noexcept {
        for (;;) {
            const std::uint64_t before =
                root_version.load(std::memory_order_acquire);
            if ((before & 1U) != 0) {
                cpu_relax();
                continue;
            }

            RootSnapshot snapshot;
            snapshot.object.address =
                root_address.load(std::memory_order_relaxed);
            snapshot.object.generation =
                root_generation.load(std::memory_order_relaxed);
            snapshot.publication =
                root_publication.load(std::memory_order_relaxed);

            const std::uint64_t after =
                root_version.load(std::memory_order_acquire);
            if (before == after) {
                return snapshot;
            }
        }
    }

    RootSnapshot publish_root(const ObjectId& object) {
        std::lock_guard<std::mutex> lock(root_write_mutex);

        const Publication next = nonwrapping_next(
            root_publication.load(std::memory_order_relaxed));

        std::uint64_t version =
            root_version.load(std::memory_order_relaxed);
        if ((version & 1U) != 0) {
            throw std::logic_error("index_reclaim: root seqlock is odd");
        }

        root_version.store(version + 1, std::memory_order_release);
        root_address.store(object.address, std::memory_order_relaxed);
        root_generation.store(object.generation,
                              std::memory_order_relaxed);
        root_publication.store(next, std::memory_order_relaxed);
        root_version.store(version + 2, std::memory_order_release);

        RootSnapshot snapshot;
        snapshot.object = object;
        snapshot.publication = next;
        return snapshot;
    }

    bool token_live(ThreadToken token) const noexcept {
        if (token.index >= max_threads || token.incarnation == 0) {
            return false;
        }
        return records[token.index].incarnation.load(
                   std::memory_order_acquire) == token.incarnation;
    }

    bool clear_slot_if_matches(ThreadToken token, const ObjectId& object,
                               SlotVersion expected_version) noexcept {
        if (!token_live(token)) {
            return false;
        }

        return slots[token.index].clear_if_matches(object,
                                                   expected_version);
    }
};

struct ScanSessionState {
    explicit ScanSessionState(std::shared_ptr<AuthorityState> authority_state)
        : authority(std::move(authority_state)),
          shards(authority->shard_count) {}

    std::shared_ptr<AuthorityState> authority;
    std::vector<ShardEvidence> shards;
};

std::shared_ptr<AuthorityState> make_authority_state(
    std::size_t max_threads, std::size_t shard_count) {
    if (max_threads == 0) {
        throw std::invalid_argument(
            "index_reclaim: at least one thread slot is required");
    }
    if (shard_count == 0) {
        throw std::invalid_argument(
            "index_reclaim: at least one scan shard is required");
    }
    if (max_threads >
        static_cast<std::size_t>(
            std::numeric_limits<std::uint32_t>::max())) {
        throw std::invalid_argument(
            "index_reclaim: too many thread slots");
    }
    return std::make_shared<AuthorityState>(max_threads, shard_count);
}

}  // namespace detail

ReadGuard::~ReadGuard() {
    release();
}

ReadGuard::ReadGuard(ReadGuard&& other) noexcept
    : state_(std::move(other.state_)),
      thread_(other.thread_),
      object_(other.object_),
      slot_version_(other.slot_version_),
      status_(other.status_) {
    other.status_ = ReaderStatus::InvalidAttempt;
    other.slot_version_ = 0;
}

ReadGuard& ReadGuard::operator=(ReadGuard&& other) noexcept {
    if (this != &other) {
        release();
        state_ = std::move(other.state_);
        thread_ = other.thread_;
        object_ = other.object_;
        slot_version_ = other.slot_version_;
        status_ = other.status_;
        other.status_ = ReaderStatus::InvalidAttempt;
        other.slot_version_ = 0;
    }
    return *this;
}

bool ReadGuard::valid() const noexcept {
    if (status_ != ReaderStatus::Ok || !state_) {
        return false;
    }
    if (!state_->token_live(thread_)) {
        return false;
    }
    const detail::Slot::Raw raw = state_->slots[thread_.index].read();
    return raw.version == slot_version_ && raw.occupied &&
           raw.object == object_;
}

void ReadGuard::release() noexcept {
    if (state_ && status_ == ReaderStatus::Ok) {
        state_->clear_slot_if_matches(thread_, object_, slot_version_);
    }
    state_.reset();
    status_ = ReaderStatus::InvalidAttempt;
    slot_version_ = 0;
}

std::size_t ScanSession::shard_count() const noexcept {
    return state_ ? state_->shards.size() : 0;
}

bool ScanSession::scanned(std::size_t shard) const noexcept {
    return state_ && shard < state_->shards.size() &&
           state_->shards[shard].present;
}

bool ScanSession::invalidated(std::size_t shard) const noexcept {
    return !state_ || shard >= state_->shards.size() ||
           state_->shards[shard].invalid;
}

ScanSession::ScanSession(
    std::shared_ptr<detail::ScanSessionState> state) noexcept
    : state_(std::move(state)) {}

Authority::Authority(std::size_t max_threads, std::size_t shard_count)
    : state_(detail::make_authority_state(max_threads, shard_count)) {}

Authority::~Authority() = default;

std::size_t Authority::max_threads() const noexcept {
    return state_->max_threads;
}

std::size_t Authority::shard_count() const noexcept {
    return state_->shard_count;
}

std::size_t Authority::shard_for(std::uint32_t thread_index) const {
    if (thread_index >= state_->max_threads) {
        throw std::out_of_range("index_reclaim: thread index out of range");
    }
    return state_->shard_for(thread_index);
}

ThreadToken Authority::register_thread() {
    std::lock_guard<std::mutex> lock(state_->lifecycle_mutex);

    for (std::size_t index = 0; index < state_->max_threads; ++index) {
        if (state_->records[index].incarnation.load(
                std::memory_order_acquire) != 0) {
            continue;
        }

        if (state_->next_incarnation ==
            std::numeric_limits<std::uint64_t>::max()) {
            throw std::overflow_error(
                "index_reclaim: thread incarnation exhausted");
        }
        const std::uint64_t incarnation = state_->next_incarnation;
        ++state_->next_incarnation;

        const std::uint32_t thread_index =
            static_cast<std::uint32_t>(index);
        const std::size_t shard = state_->shard_for(thread_index);
        auto& control = state_->shards[shard];

        control.registration_version.fetch_add(
            1, std::memory_order_acq_rel);

        // A reused index starts with an empty, versioned slot.  In
        // particular, it cannot retain the previous incarnation's protection.
        state_->slots[index].clear();
        state_->records[index].incarnation.store(
            incarnation, std::memory_order_release);

        control.registration_version.fetch_add(
            1, std::memory_order_acq_rel);

        ThreadToken token;
        token.index = thread_index;
        token.incarnation = incarnation;
        return token;
    }

    throw std::runtime_error("index_reclaim: no free thread slots");
}

void Authority::unregister_thread(ThreadToken token) noexcept {
    std::lock_guard<std::mutex> lock(state_->lifecycle_mutex);

    if (token.index >= state_->max_threads ||
        token.incarnation == 0 ||
        state_->records[token.index].incarnation.load(
            std::memory_order_acquire) != token.incarnation) {
        return;
    }

    const std::size_t shard = state_->shard_for(token.index);
    auto& control = state_->shards[shard];

    control.registration_version.fetch_add(
        1, std::memory_order_acq_rel);

    // Exit order is intentional: protection disappears before the index can
    // be reused by another incarnation.
    state_->slots[token.index].clear();
    state_->records[token.index].incarnation.store(
        0, std::memory_order_release);

    control.registration_version.fetch_add(
        1, std::memory_order_acq_rel);
}

bool Authority::thread_live(ThreadToken token) const noexcept {
    return state_->token_live(token);
}

RootSnapshot Authority::read_root() const noexcept {
    return state_->read_root();
}

RootSnapshot Authority::publish_root(ObjectId object) {
    return state_->publish_root(object);
}

ReaderAttempt Authority::publish_protection(
    ThreadToken token, const RootSnapshot& observed) {
    ReaderAttempt attempt;
    attempt.thread = token;
    attempt.observed = observed;
    attempt.state_ = state_;

    if (!state_->token_live(token)) {
        attempt.status = ReaderStatus::ThreadNotLive;
        return attempt;
    }

    state_->slots[token.index].write(observed.object);
    const detail::Slot::Raw raw = state_->slots[token.index].read();
    attempt.slot_version = raw.version;
    attempt.status = ReaderStatus::Ok;
    return attempt;
}

ReaderAttempt Authority::begin_read(ThreadToken token) {
    return publish_protection(token, state_->read_root());
}

ReadGuard Authority::finish_protection(
    const ReaderAttempt& attempt,
    const RootSnapshot& reread) noexcept {
    ReadGuard guard;
    guard.thread_ = attempt.thread;
    guard.object_ = attempt.observed.object;
    guard.slot_version_ = attempt.slot_version;
    guard.state_ = attempt.state_;

    if (attempt.status != ReaderStatus::Ok || !attempt.state_ ||
        attempt.state_.get() != state_.get()) {
        guard.status_ = ReaderStatus::InvalidAttempt;
        return guard;
    }

    if (!attempt.state_->token_live(attempt.thread)) {
        guard.status_ = ReaderStatus::ThreadNotLive;
        return guard;
    }

    if (reread.object != attempt.observed.object) {
        attempt.state_->clear_slot_if_matches(
            attempt.thread, attempt.observed.object,
            attempt.slot_version);
        guard.status_ = ReaderStatus::RootChanged;
        return guard;
    }

    const detail::Slot::Raw raw =
        attempt.state_->slots[attempt.thread.index].read();
    if (raw.version != attempt.slot_version || !raw.occupied ||
        raw.object != attempt.observed.object) {
        guard.status_ = ReaderStatus::SlotOverwritten;
        return guard;
    }

    guard.status_ = ReaderStatus::Ok;
    return guard;
}

ReadGuard Authority::finish_read(
    const ReaderAttempt& attempt) noexcept {
    return finish_protection(attempt, state_->read_root());
}

BatchId Authority::retire(const ObjectId& object,
                          Publication removal_publication) {
    return retire(std::vector<ObjectId>{object}, removal_publication);
}

BatchId Authority::retire(
    const std::vector<ObjectId>& objects,
    Publication removal_publication) {
    if (objects.empty()) {
        throw std::invalid_argument(
            "index_reclaim: an empty retirement batch is meaningless");
    }

    const RootSnapshot root = state_->read_root();
    if (removal_publication > root.publication) {
        throw std::invalid_argument(
            "index_reclaim: removal publication is not installed yet");
    }

    std::vector<ObjectId> unique_objects;
    unique_objects.reserve(objects.size());
    for (const ObjectId& object : objects) {
        append_unique(unique_objects, object);
    }

    std::lock_guard<std::mutex> lock(state_->batches_mutex);
    if (state_->next_batch_id ==
        std::numeric_limits<BatchId>::max()) {
        throw std::overflow_error(
            "index_reclaim: retirement batch ids exhausted");
    }
    const BatchId batch_id = state_->next_batch_id;
    ++state_->next_batch_id;

    state_->batches.push_back(std::make_shared<detail::BatchRecord>(
        batch_id, std::move(unique_objects), removal_publication));
    return batch_id;
}

ScanSession Authority::begin_scan() const {
    return ScanSession(
        std::make_shared<detail::ScanSessionState>(state_));
}

bool Authority::scan_shard(ScanSession& session,
                           std::size_t shard) const {
    if (!session.state_ || session.state_->authority.get() != state_.get()) {
        throw std::invalid_argument(
            "index_reclaim: scan session belongs to another authority");
    }
    if (shard >= state_->shard_count) {
        throw std::out_of_range("index_reclaim: shard out of range");
    }

    auto& evidence = session.state_->shards[shard];

    // A registration transition makes the shard control odd.  Retry a bounded
    // number of times so an adversarial registration storm does not spin a
    // reclaimer forever.
    constexpr std::size_t kScanAttempts = 1024;
    for (std::size_t attempt = 0; attempt < kScanAttempts; ++attempt) {
        const std::uint64_t version_before =
            state_->shards[shard].registration_version.load(
                std::memory_order_acquire);
        if ((version_before & 1U) != 0) {
            cpu_relax();
            continue;
        }

        const RootSnapshot root = state_->read_root();
        const std::size_t shard_begin = state_->shard_begin(shard);
        const std::size_t shard_end = state_->shard_end(shard);
        std::vector<detail::SlotObservation> observations;
        observations.reserve(shard_end > shard_begin
                                 ? shard_end - shard_begin
                                 : 0U);

        bool slot_stable = true;
        for (std::size_t index = shard_begin;
             index < shard_end; ++index) {
            const std::uint64_t incarnation =
                state_->records[index].incarnation.load(
                    std::memory_order_acquire);
            if (incarnation == 0) {
                continue;
            }

            const detail::Slot::Raw raw = state_->slots[index].read();
            if ((raw.version & 1U) != 0) {
                slot_stable = false;
                break;
            }

            detail::SlotObservation observation;
            observation.thread_index =
                static_cast<std::uint32_t>(index);
            observation.incarnation = incarnation;
            observation.slot_version = raw.version;
            observation.occupied = raw.occupied;
            observation.object = raw.object;
            observations.push_back(observation);
        }

        const std::uint64_t version_after =
            state_->shards[shard].registration_version.load(
                std::memory_order_acquire);

        if (!slot_stable || version_before != version_after ||
            (version_after & 1U) != 0) {
            cpu_relax();
            continue;
        }

        evidence.present = true;
        evidence.invalid = false;
        evidence.registration_version = version_before;
        evidence.scan_publication = root.publication;
        evidence.observations = std::move(observations);
        return true;
    }

    evidence.present = true;
    evidence.invalid = true;
    evidence.registration_version = 0;
    evidence.scan_publication = 0;
    evidence.observations.clear();
    return false;
}

ClaimResult Authority::claim(ScanSession& session) const {
    if (!session.state_ || session.state_->authority.get() != state_.get()) {
        throw std::invalid_argument(
            "index_reclaim: scan session belongs to another authority");
    }

    ClaimResult result;

    std::vector<bool> shard_valid(state_->shard_count, false);

    for (std::size_t shard = 0; shard < state_->shard_count; ++shard) {
        auto& evidence = session.state_->shards[shard];

        if (!evidence.present) {
            result.missing_shards.push_back(shard);
            continue;
        }

        if (evidence.invalid) {
            result.invalidated_shards.push_back(shard);
            continue;
        }

        const std::uint64_t version_before =
            state_->shards[shard].registration_version.load(
                std::memory_order_acquire);

        bool valid = (version_before & 1U) == 0 &&
                     version_before == evidence.registration_version;

        if (valid) {
            for (const detail::SlotObservation& observation :
                 evidence.observations) {
                const std::uint64_t incarnation =
                    state_->records[observation.thread_index].incarnation.load(
                        std::memory_order_acquire);
                if (incarnation != observation.incarnation) {
                    valid = false;
                    break;
                }

                const detail::Slot::Raw raw =
                    state_->slots[observation.thread_index].read();
                if (raw.version != observation.slot_version ||
                    raw.occupied != observation.occupied ||
                    raw.object != observation.object) {
                    valid = false;
                    break;
                }
            }
        }

        const std::uint64_t version_after =
            state_->shards[shard].registration_version.load(
                std::memory_order_acquire);
        if (version_before != version_after ||
            (version_after & 1U) != 0) {
            valid = false;
        }

        if (!valid) {
            evidence.invalid = true;
            result.invalidated_shards.push_back(shard);
            continue;
        }

        shard_valid[shard] = true;
    }

    auto unique_shards = [](std::vector<std::size_t>& values) {
        std::sort(values.begin(), values.end());
        values.erase(std::unique(values.begin(), values.end()),
                     values.end());
    };
    unique_shards(result.invalidated_shards);
    unique_shards(result.missing_shards);

    std::vector<std::shared_ptr<detail::BatchRecord>> batches;
    {
        std::lock_guard<std::mutex> lock(state_->batches_mutex);
        batches = state_->batches;
    }

    const bool all_shards_usable =
        result.invalidated_shards.empty() &&
        result.missing_shards.empty();

    std::vector<std::size_t> stale_shards;

    for (const std::shared_ptr<detail::BatchRecord>& batch : batches) {
        if (batch->issued.load(std::memory_order_acquire) != 0) {
            result.already_issued_batches.push_back(batch->id);
            continue;
        }

        bool stale = false;
        if (all_shards_usable) {
            for (std::size_t shard = 0;
                 shard < state_->shard_count; ++shard) {
                if (!shard_valid[shard]) {
                    stale = true;
                    break;
                }
                const detail::ShardEvidence& evidence =
                    session.state_->shards[shard];
                if (evidence.scan_publication <
                    batch->removal_publication) {
                    stale = true;
                    append_unique(stale_shards, shard);
                }
            }
        }

        if (!all_shards_usable || stale) {
            result.deferred_batches.push_back(batch->id);
            result.deferred.insert(result.deferred.end(),
                                   batch->objects.begin(),
                                   batch->objects.end());
            continue;
        }

        bool protected_object = false;
        for (std::size_t shard = 0;
             shard < state_->shard_count && !protected_object; ++shard) {
            for (const detail::SlotObservation& observation :
                 session.state_->shards[shard].observations) {
                if (!observation.occupied) {
                    continue;
                }
                if (contains(batch->objects, observation.object)) {
                    protected_object = true;
                    break;
                }
            }
        }

        if (protected_object) {
            result.deferred_batches.push_back(batch->id);
            result.protected_batches.push_back(batch->id);
            result.deferred.insert(result.deferred.end(),
                                   batch->objects.begin(),
                                   batch->objects.end());
            continue;
        }

        std::uint8_t expected = 0;
        if (!batch->issued.compare_exchange_strong(
                expected, 1, std::memory_order_acq_rel,
                std::memory_order_acquire)) {
            result.already_issued_batches.push_back(batch->id);
            continue;
        }

        result.permitted_batches.push_back(batch->id);
        result.permitted.insert(result.permitted.end(),
                                batch->objects.begin(),
                                batch->objects.end());
        Permit permit(batch->id, batch->objects);
        result.permits.push_back(std::move(permit));
    }

    unique_shards(stale_shards);
    result.stale_shards = std::move(stale_shards);
    return result;
}

std::vector<BatchSnapshot> Authority::batch_snapshot() const {
    std::vector<std::shared_ptr<detail::BatchRecord>> batches;
    {
        std::lock_guard<std::mutex> lock(state_->batches_mutex);
        batches = state_->batches;
    }

    std::vector<BatchSnapshot> snapshots;
    snapshots.reserve(batches.size());
    for (const std::shared_ptr<detail::BatchRecord>& batch : batches) {
        BatchSnapshot snapshot;
        snapshot.id = batch->id;
        snapshot.objects = batch->objects;
        snapshot.removal_publication = batch->removal_publication;
        snapshot.issued =
            batch->issued.load(std::memory_order_acquire) != 0;
        snapshots.push_back(std::move(snapshot));
    }
    return snapshots;
}

AuthoritySnapshot Authority::snapshot_for_reference() const {
    AuthoritySnapshot snapshot;
    snapshot.root = state_->read_root();

    snapshot.live_threads.reserve(state_->max_threads);
    for (std::size_t index = 0; index < state_->max_threads; ++index) {
        const std::uint64_t incarnation =
            state_->records[index].incarnation.load(
                std::memory_order_acquire);
        if (incarnation == 0) {
            continue;
        }

        const detail::Slot::Raw raw = state_->slots[index].read();
        ThreadSlotSnapshot thread;
        thread.thread_index = static_cast<std::uint32_t>(index);
        thread.incarnation = incarnation;
        thread.slot_version = raw.version;
        thread.occupied = raw.occupied;
        thread.object = raw.object;
        snapshot.live_threads.push_back(thread);
    }

    snapshot.batches = batch_snapshot();
    return snapshot;
}

}  // namespace index_reclaim

#include "netsec.hpp"

namespace netsec {
namespace {

// The assembler needs no per-block fingerprint table: accepted blocks are
// contiguous and fixed-size (except the final block), so a retransmitted
// sequence can be compared directly with its slice of the assembled body.
}  // namespace

BlockTransfer::BlockTransfer(BlockTransferOptions options)
    : options_(std::move(options)), clock_(options_.clock) {}

BlockTransfer::SlotKey BlockTransfer::slot_of(const BlockKey& key) {
    return {key.requester.session_id, key.requester.endpoint, key.token};
}

TimePoint BlockTransfer::now_locked() const {
    return clock_ ? clock_() : std::chrono::steady_clock::now();
}

bool BlockTransfer::expired_locked(const State& state, TimePoint now) const {
    if (options_.ttl.count() <= 0) {
        return false;
    }
    return now >= state.last_activity &&
           (now - state.last_activity) >= options_.ttl;
}

void BlockTransfer::sweep_locked(TimePoint now) {
    for (auto it = states_.begin(); it != states_.end();) {
        if (expired_locked(it->second, now)) {
            it = states_.erase(it);
        } else {
            ++it;
        }
    }
}

bool BlockTransfer::evict_oldest_locked() {
    if (states_.empty()) {
        return false;
    }
    auto oldest = states_.begin();
    for (auto it = states_.begin(); it != states_.end(); ++it) {
        if (it->second.last_activity < oldest->second.last_activity) {
            oldest = it;
        }
    }
    states_.erase(oldest);
    return true;
}

std::optional<std::uint64_t> BlockTransfer::etag_length_locked(
    const Bytes& etag) const {
    const auto it = etag_lengths_.find(etag);
    if (it == etag_lengths_.end()) {
        return std::nullopt;
    }
    return it->second;
}

BlockResult BlockTransfer::start_locked(const Block& block, TimePoint now,
                                        bool restarted) {
    BlockResult result;
    result.restarted = restarted;

    if (block.key.block_size == 0U) {
        result.status = BlockStatus::InvalidBlockSize;
        return result;
    }
    if (block.sequence != block.key.start_sequence) {
        result.status = BlockStatus::SequenceGap;
        return result;
    }
    if (block.key.etag && block.etag != *block.key.etag) {
        result.status = BlockStatus::EtagMismatch;
        return result;
    }
    if (options_.max_transfers > 0U &&
        states_.size() >= options_.max_transfers && !evict_oldest_locked()) {
        result.status = BlockStatus::CapacityExceeded;
        return result;
    }

    State state;
    state.key = block.key;
    state.etag = block.etag;
    state.next_sequence = block.key.start_sequence;
    state.last_activity = now;

    std::optional<std::uint64_t> expected = block.key.total_length;
    const auto bound_length = etag_length_locked(state.etag);
    if (bound_length) {
        if (expected && *expected != *bound_length) {
            result.status = BlockStatus::LengthMismatch;
            return result;
        }
        expected = bound_length;
    }
    state.expected_length = expected;

    if (block.payload.size() > block.key.block_size) {
        result.status = BlockStatus::InvalidPayload;
        return result;
    }
    if (block.more && block.payload.size() != block.key.block_size) {
        result.status = BlockStatus::InvalidPayload;
        return result;
    }
    if (block.payload.size() > options_.max_body_bytes) {
        result.status = BlockStatus::LengthMismatch;
        return result;
    }
    if (expected && block.payload.size() > *expected) {
        result.status = BlockStatus::LengthMismatch;
        return result;
    }
    if (block.more && expected && block.payload.size() >= *expected) {
        result.status = BlockStatus::LengthMismatch;
        return result;
    }
    if (!block.more) {
        if (options_.require_expected_length && !expected) {
            result.status = BlockStatus::MissingExpectedLength;
            return result;
        }
        if (expected && block.payload.size() != *expected) {
            result.status = BlockStatus::LengthMismatch;
            return result;
        }
    }

    state.body = block.payload;
    state.next_sequence = block.sequence + 1U;
    state.complete = !block.more;

    result.status = state.complete ? BlockStatus::Complete
                                   : BlockStatus::Accepted;
    result.next_sequence = state.next_sequence;
    result.received_bytes = state.body.size();
    result.expected_length = state.expected_length;
    if (state.complete) {
        result.body = state.body;
    }

    states_.emplace(slot_of(block.key), std::move(state));
    return result;
}

BlockResult BlockTransfer::discard_locked(StateIterator state,
                                          const Block& block,
                                          TimePoint /*now*/) {
    BlockResult result;
    result.next_sequence = state->second.next_sequence;
    result.received_bytes = state->second.body.size();
    result.expected_length = state->second.expected_length;

    const auto& current = state->second;
    if (block.sequence < current.key.start_sequence) {
        result.status = BlockStatus::Stale;
        return result;
    }
    if (current.body.empty()) {
        result.status = (block.sequence == current.key.start_sequence &&
                         block.payload.empty() && !block.more &&
                         block.etag == current.etag)
                            ? BlockStatus::Duplicate
                            : BlockStatus::Stale;
        return result;
    }

    const std::uint64_t relative =
        static_cast<std::uint64_t>(block.sequence - current.key.start_sequence);
    const std::uint64_t offset64 = relative * current.key.block_size;
    if (offset64 >= current.body.size()) {
        result.status = BlockStatus::Stale;
        return result;
    }
    const std::size_t offset = static_cast<std::size_t>(offset64);
    const std::size_t stored_length = std::min<std::size_t>(
        current.key.block_size, current.body.size() - offset);
    if (block.payload.size() != stored_length) {
        result.status = BlockStatus::ConflictingDuplicate;
        return result;
    }

    const bool same_payload =
        std::equal(block.payload.begin(), block.payload.end(),
                   current.body.begin() + static_cast<std::ptrdiff_t>(offset));
    const bool was_last_accepted =
        block.sequence + 1U == current.next_sequence;
    const bool expected_more =
        was_last_accepted ? !current.complete : true;
    const bool same_more = block.more == expected_more;
    result.status = (same_payload && same_more && block.etag == current.etag)
                        ? BlockStatus::Duplicate
                        : BlockStatus::ConflictingDuplicate;
    return result;
}

BlockResult BlockTransfer::append_locked(StateIterator state,
                                         const Block& block, TimePoint now) {
    auto& current = state->second;
    BlockResult result;
    result.next_sequence = current.next_sequence;
    result.received_bytes = current.body.size();
    result.expected_length = current.expected_length;

    if (block.sequence < current.next_sequence) {
        return discard_locked(state, block, now);
    }
    if (block.sequence > current.next_sequence) {
        result.status = BlockStatus::SequenceGap;
        return result;
    }
    if (block.etag != current.etag) {
        states_.erase(state);
        result.status = BlockStatus::EtagMismatch;
        result.state_reset = true;
        return result;
    }

    if (block.payload.size() > current.key.block_size) {
        result.status = BlockStatus::InvalidPayload;
        return result;
    }
    if (block.more && block.payload.size() != current.key.block_size) {
        result.status = BlockStatus::InvalidPayload;
        return result;
    }

    const std::uint64_t prospective =
        static_cast<std::uint64_t>(current.body.size()) +
        static_cast<std::uint64_t>(block.payload.size());
    if (prospective > options_.max_body_bytes) {
        states_.erase(state);
        result.status = BlockStatus::LengthMismatch;
        result.state_reset = true;
        return result;
    }
    if (current.expected_length && prospective > *current.expected_length) {
        states_.erase(state);
        result.status = BlockStatus::LengthMismatch;
        result.state_reset = true;
        return result;
    }
    if (block.more && current.expected_length &&
        prospective >= *current.expected_length) {
        states_.erase(state);
        result.status = BlockStatus::LengthMismatch;
        result.state_reset = true;
        return result;
    }
    if (!block.more) {
        if (options_.require_expected_length && !current.expected_length) {
            states_.erase(state);
            result.status = BlockStatus::MissingExpectedLength;
            result.state_reset = true;
            return result;
        }
        if (current.expected_length &&
            prospective != *current.expected_length) {
            states_.erase(state);
            result.status = BlockStatus::LengthMismatch;
            result.state_reset = true;
            return result;
        }
    }

    current.body.insert(current.body.end(), block.payload.begin(),
                        block.payload.end());
    current.next_sequence = block.sequence + 1U;
    current.last_activity = now;
    current.complete = !block.more;

    result.status = current.complete ? BlockStatus::Complete
                                     : BlockStatus::Accepted;
    result.next_sequence = current.next_sequence;
    result.received_bytes = current.body.size();
    result.expected_length = current.expected_length;
    if (current.complete) {
        result.body = current.body;
    }
    return result;
}

BlockResult BlockTransfer::receive(const Block& block) {
    BlockResult result;
    if (block.key.block_size == 0U) {
        result.status = BlockStatus::InvalidBlockSize;
        return result;
    }

    std::lock_guard<std::mutex> lock(mutex_);
    const TimePoint now = now_locked();
    const SlotKey slot = slot_of(block.key);

    bool expired = false;
    auto it = states_.find(slot);
    if (it != states_.end() && expired_locked(it->second, now)) {
        states_.erase(it);
        it = states_.end();
        expired = true;
    }
    sweep_locked(now);
    it = states_.find(slot);

    if (it == states_.end()) {
        if (block.sequence != block.key.start_sequence) {
            result.status =
                expired ? BlockStatus::Expired : BlockStatus::SequenceGap;
            return result;
        }
        return start_locked(block, now, false);
    }

    if (!it->second.key.same_binding(block.key)) {
        const bool can_restart =
            options_.restart_on_context_mismatch &&
            block.sequence == block.key.start_sequence;
        states_.erase(it);
        if (can_restart) {
            return start_locked(block, now, true);
        }
        result.status = BlockStatus::ContextMismatch;
        result.state_reset = true;
        return result;
    }

    if (block.key.total_length && it->second.expected_length &&
        *block.key.total_length != *it->second.expected_length) {
        states_.erase(it);
        result.status = BlockStatus::LengthMismatch;
        result.state_reset = true;
        return result;
    }
    if (!it->second.expected_length && block.key.total_length) {
        it->second.expected_length = block.key.total_length;
    }

    if (it->second.complete) {
        if (block.sequence == block.key.start_sequence) {
            const BlockResult duplicate = discard_locked(it, block, now);
            if (duplicate.status == BlockStatus::Duplicate) {
                return duplicate;
            }
            states_.erase(it);
            return start_locked(block, now, true);
        }
        return discard_locked(it, block, now);
    }

    return append_locked(it, block, now);
}

bool BlockTransfer::has_transfer(const BlockKey& key) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = states_.find(slot_of(key));
    if (it == states_.end() || !it->second.key.same_binding(key)) {
        return false;
    }
    if (key.etag && *key.etag != it->second.etag) {
        return false;
    }
    return !key.total_length || !it->second.expected_length ||
           *key.total_length == *it->second.expected_length;
}

bool BlockTransfer::is_complete(const BlockKey& key) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = states_.find(slot_of(key));
    if (it == states_.end() || !it->second.complete ||
        !it->second.key.same_binding(key)) {
        return false;
    }
    if (key.etag && *key.etag != it->second.etag) {
        return false;
    }
    return !key.total_length || !it->second.expected_length ||
           *key.total_length == *it->second.expected_length;
}

std::optional<Bytes> BlockTransfer::body(const BlockKey& key) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = states_.find(slot_of(key));
    if (it == states_.end() || !it->second.complete ||
        !it->second.key.same_binding(key)) {
        return std::nullopt;
    }
    if (key.etag && *key.etag != it->second.etag) {
        return std::nullopt;
    }
    if (key.total_length && it->second.expected_length &&
        *key.total_length != *it->second.expected_length) {
        return std::nullopt;
    }
    return it->second.body;
}

std::optional<std::uint32_t> BlockTransfer::next_sequence(
    const BlockKey& key) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = states_.find(slot_of(key));
    if (it == states_.end() || !it->second.key.same_binding(key)) {
        return std::nullopt;
    }
    return it->second.next_sequence;
}

bool BlockTransfer::reset(const Requester& requester, const Token& token) {
    std::lock_guard<std::mutex> lock(mutex_);
    return states_.erase({requester.session_id, requester.endpoint, token}) >
           0U;
}

void BlockTransfer::reset_all() {
    std::lock_guard<std::mutex> lock(mutex_);
    states_.clear();
}

std::size_t BlockTransfer::active_transfers() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return states_.size();
}

std::size_t BlockTransfer::sweep_expired() {
    std::lock_guard<std::mutex> lock(mutex_);
    const std::size_t before = states_.size();
    sweep_locked(now_locked());
    return before - states_.size();
}

void BlockTransfer::register_etag(Bytes etag, std::uint64_t total_length) {
    std::lock_guard<std::mutex> lock(mutex_);
    etag_lengths_[std::move(etag)] = total_length;
}

void BlockTransfer::unregister_etag(const Bytes& etag) {
    std::lock_guard<std::mutex> lock(mutex_);
    etag_lengths_.erase(etag);
}

std::optional<std::uint64_t> BlockTransfer::etag_length(
    const Bytes& etag) const {
    std::lock_guard<std::mutex> lock(mutex_);
    return etag_length_locked(etag);
}

void BlockTransfer::observe(const ObservationKey& key) {
    std::lock_guard<std::mutex> lock(mutex_);
    observations_[key] = {now_locked()};
}

bool BlockTransfer::cancel_observation(const ObservationKey& key) {
    std::lock_guard<std::mutex> lock(mutex_);
    return observations_.erase(key) > 0U;
}

bool BlockTransfer::is_observed(const ObservationKey& key) const {
    std::lock_guard<std::mutex> lock(mutex_);
    return observations_.find(key) != observations_.end();
}

std::size_t BlockTransfer::observation_count() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return observations_.size();
}

void BlockTransfer::clear_observations() {
    std::lock_guard<std::mutex> lock(mutex_);
    observations_.clear();
}

BlockServer::BlockServer(BlockServerOptions options)
    : options_(std::move(options)), clock_(options_.clock) {}

BlockServer::SlotKey BlockServer::slot_of(const BlockKey& key) {
    return {key.requester.session_id, key.requester.endpoint, key.token};
}

TimePoint BlockServer::now_locked() const {
    return clock_ ? clock_() : std::chrono::steady_clock::now();
}

bool BlockServer::expired_locked(const State& state, TimePoint now) const {
    if (options_.ttl.count() <= 0) {
        return false;
    }
    return now >= state.last_activity &&
           (now - state.last_activity) >= options_.ttl;
}

void BlockServer::sweep_locked(TimePoint now) {
    for (auto it = states_.begin(); it != states_.end();) {
        if (expired_locked(it->second, now)) {
            it = states_.erase(it);
        } else {
            ++it;
        }
    }
}

bool BlockServer::evict_oldest_locked() {
    if (states_.empty()) {
        return false;
    }
    auto oldest = states_.begin();
    for (auto it = states_.begin(); it != states_.end(); ++it) {
        if (it->second.last_activity < oldest->second.last_activity) {
            oldest = it;
        }
    }
    states_.erase(oldest);
    return true;
}

BlockReply BlockServer::serve_from_locked(StateIterator state,
                                          std::uint32_t sequence,
                                          bool duplicate) {
    auto& current = state->second;
    BlockReply reply;
    reply.sequence = sequence;
    reply.block_size = current.key.block_size;
    reply.etag = current.etag;
    reply.total_length = current.body.size();
    reply.next_sequence = current.next_sequence;

    if (sequence < current.key.start_sequence) {
        reply.status = BlockStatus::Stale;
        return reply;
    }
    // CoAP block numbers are absolute: sequence N starts at N * block_size.
    const std::uint64_t offset64 =
        static_cast<std::uint64_t>(sequence) * current.key.block_size;
    if (offset64 > current.body.size()) {
        reply.status = BlockStatus::Stale;
        return reply;
    }
    const std::size_t offset = static_cast<std::size_t>(offset64);

    if (current.body.empty()) {
        if (sequence != current.key.start_sequence) {
            reply.status = BlockStatus::Stale;
            return reply;
        }
        reply.offset = 0;
        reply.more = false;
        if (!duplicate) {
            current.next_sequence = sequence + 1U;
            current.complete = true;
            current.last_activity = now_locked();
        }
        reply.next_sequence = current.next_sequence;
        reply.status =
            duplicate ? BlockStatus::Duplicate : BlockStatus::Complete;
        return reply;
    }

    if (offset >= current.body.size()) {
        reply.status = BlockStatus::Stale;
        return reply;
    }

    const std::size_t length = std::min<std::size_t>(
        current.key.block_size, current.body.size() - offset);
    reply.payload.assign(
        current.body.begin() + static_cast<std::ptrdiff_t>(offset),
        current.body.begin() +
            static_cast<std::ptrdiff_t>(offset + length));
    reply.offset = offset;
    reply.more = (offset + length) < current.body.size();

    if (!duplicate) {
        current.next_sequence = sequence + 1U;
        current.complete = !reply.more;
        current.last_activity = now_locked();
    }
    reply.next_sequence = current.next_sequence;

    reply.status = duplicate ? BlockStatus::Duplicate
                             : (reply.more ? BlockStatus::Accepted
                                           : BlockStatus::Complete);
    return reply;
}

BlockReply BlockServer::start_and_serve_locked(
    const BlockRequest& request, const Representation& representation,
    TimePoint now, bool restarted) {
    BlockReply reply;
    if (request.key.block_size == 0U) {
        reply.status = BlockStatus::InvalidBlockSize;
        return reply;
    }
    if (request.sequence != request.key.start_sequence) {
        reply.status = BlockStatus::SequenceGap;
        return reply;
    }
    if (options_.max_transfers > 0U &&
        states_.size() >= options_.max_transfers && !evict_oldest_locked()) {
        reply.status = BlockStatus::CapacityExceeded;
        return reply;
    }

    State state;
    state.key = request.key;
    state.body = representation.payload;
    state.etag = representation.etag;
    state.next_sequence = request.key.start_sequence;
    state.complete = false;
    state.last_activity = now;

    const SlotKey slot = slot_of(request.key);
    const auto inserted = states_.emplace(slot, std::move(state));
    reply = serve_from_locked(inserted.first, request.sequence, false);
    reply.restarted = restarted;
    return reply;
}

BlockReply BlockServer::serve(const BlockRequest& request,
                              const Representation& representation) {
    BlockReply reply;
    if (request.key.block_size == 0U) {
        reply.status = BlockStatus::InvalidBlockSize;
        return reply;
    }
    if (request.key.etag && representation.etag != *request.key.etag) {
        reply.status = BlockStatus::EtagMismatch;
        return reply;
    }
    if (request.key.content_format &&
        representation.content_format != request.key.content_format) {
        reply.status = BlockStatus::ContextMismatch;
        return reply;
    }
    if (request.key.total_length &&
        representation.payload.size() != *request.key.total_length) {
        reply.status = BlockStatus::LengthMismatch;
        return reply;
    }
    if (request.etag && *request.etag != representation.etag) {
        reply.status = BlockStatus::EtagMismatch;
        return reply;
    }

    std::lock_guard<std::mutex> lock(mutex_);
    const TimePoint now = now_locked();
    const SlotKey slot = slot_of(request.key);

    bool expired = false;
    auto it = states_.find(slot);
    if (it != states_.end() && expired_locked(it->second, now)) {
        states_.erase(it);
        it = states_.end();
        expired = true;
    }
    sweep_locked(now);
    it = states_.find(slot);

    if (it == states_.end()) {
        if (request.sequence != request.key.start_sequence) {
            reply.status =
                expired ? BlockStatus::Expired : BlockStatus::SequenceGap;
            return reply;
        }
        return start_and_serve_locked(request, representation, now, false);
    }

    auto& current = it->second;
    if (!current.key.same_binding(request.key)) {
        const bool can_restart =
            options_.restart_on_context_mismatch &&
            request.sequence == request.key.start_sequence;
        states_.erase(it);
        if (can_restart) {
            return start_and_serve_locked(request, representation, now, true);
        }
        reply.status = BlockStatus::ContextMismatch;
        return reply;
    }
    if (request.key.total_length && current.key.total_length &&
        *request.key.total_length != *current.key.total_length) {
        states_.erase(it);
        reply.status = BlockStatus::LengthMismatch;
        return reply;
    }

    if (representation.etag != current.etag) {
        const bool can_restart =
            options_.restart_on_context_mismatch &&
            request.sequence == request.key.start_sequence;
        states_.erase(it);
        if (can_restart) {
            return start_and_serve_locked(request, representation, now, true);
        }
        reply.status = BlockStatus::EtagMismatch;
        return reply;
    }

    if (current.complete &&
        request.sequence == request.key.start_sequence) {
        states_.erase(it);
        return start_and_serve_locked(request, representation, now, true);
    }

    if (request.sequence > current.next_sequence) {
        reply.status = BlockStatus::SequenceGap;
        reply.next_sequence = current.next_sequence;
        return reply;
    }

    const bool duplicate = request.sequence < current.next_sequence;
    return serve_from_locked(it, request.sequence, duplicate);
}

bool BlockServer::reset(const Requester& requester, const Token& token) {
    std::lock_guard<std::mutex> lock(mutex_);
    return states_.erase({requester.session_id, requester.endpoint, token}) >
           0U;
}

void BlockServer::reset_all() {
    std::lock_guard<std::mutex> lock(mutex_);
    states_.clear();
}

std::size_t BlockServer::active_transfers() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return states_.size();
}

std::size_t BlockServer::sweep_expired() {
    std::lock_guard<std::mutex> lock(mutex_);
    const std::size_t before = states_.size();
    sweep_locked(now_locked());
    return before - states_.size();
}

}  // namespace netsec

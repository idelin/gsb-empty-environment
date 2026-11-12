#include "netsec.hpp"

#include <algorithm>
#include <limits>

namespace netsec {

namespace {

bool checked_add_u64(std::uint64_t lhs, std::uint64_t rhs, std::uint64_t& out) {
    if (rhs > std::numeric_limits<std::uint64_t>::max() - lhs) {
        return false;
    }
    out = lhs + rhs;
    return true;
}

} // namespace

FixedBufferLeaseManager::FixedBufferLeaseManager() : next_lease_id_(1) {}

FixedBufferLeaseManager::~FixedBufferLeaseManager() = default;

LeaseError FixedBufferLeaseManager::register_buffer(BufIndex index, std::byte* data,
                                                    std::size_t size, TenantId owner) {
    if (size != 0 && data == nullptr) {
        return LeaseError::InvalidResult;
    }
    std::lock_guard<std::mutex> lock(mutex_);
    if (buffers_.find(index) != buffers_.end()) {
        return LeaseError::DuplicateBufIndex;
    }
    buffers_.emplace(index, BufferEntry{data, size, owner, 0});
    return LeaseError::Ok;
}

LeaseError FixedBufferLeaseManager::update_buffer(BufIndex index, std::byte* data,
                                                  std::size_t size, TenantId tenant) {
    if (size != 0 && data == nullptr) {
        return LeaseError::InvalidResult;
    }
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = buffers_.find(index);
    if (it == buffers_.end()) {
        return LeaseError::InvalidBufIndex;
    }
    if (it->second.owner != tenant) {
        return LeaseError::AccessDenied;
    }
    if (it->second.in_flight_count != 0) {
        return LeaseError::BufferBusy;
    }
    it->second.data = data;
    it->second.size = size;
    return LeaseError::Ok;
}

LeaseError FixedBufferLeaseManager::unregister_buffer(BufIndex index, TenantId tenant) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = buffers_.find(index);
    if (it == buffers_.end()) {
        return LeaseError::InvalidBufIndex;
    }
    if (it->second.owner != tenant) {
        return LeaseError::AccessDenied;
    }
    if (it->second.in_flight_count != 0) {
        return LeaseError::BufferBusy;
    }
    buffers_.erase(it);
    return LeaseError::Ok;
}

LeaseGrant FixedBufferLeaseManager::acquire(BufIndex index, std::uint64_t offset,
                                            std::uint64_t length, IoOp op,
                                            TenantId tenant,
                                            ISubmissionQueue& queue) {
    LeaseId lease_id = 0;
    BufferEntry* buffer = nullptr;

    {
        std::lock_guard<std::mutex> lock(mutex_);
        auto it = buffers_.find(index);
        if (it == buffers_.end()) {
            return LeaseGrant{LeaseError::InvalidBufIndex, 0, IoDescriptor{}};
        }
        if (it->second.owner != tenant) {
            return LeaseGrant{LeaseError::AccessDenied, 0, IoDescriptor{}};
        }

        std::uint64_t end = 0;
        if (!checked_add_u64(offset, length, end)) {
            return LeaseGrant{LeaseError::OffsetOverflow, 0, IoDescriptor{}};
        }
        if (offset > it->second.size) {
            return LeaseGrant{LeaseError::OutOfRange, 0, IoDescriptor{}};
        }
        if (end > static_cast<std::uint64_t>(it->second.size)) {
            return LeaseGrant{LeaseError::OutOfRange, 0, IoDescriptor{}};
        }

        lease_id = next_lease_id_++;
        it->second.in_flight_count++;
        buffer = &it->second;
        leases_.emplace(lease_id,
                        LeaseEntry{lease_id, index, offset, length, op, tenant, false});
    }

    IoDescriptor descriptor{index, offset, length, op, lease_id};
    if (!queue.submit(descriptor)) {
        std::lock_guard<std::mutex> lock(mutex_);
        buffer->in_flight_count--;
        leases_.erase(lease_id);
        return LeaseGrant{LeaseError::QueueRejected, 0, IoDescriptor{}};
    }

    return LeaseGrant{LeaseError::Ok, lease_id, descriptor};
}

LeaseError FixedBufferLeaseManager::complete(const CompletionEvent& event,
                                             TenantId tenant) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto lease_it = leases_.find(event.lease_id);
    if (lease_it == leases_.end()) {
        return LeaseError::UnknownLease;
    }
    LeaseEntry& lease = lease_it->second;
    if (lease.tenant != tenant) {
        return LeaseError::AccessDenied;
    }
    if (lease.completed) {
        return LeaseError::AlreadyCompleted;
    }

    auto buffer_it = buffers_.find(lease.buf_index);
    if (buffer_it == buffers_.end()) {
        return LeaseError::InvalidBufIndex;
    }
    BufferEntry& buffer = buffer_it->second;

    if (event.result < 0 ||
        static_cast<std::uint64_t>(event.result) > lease.length) {
        return LeaseError::InvalidResult;
    }

    const std::uint64_t received = static_cast<std::uint64_t>(event.result);
    if (lease.op == IoOp::ReadFixed && received < lease.length) {
        std::uint64_t tail_end = 0;
        if (!checked_add_u64(lease.offset, lease.length, tail_end)) {
            return LeaseError::OffsetOverflow;
        }
        const std::uint64_t tail_start = lease.offset + received;
        if (tail_end <= static_cast<std::uint64_t>(buffer.size) &&
            tail_start <= tail_end) {
            std::fill(buffer.data + tail_start, buffer.data + tail_end,
                      std::byte{0});
        }
    }

    lease.completed = true;
    if (buffer.in_flight_count > 0) {
        buffer.in_flight_count--;
    }
    return LeaseError::Ok;
}

LeaseError FixedBufferLeaseManager::release(LeaseId lease_id, TenantId tenant) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = leases_.find(lease_id);
    if (it == leases_.end()) {
        return LeaseError::UnknownLease;
    }
    if (it->second.tenant != tenant) {
        return LeaseError::AccessDenied;
    }
    if (!it->second.completed) {
        return LeaseError::LeaseStillActive;
    }
    leases_.erase(it);
    return LeaseError::Ok;
}

bool FixedBufferLeaseManager::is_registered(BufIndex index) const {
    std::lock_guard<std::mutex> lock(mutex_);
    return buffers_.find(index) != buffers_.end();
}

std::size_t FixedBufferLeaseManager::in_flight(BufIndex index) const {
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = buffers_.find(index);
    if (it == buffers_.end()) {
        return 0;
    }
    return it->second.in_flight_count;
}

const char* to_string(LeaseError error) {
    switch (error) {
    case LeaseError::Ok:
        return "ok";
    case LeaseError::InvalidBufIndex:
        return "invalid buffer index";
    case LeaseError::AccessDenied:
        return "access denied";
    case LeaseError::OffsetOverflow:
        return "offset plus length overflows";
    case LeaseError::OutOfRange:
        return "range outside registered buffer";
    case LeaseError::BufferBusy:
        return "buffer has in-flight leases";
    case LeaseError::DuplicateBufIndex:
        return "buffer index already registered";
    case LeaseError::UnknownLease:
        return "unknown lease";
    case LeaseError::AlreadyCompleted:
        return "lease already completed";
    case LeaseError::LeaseStillActive:
        return "lease still in flight";
    case LeaseError::InvalidResult:
        return "invalid completion result";
    case LeaseError::QueueRejected:
        return "submission queue rejected descriptor";
    }
    return "unknown error";
}

} // namespace netsec

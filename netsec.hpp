#pragma once

#include <cstddef>
#include <cstdint>
#include <mutex>
#include <unordered_map>

namespace netsec {

using TenantId = std::uint64_t;
using BufIndex = std::uint16_t;
using LeaseId = std::uint64_t;

enum class IoOp {
    ReadFixed,
    WriteFixed,
};

enum class LeaseError {
    Ok = 0,
    InvalidBufIndex,
    AccessDenied,
    OffsetOverflow,
    OutOfRange,
    BufferBusy,
    DuplicateBufIndex,
    UnknownLease,
    AlreadyCompleted,
    LeaseStillActive,
    InvalidResult,
    QueueRejected,
};

struct IoDescriptor {
    BufIndex buf_index;
    std::uint64_t offset;
    std::uint64_t length;
    IoOp op;
    LeaseId lease_id;
};

struct CompletionEvent {
    LeaseId lease_id;
    std::int64_t result;
};

struct LeaseGrant {
    LeaseError error;
    LeaseId lease_id;
    IoDescriptor descriptor;

    explicit operator bool() const { return error == LeaseError::Ok; }
};

class ISubmissionQueue {
public:
    virtual ~ISubmissionQueue() = default;
    virtual bool submit(const IoDescriptor& descriptor) = 0;
};

class FixedBufferLeaseManager {
public:
    FixedBufferLeaseManager();
    ~FixedBufferLeaseManager();

    FixedBufferLeaseManager(const FixedBufferLeaseManager&) = delete;
    FixedBufferLeaseManager& operator=(const FixedBufferLeaseManager&) = delete;

    LeaseError register_buffer(BufIndex index, std::byte* data, std::size_t size,
                               TenantId owner);
    LeaseError update_buffer(BufIndex index, std::byte* data, std::size_t size,
                             TenantId tenant);
    LeaseError unregister_buffer(BufIndex index, TenantId tenant);

    LeaseGrant acquire(BufIndex index, std::uint64_t offset, std::uint64_t length,
                       IoOp op, TenantId tenant, ISubmissionQueue& queue);

    LeaseError complete(const CompletionEvent& event, TenantId tenant);
    LeaseError release(LeaseId lease_id, TenantId tenant);

    bool is_registered(BufIndex index) const;
    std::size_t in_flight(BufIndex index) const;

private:
    struct BufferEntry {
        std::byte* data;
        std::size_t size;
        TenantId owner;
        std::size_t in_flight_count;
    };

    struct LeaseEntry {
        LeaseId id;
        BufIndex buf_index;
        std::uint64_t offset;
        std::uint64_t length;
        IoOp op;
        TenantId tenant;
        bool completed;
    };

    mutable std::mutex mutex_;
    std::unordered_map<BufIndex, BufferEntry> buffers_;
    std::unordered_map<LeaseId, LeaseEntry> leases_;
    LeaseId next_lease_id_;
};

const char* to_string(LeaseError error);

} // namespace netsec

#pragma once

#include "iommu_ats/Types.hpp"

#include <string>
#include <vector>

namespace iommu_ats {

enum class InvalidationKind {
    InvalidateIOTLB,
    InvalidateDeviceATC,
    InvalidationWait,
};

[[nodiscard]] constexpr std::string_view toString(
    InvalidationKind kind) noexcept {
    switch (kind) {
    case InvalidationKind::InvalidateIOTLB:
        return "InvalidateIOTLB";
    case InvalidationKind::InvalidateDeviceATC:
        return "InvalidateDeviceATC";
    case InvalidationKind::InvalidationWait:
        return "InvalidationWait";
    }
    return "Unknown";
}

enum class CommandState {
    Queued,
    Completed,
};

struct InvalidationCommand {
    CommandId id = 0;
    InvalidationKind kind = InvalidationKind::InvalidateIOTLB;
    CommandState state = CommandState::Queued;
    TicketId ticketId = 0;
    DomainId domain = 0;
    Bdf bdf{};
    bool allPasids = true;
    Pasid pasid = 0;
    Iova iovaBase = 0;
    u64 size = 0;
};

class InvalidationQueue {
public:
    InvalidationCommand& submit(InvalidationCommand command);
    [[nodiscard]] InvalidationCommand* find(CommandId id);
    [[nodiscard]] const InvalidationCommand* find(CommandId id) const;
    [[nodiscard]] std::vector<InvalidationCommand> queued() const;
    [[nodiscard]] const std::vector<InvalidationCommand>& commands()
        const noexcept;
    [[nodiscard]] std::string canonicalState() const;

private:
    std::vector<InvalidationCommand> commands_;
    CommandId nextId_ = 1;
};

struct ReclaimTicket {
    TicketId id = 0;
    DomainId domain = 0;
    Pasid pasid = 0;
    Iova iovaBase = 0;
    u64 size = PAGE_SIZE_4K;
    PhysicalAddress physicalBase = 0;
    std::vector<Bdf> associatedBdfs;

    bool unmapped = false;
    bool iotlbIssued = false;
    bool iotlbCompleted = false;
    std::vector<Bdf> atsIssued;
    std::vector<Bdf> atsCompleted;
    bool waitIssued = false;
    bool waitCompleted = false;
    bool released = false;

    [[nodiscard]] bool allAtsCompleted() const;
    [[nodiscard]] bool atsCompletedFor(Bdf bdf) const;
    void markAtsIssued(Bdf bdf);
    void markAtsCompleted(Bdf bdf);
    [[nodiscard]] std::string canonicalState() const;
};

} // namespace iommu_ats

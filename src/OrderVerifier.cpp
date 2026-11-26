#include "iommu_ats/OrderVerifier.hpp"

#include <algorithm>

namespace iommu_ats {

bool AtsOrderVerifier::contains(const std::vector<Bdf>& bdfs, Bdf bdf) {
    return std::find(bdfs.begin(), bdfs.end(), bdf) != bdfs.end();
}

AtsOrderVerifier::State& AtsOrderVerifier::state(TicketId ticketId) {
    return states_[ticketId];
}

const AtsOrderVerifier::State* AtsOrderVerifier::findState(
    TicketId ticketId) const {
    const auto it = states_.find(ticketId);
    return it == states_.end() ? nullptr : &it->second;
}

void AtsOrderVerifier::setAssociatedBdfs(TicketId ticketId,
                                         std::vector<Bdf> bdfs) {
    state(ticketId).expectedBdfs = std::move(bdfs);
}

void AtsOrderVerifier::violate(const OrderEvent& event,
                               const std::string& message) {
    violations_.push_back("ticket " + std::to_string(event.ticketId) +
                          ": " + message);
}

void AtsOrderVerifier::record(const OrderEvent& event) {
    State& current = state(event.ticketId);

    switch (event.type) {
    case OrderEventType::Unmapped:
        current.unmapped = true;
        break;

    case OrderEventType::IotlbIssued:
        if (!current.unmapped) {
            violate(event, "InvalidateIOTLB issued before Unmap");
        }
        current.iotlbIssued = true;
        break;

    case OrderEventType::IotlbCompleted:
        if (!current.iotlbIssued) {
            violate(event, "IOTLB completion without issuance");
        }
        current.iotlbCompleted = true;
        break;

    case OrderEventType::AtsIssued:
        if (!current.iotlbCompleted) {
            violate(event,
                    "InvalidateDeviceATC issued before completed "
                    "InvalidateIOTLB");
        }
        if (!contains(current.expectedBdfs, event.bdf) &&
            !current.expectedBdfs.empty()) {
            violate(event, "ATC invalidation for unexpected BDF " +
                               event.bdf.toString());
        }
        if (!contains(current.atsIssued, event.bdf)) {
            current.atsIssued.push_back(event.bdf);
        }
        break;

    case OrderEventType::AtsCompleted:
        if (!contains(current.atsIssued, event.bdf)) {
            violate(event, "ATC completion without issuance for " +
                               event.bdf.toString());
        }
        if (!contains(current.atsCompleted, event.bdf)) {
            current.atsCompleted.push_back(event.bdf);
        }
        break;

    case OrderEventType::WaitIssued:
        if (!current.iotlbCompleted || current.atsIssued.empty()) {
            violate(event,
                    "InvalidationWait issued before IOTLB and device-ATC "
                    "invalidation commands");
        }
        current.waitIssued = true;
        break;

    case OrderEventType::WaitCompleted: {
        if (!current.waitIssued) {
            violate(event, "InvalidationWait completion without issuance");
        }
        if (!current.iotlbCompleted) {
            violate(event, "wait passed before IOTLB completion");
        }
        for (const Bdf expected : current.expectedBdfs) {
            if (!contains(current.atsCompleted, expected)) {
                violate(event,
                        "wait passed before ATC completion for alias BDF " +
                            expected.toString());
            }
        }
        current.waitCompleted = true;
        break;
    }

    case OrderEventType::ReleaseAttempt:
    case OrderEventType::Released:
        if (!isReleasable(event.ticketId)) {
            violate(event,
                    "physical page release attempted before the complete "
                    "IOTLB/ATC/wait barrier");
        }
        if (event.type == OrderEventType::Released) {
            current.released = true;
        }
        break;
    }
}

bool AtsOrderVerifier::isReleasable(TicketId ticketId) const {
    const State* current = findState(ticketId);
    if (current == nullptr || !current->unmapped ||
        !current->iotlbCompleted || !current->waitCompleted) {
        return false;
    }
    for (const Bdf expected : current->expectedBdfs) {
        if (!contains(current->atsCompleted, expected)) {
            return false;
        }
    }
    return true;
}

const std::vector<std::string>& AtsOrderVerifier::violations()
    const noexcept {
    return violations_;
}

bool AtsOrderVerifier::valid() const noexcept {
    return violations_.empty();
}

} // namespace iommu_ats

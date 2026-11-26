#pragma once

#include "iommu_ats/Types.hpp"

#include <map>
#include <string>
#include <vector>

namespace iommu_ats {

enum class OrderEventType {
    Unmapped,
    IotlbIssued,
    IotlbCompleted,
    AtsIssued,
    AtsCompleted,
    WaitIssued,
    WaitCompleted,
    ReleaseAttempt,
    Released,
};

struct OrderEvent {
    OrderEventType type = OrderEventType::Unmapped;
    TicketId ticketId = 0;
    Bdf bdf{};
};

class AtsOrderVerifier {
public:
    void setAssociatedBdfs(TicketId ticketId, std::vector<Bdf> bdfs);
    void record(const OrderEvent& event);

    [[nodiscard]] bool isReleasable(TicketId ticketId) const;
    [[nodiscard]] const std::vector<std::string>& violations()
        const noexcept;
    [[nodiscard]] bool valid() const noexcept;

private:
    struct State {
        std::vector<Bdf> expectedBdfs;
        bool unmapped = false;
        bool iotlbIssued = false;
        bool iotlbCompleted = false;
        std::vector<Bdf> atsIssued;
        std::vector<Bdf> atsCompleted;
        bool waitIssued = false;
        bool waitCompleted = false;
        bool released = false;
    };

    std::map<TicketId, State> states_;
    std::vector<std::string> violations_;

    [[nodiscard]] State& state(TicketId ticketId);
    [[nodiscard]] const State* findState(TicketId ticketId) const;
    static bool contains(const std::vector<Bdf>& bdfs, Bdf bdf);
    void violate(const OrderEvent& event, const std::string& message);
};

} // namespace iommu_ats

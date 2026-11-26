#include "iommu_ats/Interceptor.hpp"

#include <algorithm>

namespace iommu_ats {

void EventBus::subscribe(std::shared_ptr<IEventInterceptor> interceptor) {
    interceptors_.push_back(std::move(interceptor));
}

void EventBus::emit(EventKind kind, std::string message,
                    std::optional<DmaRequest> dma,
                    std::optional<TicketId> ticketId) {
    SecurityEvent event{++sequence_, kind, std::move(message), std::move(dma),
                        ticketId};
    for (const auto& interceptor : interceptors_) {
        if (interceptor) {
            interceptor->intercept(event);
        }
    }
}

u64 EventBus::eventCount() const noexcept {
    return sequence_;
}

void EventLogInterceptor::intercept(const SecurityEvent& event) {
    events_.push_back(event);
}

const std::vector<SecurityEvent>& EventLogInterceptor::events()
    const noexcept {
    return events_;
}

std::vector<SecurityEvent> EventLogInterceptor::ofKind(
    EventKind kind) const {
    std::vector<SecurityEvent> result;
    for (const SecurityEvent& event : events_) {
        if (event.kind == kind) {
            result.push_back(event);
        }
    }
    return result;
}

void OnlineSafetyInterceptor::intercept(const SecurityEvent& event) {
    if (event.kind == EventKind::SecurityViolation) {
        violations_.push_back(event);
    }
}

const std::vector<SecurityEvent>& OnlineSafetyInterceptor::violations()
    const noexcept {
    return violations_;
}

bool OnlineSafetyInterceptor::sawViolation() const noexcept {
    return !violations_.empty();
}

} // namespace iommu_ats

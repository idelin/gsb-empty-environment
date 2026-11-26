#pragma once

#include "iommu_ats/Types.hpp"

#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace iommu_ats {

enum class EventKind {
    MappingChanged,
    InvalidationIssued,
    InvalidationCompleted,
    WaitBarrier,
    PageReleased,
    PageAllocated,
    DmaAttempt,
    DmaGranted,
    DmaDenied,
    SecurityViolation,
    SafetyBlocked,
};

struct SecurityEvent {
    u64 sequence = 0;
    EventKind kind = EventKind::DmaAttempt;
    std::string message;
    std::optional<DmaRequest> dma;
    std::optional<TicketId> ticketId;
};

class IEventInterceptor {
public:
    virtual ~IEventInterceptor() = default;
    virtual void intercept(const SecurityEvent& event) = 0;
};

using EventInterceptor = IEventInterceptor;

class EventBus {
public:
    EventBus() = default;
    EventBus(const EventBus& other) : sequence_(other.sequence_) {}
    EventBus& operator=(const EventBus& other) {
        if (this != &other) {
            sequence_ = other.sequence_;
        }
        return *this;
    }
    EventBus(EventBus&&) = default;
    EventBus& operator=(EventBus&&) = default;

    void subscribe(std::shared_ptr<IEventInterceptor> interceptor);
    void emit(EventKind kind, std::string message,
              std::optional<DmaRequest> dma = std::nullopt,
              std::optional<TicketId> ticketId = std::nullopt);
    [[nodiscard]] u64 eventCount() const noexcept;

private:
    std::vector<std::shared_ptr<IEventInterceptor>> interceptors_;
    u64 sequence_ = 0;
};

class EventLogInterceptor final : public IEventInterceptor {
public:
    void intercept(const SecurityEvent& event) override;
    [[nodiscard]] const std::vector<SecurityEvent>& events() const noexcept;
    [[nodiscard]] std::vector<SecurityEvent> ofKind(EventKind kind) const;

private:
    std::vector<SecurityEvent> events_;
};

class OnlineSafetyInterceptor final : public IEventInterceptor {
public:
    void intercept(const SecurityEvent& event) override;
    [[nodiscard]] const std::vector<SecurityEvent>& violations()
        const noexcept;
    [[nodiscard]] bool sawViolation() const noexcept;

private:
    std::vector<SecurityEvent> violations_;
};

} // namespace iommu_ats

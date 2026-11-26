#include "iommu_ats/Invalidation.hpp"

#include <algorithm>

namespace iommu_ats {

InvalidationCommand& InvalidationQueue::submit(
    InvalidationCommand command) {
    command.id = nextId_++;
    command.state = CommandState::Queued;
    commands_.push_back(std::move(command));
    return commands_.back();
}

InvalidationCommand* InvalidationQueue::find(CommandId id) {
    const auto it = std::find_if(commands_.begin(), commands_.end(),
                                 [id](const InvalidationCommand& command) {
                                     return command.id == id;
                                 });
    return it == commands_.end() ? nullptr : &*it;
}

const InvalidationCommand* InvalidationQueue::find(CommandId id) const {
    const auto it = std::find_if(commands_.begin(), commands_.end(),
                                 [id](const InvalidationCommand& command) {
                                     return command.id == id;
                                 });
    return it == commands_.end() ? nullptr : &*it;
}

std::vector<InvalidationCommand> InvalidationQueue::queued() const {
    std::vector<InvalidationCommand> result;
    for (const InvalidationCommand& command : commands_) {
        if (command.state == CommandState::Queued) {
            result.push_back(command);
        }
    }
    return result;
}

const std::vector<InvalidationCommand>& InvalidationQueue::commands()
    const noexcept {
    return commands_;
}

std::string InvalidationQueue::canonicalState() const {
    std::string result = "queue[";
    for (const InvalidationCommand& command : commands_) {
        result += "{id=";
        result += std::to_string(command.id);
        result += ",k=";
        result += std::string(toString(command.kind));
        result += ",st=";
        result += command.state == CommandState::Queued ? "q" : "c";
        result += ",t=";
        result += std::to_string(command.ticketId);
        if (command.kind == InvalidationKind::InvalidateDeviceATC) {
            result += ",bdf=";
            result += command.bdf.toString();
        }
        result += "};";
    }
    result += "]";
    return result;
}

bool ReclaimTicket::allAtsCompleted() const {
    for (const Bdf bdf : associatedBdfs) {
        if (!atsCompletedFor(bdf)) {
            return false;
        }
    }
    return !associatedBdfs.empty();
}

bool ReclaimTicket::atsCompletedFor(Bdf bdf) const {
    return std::find(atsCompleted.begin(), atsCompleted.end(), bdf) !=
           atsCompleted.end();
}

void ReclaimTicket::markAtsIssued(Bdf bdf) {
    if (std::find(atsIssued.begin(), atsIssued.end(), bdf) ==
        atsIssued.end()) {
        atsIssued.push_back(bdf);
    }
}

void ReclaimTicket::markAtsCompleted(Bdf bdf) {
    markAtsIssued(bdf);
    if (!atsCompletedFor(bdf)) {
        atsCompleted.push_back(bdf);
    }
}

std::string ReclaimTicket::canonicalState() const {
    std::string result = "ticket{id=" + std::to_string(id) +
                         ",d=" + std::to_string(domain) +
                         ",unmapped=" + (unmapped ? "1" : "0") +
                         ",iotlbI=" + (iotlbIssued ? "1" : "0") +
                         ",iotlbC=" + (iotlbCompleted ? "1" : "0") +
                         ",waitI=" + (waitIssued ? "1" : "0") +
                         ",waitC=" + (waitCompleted ? "1" : "0") +
                         ",released=" + (released ? "1" : "0") + ",dev=[";
    for (const Bdf bdf : associatedBdfs) {
        result += bdf.toString();
        result.push_back(',');
    }
    result += "],atsC=[";
    for (const Bdf bdf : atsCompleted) {
        result += bdf.toString();
        result.push_back(',');
    }
    result += "]}";
    return result;
}

} // namespace iommu_ats

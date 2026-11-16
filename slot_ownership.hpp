#pragma once

#include <cstdint>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

// FlexRay frame-header limits.
constexpr uint16_t kMaxFrameId = 0x7FF;       // 11-bit frame ID / slot number
constexpr uint8_t kMaxCycleCount = 0x3F;      // 6-bit cycle counter (0..63)
constexpr uint8_t kMaxPayloadLength = 254;    // FlexRay static-slot payload limit
constexpr uint8_t kViolationQuarantineThreshold = 3;

using NodeId = std::string;
using PortId = uint16_t;
using SlotNumber = uint16_t;

// One entry of the static-slot table supplied by the caller.
struct SlotConfig {
    SlotNumber slot = 0;              // static slot number (== expected frame ID)
    NodeId owner;                     // node allowed to transmit in this slot
    uint8_t cycleRepetition = 1;      // cycle repetition (1, 2, 4, 8, ..., 64)
    uint8_t baseCycle = 0;            // base cycle offset, in [0, cycleRepetition)
};

// FlexRay frame header fields visible to the bus monitor.
struct FrameHeader {
    uint16_t frameId = 0;             // 11-bit frame ID
    uint8_t payloadLength = 0;        // payload length in bytes (1..254)
    uint8_t cycleCount = 0;           // 6-bit cycle counter carried by the frame
    bool syncIndicator = false;       // sync-frame indication bit
};

enum class Action {
    Accept,   // the frame passes every ownership/phase/sync rule
    Drop      // the frame must be discarded; see DropReason
};

enum class DropReason {
    None,
    MalformedHeader,      // frame-ID / cycle-count / payload-length field out of range
    UnknownPort,          // sending port is not bound to any node
    Quarantined,          // sender already exceeded the violation threshold
    SyncNotRegistered,    // sync bit set by a node outside the sync-node set
    SyncCycleMismatch,    // sync frame cycleCount differs from the global cycle
    UnknownSlot,          // frame ID does not match any configured static slot
    NonOwnerSender,       // sender is not the registered owner of the slot
    WrongCyclePhase,      // owner sent outside its repetition/base-cycle phase
    Conflict              // second frame in the same slot and cycle
};

struct InspectionResult {
    uint64_t sequence = 0;            // per-frame sequence number (0 when dropped)
    Action action = Action::Drop;
    DropReason reason = DropReason::None;
    NodeId sender;                    // node resolved from the sending port
    SlotNumber slot = 0;              // slot implied by the frame ID
};

// SlotOwnershipGuard enforces static-segment transmit ownership for a
// FlexRay gateway. One instance is driven from the bus-monitor callback:
// feed every observed frame together with the port that sent it and the
// current global cycle, then act on the returned verdict.
//
// An accepted frame can still be revoked later in the same cycle when a
// second frame arrives in the same slot. Collect such revoked sequence
// numbers with endCycle() (or via drainRevokedSequences(), which also works
// when the monitor skips endCycle and the cycle counter simply advances).
class SlotOwnershipGuard {
public:
    SlotOwnershipGuard(std::vector<SlotConfig> slotTable,
                       std::vector<std::pair<PortId, NodeId>> portBindings,
                       std::vector<NodeId> syncNodes) {
        for (const SlotConfig& entry : slotTable) {
            if (entry.slot == 0 || entry.slot > kMaxFrameId ||
                entry.cycleRepetition == 0 ||
                entry.baseCycle >= entry.cycleRepetition) {
                continue;
            }
            slots_[entry.slot] = entry;
        }
        for (const auto& binding : portBindings) {
            portOwner_[binding.first] = binding.second;
        }
        for (NodeId& node : syncNodes) {
            syncNodes_.insert(std::move(node));
        }
    }

    // Inspect one observed frame. currentCycle is the global FlexRay cycle
    // counter (0..63). The frame ID identifies the static slot the frame was
    // received in.
    InspectionResult inspect(const FrameHeader& header,
                             PortId senderPort,
                             uint8_t currentCycle) {
        rotateCycleIfNeeded(currentCycle);
        currentCycle_ = currentCycle;

        InspectionResult result;
        result.slot = header.frameId;

        auto portIt = portOwner_.find(senderPort);
        if (portIt == portOwner_.end()) {
            return finishDrop(result, DropReason::UnknownPort);
        }
        result.sender = portIt->second;

        if (!headerWellFormed(header)) {
            return finishDrop(result, DropReason::MalformedHeader);
        }

        if (quarantined_.count(result.sender) != 0) {
            return finishDrop(result, DropReason::Quarantined);
        }

        // Sync frames: only registered sync nodes may set the bit, and the
        // advertised cycle count must equal the global cycle counter. Both
        // offenses count as one violation against the sender.
        if (header.syncIndicator) {
            if (syncNodes_.count(result.sender) == 0) {
                return finishViolation(result, DropReason::SyncNotRegistered);
            }
            if (header.cycleCount != currentCycle) {
                return finishViolation(result, DropReason::SyncCycleMismatch);
            }
        }

        auto slotIt = slots_.find(header.frameId);
        if (slotIt == slots_.end()) {
            return finishDrop(result, DropReason::UnknownSlot);
        }
        const SlotConfig& slot = slotIt->second;

        // Same cycle, same slot: any second frame is a collision, regardless
        // of who sent it. Both frames are dropped and the event is charged
        // to the node owning the slot. A non-zero stored sequence means the
        // first frame was accepted and must now be revoked.
        auto seenIt = slotSeen_.find(slot.slot);
        if (seenIt != slotSeen_.end()) {
            if (seenIt->second != 0) {
                pendingRevocations_.push_back(seenIt->second);
            }
            slotSeen_.erase(seenIt);
            conflicts_[slot.owner] += 1;
            return finishDrop(result, DropReason::Conflict);
        }
        // Reserve the slot; the stored sequence stays 0 until the frame is
        // accepted below (a frame dropped for ownership/phase still occupies
        // the slot for conflict purposes).
        slotSeen_[slot.slot] = 0;

        // Non-owner transmission is always an ownership violation, even when
        // the slot is inactive in this cycle.
        if (result.sender != slot.owner) {
            return finishViolation(result, DropReason::NonOwnerSender);
        }

        // Owner transmitting outside its repetition/base-cycle phase.
        if (currentCycle % slot.cycleRepetition != slot.baseCycle) {
            return finishDrop(result, DropReason::WrongCyclePhase);
        }

        result.sequence = ++nextSequence_;
        result.action = Action::Accept;
        result.reason = DropReason::None;
        slotSeen_[slot.slot] = result.sequence;
        acceptedCount_ += 1;
        return result;
    }

    // Close the current cycle: forget per-slot frame state and return the
    // sequence numbers of previously accepted frames that were revoked by
    // collisions during this cycle. The revoke queue is drained.
    std::vector<uint64_t> endCycle() {
        slotSeen_.clear();
        return drainRevokedSequences();
    }

    // Drain and return accepted-frame sequence numbers revoked by collisions.
    std::vector<uint64_t> drainRevokedSequences() {
        std::vector<uint64_t> revoked;
        revoked.swap(pendingRevocations_);
        return revoked;
    }

    uint32_t violationCount(const NodeId& node) const {
        auto it = violations_.find(node);
        return it == violations_.end() ? 0u : it->second;
    }

    uint32_t conflictCount(const NodeId& node) const {
        auto it = conflicts_.find(node);
        return it == conflicts_.end() ? 0u : it->second;
    }

    bool isQuarantined(const NodeId& node) const {
        return quarantined_.count(node) != 0;
    }

    uint64_t acceptedCount() const { return acceptedCount_; }
    uint64_t droppedCount() const { return droppedCount_; }

    static const char* reasonName(DropReason reason) {
        switch (reason) {
            case DropReason::None: return "None";
            case DropReason::MalformedHeader: return "MalformedHeader";
            case DropReason::UnknownPort: return "UnknownPort";
            case DropReason::Quarantined: return "Quarantined";
            case DropReason::SyncNotRegistered: return "SyncNotRegistered";
            case DropReason::SyncCycleMismatch: return "SyncCycleMismatch";
            case DropReason::UnknownSlot: return "UnknownSlot";
            case DropReason::NonOwnerSender: return "NonOwnerSender";
            case DropReason::WrongCyclePhase: return "WrongCyclePhase";
            case DropReason::Conflict: return "Conflict";
        }
        return "Unknown";
    }

private:
    static bool headerWellFormed(const FrameHeader& header) {
        return header.frameId != 0 &&
               header.frameId <= kMaxFrameId &&
               header.payloadLength > 0 &&
               header.payloadLength <= kMaxPayloadLength &&
               header.cycleCount <= kMaxCycleCount;
    }

    void rotateCycleIfNeeded(uint8_t currentCycle) {
        if (hasCycle_ && currentCycle != currentCycle_) {
            slotSeen_.clear();  // revocations stay queued for the monitor
        }
        hasCycle_ = true;
    }

    InspectionResult finishDrop(InspectionResult& result, DropReason reason) {
        result.action = Action::Drop;
        result.reason = reason;
        result.sequence = 0;
        droppedCount_ += 1;
        return result;
    }

    InspectionResult finishViolation(InspectionResult& result, DropReason reason) {
        uint32_t count = ++violations_[result.sender];
        if (count >= kViolationQuarantineThreshold) {
            quarantined_.insert(result.sender);
        }
        return finishDrop(result, reason);
    }

    std::unordered_map<SlotNumber, SlotConfig> slots_;
    std::unordered_map<PortId, NodeId> portOwner_;
    std::unordered_set<NodeId> syncNodes_;
    std::unordered_set<NodeId> quarantined_;
    std::unordered_map<NodeId, uint32_t> violations_;
    std::unordered_map<NodeId, uint32_t> conflicts_;
    std::unordered_map<SlotNumber, uint64_t> slotSeen_;
    std::vector<uint64_t> pendingRevocations_;
    uint64_t nextSequence_ = 0;
    uint64_t acceptedCount_ = 0;
    uint64_t droppedCount_ = 0;
    uint8_t currentCycle_ = 0;
    bool hasCycle_ = false;
};

}  // namespace netsec

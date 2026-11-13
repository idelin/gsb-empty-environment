#include "netsec.hpp"

#include <stdexcept>

namespace netsec {

namespace {

constexpr int kMaxFrameId = 0x7FF;        // 11-bit
constexpr int kMaxCycleCount = 0x3F;      // 6-bit
constexpr int kMaxPayloadLength = 127;    // FlexRay static payload length field

}  // namespace

const char* to_string(Decision d) {
    switch (d) {
        case Decision::Pending:  return "Pending";
        case Decision::Accepted: return "Accepted";
        case Decision::Dropped:  return "Dropped";
    }
    return "Unknown";
}

const char* drop_reason_name(DropReason r) {
    switch (r) {
        case DropReason::None:              return "None";
        case DropReason::InvalidHeader:     return "InvalidHeader";
        case DropReason::Isolated:          return "Isolated";
        case DropReason::SyncNotRegistered: return "SyncNotRegistered";
        case DropReason::SyncCycleMismatch: return "SyncCycleMismatch";
        case DropReason::UnknownSlot:       return "UnknownSlot";
        case DropReason::WrongPhase:        return "WrongPhase";
        case DropReason::NotOwner:          return "NotOwner";
        case DropReason::Conflict:          return "Conflict";
    }
    return "Unknown";
}

SlotOwnershipMonitor::SlotOwnershipMonitor(std::vector<SlotEntry> slot_table,
                                           std::vector<NodeId> sync_nodes,
                                           int violations_to_isolate)
    : isolate_threshold_(violations_to_isolate) {
    if (violations_to_isolate <= 0) {
        throw std::invalid_argument("violations_to_isolate must be positive");
    }
    for (const SlotEntry& entry : slot_table) {
        if (entry.slot_id <= 0 || entry.slot_id > kMaxFrameId) {
            throw std::invalid_argument("slot_id out of 11-bit range");
        }
        if (entry.cycle_repetition <= 0 ||
            entry.cycle_repetition > 64 ||
            (entry.cycle_repetition & (entry.cycle_repetition - 1)) != 0) {
            throw std::invalid_argument("cycle_repetition must be a power of two in [1,64]");
        }
        if (entry.base_cycle < 0 || entry.base_cycle >= entry.cycle_repetition) {
            throw std::invalid_argument("base_cycle must satisfy 0 <= base_cycle < repetition");
        }
        if (!slots_.emplace(entry.slot_id, entry).second) {
            throw std::invalid_argument("duplicate slot_id in slot table");
        }
    }
    for (NodeId node : sync_nodes) {
        sync_nodes_.insert(node);
    }
}

void SlotOwnershipMonitor::begin_cycle(int cycle) {
    if (cycle < 0 || cycle > kMaxCycleCount) {
        throw std::invalid_argument("cycle out of 6-bit range");
    }
    if (cycle_active_) {
        end_cycle();
    }
    current_cycle_ = cycle;
    cycle_active_ = true;
    occupancy_.clear();
}

std::uint64_t SlotOwnershipMonitor::inspect(const ReceivedFrame& frame) {
    if (!cycle_active_) {
        throw std::logic_error("inspect() called without an open cycle (call begin_cycle first)");
    }
    const std::uint64_t id = next_id_++;
    ++totals_.received;

    const FrameHeader& h = frame.header;
    if (h.frame_id < 0 || h.frame_id > kMaxFrameId ||
        h.cycle_count < 0 || h.cycle_count > kMaxCycleCount ||
        h.payload_length < 0 || h.payload_length > kMaxPayloadLength) {
        record(id, frame, Decision::Dropped, DropReason::InvalidHeader);
        return id;
    }

    // Already isolated: every subsequent frame of this node is discarded.
    if (is_isolated(frame.sender)) {
        ++totals_.isolated_frames;
        record(id, frame, Decision::Dropped, DropReason::Isolated);
        return id;
    }

    // Sync policy applies to any frame that asserts the sync indicator.
    if (h.sync_frame) {
        if (sync_nodes_.find(frame.sender) == sync_nodes_.end()) {
            auto& stats = node_stats_[frame.sender];
            ++stats.sync_offenses;
            ++totals_.sync_offenses;
            record(id, frame, Decision::Dropped, DropReason::SyncNotRegistered);
            return id;
        }
        if (h.cycle_count != current_cycle_) {
            auto& stats = node_stats_[frame.sender];
            ++stats.sync_offenses;
            ++totals_.sync_offenses;
            record(id, frame, Decision::Dropped, DropReason::SyncCycleMismatch);
            return id;
        }
    }

    // In the static segment the frame ID must name an allocated static slot.
    auto slot_it = slots_.find(h.frame_id);
    if (slot_it == slots_.end()) {
        record(id, frame, Decision::Dropped, DropReason::UnknownSlot);
        return id;
    }
    const SlotEntry& entry = slot_it->second;

    // Cycle phase: the slot is only live when c mod repetition == base_cycle.
    if (current_cycle_ % entry.cycle_repetition != entry.base_cycle) {
        record(id, frame, Decision::Dropped, DropReason::WrongPhase);
        return id;
    }

    // The frame participates in same-slot conflict accounting regardless of
    // who sent it, since it physically occupied the slot this cycle.
    occupancy_[entry.slot_id].push_back(PendingFrame{id, frame});

    if (frame.sender != entry.owner) {
        // Non-owner transmission ("borrowing" someone else's slot): one
        // violation that drives isolation at the configured threshold.
        auto& stats = node_stats_[frame.sender];
        ++stats.violations;
        if (stats.violations >= isolate_threshold_) {
            stats.isolated = true;
        }
        record(id, frame, Decision::Dropped, DropReason::NotOwner);
        return id;
    }

    record(id, frame, Decision::Pending, DropReason::None);
    return id;
}

void SlotOwnershipMonitor::end_cycle() {
    if (!cycle_active_) {
        return;
    }

    for (auto& kv : occupancy_) {
        const int slot_id = kv.first;
        std::vector<PendingFrame>& frames = kv.second;
        if (frames.size() < 2) {
            continue;  // sole occupant keeps its tentative verdict
        }

        // Conflict: every frame seen on this slot this cycle is dropped and
        // the event is charged to the node that owns the slot.
        const NodeId owner = slots_.at(slot_id).owner;
        ++slot_stats_[slot_id].conflict_events;
        ++node_stats_[owner].conflicts_charged;
        ++totals_.conflicts;

        for (const PendingFrame& pf : frames) {
            Verdict& v = verdicts_.at(pf.id);
            if (v.decision == Decision::Dropped) {
                // A non-owner frame already paid a violation and was dropped;
                // keep its primary reason, it still counts as discarded.
                continue;
            }
            v.decision = Decision::Dropped;
            v.reason = DropReason::Conflict;
            ++node_stats_[pf.frame.sender].frames_dropped;
            ++totals_.dropped;
        }
    }

    // Resolve all still-pending (single-occupant, owner) frames as accepted.
    for (auto& kv : verdicts_) {
        Verdict& v = kv.second;
        if (v.cycle != current_cycle_ || v.decision != Decision::Pending) {
            continue;
        }
        v.decision = Decision::Accepted;
        ++node_stats_[v.sender].frames_accepted;
        ++totals_.accepted;
    }

    cycle_active_ = false;
    occupancy_.clear();
}

bool SlotOwnershipMonitor::cycle_open() const {
    return cycle_active_;
}

int SlotOwnershipMonitor::current_cycle() const {
    return current_cycle_;
}

Verdict SlotOwnershipMonitor::verdict(std::uint64_t observation_id) const {
    auto it = verdicts_.find(observation_id);
    if (it == verdicts_.end()) {
        throw std::out_of_range("unknown observation id");
    }
    return it->second;
}

const NodeCounters& SlotOwnershipMonitor::node_counters(NodeId node) const {
    static const NodeCounters empty;
    auto it = node_stats_.find(node);
    return it == node_stats_.end() ? empty : it->second;
}

SlotCounters SlotOwnershipMonitor::slot_counters(int slot_id) const {
    auto it = slot_stats_.find(slot_id);
    return it == slot_stats_.end() ? SlotCounters{} : it->second;
}

const Totals& SlotOwnershipMonitor::totals() const {
    return totals_;
}

bool SlotOwnershipMonitor::is_isolated(NodeId node) const {
    auto it = node_stats_.find(node);
    return it != node_stats_.end() && it->second.isolated;
}

void SlotOwnershipMonitor::record(std::uint64_t id,
                                  const ReceivedFrame& frame,
                                  Decision decision,
                                  DropReason reason) {
    Verdict v;
    v.observation_id = id;
    v.decision = decision;
    v.reason = reason;
    v.sender = frame.sender;
    v.slot_id = frame.header.frame_id;
    v.cycle = current_cycle_;
    verdicts_.emplace(id, v);

    if (decision == Decision::Dropped) {
        ++node_stats_[frame.sender].frames_dropped;
        ++totals_.dropped;
    }
}

}  // namespace netsec

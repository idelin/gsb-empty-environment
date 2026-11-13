#pragma once

#include <cstdint>
#include <map>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

// Node identity. Supplied by the bus monitor as the physical sending port /
// bus node that produced a frame, so it cannot be spoofed from the frame body.
using NodeId = int;

// One entry of the static slot table (one FlexRay static slot).
struct SlotEntry {
    int slot_id = 0;              // static slot number (also the expected frame ID)
    NodeId owner = 0;             // node that exclusively owns this slot
    int cycle_repetition = 1;     // repetition factor (power of two): 1, 2, 4, ... 64
    int base_cycle = 0;           // base cycle offset, 0 <= base_cycle < repetition
};

// FlexRay frame header fields that the monitor cares about.
struct FrameHeader {
    int frame_id = 0;             // 11-bit frame ID; in the static segment it equals the slot
    int payload_length = 0;       // words carried (only validated for range)
    int cycle_count = 0;          // 6-bit cycle counter value stamped in the frame
    bool sync_frame = false;      // sync indicator bit
};

// A frame handed to the monitor together with the port that physically sent it.
struct ReceivedFrame {
    FrameHeader header;
    NodeId sender = 0;
};

enum class Decision {
    Pending,   // tentative while the cycle is still open
    Accepted,
    Dropped
};

// Why a frame was dropped. Accepted frames carry None.
enum class DropReason {
    None,
    InvalidHeader,      // header field out of its legal bit range
    Isolated,           // sender already crossed the isolation threshold
    SyncNotRegistered,  // sync bit set by a node not registered as sync node
    SyncCycleMismatch,  // sync frame whose cycle count != global cycle
    UnknownSlot,        // frame ID does not correspond to any static slot
    WrongPhase,         // c mod repetition != base_cycle for this slot
    NotOwner,           // sender is not the registered owner of the slot
    Conflict            // two+ frames observed in the same (cycle, slot)
};

const char* to_string(Decision d);
const char* drop_reason_name(DropReason r);

// Per-node accounting.
struct NodeCounters {
    int violations = 0;       // non-owner ("borrowed slot") offenses, drives isolation
    int sync_offenses = 0;    // sync-bit / sync-cycle offenses
    int conflicts_charged = 0; // slot-conflict events charged to this node as slot owner
    int frames_dropped = 0;   // all frames of this node that ended up dropped
    int frames_accepted = 0;
    bool isolated = false;
};

struct SlotCounters {
    int conflict_events = 0;  // number of cycles in which this slot saw >1 frame
};

struct Totals {
    int received = 0;
    int accepted = 0;
    int dropped = 0;
    int conflicts = 0;
    int sync_offenses = 0;
    int isolated_frames = 0;
};

// Result of inspecting one frame. While a cycle is open the decision is
// Pending; after end_cycle() the final verdict is available via verdict().
struct Verdict {
    std::uint64_t observation_id = 0;
    Decision decision = Decision::Pending;
    DropReason reason = DropReason::None;
    NodeId sender = 0;
    int slot_id = 0;
    int cycle = 0;
};

// Bus-monitor guard for FlexRay static-slot ownership.
//
// Usage from a monitor callback:
//   monitor.begin_cycle(c);
//   id = monitor.inspect(frame);        // called per received frame
//   monitor.end_cycle();                // resolves same-slot conflicts
//   v = monitor.verdict(id);
//
// Not thread-safe: drive it from the single monitor callback thread.
class SlotOwnershipMonitor {
public:
    // violations_to_isolate defaults to the required threshold of 3.
    SlotOwnershipMonitor(std::vector<SlotEntry> slot_table,
                         std::vector<NodeId> sync_nodes,
                         int violations_to_isolate = 3);

    // Open communication cycle c (6-bit FlexRay cycle, 0..63). If a previous
    // cycle was still open it is finalized automatically.
    void begin_cycle(int cycle);

    // Inspect one frame. Returns an observation id whose final verdict is
    // retrievable after end_cycle().
    std::uint64_t inspect(const ReceivedFrame& frame);

    // Close the current cycle, resolve same-slot conflicts and finalize all
    // verdicts collected since begin_cycle(). Safe to call with no open cycle.
    void end_cycle();

    bool cycle_open() const;
    int current_cycle() const;

    // Final verdict of an observation id returned by inspect().
    Verdict verdict(std::uint64_t observation_id) const;

    // Accounting accessors.
    const NodeCounters& node_counters(NodeId node) const;
    SlotCounters slot_counters(int slot_id) const;
    const Totals& totals() const;
    bool is_isolated(NodeId node) const;

private:
    struct PendingFrame {
        std::uint64_t id;
        ReceivedFrame frame;
    };

    void record(std::uint64_t id, const ReceivedFrame& frame,
                Decision decision, DropReason reason);

    std::map<int, SlotEntry> slots_;
    std::unordered_set<NodeId> sync_nodes_;
    int isolate_threshold_;

    bool cycle_active_ = false;
    int current_cycle_ = 0;

    // slot -> frames that passed slot/phase admission in the current cycle
    std::map<int, std::vector<PendingFrame>> occupancy_;

    std::map<std::uint64_t, Verdict> verdicts_;
    std::unordered_map<NodeId, NodeCounters> node_stats_;
    std::map<int, SlotCounters> slot_stats_;
    Totals totals_;
    std::uint64_t next_id_ = 1;
};

}  // namespace netsec

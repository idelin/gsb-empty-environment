#include "netsec.hpp"

#include <iostream>

// Demonstration of wiring SlotOwnershipMonitor into a FlexRay bus-monitor
// callback. Compile against the netsec target (it exports this include dir).

using namespace netsec;

namespace {

SlotOwnershipMonitor make_monitor() {
    // Static slot table: slot -> owner, cycle repetition and base cycle.
    std::vector<SlotEntry> table = {
        SlotEntry{1, /*owner=*/10, /*repetition=*/1, /*base=*/0},  // node 10, every cycle
        SlotEntry{2, /*owner=*/20, /*repetition=*/2, /*base=*/0},  // node 20, even cycles
        SlotEntry{3, /*owner=*/30, /*repetition=*/4, /*base=*/2},  // node 30, cycles 2,6,...
    };
    std::vector<NodeId> sync_nodes = {10, 20};
    return SlotOwnershipMonitor(std::move(table), std::move(sync_nodes));
}

}  // namespace

int main() {
    SlotOwnershipMonitor monitor = make_monitor();

    for (int cycle = 0; cycle < 4; ++cycle) {
        monitor.begin_cycle(cycle);  // open the 6-bit communication cycle

        // sender is the physical ingress port determined outside the frame,
        // hence not spoofable by the transmitting node.
        ReceivedFrame owner_frame;
        owner_frame.header.frame_id = 1;
        owner_frame.header.payload_length = 16;
        owner_frame.header.cycle_count = cycle;
        owner_frame.header.sync_frame = (cycle == 0);
        owner_frame.sender = 10;

        ReceivedFrame intruder;
        intruder.header.frame_id = 1;  // same slot as node 10
        intruder.header.payload_length = 16;
        intruder.header.cycle_count = cycle;
        intruder.sender = 99;          // node 99 does not own slot 1

        auto id1 = monitor.inspect(owner_frame);
        auto id2 = monitor.inspect(intruder);

        monitor.end_cycle();  // resolves same-slot conflicts for this cycle

        for (auto id : {id1, id2}) {
            const Verdict v = monitor.verdict(id);
            std::cout << "cycle=" << cycle << " slot=" << v.slot_id
                      << " sender=" << v.sender << " -> "
                      << to_string(v.decision) << " ("
                      << drop_reason_name(v.reason) << ")\n";
        }
    }

    const Totals t = monitor.totals();
    std::cout << "accepted=" << t.accepted << " dropped=" << t.dropped
              << " conflicts=" << t.conflicts
              << " sync_offenses=" << t.sync_offenses << '\n';
    std::cout << "node 99 isolated: " << std::boolalpha
              << monitor.is_isolated(99) << '\n';
    return 0;
}

import gsb.reassembly;

#include <cassert>
#include <vector>

int main() {
    gsb::ReassemblyEngine engine;
    gsb::TrustSnapshot snapshot;
    snapshot.generation = 1;
    auto report = engine.query(snapshot, {});
    assert(!report.deliverable);
    static_assert(gsb::max_candidate_records == 22);
    return 0;
}

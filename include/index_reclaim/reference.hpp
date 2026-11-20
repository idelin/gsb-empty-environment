#ifndef INDEX_RECLAIM_REFERENCE_HPP
#define INDEX_RECLAIM_REFERENCE_HPP

#include "index_reclaim/permission.hpp"

#include <vector>

namespace index_reclaim {

// Reference decision obtained by observing every live thread slot afresh for
// every pending batch.  It intentionally has no shard evidence and no
// incremental invalidation logic.  It is suitable for deterministic,
// small-scale interleaving tests of the sharded implementation.
struct ReferenceDecision {
    std::vector<BatchId> eligible_batches;
    std::vector<BatchId> deferred_batches;
    std::vector<ObjectId> eligible;
    std::vector<ObjectId> deferred;
    std::vector<BatchId> protected_batches;
};

ReferenceDecision reference_rescan_all(const Authority& authority);

}  // namespace index_reclaim

#endif  // INDEX_RECLAIM_REFERENCE_HPP

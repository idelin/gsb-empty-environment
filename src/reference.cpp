#include "index_reclaim/reference.hpp"

#include <algorithm>

namespace index_reclaim {
namespace {

bool protects_any(const ThreadSlotSnapshot& slot,
                  const std::vector<ObjectId>& objects) {
    return slot.occupied &&
           std::find(objects.begin(), objects.end(), slot.object) !=
               objects.end();
}

}  // namespace

ReferenceDecision reference_rescan_all(const Authority& authority) {
    const AuthoritySnapshot snapshot = authority.snapshot_for_reference();
    ReferenceDecision decision;

    for (const BatchSnapshot& batch : snapshot.batches) {
        if (batch.issued) {
            continue;
        }

        bool protected_object = false;
        for (const ThreadSlotSnapshot& slot : snapshot.live_threads) {
            if (protects_any(slot, batch.objects)) {
                protected_object = true;
                break;
            }
        }

        if (protected_object ||
            snapshot.root.publication < batch.removal_publication) {
            decision.deferred_batches.push_back(batch.id);
            decision.deferred.insert(decision.deferred.end(),
                                     batch.objects.begin(),
                                     batch.objects.end());
            if (protected_object) {
                decision.protected_batches.push_back(batch.id);
            }
            continue;
        }

        decision.eligible_batches.push_back(batch.id);
        decision.eligible.insert(decision.eligible.end(),
                                 batch.objects.begin(),
                                 batch.objects.end());
    }

    return decision;
}

}  // namespace index_reclaim

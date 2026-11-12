// Package netsec provides a publication barrier that keeps models trained on
// erased individuals out of serving.
//
// A feature service may delete an individual's data while an asynchronous
// training run is still in flight. When that run's callback eventually tries
// to publish its model, the model can still contain the deleted individual.
// The Barrier closes that window by forcing each training task to pin, at
// start time, both its exact input Manifest and the current deletion
// generation of the DeletionLog. Publish then admits a model only when:
//
//   - the task is known, still running and not cancelled;
//   - the model was produced from the pinned manifest; and
//   - every deletion event known at publish time is already reflected, i.e.
//     the pinned generation equals the current generation and no pinned input
//     has been erased.
//
// A task that has gone stale cannot be rescued by republishing with a new
// version string: the version is untrusted metadata and the pinned contract is
// immutable. The only recovery is Rebuild, which starts a fresh task against
// the post-deletion inputs and pins a new generation. Every refusal is kept in
// an append-only rejection ledger as audit evidence.
package netsec

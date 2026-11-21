# indexvis

`indexvis` computes the exact minimum and maximum number of snapshot-visible
versions on a compressed index page while the commit table is still being
filled in. It returns a legal final-completion witness for each bound.

```go
package main

import (
	"fmt"

	indexvis "prescreen.local/ps-fff6434389"
)

func main() {
	snap := indexvis.Snapshot{
		UpperBound: 100,
		Known: map[indexvis.TxID]indexvis.Status{
			10: indexvis.Committed,
			11: indexvis.Aborted,
		},
	}
	deps := []indexvis.Edge{{From: 10, To: 12}}

	page := indexvis.NewPageSummary()
	_ = page.Add(10, indexvis.NoTransaction, 3) // three versions created by 10
	_ = page.Add(12, 11, 5)                     // five versions created by 12, deleted by 11

	ix, err := indexvis.NewIndex(snap, deps, page)
	if err != nil {
		panic(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Min, res.Max)
	fmt.Println(ix.Replay(res.MinWitness))
}
```

## Visibility model

A transaction is **eligible** when its ID is at most `Snapshot.UpperBound` and
it is not in `Snapshot.Active`. Any other transaction is *not yet effective*.
Eligibility is separate from the final commit/abort state: a transaction after
the snapshot can still be committed finally and can still force commits through
dependencies, but its versions remain invisible to this snapshot.

A version is visible exactly when

```
effective(creator) && !effective(deleter)
```

with

```
effective(t) = eligible(t) && final(t) == committed
```

`NoTransaction` as the deleter means there is no deleter. Transactions absent
from `Snapshot.Known` are unknown and the solver chooses their final status.
A dependency `u -> v` means `u` committed implies `v` committed. Cycles are
allowed. A known committed-to-aborted dependency path is reported as
`ContradictionError`.

## Page summaries

`PageSummary` keeps one multiplicity per `(creator, deleter)` pair. Summaries
can be cloned, split, and merged; merging preserves multiplicity. When several
pages share an unknown transaction, merge their summaries before evaluation.
Evaluating pages independently and adding their bounds is not exact because the
shared unknown outcome correlates the pages.

## Decomposition and dynamic program

`NewIndex` builds a tree decomposition with bags of at most
`indexvis.MaxBagSize` (11) transactions. Every dependency edge and every
creator/deleter pair is covered by a bag. The implementation uses min-fill and
min-degree elimination heuristics and rejects graphs for which it cannot find a
width-10 decomposition. If the caller already has a decomposition,
`NewIndexWithDecomposition` validates and uses it instead.

`Evaluate` runs a joint min/max DP over the fixed decomposition. Each bag has at
most `2^11` assignment states, so the solver never enumerates global
completions. The DP state is exactly the joint distribution of transaction
outcomes; it does not take row-wise bounds and add them.

## Incremental updates

`SetStatus` fixes or unfixes a final commit state. `AddVersions` / `SetVersions`
change one pair's multiplicity. Both operate on the fixed decomposition; a new
pair must have its creator and deleter together in some existing bag (otherwise
the operation returns `ErrPairNotCovered`):

- each transaction has one owner bag for its domain constraint,
- each dependency and version pair has one owner bag,
- only that owner bag and its path to the root are recomputed.

The returned `UpdateProof` lists the affected bag, every updated bag, and the
number of table/projection entries recomputed. `UpdateProof.CheckOnlyRootPaths`
verifies that no bag outside the root path was touched. A contradictory
`SetStatus` is rolled back automatically; the error carries the contradicting
dependency path.

## Tests

`go test ./...` covers:

- replaying every returned witness and independently recounting visible rows,
- exhaustive enumeration of legal completions on random small instances,
- dependency cycles spanning multiple bags,
- one transaction cancelling contributions across two decomposition subtrees,
- empty pages and an empty transaction universe,
- a snapshot crossing both ends of a dependency,
- contradictory completion followed by rollback,
- summary split/merge and multi-page correlation of unknown outcomes,
- path-only update proofs and 4096-transaction / treewidth-10 decompositions.

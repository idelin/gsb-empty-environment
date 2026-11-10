# acldiff

Offline comparison of two IPv4 ACL versions. The command reads a JSON
document on stdin and emits the minimal aligned CIDR ranges whose
effective allow/deny decision changed between the two rule lists.

## Input and output

Input:

```json
{
  "old": [["10.0.0.0/8", "allow"], ["10.1.0.0/16", "deny"]],
  "new": [["10.0.0.0/8", "allow"], ["192.168.0.0/16", "allow"]]
}
```

Output, a JSON array of `[CIDR, old action, new action]`, sorted by start
address and containing only changed addresses:

```json
[["10.1.0.0/16","deny","allow"],["192.168.0.0/16","deny","allow"]]
```

Rules are evaluated top to bottom with first-match-wins semantics; an
address matching no rule is denied by default. CIDR host bits are
masked (`10.1.2.3/24` is treated as `10.1.2.0/24`).

## Run

```sh
go build -buildvcs=false -o acldiff ./cmd/acldiff
acldiff < input.json
acldiff -verify -samples 2000 < input.json
```

`-verify` replays the raw rule lists against the produced blocks and
reports any disagreement on stderr; stdout stays pure JSON.

## How it works

1. Each rule list is compiled into a binary IPv4 prefix trie. Every node
   stores `exact` (a rule ending at that prefix) and `min` (the smallest
   rule index reachable in its subtree). Duplicate prefixes keep the first
   rule, and later shadowed rules are detected through `min`.
2. The two tries are walked in parallel. A subtree is uniform when every
   address in it is decided by one rule, i.e. no descendant rule index is
   smaller than the best rule inherited from ancestors. Uniform regions
   whose actions differ are emitted; otherwise the walk descends. The
   address space is never enumerated, so `0.0.0.0/0` costs one node.
3. Same-direction sibling blocks are merged bottom-up into the fewest
   aligned CIDR blocks and sorted by start address.
4. `Verify` independently replays the original ordered rules for block
   endpoints, interior and random samples and cross-checks trie lookups.

## Complexity

With `n` and `m` rules, compile is `O(32(n+m))` time and memory; the
diff walk is proportional to the union of trie nodes. On an Apple M1 Pro,
two lists of 100,000 rules compile and diff in about 130 ms using roughly
83 MB.

## Layout

- `acl/` — trie compilation, diff, merging and replay verification.
- `cmd/acldiff/` — stdin/stdout command.
- `cmd/gen/` — helper that emits a 100k-rule stress input.

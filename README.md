# fuzzy

Weighted fuzzy dictionary search in Go.

The distance between two words is the minimum total cost of turning one
into the other with four edit operations — insertion, deletion,
replacement, and swapping two adjacent characters — each carrying a
non-negative integer cost supplied by the caller. Formally it is the
shortest path in the edit graph whose nodes are the alignment positions
of the two words. A transposition is a single alignment step over two
adjacent positions (OSA-style); cascades of swaps are not modeled.

## Usage

```go
d := fuzzy.New()
d.Insert("kitten")
d.Insert("sitting")

c := fuzzy.Costs{Ins: 1, Del: 1, Rep: 1, Trans: 1}

// Exact distance between two words.
dist := fuzzy.Distance("kitten", "sitting", c) // 3

// Top 5 words, ordered by cost then lexicographically.
res := d.Search("kitten", 5, fuzzy.Unlimited, c)
for _, h := range res.Hits {
	fmt.Println(h.Word, h.Cost)
}
```

## Search and budgets

`Search(query, k, maxCost, costs)` returns up to `k` distinct words with
the smallest edit cost to `query`. The dictionary is a trie, so words
sharing a prefix share the dynamic-programming work, and a prefix is
pruned once the last two DP rows bound every completion above the budget.

`maxCost` is the exploration budget (`fuzzy.Unlimited` disables it). When
the budget runs out before the whole dictionary is explored, the returned
`SearchResult.Proven` reports how many leading ranks are certified: every
word never examined costs strictly more than the last proven hit. Hits
beyond `Proven` are best-effort. With an unlimited budget every returned
rank is proven.

## Testing

`go test ./...` cross-checks the dynamic programming against an
independent Dijkstra search that exhaustively traverses the edit graph on
short random words, covering zero-cost edges and cycles, multiple edit
paths reaching the same word, and `k` larger than the dictionary.

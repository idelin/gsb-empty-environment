# reproduce_compare

A Python 3.11 library (plus CLI) that decides whether two build outputs are
reproducibly equivalent — instead of merely *looking* the same after stripping
timestamps. It compares directory trees and the ZIP archives nested inside
them, emits an evidence bundle that explains every difference, and ships an
independent verifier that re-derives the conclusion from only the raw inputs.

## Outcomes

Every comparison ends in exactly one of four states:

| Status           | Meaning |
|------------------|---------|
| `equal`          | The originals are identical; no ignore rule was needed. |
| `equivalent`     | Different only in fields explicitly permitted by validated rules; the differing values are retained as evidence. |
| `different`      | A substantive difference (content, path, permission, symlink target, array order, …). |
| `undeterminable` | A decision cannot be made safely; a reason is always supplied. |

`undeterminable` is returned (never silently resolved) for:

* duplicate ZIP member names;
* path traversal members (`../`, leading `/`, backslashes, drive letters);
* structurally damaged archives (bad/truncated headers, CRC failure,
  unsupported compression or encryption);
* exhausted limits (nesting depth, total decompressed bytes, member count);
* duplicate JSON object keys;
* ignore rules whose target is missing, whose declared type does not match,
  or whose key paths overlap.

## What may be ignored

1. **ZIP timestamp metadata only.** The parser locates the `date_time` field
   in each member header (`ZipInfo.header_offset` + fixed local-header
   offsets). Byte-for-byte identical archives stay `equal` even when
   timestamp ignoring is enabled. A timestamp-shaped string inside a member
   payload is ordinary data and is never altered or ignored. Local and
   central-header timestamp metadata must agree, otherwise the archive is
   `undeterminable`.
2. **Complete JSON key paths chosen by the user.** Rules name a full target
   locator and a full key path; there are no wildcards or partial matches.
   Array order is always significant. Numeric literals are parsed exactly:
   integers stay `int`, decimal literals become `Decimal` from their source
   text, so no value passes through a binary float. Duplicate object keys are
   rejected at parse time.

Nothing is normalized on disk and no archive member is ever extracted to the
filesystem — all decompression happens in memory under a shared budget.

## Rule document

```json
{
  "ignore_zip_timestamps": true,
  "json_key_ignores": [
    {
      "rule_id": "build-timestamp",
      "target": "dist.zip!/pkg/app.zip!/manifest.json",
      "key_path": ["build", "timestamp"],
      "expected_type": "string"
    }
  ]
}
```

* `target` uses `!/` separators for ZIP nesting. `..`, `.`, absolute and
  empty segments are rejected.
* `expected_type` is one of `any`, `object`, `array`, `string`, `integer`,
  `number`, `bool`, `null`, `scalar`. `number` accepts integers and decimals.
* Two rules on the same target may not have overlapping key paths, and
  duplicate `(target, key_path)` rules are rejected.

## Evidence bundle

A bundle (`--evidence out.json`) binds the conclusion to the inputs:

* SHA-256 manifest digests of both raw trees (path, kind, mode, symlink
  target, raw content digest);
* the canonical rule document and its SHA-256;
* every ignored record: `locator`, `aspect`, `field_path`, `old_digest`,
  `new_digest`, `old_value`, `new_value`, `rule_id`;
* every substantive difference and every `undeterminable` reason.

## Independent verifier

`verify` does **not** trust the comparator's normalized summaries. It reparses
the rules, rescans both raw trees, reruns the full comparison, recomputes all
digests from raw bytes, and then cross-checks the claimed status and the full
set of ignore/difference/unknown records. It also recomputes each ignore
record's value digests and refuses records whose values are equal (nothing to
ignore), missing a `rule_id`, or bound to different inputs.

## Usage

```bash
python3.11 -m reproduce_compare compare OLD NEW --rules rules.json --evidence evidence.json
python3.11 -m reproduce_compare verify  OLD NEW --evidence evidence.json
```

Exit codes: `0` equal/equivalent/different (conclusion produced),
`2` undeterminable, `3` verification failed.

```python
from reproduce_compare import Budget, build_bundle, compare_trees, load_rules, verify_bundle

rules = load_rules({
    "json_key_ignores": [{
        "rule_id": "built",
        "target": "a.zip!/doc.json",
        "key_path": ["built_at"],
        "expected_type": "string",
    }]
})
result = compare_trees("out-old", "out-new", rules,
                       Budget(max_depth=8, max_uncompressed_bytes=256*1024*1024,
                              max_entries=100_000))
bundle = build_bundle(result, rules)
verdict = verify_bundle(bundle, "out-old", "out-new")
assert verdict.ok and verdict.recomputed_status == result.status
```

## Tests

```bash
python3.11 -m unittest tests.test_compare -v
```

The suite covers nested ZIPs containing big-integer JSON, symlink target
changes (filesystem and ZIP members), payload pseudo-timestamps, protected
random byte mutation, ignorable-field mutation (with retained evidence),
duplicate members, path traversal, truncation/CRC corruption, all three
budget limits, duplicate JSON keys, rule overlap/type/missing errors, and
evidence forgery/tampering rejected by the verifier.

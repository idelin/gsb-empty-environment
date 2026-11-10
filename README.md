# keystream-audit

Offline audit tool that locates ciphertext fragments reusing the same CTR-mode
keystream and recovers plaintext from known-plaintext fragments.

Keystream identity is scoped strictly to a `(key_id, nonce)` pair. A fragment
starts at absolute keystream position

```
abs_start = start_block * 16 + block_offset
```

and occupies `len(ciphertext)` consecutive byte positions. Two fragments in the
same group overlap when their absolute ranges intersect. Overlaps are reported
both as a byte range and, for the block view, as the inclusive
`[start_block..end_block]` range of 16-byte blocks they touch.

## Input

JSON document (file via `-in`, otherwise stdin). Every hex field uses lowercase
or uppercase hex without separators.

```json
{
  "fragments": [
    {
      "id": "frag-a",
      "key_id": "000102030405060708090a0b0c0d0e0f",
      "nonce": "aa000000000000000000000000000000",
      "start_block": 0,
      "block_offset": 0,
      "ciphertext": "1020304050607080",
      "known_plaintext": [
        {"offset": 0, "data": "deadbeefcafebabe"}
      ]
    }
  ]
}
```

- `block_offset` must be in `[0,15]` and is the byte offset inside the first
  block touched by the fragment.
- `known_plaintext[].offset` is relative to the start of that fragment's
  ciphertext; `data` must not run past the ciphertext. Multiple entries are
  allowed.
- Fragment IDs must be unique.

## Recovery rules

- A known plaintext byte yields the keystream byte `K = C XOR P` at its absolute
  position.
- That keystream byte decrypts every other fragment in the same
  `(key_id, nonce)` group covering the same position. Because keystream is
  tracked per absolute position, propagation works transitively across multiple
  overlapping records but can never jump across a gap with no shared position.
- Different `key_id` or `nonce` values always form independent groups; nothing
  propagates between them even if block numbers coincide.
- When two known bytes imply different keystream values for one position, the
  position is marked `conflict` and every candidate is retained with its own
  evidence (source fragment id and ciphertext offset). No value is overwritten or
  silently dropped; each conflicting candidate produces its own recovered byte.

## Output

- `format=json` (default): machine-readable report with overlaps, per-position
  keystream candidates, and per-fragment recovered bytes with provenance.
- `format=text`: human-readable audit summary.
- `-out` writes to a file instead of stdout.

Each recovered byte records its absolute `position`, `ciphertext_offset`,
`plaintext_byte`, the `source` evidence it was derived from, a
`from_known_plaintext` flag (true for the fragment that supplied the known
plaintext, false for cross-record propagation), and a `conflict` flag.

## Build and test

```sh
go build -buildvcs=false -o keystream-audit .
go test -buildvcs=false ./...
./keystream-audit -in example_input.json -format text
```

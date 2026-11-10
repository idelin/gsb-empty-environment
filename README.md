# QPACK offline replayer

This tool replays encoder and header-block events offline and restores each QPACK dynamic-table reference to an absolute index according to RFC 9204.

## Input

```json
{
  "initial_capacity": 320,
  "max_entries": 4,
  "events": [
    {"seq": 1, "type": "insert", "name": "x-test", "value": "1", "size": 40},
    {"seq": 2, "type": "duplicate", "abs_index": 0},
    {"seq": 3, "type": "capacity", "capacity": 80},
    {
      "seq": 4,
      "type": "header_block",
      "stream_id": 0,
      "encoded_ric": 2,
      "sign": 1,
      "delta_base": 1,
      "references": [
        {"kind": "post_base", "index": 0}
      ]
    }
  ]
}
```

Event types:

- `insert`: append a dynamic-table entry. Omit `size` to use RFC 9204's `name length + value length + 32`.
- `duplicate`: append a copy of the entry at `abs_index`.
- `capacity`: set dynamic-table capacity and evict oldest entries until the table fits.
- `header_block`: replay a stream's header-block prefix and references.

Reference kinds:

- `indexed`: relative indexed header field, absolute index is `base - index`.
- `name`: relative name reference, absolute index is `base - index`.
- `post_base`: post-base indexed header field, absolute index is `base + 1 + index`.
- `post_base_name`: post-base name reference, absolute index is `base + 1 + index`.

## RIC and Base

Required Insert Count is decoded with the decoder's current full range (`Known Received Count`) and the negotiated `max_entries`. Its wrap modulus is `2 * max_entries`, not a fixed 8-bit value.

Base is restored as:

- `sign = 0`: `base = required_insert_count - delta_base`
- `sign = 1`: `base = required_insert_count + delta_base`

## Run

```sh
go run . -file events.json
```

If `-file` is omitted, the JSON document is read from stdin.

## Output States

- `decodable`: every referenced absolute index exists and the current insert count is at least Required Insert Count.
- `blocked`: at least one required entry is still in the future; the first satisfying event sequence is reported after replay.
- `evicted`: a referenced absolute index is older than the current oldest table index. This is permanent and cannot be repaired by later inserts.
- `invalid`: the prefix or reference encoding is malformed.
- `undecodable`: replay ended while the stream was still blocked.

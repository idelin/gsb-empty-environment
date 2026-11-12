// Security invariants enforced while decoding StandardMessageCodec messages:
//
//   - Declared lengths are bounds-checked against the remaining bytes and are
//     never used to pre-allocate slices or maps.
//   - Container nesting is capped at 16 levels and the total number of decoded
//     values (including uint8-list bytes) at 100000.
//   - Map keys must be unique; duplicate keys fail the decode.
//   - 8-byte alignment padding before int64/float64 values must be all zero.
//   - Unknown type bytes and any bytes trailing the root value are rejected.
package netsec

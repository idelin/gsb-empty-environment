# configstore

Crash-safe configuration publication over two storage slots. A single write
may tear, so every slot is self-validating and readers never trust a slot
that is not fully committed.

## Slot format

Fixed-size image (all integers big-endian):

```
[0:4]               magic "CFGS"
[4:12]              generation uint64
[12:16]             payload length uint32
[16:48]             SHA-256 over (generation || length || payload)
[48:48+MaxPayload]  payload, zero-padded (padding is validated as zero)
[SlotSize-1]        commit flag (0xA5 once committed)
```

The commit flag is the last byte of the image and the last write issued, so
any truncation or torn body write removes or invalidates it.

## Protocol

- **Publish**: validate both slots, take `gen = max(valid generations)`,
  write the full body (flag cleared) into the *other* slot, sync, then write
  the commit flag, sync. Any failure leaves the previous committed
  configuration readable.
- **Load**: validate both slots independently (`ValidateSlot`), return the
  payload of the valid slot with the highest generation. A torn, corrupted
  or uncommitted slot with a higher generation is ignored — a failed publish
  can never shadow the old configuration.
- **Generation overflow**: explicitly rejected with
  `ErrGenerationOverflow` when the current generation is
  `math.MaxUint64` (uint64 never wraps; at one publish/second it lasts
  ~5.8e11 years, so rejection is the verifiable rule).

## Independent validator

`ValidateSlot(data []byte)` performs no I/O and shares no state with the
store. The `slotcheck` CLI exposes it for files:

```
go run ./cmd/slotcheck <slot-file>   # exit 0 = VALID, 1 = INVALID
```

## Tests

```
GO111MODULE=on go test ./...
```

Coverage includes: truncation at **every** byte offset, corruption of
**every** byte, commit-flag write ordering (flag is the final write; old
value visible between body and commit), torn publish at **every** byte with
old config still readable, commit-write failure, generation overflow
rejection, high-generation broken slots (uncommitted / bad digest / bad
magic / truncated / garbage) never shadowing the old value, recovery after
torn publishes, and a real-file roundtrip with a simulated crash.

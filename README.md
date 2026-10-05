# debugloc

Budgeted evaluator for stack-based debug location expressions (C++17).

## Features

- **Stack-based expression program**: `PushConst`, `LoadReg`, `Deref`,
  `Add`/`Sub`/`Mul`, `BitField`, `Concat`, `SetEndian`.
- **Partially-unknown values**: every value carries a per-bit known mask;
  unavailable memory yields unknown bits instead of zero.
- **Overflow-checked address arithmetic**: `Add`/`Sub`/`Mul` and the
  dereference address range are checked; overflow is a hard error.
- **Unaligned bit segments**: `BitField(offset, size)` slices at bit
  granularity; `Concat` joins segments, preserving unknown bits.
- **Endianness switching**: `SetEndian` changes how later `Deref`s interpret
  memory bytes.
- **Budget**: `EvalOptions::maxOps` caps executed instructions.

## Errors

`BudgetExhausted`, `StackUnderflow`, `AddressOverflow`, `UnknownAddress`,
`RegisterUnavailable`, `InvalidBitRange`.

## Example

```cpp
struct Mem : debugloc::Memory { /* read() -> optional byte per address */ };
struct Regs : debugloc::Registers { /* read(id) -> little-endian bytes */ };

Mem mem; Regs regs;
debugloc::Evaluator ev(mem, regs, {.maxOps = 500});
auto r = ev.run({
    debugloc::Instr::loadReg(3),      // push register 3
    debugloc::Instr::pushConst(8),    // push 8
    debugloc::Instr::add(),           // checked add
    debugloc::Instr::deref(4),        // read 4 bytes (may be partly unknown)
    debugloc::Instr::bitField(3, 13), // unaligned bit field
});
if (r) { /* r.value().toUint64() if fully known */ }
```

## Build & test

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

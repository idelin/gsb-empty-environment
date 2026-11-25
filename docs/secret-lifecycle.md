# Secret Page Lifecycle

`secret_lifecycle` is a C++20 header-only library for secret-bearing pages whose lifetime
must remain explicit across clones, partial key rotation, read-only borrows, asynchronous
erasure, and address reuse.

## Integration

```cpp
#include "secret_lifecycle/secret_lifecycle.hpp"

secret_lifecycle::TrackingPageAllocator allocator(4096);
secret_lifecycle::PausableEraser eraser;
secret_lifecycle::SecretHost host(allocator, allocator, eraser);

auto store = host.create_store();
store.write(generation, secret_lifecycle::ByteRange{offset, size}, bytes);
auto view = store.read(secret_lifecycle::ByteRange{offset, size});
```

Production hosts can replace `TrackingPageAllocator` and `PausableEraser` by implementing
`IPageAllocator`, `IErasableAllocator`, and `IEraser`. Page identities include an allocation
generation so receipts cannot be replayed against a page later reused at the same address.

## Guarantees

- Logical byte intervals may span multiple pages and retain their byte order in `ReadView`.
- Clones share physical pages. The first write to a borrowed or shared page allocates a
  replacement and copies through an allocator-owned scratch buffer.
- Partial rotation updates only the requested byte interval. One physical page can contain
  several generations, and erasure is range-limited so adjacent active generations survive.
- Destroying a generation rejects all new borrows. Existing borrows pin the bytes until their
  RAII view reaches its release point.
- Destruction proof is emitted only after every physical and scratch receipt for that
  generation is accounted for. Duplicate, stale, out-of-order, and late receipts are tolerated
  without changing proof validity.
- `EventAcceptor` is a read-only verifier. It rejects missing logical intervals, mismatched
  borrow barriers, unknown receipts, and receipts bound to an older allocation generation at a
  reused address.
- Allocation and scratch-copy failures roll back committed mappings and securely erase every
  freshly written page or scratch copy.

## Testing

The test allocator records every live page, historical page allocation, scratch buffer, and
per-byte zero state. Tests enumerate cross-page splits, clone copy-on-write, re-merging,
borrow exits, paused/late/duplicate erasure acknowledgements, same-address reallocation, and
failures injected after scratch copies.

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

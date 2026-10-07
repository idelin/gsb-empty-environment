# frozen-submit-form

A small TypeScript form store whose submission semantics are safe against
slow or out-of-order server responses.

## Semantics

- **Frozen snapshot**: `submit()` freezes a copy of the current values and
  sends exactly that to the transport. Later edits never leak into the
  in-flight request.
- **Versioned confirmation**: a success response confirms only the submitted
  version. Fields edited while the request was in flight stay dirty against
  the last confirmed values.
- **Scoped field errors**: a server field error is applied to the live form
  only when the field still holds the submitted value. Errors for fields
  that were edited or deleted since the snapshot are kept on the submission
  record (`unappliedErrors`) instead of clobbering local state.
- **Duplicate submit**: while a submission is pending, `submit()` is
  idempotent and returns the in-flight record — no duplicate request.
- **Cancel**: `cancel()` detaches the pending submission; a late response
  confirms nothing and applies no errors.

## Usage

```ts
import { FormStore } from './src/form.ts';

const form = new FormStore(
  async (snapshot) => fetch('/api/form', {
    method: 'POST',
    body: JSON.stringify(snapshot),
  }).then((r) => r.json()),
  { name: 'Ada' },
);

const record = form.submit();
form.setValue('name', 'Ada Lovelace'); // stays dirty even if submit succeeds
await record.settled;
record.unappliedErrors; // server errors for fields that moved on
```

Every submission is appended to `form.submissions` with its `snapshot`,
final `status` (`confirmed` / `failed` / `cancelled`), `appliedErrors`,
`unappliedErrors` and optional `message`.

## Tests

Requires Node >= 22.6 (runs TypeScript via type stripping, no build step):

```sh
npm test
```

The tests drive the transport with manually resolved promises
(`controllableTransport`) to verify duplicate submits, cancellation, and
fields deleted mid-flight.

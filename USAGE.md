# RSA parity/LSB oracle attack verifier (`parity`)

Exact, integer-only recovery for a local Go crypto regression host. The
package never imports `crypto/rsa` and never reads a private key: the host
generates the key and supplies a `Callback` that performs the private
operation locally and reports only parity.

## Host contract

```go
type Callback func(x *big.Int) (Parity, error)
//   0 <= x < N
//   (ParityEven|ParityOdd), nil                 -> leaked LSB
//   ..., ErrUnavailable                         -> temporarily unavailable / rate limited
//   ..., ErrVersionMismatch                     -> key rotation version mismatch
```

A generic `func(*big.Int) (int, error)` is accepted via `AdaptFunc`,
`RecoverIntE`, or the builder's `QueryFunc`; a `(bool, error)` via `AdaptBool`.
Any non-nil error is checked first and is **never** interpreted as even.

## Minimal use

```go
res, err := parity.NewAttack().
    N(pub.N).EInt(pub.E).C(c).
    KeyVersion("key-42").
    QueryFunc(hostParityFunc). // func(*big.Int)(int, error)
    QueryBudget(4096).
    Run()

switch {
case err == nil:
    _ = res.Plaintext // already checked: m^e mod N == c
case parity.IsBudget(err):
    cp := res.Checkpoint // JSON-serializable
    _ = cp.Save("cp.json")
case parity.IsVersionSwitch(err):
    // res.Status == parity.StatusVersionInterrupted; abort the campaign
}
```

## Exact interval math

Queries iterate `q <- q * 2^e mod N`; decryption gives `2^i*m mod N`. With odd
`N`, `parity((T*m) mod N) == parity(floor(T*m/N))` for even `T`, and exact
`math/big.Rat` strips at `k*N/T` halve the rational interval. No floats.
Endpoints after `i` bits are exactly `[jN/2^i, (j+1)N/2^i]` with
`j=floor(2^i m/N)`. A unique integer candidate is accepted only after
`m^e mod N == c`.

## Blinding / duplicate-point bypass

- Default `BlindRetries`: an unavailable retry is rerandomized as
  `x*r^e mod N` with a fresh odd coprime `r` whose factors are recorded
  (`Blind.Factors`, `QueryRecord.RFactors`). Previously sent points are avoided.
  The general exact strip engine absorbs `parity((T*r)*m mod N)`.
- `BaseBlind()`: scalable classic blinding for large keys — recover
  `m' = r*m mod N` on `c*r^e`, then `m = m'*r^{-1} mod N` and re-verify `c`.
  This keeps a single exact dyadic interval.

## Checkpoint / resume binding

`Checkpoint` is JSON and binds `N`, `e`, `c`, `KeyVersion`, a binding digest,
the **complete** ordered `History`, and its SHA-256 `HistoryDigest`.
`Resume` verifies the supplied binding and digests, then replays history to
rebuild exact state. Tampering or an `N/e/c/keyVersion` mismatch is rejected
with `ErrBindingMismatch` / `ErrDigestMismatch`.

## Fixed callback and recovery experiment

`NewFixedCallback(inner)` performs no private operation before
`Authenticate()` and returns the identical `(ParityRejected, ErrAuthRequired)`
for every input. `RunExperiment` then reports `NoOracle == true`
(`ErrNoOracle`); the uniform rejection is never even and never yields a false
`m = 0`.

## Tests / regression coverage

- `TestExhaustiveSmallModuliEndpoints`: every plaintext over many small RSA
  moduli, checking every rational endpoint.
- `TestHostGeneratedKeyRandom1024` / `...BaseBlind1024`: host-generated
  `crypto/rsa` keys with random and edge plaintexts (`0`, `1`, `N-1`, `N-2`).
- `PARITY_TEST_LARGE=1 go test -run TestHostGeneratedKeyLarge2048OptIn`.
- Unavailable streaks, all-unavailable checkpoint/resume, budget resume,
  binding/digest tampering, immediate version abort, invalid input, duplicate
  bypass, random delay/rate-limit noise, and log-safety.

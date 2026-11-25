# Symbolic protocol mismatch search

This Go package provides a bounded symbolic-execution engine for a protocol
teaching platform. It takes role scripts from two (or more) sessions, explores
all interleavings in breadth-first order, and returns the shortest trace in
which an `end` event's `(principal, nonce)` pair has no matching prior `begin`
event.

Messages are built from atoms, binary pairs, and symmetric encryption:

```go
pair := symexec.Pair(symexec.Var("X"), symexec.Atom("B"))
ciphertext := symexec.Enc(symexec.Atom("K"), pair)
```

## Role scripts

A role has at most twelve steps (`symexec.MaxRoleSteps`). A step is one of:

- `Send(term)` - instantiate the template and send it on the network.
- `Receive(pattern)` - consume any attacker-constructible message matching the
  pattern; pattern variables become bound.
- `Fresh(names...)` - generate new atoms and bind them to local variables.
- `Begin(principal, nonce)` and `End(principal, nonce)` - protocol events.

Sessions supply role parameters such as principal identities and shared keys:

```go
role := symexec.NewRole("responder",
    symexec.Receive(symexec.Pair(symexec.Var("A"), symexec.Var("N"))),
    symexec.Fresh("M"),
    symexec.Send(symexec.Enc(
        symexec.Var("K"),
        symexec.Pair(symexec.Var("N"), symexec.Var("M")),
    )),
)

s := symexec.NewSession("s1", role, map[string]symexec.Term{
    "K": symexec.Atom("K"),
})
```

## Analysis

```go
scenario := symexec.NewScenario(
    symexec.Public("A", "B", "K"),
    symexec.NewSession("s1", roleA, nil),
    symexec.NewSession("s2", roleB, nil),
)

result, err := symexec.Analyze(scenario, symexec.Options{
    Budget: symexec.Budget{MaxStates: 200000, MaxSteps: 64},
    Limits: symexec.Limits{MaxComposeDepth: 3},
})
```

`result.Status` is one of `Attack`, `Safe`, or `Unknown`. `Unknown` is returned
when a state, step, time, or attacker-term budget is exhausted. For an attack,
`result.Trace` is a shortest interleaving and `result.Attack.Certificates`
contains one derivation certificate for every attacker-supplied receive
message. Trace length counts every executed role operation (`send`, `receive`,
`fresh`, `begin`, `end`), so breadth-first order minimizes the total number of
role steps.

## Attacker model

The attacker starts with scenario initial knowledge and observes every honest
send. It can:

- project known pairs (`fst`/`snd`);
- decrypt a known encryption when the key is known;
- compose new pairs and symmetric encryptions.

New compositions are bounded by `MaxComposeDepth` (default 3, atoms have depth
zero). Replaying observed terms is always allowed. Fresh atoms cannot be
guessed: they appear only if an honest role sends them and they are reachable
through the attacker's decomposition rules.

## Replayable certificates

Every attacker message has a proof tree whose leaves are either initial
knowledge or terms observed earlier in the trace. Internal rules are `fst`,
`snd`, `decrypt`, `pair`, and `encrypt`. Verification re-checks the term,
premises, depth bound, and every leaf:

```go
if err := result.Verify(scenario, symexec.Options{}); err != nil {
    // The trace or one of the attacker messages is not replayable.
}
```

`VerifyCertificates` checks the attacker proofs alone, while `VerifyTrace` also
replays role ordering, pattern matching, freshness, and event ordering.

Run the tests with:

```sh
go test ./...
```

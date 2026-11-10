# capreplay

Replays the `capabilities(7)` transformations that happen across `execve(2)`
and `capset(2)` for an **ordinary, non-root** thread, tracking the five sets:

- `P` permitted, `I` inheritable, `E` effective, `A` ambient, `B` bounding

It is a pure state machine: feed it a trace of exec/capset/prctl operations,
and it reports each set change plus the evidence for the first time every
capability enters the effective set.

## Exec transformation (non-root case from capabilities(7))

For each capability, with file sets `fP`/`fI` and file-effective flag `fE`:

```
P' = (I & fI) | (B & fP) | A
I' = I
E' = fE ? P' : A
A' = privileged_exec ? 0 : A
```

`B` survives exec unchanged. A privileged exec is any file with file
capabilities or set-user-ID/set-group-ID mode bits; on an `MS_NOSUID` mount
those are ignored and the file is treated as unprivileged.

## Other operations modeled

- `capset`: `P'` must be a subset of old `P`; `E'` must be a subset of new
  `P`; without `CAP_SETPCAP` in the old effective set, new `I` must be a
  subset of old `I | old P`. Rejected calls leave state unchanged (`EPERM`).
- `PR_CAP_AMBIENT_RAISE`: the cap must be in both `P` and `I`; ambient is
  always kept within `P & I` (capset/cap drops pull it back automatically).
- `PR_CAPBSET_DROP`: requires `CAP_SETPCAP` in `E`; the bit is removed from
  `B` and simultaneously from `P`, `I`, `E` and `A`.

## Trace syntax

Blank lines and `#` comments are ignored. Capability lists accept names
(`cap_net_raw`), numbers (`13`), comma-separated lists, `none` or `all`.

```
init p=<caps> i=<caps> e=<caps> [b=<caps>]
exec <path> [fp=<caps>] [fi=<caps>] [fe=0|1] [suid] [sgid] [nosuid]
capset p=<caps> i=<caps> e=<caps>
ambient raise <caps>
ambient lower <caps>
ambient drop
bset drop <caps>
```

Without an `init` line the thread starts with all 41 capabilities (0..40) in
`B` and empty `P/I/E/A`, matching a freshly execed unprivileged process.

## Usage

```
go run . examples/full.trace
go run . --strict trace.txt   # exit at the first operation the kernel rejects
```

The report shows, per operation:

- set deltas (`+added`/`-removed`) and the full five-set snapshot;
- `FIRST-EFFECTIVE` lines naming the exact formula term
  (`B & fP`, `I & fI`, or ambient `A`) that first made each cap effective;
- a closing table of every capability that ever became effective with the
  originating line and reason;
- any operations that would return `EPERM`, which are skipped by default so
  the rest of the trace still replays.

## Scope notes

- Only the non-root exec rule is modeled; the legacy root (`euid 0`) rule and
  user-namespace transitions are out of scope.
- `securebits`/`NO_NEW_PRIVS` ambient suppression is not modeled; ambient
  raising is assumed permitted by the security-bits configuration.
- The capability universe is 0..40 (`CAP_CHECKPOINT_RESTORE`, current
  `CAP_LAST_CAP`).

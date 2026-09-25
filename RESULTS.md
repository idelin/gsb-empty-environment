# Actual test run results

Date: 2026-09-25 19:14:40 CST
Node: v23.9.0 (built-in type stripping + node:test, no dependencies)
Command: node --test test/*.test.ts
Exit code: 0

```
(node:95263) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ two subjects have isolated cache entries and decisions (1.039791ms)
✔ attribute hot-update invalidates only dependent cache entries (0.245417ms)
✔ missing-attribute reads are tracked so later updates invalidate unknowns (0.402917ms)
✔ rule hot-update invalidates only that rule (0.483958ms)
✔ a failed attribute read leaves no reusable (allow) cache entry (55.478167ms)
✔ cancellation leaves no reusable cache entry (6.483042ms)
✔ abort error type is used for cancelled reads (0.3335ms)
(node:95268) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ finite-domain truth enumeration: DAG engine == naive reference (145.115084ms)
✔ finite-domain truth enumeration: partial evaluation preserves semantics (157.774625ms)
✔ compilation shares identical subtrees in the DAG (0.566042ms)
✔ engine residual API matches partial evaluation over the same rule (0.15575ms)
(node:95272) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ false AND returns only the falsifying branch as the minimal slice (1.369125ms)
✔ unknown outcome keeps only the unknown branches in the slice (0.188291ms)
✔ true OR slice is the single succeeding branch (0.212167ms)
✔ all-true AND keeps every branch (all are load-bearing) (0.207208ms)
(node:95278) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ false AND <expensive attribute> never reads the expensive attribute (7.600125ms)
✔ true OR <expensive attribute> never reads the expensive attribute (0.174542ms)
✔ attribute updated mid-request triggers a retry, never a mixed-version allow (0.384375ms)
✔ continuously changing versions surface as conflict, never as allow (15.12675ms)
(node:95286) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ x OR NOT x stays unknown when x is missing (never simplified to false) (1.34775ms)
✔ unknown AND true is unknown, not false (0.374375ms)
✔ unknown OR false is unknown, not false (0.189417ms)
✔ non-integer attribute values are unknown, not false (0.520917ms)
✔ partial evaluation folds known attributes and keeps the residual condition (0.34925ms)
ℹ tests 24
ℹ suites 0
ℹ pass 24
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 1327.748291
```

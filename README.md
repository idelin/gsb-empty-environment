# schedule

Weekly recurring-meeting calendar library + JSON-lines CLI, Go standard
library only. Fixed UTC offsets; no IANA time zone database.

## Model

- A rule starts on a Monday and produces one instance per week for a
  finite number of weeks. Identity = `ruleID#weekIndex`, anchored at the
  original start Monday via civil-date arithmetic; editing weekday,
  minute, duration or UTC offset never changes identity.
- Range revisions take effect from a given week index; ties on the same
  index are won by the greater revision number.
- Priority: cancellation > single-instance override > range revision.
  Restore removes the cancellation mark.
- Queries are UTC half-open intervals `[from, to)` intersected with the
  actual instance interval `[start, end)`, served from an incrementally
  updated sorted index (binary search), never by expanding all weeks.

## CLI

`go run ./cmd/schedule` reads one JSON command per line on stdin and
writes one JSON response per line on stdout.

```json
{"op":"new_rule","id":"standup","start":"2026-01-05","weeks":8}
{"op":"add_revision","rule":"standup","rev":1,"from_week":0,"weekday":0,"minute":600,"duration_min":60,"offset_min":480}
{"op":"override","rule":"standup","date":"2026-01-26","rev":9,"weekday":5,"minute":1200,"duration_min":30,"offset_min":480}
{"op":"cancel","rule":"standup","week":1}
{"op":"restore","rule":"standup","week":1}
{"op":"query","rule":"standup","from":"2026-01-05T00:00:00Z","to":"2026-02-15T00:00:00Z"}
{"op":"diff","rule":"standup","from_version":1,"to_version":2}
```

- `weekday`: 0 = Monday .. 6 = Sunday; `minute`: minutes after local
  midnight; `offset_min`: fixed UTC offset in minutes.
- `override`/`cancel`/`restore` accept `week` (index) or `date`
  (YYYY-MM-DD, converted via the rule's original start Monday).
- Times are RFC3339 with explicit offset; responses render instance
  start/end in the instance's own offset.

## Test

```sh
go test ./...
```

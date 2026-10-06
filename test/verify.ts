import { Calendar } from "../src/calendar";
import { Instance, WeeklyRule, instanceId } from "../src/recurrence";

let failures = 0;

function check(cond: boolean, msg: string): void {
  if (!cond) {
    failures++;
    console.error(`FAIL: ${msg}`);
  }
}

function eq(actual: unknown, expected: unknown, msg: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  check(a === e, `${msg}\n  actual:   ${a}\n  expected: ${e}`);
}

/**
 * Independent oracle: scans day by day from rule.start, decides each day's
 * candidacy with its own arithmetic, applies count BEFORE exdates, then
 * applies reschedule exceptions. Returns instances in [from, to) sorted by
 * (date, originalDate, id).
 */
function oracleScan(
  rule: WeeklyRule,
  exceptions: ReadonlyMap<string, number>,
  from: number,
  to: number
): Instance[] {
  const exdates = new Set(rule.exdates ?? []);
  const weekdays = new Set(rule.weekdays);
  const startWeek = rule.start - (((rule.start % 7) + 7) % 7);
  // An exception may pull an instance from beyond the window into it, so
  // scan far enough to cover every rescheduled original date.
  let scanTo = to;
  for (const id of exceptions.keys()) {
    const originalDate = Number(id.slice("inst@".length));
    if (originalDate + 1 > scanTo) scanTo = originalDate + 1;
  }
  const out: Instance[] = [];
  let candidates = 0;
  for (let d = rule.start; d < scanTo; d++) {
    const wd = ((d % 7) + 7) % 7;
    const weekIndex = Math.floor((d - startWeek) / 7);
    const isCand = d >= rule.start && weekdays.has(wd) && weekIndex % rule.interval === 0;
    if (!isCand) continue;
    candidates++;
    if (rule.count !== undefined && candidates > rule.count) break;
    if (exdates.has(d)) continue;
    const id = instanceId(d);
    const date = exceptions.has(id) ? (exceptions.get(id) as number) : d;
    if (date >= from && date < to) {
      out.push({ id, date, originalDate: d });
    }
  }
  out.sort((a, b) =>
    a.date !== b.date
      ? a.date - b.date
      : a.originalDate !== b.originalDate
      ? a.originalDate - b.originalDate
      : a.id < b.id
      ? -1
      : a.id > b.id
      ? 1
      : 0
  );
  return out;
}

function drainPages(cal: Calendar, from: number, to: number, limit: number): Instance[] {
  const all: Instance[] = [];
  let cursor: string | undefined = undefined;
  for (let guard = 0; guard < 10000; guard++) {
    const page = cal.expand({ from, to, limit, cursor });
    all.push(...page.items);
    if (page.nextCursor === null) return all;
    check(page.items.length > 0, "page with nextCursor must be non-empty");
    cursor = page.nextCursor;
  }
  throw new Error("pagination did not terminate");
}

function crossCheck(
  name: string,
  rule: WeeklyRule,
  exceptions: Map<string, number>,
  from: number,
  to: number
): void {
  const cal = new Calendar(rule);
  for (const [id, newDate] of exceptions) cal.reschedule(id, newDate);
  const expected = oracleScan(rule, exceptions, from, to);
  eq(cal.expandAll(from, to), expected, `${name}: expandAll vs day-by-day oracle`);
  for (const limit of [1, 2, 3, 5, 7, 64]) {
    eq(drainPages(cal, from, to, limit), expected, `${name}: paginated(limit=${limit}) vs oracle`);
  }
}

// --- Scenario 1: basic weekly, count termination ---------------------------
{
  // start ordinal 8 => weekday 1; weekdays Mon/Wed/Fri pattern (1,3,5)
  const rule: WeeklyRule = { start: 8, interval: 1, weekdays: [1, 3, 5], count: 10 };
  crossCheck("basic-count10", rule, new Map(), 0, 200);
  const all = new Calendar(rule).expandAll(-100, 1000);
  eq(all.length, 10, "basic-count10: exactly 10 occurrences");
  eq(
    all.map((i) => i.date),
    [8, 10, 12, 15, 17, 19, 22, 24, 26, 29],
    "basic-count10: expected ordinals"
  );
  eq(new Calendar(rule).expandAll(30, 1000), [], "basic-count10: nothing after last occurrence");
}

// --- Scenario 2: interval + exdates, count consumed pre-exclusion ----------
{
  const rule: WeeklyRule = {
    start: 14,
    interval: 3,
    weekdays: [0, 6],
    count: 8,
    exdates: [20, 41],
  };
  crossCheck("interval3-exdates", rule, new Map(), 0, 500);
  const all = new Calendar(rule).expandAll(0, 500);
  // Candidates: 14,20 | 35,41 | 56,62 | 77,83 -> exdates remove 20 and 41.
  eq(
    all.map((i) => i.date),
    [14, 35, 56, 62, 77, 83],
    "interval3-exdates: visible dates (excluded candidates still consume count)"
  );
  eq(all.length, 6, "interval3-exdates: count not refilled after exclusions");
  eq(new Calendar(rule).expandAll(84, 500), [], "interval3-exdates: empty beyond termination");
}

// --- Scenario 3: exdate on the final candidate -----------------------------
{
  const rule: WeeklyRule = { start: 1, interval: 1, weekdays: [1], count: 4, exdates: [22] };
  crossCheck("exdate-on-last", rule, new Map(), 0, 100);
  const all = new Calendar(rule).expandAll(0, 100);
  eq(
    all.map((i) => i.date),
    [1, 8, 15],
    "exdate-on-last: last visible is candidate #3, series not extended"
  );
}

// --- Scenario 4: reschedule onto another instance's date -------------------
{
  const rule: WeeklyRule = { start: 10, interval: 2, weekdays: [2, 4], count: 12 };
  const base = new Calendar(rule).expandAll(0, 1000);
  const moved = base[2]; // third occurrence
  const target = base[6]; // seventh occurrence
  const exceptions = new Map<string, number>([[moved.id, target.date]]);
  crossCheck("reschedule-onto-instance", rule, exceptions, 0, 1000);

  const cal = new Calendar(rule);
  const result = cal.reschedule(moved.id, target.date);
  eq(result, { id: moved.id, date: target.date, originalDate: moved.date }, "reschedule keeps id");
  const onTarget = cal.expandAll(target.date, target.date + 1);
  eq(onTarget.length, 2, "reschedule-onto-instance: two instances share the target date");
  eq(
    onTarget.map((i) => i.id).sort(),
    [moved.id, target.id].sort(),
    "reschedule-onto-instance: both ids present on target date"
  );
  const onSource = cal.expandAll(moved.date, moved.date + 1);
  eq(onSource, [], "reschedule-onto-instance: source date vacated");
  const total = cal.expandAll(0, 1000);
  eq(total.length, 12, "reschedule-onto-instance: total instance count unchanged");
  eq(
    total.filter((i) => i.id === moved.id)[0].originalDate,
    moved.date,
    "reschedule-onto-instance: originalDate preserved"
  );
}

// --- Scenario 5: reschedule to non-candidate and to exdate -----------------
{
  const rule: WeeklyRule = {
    start: 0,
    interval: 1,
    weekdays: [1],
    count: 6,
    exdates: [8],
  };
  const base = new Calendar(rule).expandAll(0, 100);
  const exceptions = new Map<string, number>([
    [base[0].id, 3],  // weekday 3: not a candidate day
    [base[1].id, 8],  // an exdate of the rule
  ]);
  crossCheck("reschedule-offgrid", rule, exceptions, 0, 100);
  const cal = new Calendar(rule);
  cal.reschedule(base[0].id, 3);
  cal.reschedule(base[1].id, 8);
  eq(
    cal.expandAll(3, 4).map((i) => i.id),
    [base[0].id],
    "reschedule-offgrid: instance lands on non-candidate date"
  );
  eq(
    cal.expandAll(8, 9).map((i) => i.id),
    [base[1].id],
    "reschedule-offgrid: instance lands on exdate"
  );
}

// --- Scenario 6: termination boundary, count 0 / 1 / exact -----------------
{
  const rule0: WeeklyRule = { start: 5, interval: 1, weekdays: [0, 2, 4, 6], count: 0 };
  eq(new Calendar(rule0).expandAll(0, 1000), [], "count0: no occurrences");
  crossCheck("count0", rule0, new Map(), 0, 1000);

  const rule1: WeeklyRule = { start: 5, interval: 1, weekdays: [0, 2, 4, 6], count: 1 };
  const one = new Calendar(rule1).expandAll(0, 1000);
  eq(one.map((i) => i.date), [6], "count1: single occurrence on first candidate weekday");
  eq(new Calendar(rule1).expandAll(7, 1000), [], "count1: empty after the single occurrence");
  crossCheck("count1", rule1, new Map(), 0, 100);

  // Window boundary exactly on the last occurrence: [from, to) excludes it.
  const ruleN: WeeklyRule = { start: 0, interval: 2, weekdays: [6], count: 3 };
  const calN = new Calendar(ruleN);
  eq(calN.expandAll(0, 20).map((i) => i.date), [6], "boundary: to is exclusive");
  eq(calN.expandAll(0, 21).map((i) => i.date), [6, 20], "boundary: to just past occurrence");
  eq(calN.expandAll(0, 35).map((i) => i.date), [6, 20, 34], "boundary: last occurrence included");
  eq(calN.expandAll(34, 35).map((i) => i.date), [34], "boundary: exact 1-day window");
  eq(calN.expandAll(35, 36), [], "boundary: day after termination is empty");
  crossCheck("boundary", ruleN, new Map(), 0, 40);
}

// --- Scenario 7: cursor stability with co-located instances ----------------
{
  const rule: WeeklyRule = { start: 2, interval: 1, weekdays: [2], count: 5 };
  const base = new Calendar(rule).expandAll(0, 100);
  // Pile instances 0,1,2 all onto instance 3's date.
  const exceptions = new Map<string, number>([
    [base[0].id, base[3].date],
    [base[1].id, base[3].date],
    [base[2].id, base[3].date],
  ]);
  crossCheck("co-located", rule, exceptions, 0, 100);
  const cal = new Calendar(rule);
  for (const [id, nd] of exceptions) cal.reschedule(id, nd);
  const piled = cal.expandAll(base[3].date, base[3].date + 1);
  eq(piled.length, 4, "co-located: four instances on one date");
  const seen = new Set(drainPages(cal, 0, 100, 1).map((i) => i.id));
  eq(seen.size, 5, "co-located: page-by-page(limit=1) yields each id exactly once");
}

// --- Scenario 8: negative ordinals + error handling ------------------------
{
  const rule: WeeklyRule = { start: -20, interval: 2, weekdays: [0, 3], count: 6 };
  crossCheck("negative-ordinals", rule, new Map(), -50, 50);

  const cal = new Calendar({ start: 0, interval: 1, weekdays: [0], count: 2 });
  let threw = false;
  try {
    cal.reschedule("inst@999", 5);
  } catch {
    threw = true;
  }
  check(threw, "reschedule of unknown id throws");
  threw = false;
  try {
    new Calendar({ start: 0, interval: 0, weekdays: [0] });
  } catch {
    threw = true;
  }
  check(threw, "interval 0 rejected");
}

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("All recurrence checks passed.");

import assert from "node:assert/strict";
import test from "node:test";
import { ScheduleInfeasibleError, scheduleUpgrades, validateSchedule, } from "../src/index.js";
function expectValid(input) {
    const result = scheduleUpgrades(input);
    const validation = validateSchedule(input, result.startMinutes);
    assert.deepEqual(validation.violations, []);
    assert.equal(validation.valid, true);
    return result;
}
test("all cells start at zero when all capacity constraints allow it", () => {
    const input = {
        startUtc: 0,
        cells: [
            { id: "a", durationMinutes: 5 },
            { id: "b", durationMinutes: 3 },
            { id: "c", durationMinutes: 10 },
        ],
        U: 1,
        M: 3,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [0, 0, 0]);
    assert.equal(result.makespanMinutes, 10);
    assert.equal(result.sumStartMinutes, 0);
});
test("DR pair is postponed and equal-duration tie uses lexicographic starts", () => {
    const input = {
        startUtc: 0,
        cells: [
            { id: "dr-0", durationMinutes: 10, weight: 1 },
            { id: "dr-1", durationMinutes: 10, weight: 1 },
        ],
        U: 1,
        M: 2,
        drEdges: [["dr-0", "dr-1"]],
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [0, 10]);
    assert.equal(result.makespanMinutes, 20);
    assert.equal(result.sumStartMinutes, 10);
});
test("minimum start sum chooses the shorter DR cell first at equal makespan", () => {
    const input = {
        startUtc: 0,
        cells: [
            { id: "long-index-zero", durationMinutes: 10, weight: 1 },
            { id: "short-index-one", durationMinutes: 5, weight: 1 },
        ],
        U: 1,
        M: 2,
        drEdges: [["long-index-zero", "short-index-one"]],
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [5, 0]);
    assert.equal(result.makespanMinutes, 15);
    assert.equal(result.sumStartMinutes, 5);
});
test("global traffic limit splits cells into waves", () => {
    const input = {
        startUtc: 0,
        totalTrafficWeight: 6,
        cells: [
            { id: "g0", durationMinutes: 4, trafficWeight: 2 },
            { id: "g1", durationMinutes: 4, trafficWeight: 2 },
            { id: "g2", durationMinutes: 4, trafficWeight: 2 },
        ],
        U: { numerator: 2, denominator: 3 },
        M: 3,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [0, 0, 4]);
    assert.equal(result.makespanMinutes, 8);
    assert.equal(result.sumStartMinutes, 4);
    const activeAtZero = result.cells.filter((cell) => cell.startMinute <= 0 && 0 < cell.endMinute);
    assert.equal(activeAtZero.length, 2);
    assert.equal(activeAtZero.reduce((sum, cell) => sum + cell.trafficWeight, 0), 4);
});
test("regional M=1 serializes same-region cells", () => {
    const input = {
        startUtc: 0,
        cells: [
            { id: "r0", regionId: "east", durationMinutes: 7 },
            { id: "r1", regionId: "east", durationMinutes: 7 },
        ],
        U: 1,
        M: 1,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [0, 7]);
    assert.equal(result.makespanMinutes, 14);
});
test("health observation bake time occupies slots and budget", () => {
    const input = {
        startUtc: 0,
        cells: [
            { id: "upgrade", durationMinutes: 6, trafficWeight: 1 },
            { id: "upgrade-bake", upgradeMinutes: 3, bakeMinutes: 3, trafficWeight: 1 },
        ],
        U: { numerator: 1, denominator: 2 },
        totalTrafficWeight: 2,
        M: 2,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [0, 6]);
    assert.deepEqual(result.endMinutes, [6, 12]);
    assert.equal(result.makespanMinutes, 12);
});
test("overnight freeze window ends on the next day", () => {
    const input = {
        startUtc: 21 * 60,
        cells: [{ id: "overnight", regionId: "night", durationMinutes: 120 }],
        regions: {
            night: {
                freezeWindows: [{ start: "22:00", end: "06:00" }],
            },
        },
        U: 1,
        M: 1,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [540]);
    assert.equal(result.cells[0].startUtc, "1970-01-02T06:00:00.000Z");
});
test("freeze boundary may be touched: finish exactly at start or start exactly at end", () => {
    const input = {
        startUtc: 7 * 60,
        cells: [{ id: "freeze-touch", regionId: "r", durationMinutes: 60 }],
        regions: {
            r: {
                freezeWindows: [{ start: "08:00", end: "10:00" }],
            },
        },
        U: 1,
        M: 1,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [0]);
    assert.equal(result.cells[0].startUtc, "1970-01-01T07:00:00.000Z");
    const delayed = {
        ...input,
        startUtc: 7 * 60 + 30,
    };
    const delayedResult = expectValid(delayed);
    assert.deepEqual(delayedResult.startMinutes, [150]);
    assert.equal(delayedResult.cells[0].startUtc, "1970-01-01T10:00:00.000Z");
});
test("DST offset jump changes the freeze-window UTC end used by the scheduler", () => {
    const day = Math.floor(Date.UTC(2026, 2, 29) / 60000);
    const transition = day + 60;
    const input = {
        startUtc: day + 30,
        cells: [{ id: "dst-cell", regionId: "dst", upgradeMinutes: 60 }],
        regions: {
            dst: {
                freezeWindows: [{ start: "01:00", end: "05:00" }],
                utcOffsetIntervals: [
                    { endUtc: transition, offsetMinutes: 0 },
                    { startUtc: transition, offsetMinutes: 120 },
                ],
            },
        },
        U: 1,
        M: 1,
    };
    const result = expectValid(input);
    assert.deepEqual(result.startMinutes, [150]);
    assert.equal(result.cells[0].startUtc, new Date((day + 180) * 60000).toISOString());
});
test("cell longer than the gap between freezes reports infeasible", () => {
    const input = {
        startUtc: Math.floor(Date.UTC(2026, 0, 1) / 60000),
        cells: [{ id: "too-long", regionId: "peak", upgradeMinutes: 121 }],
        regions: {
            peak: {
                freezeWindows: [
                    { start: "00:00", end: "08:00" },
                    { start: "10:00", end: "24:00" },
                ],
            },
        },
        U: 1,
        M: 1,
    };
    assert.throws(() => scheduleUpgrades(input), (error) => error instanceof ScheduleInfeasibleError &&
        error.reason === "CELL_NEVER_FITS_FREEZE_WINDOWS" &&
        /too-long/.test(error.message));
});
function oracleOptimal(testCase, maxStart) {
    const count = testCase.durations.length;
    let best = null;
    const frozenMinute = (minute) => {
        const phase = ((minute % 1440) + 1440) % 1440;
        return testCase.freezes.some(([start, end]) => start <= phase && phase < end);
    };
    const freezeLegal = (start, duration) => {
        for (let minute = start; minute < start + duration; minute += 1) {
            if (frozenMinute(minute))
                return false;
        }
        return true;
    };
    const consider = (starts) => {
        for (let i = 0; i < count; i += 1) {
            if (!freezeLegal(starts[i], testCase.durations[i]))
                return;
        }
        for (const [a, b] of testCase.edges) {
            const aEnd = starts[a] + testCase.durations[a];
            const bEnd = starts[b] + testCase.durations[b];
            if (starts[a] < bEnd && starts[b] < aEnd)
                return;
        }
        const makespan = Math.max(...starts.map((start, i) => start + testCase.durations[i]));
        for (let minute = 0; minute < makespan; minute += 1) {
            const activeIndices = starts.flatMap((start, i) => start <= minute && minute < start + testCase.durations[i] ? [i] : []);
            if (activeIndices.length > testCase.m)
                return;
            const activeWeight = activeIndices.reduce((sum, i) => sum + testCase.weights[i], 0);
            if (activeWeight * testCase.denominator >
                testCase.numerator * testCase.totalWeight) {
                return;
            }
        }
        const sum = starts.reduce((a, b) => a + b, 0);
        const key = [makespan, sum, starts];
        if (!best ||
            key[0] < best.key[0] ||
            (key[0] === best.key[0] && key[1] < best.key[1]) ||
            (key[0] === best.key[0] &&
                key[1] === best.key[1] &&
                lexSmaller(starts, best.key[2]))) {
            best = { key: [makespan, sum, [...starts]] };
        }
    };
    function lexSmaller(candidate, current) {
        for (let i = 0; i < candidate.length; i += 1) {
            if (candidate[i] !== current[i])
                return candidate[i] < current[i];
        }
        return false;
    }
    const starts = new Array(count).fill(0);
    const enumerate = (index) => {
        if (index === count) {
            consider(starts);
            return;
        }
        for (let value = 0; value <= maxStart; value += 1) {
            starts[index] = value;
            enumerate(index + 1);
        }
    };
    enumerate(0);
    return best;
}
const oracleCases = [
    {
        name: "serial global budget",
        durations: [2, 3, 2],
        weights: [1, 1, 1],
        totalWeight: 3,
        numerator: 1,
        denominator: 3,
        m: 3,
        edges: [],
        freezes: [],
    },
    {
        name: "two-unit global wave",
        durations: [2, 2, 3],
        weights: [1, 1, 1],
        totalWeight: 3,
        numerator: 2,
        denominator: 3,
        m: 3,
        edges: [],
        freezes: [],
    },
    {
        name: "regional concurrency one",
        durations: [2, 3, 2],
        weights: [1, 1, 1],
        totalWeight: 3,
        numerator: 1,
        denominator: 1,
        m: 1,
        edges: [],
        freezes: [],
    },
    {
        name: "DR ordering",
        durations: [3, 2, 2],
        weights: [1, 1, 1],
        totalWeight: 3,
        numerator: 1,
        denominator: 1,
        m: 3,
        edges: [[0, 1]],
        freezes: [],
    },
    {
        name: "freeze gap",
        durations: [2, 3, 2],
        weights: [1, 1, 1],
        totalWeight: 3,
        numerator: 1,
        denominator: 1,
        m: 3,
        edges: [],
        freezes: [
            [4 * 60, 6 * 60],
            [10 * 60, 12 * 60],
        ],
    },
    {
        name: "freeze plus M1",
        durations: [2, 2, 2],
        weights: [1, 1, 1],
        totalWeight: 3,
        numerator: 1,
        denominator: 1,
        m: 1,
        edges: [],
        freezes: [[4, 6]],
    },
    {
        name: "capacity and DR",
        durations: [2, 2, 3],
        weights: [2, 2, 1],
        totalWeight: 5,
        numerator: 3,
        denominator: 5,
        m: 2,
        edges: [[0, 2]],
        freezes: [],
    },
    {
        name: "all constraints small",
        durations: [2, 3, 2],
        weights: [1, 2, 1],
        totalWeight: 4,
        numerator: 3,
        denominator: 4,
        m: 2,
        edges: [[0, 1]],
        freezes: [[5, 7]],
    },
];
for (const testCase of oracleCases) {
    test(`minute-grid oracle: ${testCase.name}`, () => {
        const maxStart = 18;
        const input = {
            startUtc: 0,
            totalTrafficWeight: testCase.totalWeight,
            U: { numerator: testCase.numerator, denominator: testCase.denominator },
            M: testCase.m,
            cells: testCase.durations.map((duration, i) => ({
                id: `c${i}`,
                regionId: "oracle-region",
                durationMinutes: duration,
                trafficWeight: testCase.weights[i],
            })),
            regions: {
                "oracle-region": {
                    utcOffsetMinutes: 0,
                    freezeWindows: testCase.freezes.map(([start, end]) => ({ start, end })),
                },
            },
            drEdges: testCase.edges.map(([a, b]) => [`c${a}`, `c${b}`]),
        };
        const oracle = oracleOptimal(testCase, maxStart);
        assert.ok(oracle, "minute-grid oracle should find a feasible schedule");
        const result = expectValid(input);
        assert.equal(result.makespanMinutes, oracle.key[0]);
        assert.equal(result.sumStartMinutes, oracle.key[1]);
        assert.deepEqual(result.startMinutes, oracle.key[2]);
        const cellEnds = result.startMinutes.map((start, i) => start + testCase.durations[i]);
        const freezeEnds = testCase.freezes.map(([, end]) => end);
        for (const start of result.startMinutes) {
            const isGlobalStart = start === 0;
            const isCellEnd = cellEnds.includes(start);
            const isFreezeEnd = freezeEnds.includes(start % 1440);
            assert.equal(isGlobalStart || isCellEnd || isFreezeEnd, true, `start ${start} must be a key event time`);
        }
    });
}
//# sourceMappingURL=scheduler.test.js.map
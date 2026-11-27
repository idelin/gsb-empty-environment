import assert from "node:assert/strict";
import test from "node:test";
import { CardinalityViolationError, MergeTable, UniqueViolationError, deleteRow, doNothing, executeMerge, insert, update, validateUniqueConstraints, } from "../src/index.js";
function isCardinalityViolation(error) {
    return error instanceof CardinalityViolationError
        && error.code === "CARDINALITY_VIOLATION";
}
function isUniqueViolation(error) {
    return error instanceof UniqueViolationError
        && error.code === "UNIQUE_VIOLATION";
}
test("WHEN MATCHED branches stop at the first TRUE condition", () => {
    const calls = [];
    const target = [{ id: 1, marker: "initial" }];
    const source = [{ id: 1, flag: true }];
    const result = executeMerge({
        target,
        source,
        on: (t, s) => t.id === s.id,
        whenMatched: [
            {
                condition: (t, s) => {
                    calls.push("first");
                    return t.id === s.id && s.flag === true;
                },
                action: update({ marker: "first" }),
            },
            {
                condition: () => {
                    calls.push("second");
                    return true;
                },
                action: update({ marker: "second" }),
            },
        ],
    });
    assert.deepEqual(calls, ["first"]);
    assert.deepEqual(result.rows, [{ id: 1, marker: "first" }]);
    assert.equal(result.stats.updated, 1);
});
test("branch shorthand and nested action objects are normalized", () => {
    const withShorthand = executeMerge({
        target: [{ id: 1, value: 0 }],
        source: [{ id: 1, value: 5 }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            { condition: true, update: { value: (t, s) => s.value } },
        ],
    });
    assert.deepEqual(withShorthand.rows, [{ id: 1, value: 5 }]);
    const withNestedThen = executeMerge({
        target: [],
        source: [{ id: 2, value: 7 }],
        on: (t, s) => t.id === s.id,
        whenNotMatched: [
            { then: { insert: (s) => ({ id: s.id, value: s.value }) } },
        ],
    });
    assert.deepEqual(withNestedThen.rows, [{ id: 2, value: 7 }]);
});
test("uniform two-argument callbacks see null on the unmatched side", () => {
    const inserted = executeMerge({
        target: [],
        source: [{ id: 4, value: 9 }],
        on: (t, s) => t.id === s.id,
        whenNotMatched: [
            {
                action: insert((target, source) => {
                    assert.equal(target, null);
                    return { id: source.id, value: source.value };
                }),
            },
        ],
    });
    assert.deepEqual(inserted.rows, [{ id: 4, value: 9 }]);
    const updated = executeMerge({
        target: [{ id: 1, active: true }],
        source: [],
        on: (t, s) => t.id === s.id,
        whenNotMatchedBySource: [
            {
                condition: (target, source) => {
                    assert.equal(source, null);
                    return target.active === true;
                },
                action: update({ flag: true }),
            },
        ],
    });
    assert.deepEqual(updated.rows, [{ id: 1, active: true, flag: true }]);
});
test("a FALSE branch falls through to the next branch", () => {
    const calls = [];
    const result = executeMerge({
        target: [{ id: 1, marker: "initial" }],
        source: [{ id: 1, flag: false }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            {
                condition: (t, s) => {
                    calls.push("first");
                    return s.flag === true;
                },
                action: update({ marker: "first" }),
            },
            {
                condition: (t, s) => {
                    calls.push("second");
                    return s.flag === false;
                },
                action: update({ marker: "second" }),
            },
        ],
    });
    assert.deepEqual(calls, ["first", "second"]);
    assert.deepEqual(result.rows, [{ id: 1, marker: "second" }]);
});
test("DO NOTHING also short-circuits later MATCHED branches", () => {
    const calls = [];
    const result = executeMerge({
        target: [{ id: 1, marker: "initial" }],
        source: [{ id: 1 }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            { condition: () => { calls.push("skip"); return true; }, action: doNothing() },
            { condition: () => { calls.push("update"); return true; }, action: update({ marker: "changed" }) },
        ],
    });
    assert.deepEqual(calls, ["skip"]);
    assert.deepEqual(result.rows, [{ id: 1, marker: "initial" }]);
    assert.equal(result.stats.doNothing, 1);
    assert.equal(result.stats.updated, 0);
});
test("two source rows modifying the same target raise CARDINALITY_VIOLATION", () => {
    const target = [{ id: 1, value: 0 }];
    const before = structuredClone(target);
    assert.throws(() => executeMerge({
        target,
        source: [{ id: 1, value: 10 }, { id: 1, value: 20 }],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ value: (t, s) => s.value }) }],
    }), (error) => {
        assert.ok(isCardinalityViolation(error));
        assert.equal(error.sourceIndex, 1);
        assert.equal(error.action, "update");
        return true;
    });
    assert.deepEqual(target, before, "failed MERGE must not mutate caller rows");
});
test("an earlier DO NOTHING does not mark the target as modified", () => {
    const result = executeMerge({
        target: [{ id: 1, value: 0 }],
        source: [{ id: 1, value: 10, skip: true }, { id: 1, value: 20, skip: false }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            { condition: (t, s) => s.skip === true, action: doNothing() },
            { action: update({ value: (t, s) => s.value }) },
        ],
    });
    assert.deepEqual(result.rows, [{ id: 1, value: 20 }]);
    assert.equal(result.stats.doNothing, 1);
    assert.equal(result.stats.updated, 1);
});
test("a later DO NOTHING does not attempt a second write", () => {
    const result = executeMerge({
        target: [{ id: 1, value: 0 }],
        source: [{ id: 1, value: 10, skip: false }, { id: 1, value: 20, skip: true }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            { condition: (t, s) => s.skip === true, action: doNothing() },
            { action: update({ value: (t, s) => s.value }) },
        ],
    });
    assert.deepEqual(result.rows, [{ id: 1, value: 10 }]);
    assert.equal(result.stats.updated, 1);
    assert.equal(result.stats.doNothing, 1);
});
test("an earlier branch whose conditions are all FALSE is not a write", () => {
    const result = executeMerge({
        target: [{ id: 1, value: 0 }],
        source: [{ id: 1, value: 10, apply: false }, { id: 1, value: 20, apply: true }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            { condition: (t, s) => s.apply === true, action: update({ value: (t, s) => s.value }) },
        ],
    });
    assert.deepEqual(result.rows, [{ id: 1, value: 20 }]);
});
test("UPDATE followed by DELETE for the same target is a cardinality error", () => {
    assert.throws(() => executeMerge({
        target: [{ id: 1, value: 0 }],
        source: [{ id: 1, value: 10, remove: false }, { id: 1, value: 20, remove: true }],
        on: (t, s) => t.id === s.id,
        whenMatched: [
            { condition: (t, s) => s.remove === true, action: deleteRow() },
            { action: update({ value: (t, s) => s.value }) },
        ],
    }), (error) => isCardinalityViolation(error));
});
test("rows inserted by this MERGE are invisible to later source rows", () => {
    const result = executeMerge({
        target: [],
        source: [{ id: 1, parent: null }, { id: 2, parent: 1 }],
        // If the first inserted row were visible, source 2 would match it instead
        // of taking the WHEN NOT MATCHED branch.
        on: (t, s) => t.id === s.parent,
        whenNotMatched: [
            { action: insert((s) => ({ id: s.id, parent: s.parent })) },
        ],
    });
    assert.deepEqual(result.rows, [
        { id: 1, parent: null },
        { id: 2, parent: 1 },
    ]);
    assert.equal(result.stats.inserted, 2);
    assert.equal(result.stats.matchedPairs, 0);
});
test("WHEN NOT MATCHED branches short-circuit at the first TRUE condition", () => {
    let laterBranchCalls = 0;
    const result = executeMerge({
        target: [],
        source: [{ id: 1 }],
        on: (t, s) => t.id === s.id,
        whenNotMatched: [
            { condition: () => true, action: doNothing() },
            {
                condition: () => {
                    laterBranchCalls += 1;
                    return true;
                },
                action: insert((s) => ({ id: s.id })),
            },
        ],
    });
    assert.equal(laterBranchCalls, 0);
    assert.deepEqual(result.rows, []);
    assert.equal(result.stats.doNothing, 1);
    assert.equal(result.stats.inserted, 0);
});
test("WHEN NOT MATCHED BY SOURCE branches short-circuit", () => {
    const result = executeMerge({
        target: [{ id: 1, active: true }],
        source: [],
        on: (t, s) => t.id === s.id,
        whenNotMatchedBySource: [
            { condition: () => true, action: update({ flag: true }) },
            { action: deleteRow() },
        ],
    });
    assert.deepEqual(result.rows, [{ id: 1, active: true, flag: true }]);
    assert.equal(result.stats.updated, 1);
    assert.equal(result.stats.deleted, 0);
});
test("DEFERRED unique index permits an atomic unique-key swap", () => {
    const result = executeMerge({
        target: [
            { id: 1, code: "A" },
            { id: 2, code: "B" },
        ],
        source: [
            { id: 1, code: "B" },
            { id: 2, code: "A" },
        ],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ code: (t, s) => s.code }) }],
        uniqueIndexes: [
            { name: "ux_code", columns: ["code"], timing: "deferred", nulls: "distinct" },
        ],
    });
    assert.deepEqual(result.rows, [
        { id: 1, code: "B" },
        { id: 2, code: "A" },
    ]);
    assert.equal(result.stats.updated, 2);
});
test("IMMEDIATE unique index rejects the same swap and rolls back", () => {
    const target = [
        { id: 1, code: "A" },
        { id: 2, code: "B" },
    ];
    const before = structuredClone(target);
    assert.throws(() => executeMerge({
        target,
        source: [
            { id: 1, code: "B" },
            { id: 2, code: "A" },
        ],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ code: (t, s) => s.code }) }],
        uniqueIndexes: [
            { name: "ux_code", columns: ["code"], timing: "immediate" },
        ],
    }), (error) => isUniqueViolation(error));
    assert.deepEqual(target, before);
});
test("DEFERRED still validates uniqueness at statement end", () => {
    assert.throws(() => executeMerge({
        target: [{ id: 1, code: "A" }, { id: 2, code: "B" }],
        source: [{ id: 1, code: "B" }],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ code: (t, s) => s.code }) }],
        uniqueIndexes: [
            { name: "ux_code", columns: ["code"], timing: "deferred" },
        ],
    }), (error) => isUniqueViolation(error));
});
test("NULLS DISTINCT allows multiple NULL unique keys", () => {
    const result = executeMerge({
        target: [{ id: 1, code: null }],
        source: [{ id: 2, code: null }],
        on: (t, s) => t.id === s.id,
        whenNotMatched: [{ action: insert((s) => ({ id: s.id, code: s.code })) }],
        uniqueIndexes: [
            { name: "ux_code", columns: ["code"], timing: "immediate", nulls: "distinct" },
        ],
    });
    assert.deepEqual(result.rows, [
        { id: 1, code: null },
        { id: 2, code: null },
    ]);
});
test("NULLS NOT DISTINCT treats multiple NULL unique keys as duplicates", () => {
    const target = [{ id: 1, code: null }];
    const before = structuredClone(target);
    assert.throws(() => executeMerge({
        target,
        source: [{ id: 2, code: null }],
        on: (t, s) => t.id === s.id,
        whenNotMatched: [{ action: insert((s) => ({ id: s.id, code: s.code })) }],
        uniqueIndexes: [
            { name: "ux_code", columns: ["code"], timing: "immediate", nulls: "not distinct" },
        ],
    }), (error) => isUniqueViolation(error));
    assert.deepEqual(target, before);
});
test("composite NULLS NOT DISTINCT compares NULL components as equal", () => {
    assert.throws(() => executeMerge({
        target: [{ id: 1, tenant: null, code: "X" }],
        source: [{ id: 2, tenant: null, code: "X" }],
        on: (t, s) => t.id === s.id,
        whenNotMatched: [{ action: insert((s) => ({ ...s })) }],
        uniqueIndexes: [
            {
                name: "ux_tenant_code",
                columns: ["tenant", "code"],
                timing: "deferred",
                nulls: "not distinct",
            },
        ],
    }), (error) => isUniqueViolation(error));
});
test("WHEN NOT MATCHED BY SOURCE can delete or update unmatched target rows", () => {
    const deleted = executeMerge({
        target: [{ id: 1, value: 1 }, { id: 2, value: 2 }],
        source: [{ id: 1, value: 10 }],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ value: (t, s) => s.value }) }],
        whenNotMatchedBySource: [{ action: deleteRow() }],
    });
    assert.deepEqual(deleted.rows, [{ id: 1, value: 10 }]);
    assert.equal(deleted.stats.deleted, 1);
    const updated = executeMerge({
        target: [{ id: 1, value: 1 }, { id: 2, value: 2 }],
        source: [{ id: 1, value: 10 }],
        on: (t, s) => t.id === s.id,
        whenNotMatchedBySource: [{ action: update({ retired: true }) }],
    });
    assert.deepEqual(updated.rows, [
        { id: 1, value: 1 },
        { id: 2, value: 2, retired: true },
    ]);
});
test("standalone unique validation honors NULL handling", () => {
    assert.doesNotThrow(() => {
        validateUniqueConstraints([{ code: null }, { code: null }], [{ columns: ["code"], nulls: "distinct" }]);
    });
    assert.throws(() => validateUniqueConstraints([{ code: null }, { code: null }], [{ columns: ["code"], nulls: "not distinct" }]), (error) => isUniqueViolation(error));
    assert.throws(() => validateUniqueConstraints([{ code: "A" }, { code: "A" }], [{ columns: ["code"], nulls: "distinct" }]), (error) => isUniqueViolation(error));
});
test("MergeTable commits a successful MERGE and preserves state on failure", () => {
    const table = new MergeTable({
        rows: [{ id: 1, value: 0 }],
    });
    table.merge({
        source: [{ id: 1, value: 5 }],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ value: (t, s) => s.value }) }],
    });
    assert.deepEqual(table.rows, [{ id: 1, value: 5 }]);
    assert.throws(() => table.merge({
        source: [{ id: 1, value: 6 }, { id: 1, value: 7 }],
        on: (t, s) => t.id === s.id,
        whenMatched: [{ action: update({ value: (t, s) => s.value }) }],
    }), (error) => isCardinalityViolation(error));
    assert.deepEqual(table.rows, [{ id: 1, value: 5 }]);
});
//# sourceMappingURL=merge.test.js.map
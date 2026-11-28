import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { aggregate, allCompare, anyCompare, avg, col, containsSubquery, count, eq, evaluatePlan, executeUnnested, existsSubquery, filter, freeColumns, inSubquery, limit, literal, max, min, notExistsSubquery, notInSubquery, or, project, rewriteQuery, scalar, schemaOf, singleJoin, sortKey, sum, table, walkPlan, } from "../src/index.js";
function assertEquivalent(query, context) {
    const reference = evaluatePlan(query, context);
    const rewrittenPlan = rewriteQuery(query, context);
    const rewritten = evaluatePlan(rewrittenPlan, context);
    assert.deepEqual(rewritten.columns, reference.columns);
    assert.deepEqual(rewritten.rows, reference.rows);
    assert.equal(containsSubquery(rewrittenPlan), false);
    assert.deepEqual(freeColumns(rewrittenPlan, context), []);
}
const aggregateContext = {
    catalog: {
        R: {
            columns: ["id"],
            rows: [{ id: 1 }, { id: 2 }, { id: 3 }],
        },
        S: {
            columns: ["rid", "v"],
            rows: [
                { rid: 1, v: 10 },
                { rid: 1, v: 20 },
                { rid: 3, v: null },
            ],
        },
    },
};
function scalarAggregate(fn, alias, argument) {
    const call = argument === undefined ? fn(alias) : fn(alias, argument);
    return project([{ as: alias, expr: col(alias) }], aggregate(filter(eq(col("S", "rid"), col("R", "id")), table("S", "S")), [], [call]));
}
describe("general unnesting", () => {
    it("keeps COUNT(*) = 0 and other aggregates NULL for empty groups", () => {
        const query = project([
            { as: "id", expr: col("R", "id") },
            {
                as: "count_star",
                expr: scalar(scalarAggregate(count, "count_star")),
            },
            {
                as: "sum_v",
                expr: scalar(scalarAggregate(sum, "sum_v", col("S", "v"))),
            },
            {
                as: "avg_v",
                expr: scalar(scalarAggregate(avg, "avg_v", col("S", "v"))),
            },
            {
                as: "max_v",
                expr: scalar(scalarAggregate(max, "max_v", col("S", "v"))),
            },
            {
                as: "min_v",
                expr: scalar(scalarAggregate(min, "min_v", col("S", "v"))),
            },
        ], table("R", "R"));
        assertEquivalent(query, aggregateContext);
        const actual = executeUnnested(query, aggregateContext).rows;
        assert.deepEqual(actual, [
            {
                id: 1,
                count_star: 2,
                sum_v: 30,
                avg_v: 15,
                max_v: 20,
                min_v: 10,
            },
            {
                id: 2,
                count_star: 0,
                sum_v: null,
                avg_v: null,
                max_v: null,
                min_v: null,
            },
            {
                id: 3,
                count_star: 1,
                sum_v: null,
                avg_v: null,
                max_v: null,
                min_v: null,
            },
        ]);
        let countArgument;
        walkPlan(rewriteQuery(query, aggregateContext), (node) => {
            if (node.kind === "aggregate") {
                const call = node.aggregates.find((entry) => entry.as === "count_star");
                if (call !== undefined)
                    countArgument = call.arg;
            }
        });
        assert.notEqual(countArgument, undefined);
    });
    it("implements NOT IN three-valued logic including the empty-set exemption", () => {
        const outer = {
            kind: "table",
            name: "T",
            alias: "T",
            columns: ["x"],
            rows: [{ x: 1 }, { x: null }, { x: 2 }],
        };
        const subquery = project([{ as: "y", expr: col("S", "y") }], table("S", "S"));
        const query = project([
            { as: "x", expr: col("T", "x") },
            { as: "mark", expr: notInSubquery(col("T", "x"), subquery) },
        ], outer);
        const filteredQuery = project([{ as: "x", expr: col("T", "x") }], filter(notInSubquery(col("T", "x"), subquery), outer));
        const withNull = {
            catalog: {
                S: {
                    columns: ["y"],
                    rows: [{ y: 2 }, { y: null }],
                },
            },
        };
        assertEquivalent(query, withNull);
        assert.deepEqual(executeUnnested(query, withNull).rows, [
            { x: 1, mark: null },
            { x: null, mark: null },
            { x: 2, mark: false },
        ]);
        assertEquivalent(filteredQuery, withNull);
        assert.deepEqual(executeUnnested(filteredQuery, withNull).rows, []);
        const empty = {
            catalog: { S: { columns: ["y"], rows: [] } },
        };
        assertEquivalent(query, empty);
        assert.deepEqual(executeUnnested(query, empty).rows, [
            { x: 1, mark: true },
            { x: null, mark: true },
            { x: 2, mark: true },
        ]);
        assertEquivalent(filteredQuery, empty);
        assert.deepEqual(executeUnnested(filteredQuery, empty).rows, [
            { x: 1 },
            { x: null },
            { x: 2 },
        ]);
    });
    it("keeps the empty-set exemption when the correlated subquery is empty per row", () => {
        const context = {
            catalog: {
                T: {
                    columns: ["k", "x"],
                    rows: [
                        { k: 1, x: 1 },
                        { k: 1, x: null },
                        { k: 2, x: null },
                        { k: 3, x: 5 },
                    ],
                },
                S: {
                    columns: ["k", "y"],
                    rows: [
                        { k: 1, y: 2 },
                        { k: 1, y: null },
                        { k: 3, y: 5 },
                    ],
                },
            },
        };
        const subquery = project([{ as: "y", expr: col("S", "y") }], filter(eq(col("S", "k"), col("T", "k")), table("S", "S")));
        const query = project([
            { as: "k", expr: col("T", "k") },
            { as: "x", expr: col("T", "x") },
            { as: "mark", expr: notInSubquery(col("T", "x"), subquery) },
        ], table("T", "T"));
        assertEquivalent(query, context);
        assert.deepEqual(executeUnnested(query, context).rows, [
            { k: 1, x: 1, mark: null },
            { k: 1, x: null, mark: null },
            { k: 2, x: null, mark: true },
            { k: 3, x: 5, mark: false },
        ]);
    });
    it("compiles EXISTS, NOT EXISTS and OR-correlated MarkJoins", () => {
        const context = {
            catalog: {
                R: {
                    columns: ["a", "b"],
                    rows: [
                        { a: 1, b: 10 },
                        { a: 2, b: 20 },
                        { a: 3, b: 30 },
                    ],
                },
                S: {
                    columns: ["a", "b"],
                    rows: [
                        { a: 1, b: 99 },
                        { a: 9, b: 20 },
                    ],
                },
            },
        };
        const correlated = filter(or(eq(col("S", "a"), col("R", "a")), eq(col("S", "b"), col("R", "b"))), table("S", "S"));
        const query = project([
            { as: "a", expr: col("R", "a") },
            { as: "found", expr: existsSubquery(correlated) },
            { as: "missing", expr: notExistsSubquery(correlated) },
        ], table("R", "R"));
        assertEquivalent(query, context);
        assert.deepEqual(executeUnnested(query, context).rows, [
            { a: 1, found: true, missing: false },
            { a: 2, found: true, missing: false },
            { a: 3, found: false, missing: true },
        ]);
    });
    it("unnests comparison quantifiers with SQL ALL/ANY semantics", () => {
        const context = {
            catalog: {
                S: {
                    columns: ["y"],
                    rows: [{ y: 2 }, { y: 3 }, { y: 4 }],
                },
            },
        };
        const subquery = project([{ as: "y", expr: col("S", "y") }], table("S", "S"));
        const query = project([
            { as: "x", expr: literal(3) },
            { as: "any_ge", expr: anyCompare(literal(3), ">=", subquery) },
            { as: "all_lt", expr: allCompare(literal(3), "<", subquery) },
            { as: "all_ne", expr: allCompare(literal(3), "<>", subquery) },
            { as: "in_set", expr: inSubquery(literal(3), subquery) },
        ], { kind: "values", columns: ["dummy"], rows: [{}] });
        assertEquivalent(query, context);
        assert.deepEqual(executeUnnested(query, context).rows, [
            { x: 3, any_ge: true, all_lt: false, all_ne: false, in_set: true },
        ]);
    });
    it("unnests arbitrary nested correlated scalar subqueries", () => {
        const context = {
            catalog: {
                R: {
                    columns: ["id"],
                    rows: [{ id: 1 }, { id: 2 }],
                },
                S: {
                    columns: ["rid", "k"],
                    rows: [
                        { rid: 1, k: 10 },
                        { rid: 1, k: 20 },
                        { rid: 2, k: 30 },
                    ],
                },
                T: {
                    columns: ["k", "v"],
                    rows: [
                        { k: 10, v: 1 },
                        { k: 10, v: 2 },
                        { k: 20, v: 3 },
                        { k: 30, v: 4 },
                        { k: 30, v: 5 },
                        { k: 30, v: 6 },
                    ],
                },
            },
        };
        const nested = project([{ as: "c", expr: col("c") }], aggregate(filter(eq(col("T", "k"), col("S", "k")), table("T", "T")), [], [count("c")]));
        const middle = project([{ as: "total", expr: col("total") }], aggregate(filter(eq(col("S", "rid"), col("R", "id")), table("S", "S")), [], [sum("total", scalar(nested))]));
        const query = project([
            { as: "id", expr: col("R", "id") },
            { as: "total", expr: scalar(middle) },
        ], table("R", "R"));
        assertEquivalent(query, context);
        assert.deepEqual(executeUnnested(query, context).rows, [
            { id: 1, total: 3 },
            { id: 2, total: 3 },
        ]);
    });
    it("pushes the domain through LIMIT as a per-outer-row row-number filter", () => {
        const context = {
            catalog: {
                R: {
                    columns: ["id"],
                    rows: [{ id: 1 }, { id: 2 }, { id: 3 }],
                },
                S: {
                    columns: ["rid", "v"],
                    rows: [
                        { rid: 1, v: 20 },
                        { rid: 1, v: 10 },
                        { rid: 2, v: 7 },
                    ],
                },
            },
        };
        const subquery = project([{ as: "v", expr: col("S", "v") }], limit(filter(eq(col("S", "rid"), col("R", "id")), table("S", "S")), 1, 0, [sortKey(col("S", "v"))]));
        const query = project([
            { as: "id", expr: col("R", "id") },
            { as: "v", expr: scalar(subquery) },
        ], table("R", "R"));
        assertEquivalent(query, context);
        assert.deepEqual(executeUnnested(query, context).rows, [
            { id: 1, v: 10 },
            { id: 2, v: 7 },
            { id: 3, v: null },
        ]);
    });
    it("raises a cardinality violation for multi-row scalar subqueries", () => {
        const context = {
            catalog: {
                R: { columns: ["id"], rows: [{ id: 1 }] },
                S: {
                    columns: ["rid", "v"],
                    rows: [
                        { rid: 1, v: 1 },
                        { rid: 1, v: 2 },
                    ],
                },
            },
        };
        const query = project([
            {
                as: "v",
                expr: scalar(project([{ as: "v", expr: col("S", "v") }], filter(eq(col("S", "rid"), col("R", "id")), table("S", "S")))),
            },
        ], table("R", "R"));
        assert.throws(() => evaluatePlan(query, context), (error) => error instanceof Error &&
            error.name === "CardinalityViolationError" &&
            error.actualRows === 2);
        assert.throws(() => executeUnnested(query, context), (error) => error instanceof Error &&
            error.name === "CardinalityViolationError" &&
            error.actualRows === 2);
    });
    it("produces the original output schema after removing internal columns", () => {
        const query = project([
            { as: "id", expr: col("R", "id") },
            {
                as: "ok",
                expr: inSubquery(col("R", "id"), project([{ as: "rid", expr: col("S", "rid") }], table("S", "S"))),
            },
        ], table("R", "R"));
        const rewritten = rewriteQuery(query, aggregateContext);
        assert.deepEqual(schemaOf(rewritten, aggregateContext), ["id", "ok"]);
    });
    it("keeps the reference engine's SingleJoin behavior for zero matches", () => {
        const query = singleJoin(table("R", "R"), project([{ as: "v", expr: col("S", "v") }], table("S", "S")), literal(false));
        const result = evaluatePlan(query, aggregateContext);
        assert.equal(result.rows[0].v, null);
    });
});

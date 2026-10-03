import { test } from "node:test";
import assert from "node:assert/strict";
import { makeWideSchema, type Schema } from "../src/schema.ts";
import { Filter, Join, Project, Scan, outputNames } from "../src/operators.ts";
import { ProjectionPruner } from "../src/pruner.ts";

/** 验收场景：Project(id, name) -> Join(id) -> Scan(20 列宽表)。 */
function buildAcceptancePlan() {
  const users = new Scan("users", makeWideSchema(20));
  const dept = new Scan("dept", [
    { name: "id", type: "BIGINT" },
    { name: "dept_name", type: "VARCHAR" },
    { name: "budget", type: "DECIMAL" },
  ]);
  const join = new Join(users, dept, ["id"], ["id"]);
  const project = new Project(join, [
    { name: "id", refs: ["id"] },
    { name: "name", refs: ["name"] },
  ]);
  return { users, dept, join, project };
}

test("验收：20 列宽表被压缩为仅 id 和 name 两列", () => {
  const { users, project } = buildAcceptancePlan();
  assert.equal(users.outputSchema.length, 20, "修剪前 Scan 应有 20 列");

  const report = new ProjectionPruner().prune(project, new Set(["id", "name"]));

  assert.deepEqual(outputNames(users), ["id", "name"]);
  assert.equal(users.outputSchema.length, 2);
  assert.equal(report.prunedColumnCount, 20, "users 裁 18 列 + dept 裁 2 列");
});

test("Join 右表仅保留 join key 'id'", () => {
  const { dept, project } = buildAcceptancePlan();
  new ProjectionPruner().prune(project, new Set(["id", "name"]));
  assert.deepEqual(outputNames(dept), ["id"]);
});

test("修剪后上层算子输出模式同步收紧", () => {
  const { join, project } = buildAcceptancePlan();
  new ProjectionPruner().prune(project, new Set(["id", "name"]));
  assert.deepEqual(outputNames(join), ["id", "name", "id"]);
  assert.deepEqual(outputNames(project), ["id", "name"]);
});

test("投影消除：无人消费的投影表达式被整体移除", () => {
  const scan = new Scan("t", makeWideSchema(20));
  const project = new Project(scan, [
    { name: "id", refs: ["id"] },
    { name: "name", refs: ["name"] },
    { name: "upper_name", refs: ["name"] },
    { name: "col_03_copy", refs: ["col_03"] },
  ]);
  const report = new ProjectionPruner().prune(project, new Set(["id", "name"]));

  assert.deepEqual(
    project.expressions.map((e) => e.name),
    ["id", "name"],
  );
  assert.deepEqual(report.eliminatedProjections.sort(), ["col_03_copy", "upper_name"]);
  assert.deepEqual(outputNames(scan), ["id", "name"]);
});

test("Filter 谓词引用的字段即使上层不需要也会被保留", () => {
  const scan = new Scan("t", makeWideSchema(20));
  const filter = new Filter(scan, ["col_07"]);
  const project = new Project(filter, [{ name: "id", refs: ["id"] }]);

  new ProjectionPruner().prune(project, new Set(["id"]));

  assert.deepEqual(outputNames(scan), ["id", "col_07"]);
  assert.deepEqual(outputNames(filter), ["id", "col_07"]);
});

test("表达式引用多列时，所有引用列都被保留", () => {
  const scan = new Scan("t", makeWideSchema(20));
  const project = new Project(scan, [
    { name: "greeting", refs: ["name", "col_05"] },
  ]);
  new ProjectionPruner().prune(project, new Set(["greeting"]));
  assert.deepEqual(outputNames(scan), ["name", "col_05"]);
});

test("上层消费全部字段时修剪为无操作", () => {
  const scan = new Scan("t", makeWideSchema(20));
  const all = outputNames(scan);
  const project = new Project(
    scan,
    all.map((name) => ({ name, refs: [name] })),
  );
  const report = new ProjectionPruner().prune(project, new Set(all));

  assert.equal(outputNames(scan).length, 20);
  assert.equal(report.prunedColumnCount, 0);
});

test("修剪报告记录每个 Scan 的前后模式", () => {
  const { project } = buildAcceptancePlan();
  const report = new ProjectionPruner().prune(project, new Set(["id", "name"]));

  const usersReport = report.scans.find((s) => s.table === "users");
  assert.equal(usersReport?.before.length, 20);
  assert.deepEqual(usersReport?.after, ["id", "name"]);
  const deptReport = report.scans.find((s) => s.table === "dept");
  assert.deepEqual(deptReport?.after, ["id"]);
});

test("若修剪会导致 Scan 零列输出则报错", () => {
  const scan = new Scan("t", makeWideSchema(20));
  const project = new Project(scan, [{ name: "x", refs: ["ghost"] }]);
  assert.throws(
    () => new ProjectionPruner().prune(project, new Set(["x"])),
    /zero columns/,
  );
});

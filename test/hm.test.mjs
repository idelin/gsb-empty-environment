import assert from "node:assert/strict";
import test from "node:test";
import {
  HMTypeError,
  extendRow,
  flattenRow,
  freshRowVariable,
  freeRowVariableIds,
  infer,
  inferScheme,
  instantiate,
  intType,
  principalType,
  recordType,
  schemeRowConstraints,
  stringType,
  typeToString,
  unify,
  unifyRows,
  unifyTypes,
} from "../dist/src/index.js";

function expectCode(code) {
  return (error) => error instanceof HMTypeError && error.code === code;
}

test("let polymorphism instantiates at every use", () => {
  assert.equal(
    principalType("let id = fn x => x in id"),
    "forall 'a. 'a -> 'a",
  );
  assert.equal(
    principalType('let id = fn x => x in (id 1, id "s")'),
    "(Int, String)",
  );
  assert.equal(
    principalType("let id = fn x => x in id 1"),
    "Int",
  );
});

test("value restriction keeps ref(nil) monomorphic", () => {
  const unsafe = `
    let r = ref(nil) in
      (r := cons(1, nil); head(!r) + 'x')
  `;
  assert.throws(() => infer(unsafe), expectCode("TYPE_MISMATCH"));

  const scheme = inferScheme("let r = ref(nil) in r");
  assert.equal(scheme.typeVars.size, 0);
  assert.equal(scheme.rowVars.size, 0);

  assert.equal(
    principalType("let f = fn x => ref(x) in (f 1, f \"s\")"),
    "(Ref<Int>, Ref<String>)",
  );
});

test("non-sequential row unification and row projection", () => {
  const left = recordType({ x: intType, y: stringType });
  const right = recordType({ y: stringType, x: intType });
  unify(left, right);
  assert.equal(typeToString(left), "{x: Int, y: String}");

  assert.equal(
    principalType('(fn r => r.x) {b = "s", x = 1}'),
    "Int",
  );
  assert.equal(
    principalType(
      "let get = fn r => r.x in (get {x = 1}, get {x = \"s\"})",
    ),
    "(Int, String)",
  );
});

test("row polymorphism carries the lacks constraint and rejects duplicates", () => {
  assert.throws(
    () => infer("{x = 1 | {x = 2}}"),
    expectCode("DUPLICATE_FIELD"),
  );
  assert.throws(
    () => infer("let r = {x = 1} in {x = 2 | r}"),
    expectCode("DUPLICATE_FIELD"),
  );

  const row = freshRowVariable();
  const open = recordType({}, row);
  extendRow("x", intType, open.row);
  assert.equal(row.lacks.has("x"), true);
  assert.throws(
    () => unify(open, recordType({ x: intType })),
    expectCode("LACKS_VIOLATION"),
  );
});

test("the same row variable may not acquire conflicting duplicate fields", () => {
  const row = freshRowVariable();
  const left = recordType({ x: intType }, row);
  const right = recordType({ x: stringType }, row);
  assert.throws(() => unify(left, right), expectCode("TYPE_MISMATCH"));

  const differentRow = freshRowVariable();
  const leftDifferent = recordType({ x: intType }, differentRow);
  const rightDifferent = recordType({ y: stringType }, differentRow);
  assert.throws(
    () => unify(leftDifferent, rightDifferent),
    expectCode("ROW_OCCURS_CHECK"),
  );
});

test("ordinary and row occurs checks reject infinite types", () => {
  assert.throws(
    () => infer("fn x => x x"),
    expectCode("OCCURS_CHECK"),
  );

  const typeVariable = { tag: "tvar", id: 900001 };
  assert.throws(
    () => unifyTypes(typeVariable, { tag: "fun", arg: typeVariable, result: intType }),
    expectCode("OCCURS_CHECK"),
  );

  const row = freshRowVariable();
  assert.throws(
    () => unifyRows(row, extendRow("x", intType, row)),
    expectCode("ROW_OCCURS_CHECK"),
  );
});

test("canonical principal types", () => {
  assert.equal(
    principalType("fn r => r.x"),
    "forall 'a 'r. {x: 'a | 'r} -> 'a",
  );
  assert.equal(
    principalType("fn r => {y = 1 | r}"),
    "forall 'r. { | 'r} -> {y: Int | 'r}",
  );
  assert.equal(
    principalType("fn r => {r \\ x}"),
    "forall 'a 'r. {x: 'a | 'r} -> { | 'r}",
  );
  assert.equal(
    principalType("{z = 1, a = true, m = \"s\"}"),
    "{a: Bool, m: String, z: Int}",
  );
});

test("row deletion and extension remain principal", () => {
  assert.equal(
    principalType(
      "let delX = fn r => {r \\ x} in (delX {x = 1, y = \"s\"}, delX {x = true, z = 1})",
    ),
    "({y: String}, {z: Int})",
  );
  assert.equal(
    principalType(
      "let addX = fn r => {x = 1 | r} in (addX {y = \"s\"}, addX {z = true})",
    ),
    "({x: Int, y: String}, {x: Int, z: Bool})",
  );
});

test("row scheme exposes its residual lacks constraints", () => {
  const scheme = inferScheme("fn r => {y = 1 | r}");
  assert.equal(scheme.rowVars.size, 1);
  const constraints = Object.values(schemeRowConstraints(scheme));
  assert.deepEqual(constraints, [["y"]]);
});

test("row variables survive instantiation independently", () => {
  const first = inferScheme("fn r => r.x");
  const second = inferScheme("fn r => r.x");
  assert.notEqual(first, second);
  assert.equal(freeRowVariableIds(first.type).size, 1);
  assert.equal(freeRowVariableIds(second.type).size, 1);

  const extensionScheme = inferScheme("fn r => {y = 1 | r}");
  const firstInstance = instantiate(extensionScheme);
  const secondInstance = instantiate(extensionScheme);
  const firstRows = freeRowVariableIds(firstInstance);
  const secondRows = freeRowVariableIds(secondInstance);
  assert.notDeepEqual([...firstRows], [...secondRows]);
  assert.equal(firstInstance.tag, "fun");
  assert.equal(firstInstance.arg.tag, "record");
  const firstTail = flattenRow(firstInstance.arg.row).tail;
  assert.equal(firstTail.tag, "rvar");
  assert.equal(firstTail.lacks.has("y"), true);
});

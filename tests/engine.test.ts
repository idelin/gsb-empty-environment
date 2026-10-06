import assert from "node:assert/strict";
import test from "node:test";
import {
  parseTemplate,
  renderFragments,
  renderTemplate,
  Source,
  TemplateRenderError,
  TemplateSyntaxError,
  type Node,
} from "../src/index.ts";

test("variables: plain and dotted-path interpolation", () => {
  assert.equal(renderTemplate("Hello {{ name }}!", { name: "World" }), "Hello World!");
  assert.equal(
    renderTemplate("{{ user.name }} <{{ user.age }}>", { user: { name: "Ada", age: 36 } }),
    "Ada <36>",
  );
  assert.equal(renderTemplate("{{ 'lit' }} {{ 42 }} {{ true }}", {}), "lit 42 true");
});

test("conditionals: if / elif / else select the right branch", () => {
  const tpl = "{% if a %}A{% elif b %}B{% else %}C{% endif %}";
  assert.equal(renderTemplate(tpl, { a: true, b: true }), "A");
  assert.equal(renderTemplate(tpl, { a: false, b: 1 }), "B");
  assert.equal(renderTemplate(tpl, { a: 0, b: "" }), "C");
  assert.equal(renderTemplate("{% if xs %}x{% endif %}", { xs: [] }), "");
});

test("conditionals: unselected branches never access variables", () => {
  // `missing` is not defined; the taken branch must not touch it.
  const tpl = "{% if ok %}yes{% else %}{{ missing }}{% endif %}";
  assert.equal(renderTemplate(tpl, { ok: true }), "yes");
  // And the mirror: else taken, if-branch references undefined var.
  const tpl2 = "{% if ok %}{{ missing }}{% else %}no{% endif %}";
  assert.equal(renderTemplate(tpl2, { ok: false }), "no");
  // Conditions after a matched branch are not evaluated either.
  const tpl3 = "{% if ok %}yes{% elif missing %}?{% endif %}";
  assert.equal(renderTemplate(tpl3, { ok: true }), "yes");
});

test("loops: iterate arrays and expose the loop variable", () => {
  assert.equal(
    renderTemplate("{% for x in items %}{{ x }};{% endfor %}", { items: [1, 2, 3] }),
    "1;2;3;",
  );
  assert.equal(
    renderTemplate("{% for u in users %}{{ u.name }} {% endfor %}", {
      users: [{ name: "a" }, { name: "b" }],
    }),
    "a b ",
  );
});

test("loops: loop variable shadows outer scope, restored after exit", () => {
  const tpl = "{{ x }}|{% for x in items %}{{ x }}{% endfor %}|{{ x }}";
  assert.equal(renderTemplate(tpl, { x: "outer", items: [1, 2] }), "outer|12|outer");
  // Nested shadowing: inner loop re-shadows, middle scope restored between them.
  const nested = "{% for x in a %}[{{ x }}{% for x in b %}{{ x }}{% endfor %}{{ x }}]{% endfor %}";
  assert.equal(renderTemplate(nested, { a: ["A", "B"], b: [1, 2] }), "[A12A][B12B]");
});

test("loops: empty array produces no output and evaluates nothing", () => {
  // Body references undefined variables; with zero iterations it never runs.
  const tpl = "a{% for x in items %}{{ missing.thing }}{% endfor %}b";
  assert.equal(renderTemplate(tpl, { items: [] }), "ab");
});

test("errors: undefined variable reports template position and call stack", () => {
  const tpl = "line1\n{% for u in users %}\n{% if u.ok %}{{ u.name }}{% endif %}\n{% endfor %}";
  const data = { users: [{ ok: false }, { ok: true }] }; // second user lacks `name`
  try {
    renderTemplate(tpl, data);
    assert.fail("should have thrown");
  } catch (err) {
    assert.ok(err instanceof TemplateRenderError);
    const e = err as TemplateRenderError;
    // Position of the failing `{{ u.name }}` expression: line 3.
    assert.match(e.message, /Undefined variable 'u\.name' at line 3, column 17/);
    // Template call stack, most recent first: if-frame inside for-frame.
    assert.equal(e.templateStack.length, 2);
    assert.match(e.templateStack[0].label, /if/);
    assert.match(e.templateStack[1].label, /for u/);
    assert.match(e.templateStack[1].label, /iteration 2/);
    const src = new Source(tpl);
    assert.equal(src.describe(e.templateStack[0].span), "line 3, column 1");
    assert.equal(src.describe(e.templateStack[1].span), "line 2, column 1");
  }
});

test("errors: undefined root variable and bad property access", () => {
  assert.throws(() => renderTemplate("{{ nope }}", {}), /Undefined variable 'nope' at line 1, column 4/);
  assert.throws(
    () => renderTemplate("{{ a.b }}", { a: 5 }),
    /Cannot read property 'b' of number/,
  );
  assert.throws(
    () => renderTemplate("{% for x in items %}{% endfor %}", { items: 7 }),
    /Cannot iterate over number/,
  );
});

test("errors: syntax problems report positions", () => {
  assert.throws(() => parseTemplate("{{ x "), /Unclosed '\{\{' at line 1, column 1/);
  assert.throws(() => parseTemplate("{% if x %}a"), /Missing \{% endif %\}/);
  assert.throws(() => parseTemplate("{% for x items %}{% endfor %}"), /Expected '\{% for <name> in <expression> %\}'/);
  assert.throws(() => parseTemplate("{% endfor %}"), /Unknown tag/);
});

test("fragments: every output chunk keeps its source span", () => {
  const tpl = "A{{ x }}B{% if ok %}C{% endif %}";
  const fragments = renderFragments(tpl, { x: "X", ok: true });
  assert.deepEqual(
    fragments.map((f) => [f.text, f.origin]),
    [
      ["A", "text"],
      ["X", "output"],
      ["B", "text"],
      ["C", "text"],
    ],
  );
  // Each fragment's span slices back to its own source text.
  assert.equal(tpl.slice(fragments[0].span.start, fragments[0].span.end), "A");
  assert.equal(tpl.slice(fragments[1].span.start, fragments[1].span.end), "{{ x }}");
  assert.equal(tpl.slice(fragments[3].span.start, fragments[3].span.end), "C");
});

/**
 * Independent reference model: a minimal, direct AST-walking evaluator that
 * renders to a plain string. Used to cross-check the engine's interpreter on
 * nested loops, empty loops and shadowing.
 */
function referenceEval(nodes: Node[], scopes: Record<string, unknown>[], out: string[]): void {
  const lookup = (name: string): unknown => {
    for (let i = scopes.length - 1; i >= 0; i--) {
      if (name in scopes[i]) return scopes[i][name];
    }
    throw new Error(`reference: unbound ${name}`);
  };
  const evalPath = (parts: string[]): unknown => {
    let v = lookup(parts[0]);
    for (const p of parts.slice(1)) v = (v as Record<string, unknown>)[p];
    return v;
  };
  for (const node of nodes) {
    switch (node.kind) {
      case "text":
        out.push(node.text);
        break;
      case "output":
        out.push(String(node.expr.kind === "literal" ? node.expr.value : evalPath(node.expr.parts)));
        break;
      case "if": {
        const taken = node.branches.find((b) =>
          Boolean(b.cond.kind === "literal" ? b.cond.value : evalPath(b.cond.parts)),
        );
        if (taken) referenceEval(taken.body, scopes, out);
        else if (node.elseBody) referenceEval(node.elseBody, scopes, out);
        break;
      }
      case "for": {
        const list = evalPath(node.iterable.parts) as unknown[];
        for (const item of list) {
          referenceEval(node.body, [...scopes, { [node.varName]: item }], out);
        }
        break;
      }
    }
  }
}

function referenceRender(template: string, data: Record<string, unknown>): string {
  const out: string[] = [];
  referenceEval(parseTemplate(template), [data], out);
  return out.join("");
}

test("model cross-check: nested loops, empty loops and shadowing", () => {
  const cases: Array<[string, Record<string, unknown>]> = [
    // Nested loops over a matrix, inner loop shadows nothing.
    ["{% for row in m %}{% for c in row %}{{ c }}{% endfor %};{% endfor %}", { m: [[1, 2], [3]] }],
    // Inner loop shadows the outer loop variable.
    ["{% for x in a %}{{ x }}{% for x in b %}({{ x }}){% endfor %}{{ x }}{% endfor %}", { a: ["p", "q"], b: [0] }],
    // Empty outer loop.
    ["pre{% for x in xs %}{{ x }}{% endfor %}post", { xs: [] }],
    // Empty inner loop inside a non-empty outer loop.
    ["{% for g in gs %}[{{ g.name }}{% for t in g.items %}{{ t }}{% endfor %}]{% endfor %}", {
      gs: [{ name: "a", items: [] }, { name: "b", items: [1, 2] }],
    }],
    // Loop variable shadowing a root variable, with conditionals inside.
    ["{{ v }}{% for v in vs %}{% if v %}+{% else %}-{% endif %}{% endfor %}{{ v }}", {
      v: "R",
      vs: [true, false, 1],
    }],
    // Deeply nested: loop > if > loop, empty branches mixed in.
    ["{% for p in ps %}{% if p.on %}{% for q in p.ns %}{{ q }}{% endfor %}{% else %}off{% endif %}|{% endfor %}", {
      ps: [{ on: true, ns: [1, 2] }, { on: false, ns: [] }, { on: true, ns: [] }],
    }],
  ];
  for (const [template, data] of cases) {
    assert.equal(renderTemplate(template, data), referenceRender(template, data), `template: ${template}`);
  }
});

import type { CheckResult, Shape } from "./types.js";

export function renderShape(s: Shape): string {
  switch (s.kind) {
    case "lit":
      return s.text;
    case "cycle":
      return "…" + s.name;
    case "arg":
      return renderShape(s.shape) + "  (call argument)";
    case "func":
      return "(…) => " + renderShape(s.ret);
    case "record": {
      if (s.fields.length === 0) return "{}";
      const inner = s.fields.map((f) => f.name + ": " + renderShape(f.shape)).join(", ");
      return "{ " + inner + " }";
    }
  }
}

export function renderReport(result: CheckResult): string {
  if (result.compatible) {
    return "✓ compatible: the new interface can replace the old one";
  }
  const lines: string[] = ["✗ incompatible: " + result.problems.length + " problem(s) found"];
  for (const p of result.problems) {
    lines.push("  at " + p.path + ": " + p.message);
    lines.push("    counterexample: " + renderShape(p.witness));
  }
  return lines.join("\n");
}

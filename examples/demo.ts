import { assemble } from "../src/isa.ts";
import { verify, formatErrors } from "../src/verifier.ts";

function show(name: string, code: Uint8Array): void {
  console.log(`=== ${name} ===`);
  console.log(`bytes: [${[...code].join(" ")}]`);
  const r = verify(code);
  if (r.ok) {
    console.log(`OK — reachable entry heights: ${[...r.heights.entries()].map(([o, h]) => `${o}:${h}`).join(" ")}`);
  } else {
    console.log(formatErrors(code, r.errors));
  }
  console.log();
}

show(
  "good: balanced diamond",
  assemble([
    ["push", 10],
    ["jz", "else"],
    ["push", 1],
    ["jmp", "end"],
    ["label", "else"],
    ["push", 2],
    ["label", "end"],
    ["ret"],
  ]),
);

show(
  "bad: height conflict at branch merge",
  assemble([["push", 0], ["jz", "join"], ["push", 1], ["label", "join"], ["ret"]]),
);

show(
  "bad: growing loop",
  assemble([["label", "l"], ["push", 1], ["jmp", "l"]]),
);

show(
  "bad: underflow",
  assemble([["push", 1], ["pop"], ["pop"]]),
);

show(
  "bad: RET height 2",
  assemble([["push", 1], ["push", 2], ["ret"]]),
);

show(
  "good: unreachable bad code after RET is ignored",
  assemble([["push", 1], ["ret"], ["pop"], ["label", "l"], ["push", 1], ["jmp", "l"]]),
);

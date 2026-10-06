import { Op, InstrSpec, encodeProgram } from "./isa";
import { verify, formatEvidence, VerifyError } from "./verify";

let failures = 0;
let checks = 0;

function check(name: string, cond: boolean, detail?: string): void {
  checks++;
  if (!cond) {
    failures++;
    console.log(`FAIL: ${name}${detail ? `\n${detail}` : ""}`);
  }
}

const P: InstrSpec = { op: Op.Push };
const POP: InstrSpec = { op: Op.Pop };
const ADD: InstrSpec = { op: Op.Add };
const RET: InstrSpec = { op: Op.Ret };
const JMP = (targetIndex: number): InstrSpec => ({ op: Op.Jmp, targetIndex });
const JZ = (targetIndex: number): InstrSpec => ({ op: Op.Jz, targetIndex });
const JMP_RAW = (rawTarget: number): InstrSpec => ({ op: Op.Jmp, rawTarget });

function errorOf(specs: InstrSpec[], maxStack = 32): VerifyError {
  const result = verify(encodeProgram(specs), maxStack);
  if (result.ok) {
    throw new Error("expected verification to fail, but it passed");
  }
  return result.error;
}

function expectOk(name: string, specs: InstrSpec[], maxStack = 32): void {
  const result = verify(encodeProgram(specs), maxStack);
  check(name, result.ok, result.ok ? undefined : formatEvidence(encodeProgram(specs), result.error));
}

const straight = [P, P, ADD, RET];
expectOk("valid: straight-line arithmetic", straight);

const joinSameHeight = [P, P, JZ(5), P, ADD, RET];
expectOk("valid: branches rejoin at equal height", joinSameHeight);

expectOk("valid: consistent infinite loop", [P, JMP(1)]);

const underflow = errorOf([POP, RET]);
check("underflow: kind", underflow.kind === "underflow");
if (underflow.kind === "underflow") {
  check("underflow: offset", underflow.offset === 0);
  check("underflow: needed", underflow.needed === 1);
  check(
    "underflow: witness path",
    underflow.path.length === 1 && underflow.path[0].offset === 0 && underflow.path[0].height === 0,
  );
}

const underflowAdd = errorOf([P, ADD, RET]);
check("underflow(add): kind", underflowAdd.kind === "underflow");
if (underflowAdd.kind === "underflow") {
  check("underflow(add): needed 2", underflowAdd.needed === 2 && underflowAdd.height === 1);
}

const retHigh = errorOf([P, P, RET]);
check("bad-return-height: kind", retHigh.kind === "bad-return-height");
if (retHigh.kind === "bad-return-height") {
  check("bad-return-height: height 2", retHigh.height === 2);
}

const retZero = errorOf([RET]);
check("return at height 0 rejected", retZero.kind === "bad-return-height");

const conflict = errorOf([P, JZ(3), P, RET]);
check("height-conflict: kind", conflict.kind === "height-conflict");
if (conflict.kind === "height-conflict") {
  const heights = [conflict.heightA, conflict.heightB].sort();
  check("height-conflict: heights 0 and 1", heights[0] === 0 && heights[1] === 1);
  const offsA = conflict.pathA.map((s) => s.offset);
  const offsB = conflict.pathB.map((s) => s.offset);
  check(
    "height-conflict: both paths end at join point",
    offsA[offsA.length - 1] === 5 && offsB[offsB.length - 1] === 5,
  );
  check(
    "height-conflict: paths start at entry",
    offsA[0] === 0 && offsB[0] === 0 && conflict.pathA[0].height === 0 && conflict.pathB[0].height === 0,
  );
  const taken = conflict.pathA.length === 3 ? conflict.pathA : conflict.pathB;
  const through = conflict.pathA.length === 3 ? conflict.pathB : conflict.pathA;
  check(
    "height-conflict: taken path heights",
    taken.map((s) => s.height).join(",") === "0,1,0",
    JSON.stringify(taken),
  );
  check(
    "height-conflict: fallthrough path heights",
    through.map((s) => s.height).join(",") === "0,1,0,1",
    JSON.stringify(through),
  );
  const evidence = formatEvidence(encodeProgram([P, JZ(3), P, RET]), conflict);
  check("height-conflict: evidence mentions both heights", evidence.includes("0 vs 1"));
  check("height-conflict: evidence shows both paths", evidence.includes("path A") && evidence.includes("path B"));
}

const midInstr = errorOf([JMP_RAW(1)]);
check("bad-jump-target: mid-instruction", midInstr.kind === "bad-jump-target");
if (midInstr.kind === "bad-jump-target") {
  check("bad-jump-target: target", midInstr.target === 1);
}

const pastEnd = errorOf([P, JMP(2)]);
check("bad-jump-target: past end of code", pastEnd.kind === "bad-jump-target");

const offEnd = errorOf([P]);
check("falls-off-end: kind", offEnd.kind === "falls-off-end");

const overflow = errorOf([P, P, P, RET], 2);
check("stack-overflow: kind", overflow.kind === "stack-overflow");
if (overflow.kind === "stack-overflow") {
  check("stack-overflow: height", overflow.height === 3 && overflow.max === 2);
}

const loopConflict = errorOf([P, JMP(0)]);
check("loop with growing stack is a height conflict", loopConflict.kind === "height-conflict");

const empty = verify(new Uint8Array(0));
check("empty program rejected", !empty.ok && empty.error.kind === "empty-program");

const garbage = verify(new Uint8Array([0x7f]));
check("unknown opcode rejected", !garbage.ok && garbage.error.kind === "decode");

const truncated = verify(new Uint8Array([Op.Jmp, 0x00]));
check("truncated jump rejected", !truncated.ok && truncated.error.kind === "decode");

expectOk("no false positive: unreachable underflowing POP", [P, RET, POP]);
expectOk("no false positive: unreachable RET at wrong height", [P, RET, RET, POP]);
expectOk("no false positive: unreachable bad jump target", [P, RET, JMP_RAW(1)]);
expectOk("no false positive: unreachable jump past end", [P, RET, JMP(3)]);
expectOk("no false positive: unreachable height-conflicting loop", [P, RET, P, JMP(3)]);
expectOk("no false positive: unreachable code skipped over", [P, JMP(3), POP, RET]);
expectOk("no false positive: unreachable truncated-looking tail is still decoded", [P, RET, ADD, POP, POP]);

console.log(`${checks} checks, ${failures} failures`);
if (failures > 0) {
  throw new Error("test failures");
}

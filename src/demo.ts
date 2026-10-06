import { Op, InstrSpec, encodeProgram } from "./isa";
import { verify, formatEvidence } from "./verify";

const P: InstrSpec = { op: Op.Push };
const POP: InstrSpec = { op: Op.Pop };
const RET: InstrSpec = { op: Op.Ret };

function show(title: string, specs: InstrSpec[]): void {
  const code = encodeProgram(specs);
  const result = verify(code);
  console.log(`=== ${title} ===`);
  console.log(result.ok ? "valid" : formatEvidence(code, result.error));
  console.log();
}

show("height conflict: two paths reach RET with heights 0 and 1", [
  P,
  { op: Op.Jz, targetIndex: 3 },
  P,
  RET,
]);

show("stack underflow: POP on empty stack", [POP, RET]);

show("return with wrong stack height", [P, P, RET]);

show("jump into the middle of an instruction", [{ op: Op.Jmp, rawTarget: 1 }]);

show("unreachable bad code is not reported", [P, RET, POP, { op: Op.Jmp, rawTarget: 1 }]);

package wasmvalidate

// Design notes
//
// The validator is a faithful Go transcription of the appendix validation
// algorithm:
//
//   - The operand stack stores ValType values. The synthetic Unknown value is
//     never pushed explicitly; popVal returns it when a pop reaches the height
//     of an unreachable control frame, which is exactly the polymorphic stack
//     rule of the specification.
//   - Control frames are ctrlFrame values holding opcode, start_types,
//     end_types, height and unreachable (plus an inElse marker). An implicit
//     function frame is pushed before the first instruction.
//   - unreachable/br/br_table/return truncate the operand stack to the frame
//     height and set unreachable = true. Subsequent instructions remain fully
//     type checked: concrete operands still conflict, while Unknown unifies
//     with any expected concrete type.
//   - Branch label types follow the structured-instruction rule: loops expose
//     start_types (a branch re-enters the loop header) and blocks/ifs expose
//     end_types. br_table requires every target label, including the default
//     label, to have equal length and identical types.
//   - An if without an else is only sound when its input and output type
//     sequences are identical, because the implicit empty else branch must
//     produce the same results as the then branch.
//   - Untyped select requires two equal numeric/vector operands; two Unknown
//     operands keep Unknown rather than guessing a type.
//
// All failures are surfaced as *ValidationError values carrying a stable
// ErrorKind classification and the zero-based index of the offending
// instruction (or -1 for whole-body failures such as an unclosed block).
//
// The input model is decoded bytecode: callers parse a wasm binary into the
// provided Module/Body/Instr structures and call Validate. Numeric, comparison
// and conversion opcodes are driven by a fixed signature table, so extending
// the instruction set only requires adding entries to numericSigs.

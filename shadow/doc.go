// Package shadow performs bit-precise data-flow shadow execution for an
// embedded instruction simulator.
//
// A Go host decodes real target instructions itself and submits either direct
// value operations or machine-independent Instruction values. The package never
// receives executable bytes as code, allocates executable memory, or invokes
// native machine code. Every simulated result bit keeps the set of input byte
// bits that can influence it. Arithmetic models carry and borrow bit by bit,
// and an xor-reg,reg clearing idiom removes dependencies through lineage.
package shadow

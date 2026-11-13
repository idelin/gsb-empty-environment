// Package netsec provides type verification for a bytecode-loading host.
// It verifies decoded instruction arrays (no class-file parsing) and
// guarantees that an object whose constructor failed can never escape
// along an exception edge as a usable reference: new produces an
// uninitialized reference tagged with its instruction id, init's normal
// edge initializes every alias of that id in the frame, and init's
// exception edge poisons those aliases in the locals, clears the operand
// stack, and pushes an initialized Throwable. Verification runs a
// worklist fixpoint over all normal and exception-table edges and
// reports the first rejection path.
package netsec

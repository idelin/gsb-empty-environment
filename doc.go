// Package netsec implements a small information-flow-typed expression
// executor.
//
// Every evaluated value carries a Label, a set of at most MaxSources secret
// sources. Constants are low; secret reads carry their source; addition unions
// operand labels; and conditionals add the condition's label to the program
// counter (pc) used for the selected branch. A value produced in a branch is
// therefore also labelled by every enclosing condition.
//
// DeclassifyNode is the only operation that can remove sources from a label.
// It requires an Authorization issued by a trusted Registrar for the same
// request and the same immutable node identity. Declassification removes only
// the authorized sources and then re-applies the current pc, so a
// declassification inside a secret branch cannot wash away the surrounding
// control dependency.
//
// Execute is transactional. Public outputs and authorization consumption are
// committed together after the entire expression succeeds; on any error the
// caller receives no outputs and no authorization is consumed. Executions of
// one Request are serialized, while independent Requests run concurrently and
// meet only in the registrar's atomic commit.
package netsec

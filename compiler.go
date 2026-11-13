package netsec

import "fmt"

// Compiler turns a complete compile input into immutable execution operators.
// It must not read parameter values, because none are present in the input.
type Compiler interface {
	Compile(CompileInput) ([]Operator, error)
}

// CompilerFunc adapts a function to Compiler.
type CompilerFunc func(CompileInput) ([]Operator, error)

func (f CompilerFunc) Compile(in CompileInput) ([]Operator, error) { return f(in) }

// DefaultCompiler emits one scan/write primitive per required operation and a
// row-security primitive whenever a referenced object has an RLS policy.
type DefaultCompiler struct{}

func (DefaultCompiler) Compile(in CompileInput) ([]Operator, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	var ops []Operator
	for _, dep := range in.Objects {
		if dep.Operations.Has(OpSelect) {
			ops = append(ops, NewOperator(OperatorScan, string(dep.ObjectID)))
		}
		if dep.Operations.Has(OpInsert) {
			ops = append(ops, NewOperator(OperatorWrite, "insert:"+string(dep.ObjectID)))
		}
		if dep.Operations.Has(OpUpdate) {
			ops = append(ops, NewOperator(OperatorWrite, "update:"+string(dep.ObjectID)))
		}
		if dep.Operations.Has(OpDelete) {
			ops = append(ops, NewOperator(OperatorWrite, "delete:"+string(dep.ObjectID)))
		}
		if dep.Operations.Has(OpTruncate) {
			ops = append(ops, NewOperator(OperatorWrite, "truncate:"+string(dep.ObjectID)))
		}
		if dep.RLSDigest != "" && dep.RLSDigest != "none" {
			ops = append(ops, NewOperator(OperatorRowSecurity, string(dep.ObjectID)+":"+dep.RLSDigest))
		}
	}
	if len(ops) == 0 {
		ops = append(ops, NewOperator(OperatorQuery, in.QueryASTDigest))
	}
	return ops, nil
}

func compilerOrNil(compilers []Compiler) Compiler {
	for _, compiler := range compilers {
		if compiler != nil {
			return compiler
		}
	}
	return DefaultCompiler{}
}

func compileWith(compiler Compiler, in CompileInput) ([]Operator, error) {
	if compiler == nil {
		compiler = DefaultCompiler{}
	}
	ops, err := compiler.Compile(in)
	if err != nil {
		return nil, fmt.Errorf("compile plan: %w", err)
	}
	for i, op := range ops {
		if op.Kind() == 0 {
			return nil, fmt.Errorf("compile plan: operator %d has no kind", i)
		}
	}
	return append([]Operator(nil), ops...), nil
}

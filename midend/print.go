package midend

import "strings"

// PrintProgram renders a program in a stable textual form.
func PrintProgram(p *Program) string {
	var b strings.Builder
	for _, g := range p.Globals {
		b.WriteString("global @" + g.Name + " " + g.Ty.String() + "\n")
	}
	for _, f := range p.Funcs {
		b.WriteString(PrintFunc(f))
	}
	return b.String()
}

// PrintFunc renders one function.
func PrintFunc(f *Func) string {
	var b strings.Builder
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	b.WriteString("func @" + f.Name + "(" + strings.Join(params, ", ") + ") " + f.Ret.String() + " {\n")
	for _, blk := range f.Blocks {
		b.WriteString(" " + blk.Name + ":\n")
		for _, ins := range blk.Insts {
			b.WriteString("  " + ins.String() + "\n")
		}
		b.WriteString("  " + blk.Term.String() + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

package configcomplete

// pstate is a state of the document parser. The parser is an explicit
// state machine (not recursive descent) so that its exact continuation
// can be inspected at the cursor and used to derive completions.
type pstate int

const (
	psStart pstate = iota
	psDone
	psObjKeyOrEnd
	psObjColon
	psObjValue
	psObjCommaOrEnd
	psObjKey
	psArrValueOrEnd
	psArrCommaOrEnd
	psArrValue
)

type parser struct {
	state pstate
	stack []pstate
}

func valueState(s pstate) bool {
	return s == psStart || s == psObjValue || s == psArrValue || s == psArrValueOrEnd
}

// returnState is the state reached when a value begun in state s finishes.
func returnState(s pstate) pstate {
	switch s {
	case psStart:
		return psDone
	case psObjValue:
		return psObjCommaOrEnd
	default:
		return psArrCommaOrEnd
	}
}

func (p *parser) pop() pstate {
	top := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	return top
}

// feed consumes one complete token; it reports whether the token is legal
// in the current state.
func (p *parser) feed(cat TokenCategory) bool {
	switch p.state {
	case psStart, psObjValue, psArrValue, psArrValueOrEnd:
		switch cat {
		case CatLBrace:
			p.stack = append(p.stack, returnState(p.state))
			p.state = psObjKeyOrEnd
		case CatLBracket:
			p.stack = append(p.stack, returnState(p.state))
			p.state = psArrValueOrEnd
		case CatString, CatNumber, CatTrue, CatFalse, CatNull:
			p.state = returnState(p.state)
		case CatRBracket:
			if p.state != psArrValueOrEnd {
				return false
			}
			p.state = p.pop()
		default:
			return false
		}
	case psObjKeyOrEnd:
		switch cat {
		case CatString:
			p.state = psObjColon
		case CatRBrace:
			p.state = p.pop()
		default:
			return false
		}
	case psObjColon:
		if cat != CatColon {
			return false
		}
		p.state = psObjValue
	case psObjCommaOrEnd:
		switch cat {
		case CatComma:
			p.state = psObjKey
		case CatRBrace:
			p.state = p.pop()
		default:
			return false
		}
	case psObjKey:
		if cat != CatString {
			return false
		}
		p.state = psObjColon
	case psArrCommaOrEnd:
		switch cat {
		case CatComma:
			p.state = psArrValue
		case CatRBracket:
			p.state = p.pop()
		default:
			return false
		}
	case psDone:
		return false
	}
	return true
}

var valueCats = []TokenCategory{CatLBrace, CatLBracket, CatString, CatNumber, CatTrue, CatFalse, CatNull}

// legalCategories returns the categories of tokens that may legally start
// at the cursor when the parser is in state s.
func legalCategories(s pstate) []TokenCategory {
	switch s {
	case psStart, psObjValue, psArrValue:
		return valueCats
	case psArrValueOrEnd:
		cats := make([]TokenCategory, 0, len(valueCats)+1)
		return append(append(cats, valueCats...), CatRBracket)
	case psObjKeyOrEnd:
		return []TokenCategory{CatString, CatRBrace}
	case psObjColon:
		return []TokenCategory{CatColon}
	case psObjCommaOrEnd:
		return []TokenCategory{CatComma, CatRBrace}
	case psObjKey:
		return []TokenCategory{CatString}
	case psArrCommaOrEnd:
		return []TokenCategory{CatComma, CatRBracket}
	default:
		return nil
	}
}

func shorter(a, b string) string {
	if len(a) <= len(b) {
		return a
	}
	return b
}

// minComplete computes the shortest byte string that, appended to a
// document whose parser is in the given state with the given return-state
// stack, produces a complete valid document. The shortest scalar value is
// a single digit ("0"); every other value needs at least two bytes, so
// only scalar candidates compete at value positions. In a comma-or-end
// state, extending with "," costs at least three bytes and returns to the
// same state, so the least fixed point of that recurrence is the closing
// bracket alone.
func minComplete(state pstate, stack []pstate) string {
	switch state {
	case psDone:
		return ""
	case psStart, psObjValue, psArrValue, psArrValueOrEnd:
		ret := returnState(state)
		after := minComplete(ret, stack)
		best := ""
		if state == psArrValueOrEnd {
			best = "]" + minComplete(stack[len(stack)-1], stack[:len(stack)-1])
		}
		for _, v := range []string{"0", `""`, "true", "null", "false"} {
			cand := v + after
			if best == "" || len(cand) < len(best) {
				best = cand
			}
		}
		return best
	case psObjKeyOrEnd:
		closed := "}" + minComplete(stack[len(stack)-1], stack[:len(stack)-1])
		open := `""` + minComplete(psObjColon, stack)
		return shorter(closed, open)
	case psObjColon:
		return ":" + minComplete(psObjValue, stack)
	case psObjCommaOrEnd:
		return "}" + minComplete(stack[len(stack)-1], stack[:len(stack)-1])
	case psObjKey:
		return `""` + minComplete(psObjColon, stack)
	case psArrCommaOrEnd:
		return "]" + minComplete(stack[len(stack)-1], stack[:len(stack)-1])
	}
	return ""
}

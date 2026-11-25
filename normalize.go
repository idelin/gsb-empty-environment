package displaycommit

// Normalized returns a deep-ish copy with dense repeated-field ordinals and
// -1 for singular field occurrences. Input slice order is used when explicit
// ordinals are absent or inconsistent.
func (m Message) Normalized() Message {
	out := m
	out.Fields = normalizeFields(m.Fields)
	return out
}

func normalizeFields(fields []Field) []Field {
	sorted := sortedFields(fields)
	out := make([]Field, len(sorted))
	copy(out, sorted)

	counts := map[int]int{}
	for i := range out {
		n := out[i].number()
		idx := counts[n]
		counts[n]++
		if counts[n] == 1 {
			// Temporarily store candidate index; finalize after group size known.
			out[i].Ordinal = idx
		} else {
			out[i].Ordinal = idx
		}
		out[i].Index = 0
		out[i].Sequence = 0
		out[i].Value = normalizeValue(out[i].Value)
	}
	for n, total := range counts {
		if total == 1 {
			for i := range out {
				if out[i].number() == n {
					out[i].Ordinal = -1
				}
			}
		}
	}
	return out
}

func normalizeValue(v Value) Value {
	v.Kind = v.effectiveKind()
	switch v.Kind {
	case KindMessage:
		v.Fields = normalizeFields(v.Fields)
	case KindList:
		for i := range v.Elements {
			v.Elements[i] = normalizeValue(v.Elements[i])
		}
	}
	return v
}

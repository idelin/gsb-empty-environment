package causalslice

import "sort"

func formatSel(sel map[string]VersionID) string {
	keys := make([]string, 0, len(sel))
	for k := range sel {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{"
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k + "=" + sel[k].String()
	}
	return out + "}"
}

func formatVC(vc map[string]int) string {
	keys := make([]string, 0, len(vc))
	for k := range vc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{"
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k + ":" + itoa(vc[k])
	}
	return out + "}"
}

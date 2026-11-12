package coververify

import (
	"encoding/json"
	"fmt"
)

// contentUID 由回执四要素派生确定性标识：完全相同的转换回执得到相同 UID，
// 任一要素被篡改都会得到不同 UID，使重复回执无法当作新转换重复计数。
func contentUID(rcpt Receipt) string {
	payload := struct {
		ModelVersion string `json:"m"`
		PrevState    Digest `json:"p"`
		Input        Digest `json:"i"`
		Output       Digest `json:"o"`
	}{
		ModelVersion: rcpt.ModelVersion,
		PrevState:    rcpt.PrevState,
		Input:        rcpt.Input,
		Output:       rcpt.Output,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		// 该结构仅含字符串，Marshal 不会失败；失败时退化为拼接仍保持确定性。
		return string(SumDigest([]byte(rcpt.ModelVersion + "|" + string(rcpt.PrevState) + "|" + string(rcpt.Input) + "|" + string(rcpt.Output))))
	}
	return string(SumDigest(raw))
}

func edgeOf(rcpt Receipt) TransitionKey {
	return TransitionKey{
		ModelVersion: rcpt.ModelVersion,
		PrevState:    rcpt.PrevState,
		Input:        rcpt.Input,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func formatEdge(edge TransitionKey) string {
	return fmt.Sprintf("(model=%q prev=%q input=%q)", edge.ModelVersion, edge.PrevState, edge.Input)
}

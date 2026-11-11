package netsec

import "fmt"

// Replay re-applies a finding's decoding chain to the original text and
// returns the final decoded content, letting a host verify that the
// reported secret is present. An empty chain returns text unchanged.
func Replay(text []byte, chain []Step) ([]byte, error) {
	cur := text
	for i, st := range chain {
		if st.Src.Start < 0 || st.Src.End > len(cur) || st.Src.Start >= st.Src.End {
			return nil, fmt.Errorf("netsec: step %d: source range %v out of bounds", i, st.Src)
		}
		seg := cur[st.Src.Start:st.Src.End]
		var dec []byte
		var err error
		switch st.Encoding {
		case EncHex:
			dec, err = decodeHexStrict(seg)
		case EncBase64:
			dec, err = decodeBase64Strict(seg)
		default:
			err = fmt.Errorf("unsupported encoding %q", st.Encoding)
		}
		if err != nil {
			return nil, fmt.Errorf("netsec: step %d: %w", i, err)
		}
		cur = dec
	}
	return cur, nil
}

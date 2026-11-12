package pdfscan

import "io"

// readAllLimited reads from r up to limit+1 bytes and returns an error if
// the stream is larger than limit.
func readAllLimited(r io.Reader, limit int) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	total := 0
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			total += n
			if total > limit {
				return nil, errTooLarge
			}
			buf = append(buf, tmp[:n]...)
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

var errTooLarge = pdfErr("decoded stream exceeds size limit")

type pdfErr string

func (e pdfErr) Error() string { return "pdfscan: " + string(e) }

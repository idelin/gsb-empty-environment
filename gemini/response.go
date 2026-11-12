package gemini

import (
	"io"
	"strconv"
	"strings"
)

// WriteStatus writes one Gemini response header line ("<status> <meta>\r\n").
func WriteStatus(w io.Writer, status int, meta string) error {
	meta = strings.NewReplacer("\r", " ", "\n", " ").Replace(meta)
	_, err := io.WriteString(w, strconv.Itoa(status)+" "+meta+"\r\n")
	return err
}

// WriteError writes the status line carried by a Gemini Error.
func WriteError(w io.Writer, err *Error) error {
	return WriteStatus(w, err.Status, err.Meta)
}

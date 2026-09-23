package layerpack

import (
	"io"
	"os"
	"path/filepath"
)

// EncodeFile encodes r into the file at dst. The container is first written
// to a temporary file in the same directory and atomically renamed into
// place; on any error the temporary file is removed and dst is untouched.
func EncodeFile(dst string, r io.Reader, codecs []Codec, lim Limits) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".layerpack-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err = Encode(tmp, r, codecs, lim); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// DecodeFile decodes the container at src into the file at dst, using the
// same write-temp-then-rename discipline as EncodeFile: on any error the
// temporary output is removed and dst is left untouched.
func DecodeFile(dst, src string, lim Limits) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".layerpack-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err = Decode(tmp, in, lim); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

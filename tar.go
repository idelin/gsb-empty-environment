package oci

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

func entriesFromLayer(layer Layer) ([]Entry, error) {
	entries := coerceEntries(layer.Entries)
	files := coerceEntries(layer.Files)
	if len(entries) > 0 || len(files) > 0 {
		return append(entries, files...), nil
	}

	var reader io.Reader
	switch {
	case layer.Tar != nil:
		reader = layer.Tar
	case layer.Reader != nil:
		reader = layer.Reader
	case len(layer.TarBytes) > 0:
		reader = bytes.NewReader(layer.TarBytes)
	case len(layer.Blob) > 0:
		reader = bytes.NewReader(layer.Blob)
	}
	if reader == nil {
		return nil, nil
	}
	uncompressed, err := maybeGzip(reader)
	if err != nil {
		return nil, err
	}
	parsed, err := readLayerTar(uncompressed, DefaultTarOptions())
	if err != nil {
		return nil, err
	}
	return coerceEntries(parsed.Entries), nil
}

// TarOptions controls layer tar parsing.
type TarOptions struct {
	// ReadContent reads regular-file bytes for credential scanning.
	ReadContent bool
	// MaxContentBytes limits retained content. Zero uses 16 MiB; a negative
	// value retains everything.
	MaxContentBytes int64
}

// DefaultTarOptions returns metadata-safe content scanning defaults.
func DefaultTarOptions() TarOptions {
	return TarOptions{ReadContent: true, MaxContentBytes: 16 << 20}
}

// ParseLayerTar reads one uncompressed OCI tar layer as a Layer value.
func ParseLayerTar(reader io.Reader) (Layer, error) {
	return ParseLayerTarWithOptions(reader, DefaultTarOptions())
}

// ParseLayer parses a plain tar or gzip-compressed tar layer.
func ParseLayer(reader io.Reader) (Layer, error) {
	return ParseLayerWithOptions(reader, DefaultTarOptions())
}

// ParseLayerWithOptions is the format-detecting option-aware layer parser.
func ParseLayerWithOptions(reader io.Reader, options TarOptions) (Layer, error) {
	uncompressed, err := maybeGzip(reader)
	if err != nil {
		return Layer{}, err
	}
	return ParseLayerTarWithOptions(uncompressed, options)
}

// ParseLayerTarWithOptions reads a tar layer with caller options.
func ParseLayerTarWithOptions(reader io.Reader, options TarOptions) (Layer, error) {
	layer, err := readLayerTar(reader, options)
	if err != nil {
		return Layer{}, err
	}
	return *layer, nil
}

// LoadLayerTar reads one tar layer and returns a pointer.
func LoadLayerTar(reader io.Reader) (*Layer, error) {
	return readLayerTar(reader, DefaultTarOptions())
}

// LoadLayer reads a plain or gzip-compressed tar layer.
func LoadLayer(reader io.Reader) (*Layer, error) {
	uncompressed, err := maybeGzip(reader)
	if err != nil {
		return nil, err
	}
	return readLayerTar(uncompressed, DefaultTarOptions())
}

// LayerFromTar is an alias for LoadLayerTar.
func LayerFromTar(reader io.Reader) (*Layer, error) {
	return readLayerTar(reader, DefaultTarOptions())
}

// ReadLayer is a concise alias for ParseLayerTar.
func ReadLayer(reader io.Reader) (Layer, error) { return ParseLayerTar(reader) }

func maybeGzip(reader io.Reader) (io.Reader, error) {
	buffered := bufio.NewReader(reader)
	magic, peekErr := buffered.Peek(2)
	if peekErr != nil && peekErr != io.EOF {
		return nil, fmt.Errorf("detect compression: %w", peekErr)
	}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, fmt.Errorf("open gzip layer: %w", err)
		}
		return gz, nil
	}
	return buffered, nil
}

func readLayerTar(reader io.Reader, options TarOptions) (*Layer, error) {
	if options.MaxContentBytes == 0 {
		options = DefaultTarOptions()
	}

	tr := tar.NewReader(reader)
	layer := &Layer{}
	var entries []Entry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar header: %w", err)
		}

		xattrs := extractTarXattrs(hdr)
		entry := Entry{
			Path:      hdr.Name,
			Name:      hdr.Name,
			Typeflag:  hdr.Typeflag,
			Mode:      uint32(hdr.Mode),
			UID:       hdr.Uid,
			Uid:       hdr.Uid,
			GID:       hdr.Gid,
			Gid:       hdr.Gid,
			Uname:     hdr.Uname,
			Gname:     hdr.Gname,
			Size:      hdr.Size,
			ModTime:   hdr.ModTime,
			Linkname:  hdr.Linkname,
			LinkName:  hdr.Linkname,
			Target:    hdr.Linkname,
			Xattrs:    xattrs,
			RawXattrs: hdr.Xattrs,
			Header:    hdr,
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			entry.Symlink = hdr.Linkname
		case tar.TypeLink:
			entry.Type = TypeHardlink
		case tar.TypeDir:
			entry.Type = TypeDirectory
		}

		if options.ReadContent && (hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA || hdr.Typeflag == 0) {
			limit := options.MaxContentBytes
			if limit < 0 {
				content, readErr := io.ReadAll(tr)
				if readErr != nil {
					return nil, fmt.Errorf("read tar entry %s: %w", hdr.Name, readErr)
				}
				entry.Content = content
			} else {
				content, readErr := io.ReadAll(io.LimitReader(tr, limit))
				if readErr != nil {
					return nil, fmt.Errorf("read tar entry %s: %w", hdr.Name, readErr)
				}
				entry.Content = append([]byte(nil), content...)
				entry.ContentTruncated = hdr.Size > limit
				_, _ = io.Copy(io.Discard, tr)
			}
		}

		entries = append(entries, entry)
	}
	layer.Entries = entries
	return layer, nil
}

func extractTarXattrs(hdr *tar.Header) map[string][]byte {
	out := make(map[string][]byte)
	for key, value := range hdr.Xattrs {
		out[key] = []byte(value)
	}
	if hdr.PAXRecords != nil {
		for key, value := range hdr.PAXRecords {
			switch {
			case strings.HasPrefix(key, paxSchilyXattr):
				name := strings.TrimPrefix(key, paxSchilyXattr)
				out[name] = []byte(value)
			case strings.HasPrefix(key, "LIBARCHIVE.xattr."):
				name := strings.TrimPrefix(key, "LIBARCHIVE.xattr.")
				out[name] = []byte(value)
			case key == paxSchilyXattr+SecurityCapabilityXattr:
				out[SecurityCapabilityXattr] = []byte(value)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

const paxSchilyXattr = "SCHILY.xattr."

// MergeTarLayers parses and merges uncompressed tar readers bottom-to-top.
func MergeTarLayers(readers ...io.Reader) (*FileSystem, error) {
	layers := make([]Layer, 0, len(readers))
	for i, reader := range readers {
		layer, err := ParseLayer(reader)
		if err != nil {
			return nil, fmt.Errorf("layer %d: %w", i, err)
		}
		layers = append(layers, layer)
	}
	return Merge(layers...)
}

// ScanTarLayers parses, merges, and validates uncompressed tar readers.
func ScanTarLayers(policy any, readers ...io.Reader) (*Report, *MergeResult, error) {
	layers := make([]Layer, 0, len(readers))
	for i, reader := range readers {
		layer, err := ParseLayer(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("layer %d: %w", i, err)
		}
		layers = append(layers, layer)
	}
	return VerifyLayers(layers, policy)
}

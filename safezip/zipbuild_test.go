package safezip

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
)

// nopSeek wraps a writer so archive/zip cannot seek it, which forces the
// writer to emit data descriptors instead of precomputed local headers.
type nopSeek struct{ b *bytes.Buffer }

func (w nopSeek) Write(p []byte) (int, error) { return w.b.Write(p) }

type fixtureMember struct {
	name string
	data []byte
	dir  bool
}

// buildZip produces a conventional archive/zip archive. When descriptors is
// true every member uses a trailing data descriptor with zeroed local
// header size fields.
func buildZip(descriptors bool, members ...fixtureMember) []byte {
	var buf bytes.Buffer
	var w *zip.Writer
	if descriptors {
		w = zip.NewWriter(nopSeek{b: &buf})
	} else {
		w = zip.NewWriter(&buf)
	}
	for _, m := range members {
		hdr := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		if m.dir {
			hdr.Name = m.name
			hdr.SetMode(0o755 | 0o040000)
			hdr.Method = zip.Store
		}
		f, err := w.CreateHeader(hdr)
		if err != nil {
			panic(err)
		}
		if !m.dir {
			if _, err := f.Write(m.data); err != nil {
				panic(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// rawZip64Member describes one member of a hand-built ZIP64 archive.
type rawZip64Member struct {
	name string
	data []byte
	dir  bool
}

// buildRawZip64 assembles a minimal ZIP64 archive manually so tests are not
// dependent on archive/zip's ZIP64 emission rules. Every member gets ZIP64
// extra fields and a data descriptor.
func buildRawZip64(members []rawZip64Member) []byte {
	var out bytes.Buffer
	type meta struct {
		offset               int64
		crc                  uint32
		compSize, uncompSize int64
		method               uint16
		flags                uint16
		dir                  bool
		name                 string
		comp                 []byte
	}
	metas := make([]meta, 0, len(members))

	for _, m := range members {
		offset := int64(out.Len())
		nameB := []byte(m.name)
		var method uint16 = methodStore
		var comp []byte
		crc := crc32.ChecksumIEEE(m.data)
		if !m.dir {
			method = methodDeflate
			var cbuf bytes.Buffer
			fw, _ := flate.NewWriter(&cbuf, flate.DefaultCompression)
			_, _ = fw.Write(m.data)
			_ = fw.Close()
			comp = cbuf.Bytes()
		}
		flags := uint16(flagDataDescriptor)

		// Local file header with ZIP64 extra (comp + uncomp sizes).
		localExtra := buildZip64LocalExtra(int64(len(comp)), int64(len(m.data)))
		hdr := make([]byte, localHeaderFixed)
		binary.LittleEndian.PutUint32(hdr[0:], sigLocal)
		binary.LittleEndian.PutUint16(hdr[4:], 20)
		binary.LittleEndian.PutUint16(hdr[6:], flags)
		binary.LittleEndian.PutUint16(hdr[8:], method)
		binary.LittleEndian.PutUint16(hdr[26:], uint16(len(nameB)))
		binary.LittleEndian.PutUint16(hdr[28:], uint16(len(localExtra)))
		out.Write(hdr)
		out.Write(nameB)
		out.Write(localExtra)
		out.Write(comp)

		// ZIP64 data descriptor: signature + crc + 8-byte sizes.
		desc := make([]byte, 24)
		binary.LittleEndian.PutUint32(desc[0:], sigDataDesc)
		binary.LittleEndian.PutUint32(desc[4:], crc)
		binary.LittleEndian.PutUint64(desc[8:], uint64(len(comp)))
		binary.LittleEndian.PutUint64(desc[16:], uint64(len(m.data)))
		out.Write(desc)

		metas = append(metas, meta{
			offset: offset, crc: crc,
			compSize: int64(len(comp)), uncompSize: int64(len(m.data)),
			method: method, flags: flags, dir: m.dir, name: m.name, comp: comp,
		})
	}

	cdOffset := int64(out.Len())
	for _, mt := range metas {
		nameB := []byte(mt.name)
		extra := buildZip64CentralExtra(mt.compSize, mt.uncompSize, mt.offset)
		hdr := make([]byte, centralHeaderFixed)
		binary.LittleEndian.PutUint32(hdr[0:], sigCentral)
		binary.LittleEndian.PutUint16(hdr[4:], 20)
		binary.LittleEndian.PutUint16(hdr[6:], 20)
		binary.LittleEndian.PutUint16(hdr[8:], mt.flags)
		binary.LittleEndian.PutUint16(hdr[10:], mt.method)
		binary.LittleEndian.PutUint32(hdr[16:], mt.crc)
		binary.LittleEndian.PutUint32(hdr[20:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(hdr[24:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint16(hdr[28:], uint16(len(nameB)))
		binary.LittleEndian.PutUint16(hdr[30:], uint16(len(extra)))
		binary.LittleEndian.PutUint32(hdr[38:], makeExternalAttr(mt.dir))
		binary.LittleEndian.PutUint32(hdr[42:], 0xFFFFFFFF)
		out.Write(hdr)
		out.Write(nameB)
		out.Write(extra)
	}
	cdSize := int64(out.Len()) - cdOffset

	z64EOCDOff := int64(out.Len())
	rec := make([]byte, zip64EOCDFixedSize)
	binary.LittleEndian.PutUint32(rec[0:], sigZip64EOCD)
	binary.LittleEndian.PutUint64(rec[4:], 44)                  // size of ZIP64 EOCD record
	binary.LittleEndian.PutUint64(rec[24:], uint64(len(metas))) // entries on this disk
	binary.LittleEndian.PutUint64(rec[32:], uint64(len(metas))) // total entries
	binary.LittleEndian.PutUint64(rec[40:], uint64(cdSize))
	binary.LittleEndian.PutUint64(rec[48:], uint64(cdOffset))
	out.Write(rec)

	loc := make([]byte, locatorSize)
	binary.LittleEndian.PutUint32(loc[0:], sigZip64Locator)
	binary.LittleEndian.PutUint64(loc[8:], uint64(z64EOCDOff))
	binary.LittleEndian.PutUint32(loc[16:], 1)
	out.Write(loc)

	eocd := make([]byte, eocdFixedSize)
	binary.LittleEndian.PutUint32(eocd[0:], sigEOCD)
	binary.LittleEndian.PutUint16(eocd[8:], 0xFFFF)
	binary.LittleEndian.PutUint16(eocd[10:], 0xFFFF)
	binary.LittleEndian.PutUint32(eocd[12:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(eocd[16:], 0xFFFFFFFF)
	out.Write(eocd)
	return out.Bytes()
}

func buildZip64LocalExtra(comp, uncomp int64) []byte {
	var b bytes.Buffer
	hdr := make([]byte, 4)
	binary.LittleEndian.PutUint16(hdr[0:], extraZip64)
	binary.LittleEndian.PutUint16(hdr[2:], 16)
	b.Write(hdr)
	tmp := make([]byte, 8)
	binary.LittleEndian.PutUint64(tmp, uint64(uncomp))
	b.Write(tmp)
	binary.LittleEndian.PutUint64(tmp, uint64(comp))
	b.Write(tmp)
	return b.Bytes()
}

func buildZip64CentralExtra(comp, uncomp, offset int64) []byte {
	var b bytes.Buffer
	hdr := make([]byte, 4)
	binary.LittleEndian.PutUint16(hdr[0:], extraZip64)
	binary.LittleEndian.PutUint16(hdr[2:], 24)
	b.Write(hdr)
	tmp := make([]byte, 8)
	binary.LittleEndian.PutUint64(tmp, uint64(uncomp))
	b.Write(tmp)
	binary.LittleEndian.PutUint64(tmp, uint64(comp))
	b.Write(tmp)
	binary.LittleEndian.PutUint64(tmp, uint64(offset))
	b.Write(tmp)
	return b.Bytes()
}

func makeExternalAttr(dir bool) uint32 {
	if dir {
		return msdosDirectoryAttr | (0o755 << 16)
	}
	return 0o644 << 16
}

// buildRawZip64Store creates a single-member ZIP64 archive using the store
// (no compression) method with a data descriptor.
func buildRawZip64Store(name string, data []byte) []byte {
	var out bytes.Buffer
	offset := int64(0)
	nameB := []byte(name)
	crc := crc32.ChecksumIEEE(data)
	flags := uint16(flagDataDescriptor)

	localExtra := buildZip64LocalExtra(int64(len(data)), int64(len(data)))
	hdr := make([]byte, localHeaderFixed)
	binary.LittleEndian.PutUint32(hdr[0:], sigLocal)
	binary.LittleEndian.PutUint16(hdr[4:], 20)
	binary.LittleEndian.PutUint16(hdr[6:], flags)
	binary.LittleEndian.PutUint16(hdr[8:], methodStore)
	binary.LittleEndian.PutUint16(hdr[26:], uint16(len(nameB)))
	binary.LittleEndian.PutUint16(hdr[28:], uint16(len(localExtra)))
	out.Write(hdr)
	out.Write(nameB)
	out.Write(localExtra)
	out.Write(data)

	desc := make([]byte, 24)
	binary.LittleEndian.PutUint32(desc[0:], sigDataDesc)
	binary.LittleEndian.PutUint32(desc[4:], crc)
	binary.LittleEndian.PutUint64(desc[8:], uint64(len(data)))
	binary.LittleEndian.PutUint64(desc[16:], uint64(len(data)))
	out.Write(desc)

	cdOffset := int64(out.Len())
	extra := buildZip64CentralExtra(int64(len(data)), int64(len(data)), offset)
	ch := make([]byte, centralHeaderFixed)
	binary.LittleEndian.PutUint32(ch[0:], sigCentral)
	binary.LittleEndian.PutUint16(ch[4:], 20)
	binary.LittleEndian.PutUint16(ch[6:], 20)
	binary.LittleEndian.PutUint16(ch[8:], flags)
	binary.LittleEndian.PutUint16(ch[10:], methodStore)
	binary.LittleEndian.PutUint32(ch[16:], crc)
	binary.LittleEndian.PutUint32(ch[20:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(ch[24:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint16(ch[28:], uint16(len(nameB)))
	binary.LittleEndian.PutUint16(ch[30:], uint16(len(extra)))
	binary.LittleEndian.PutUint32(ch[38:], makeExternalAttr(false))
	binary.LittleEndian.PutUint32(ch[42:], 0xFFFFFFFF)
	out.Write(ch)
	out.Write(nameB)
	out.Write(extra)
	cdSize := int64(out.Len()) - cdOffset

	z64EOCDOff := int64(out.Len())
	rec := make([]byte, zip64EOCDFixedSize)
	binary.LittleEndian.PutUint32(rec[0:], sigZip64EOCD)
	binary.LittleEndian.PutUint64(rec[4:], 44)
	binary.LittleEndian.PutUint64(rec[24:], 1)
	binary.LittleEndian.PutUint64(rec[32:], 1)
	binary.LittleEndian.PutUint64(rec[40:], uint64(cdSize))
	binary.LittleEndian.PutUint64(rec[48:], uint64(cdOffset))
	out.Write(rec)

	loc := make([]byte, locatorSize)
	binary.LittleEndian.PutUint32(loc[0:], sigZip64Locator)
	binary.LittleEndian.PutUint64(loc[8:], uint64(z64EOCDOff))
	binary.LittleEndian.PutUint32(loc[16:], 1)
	out.Write(loc)

	eocd := make([]byte, eocdFixedSize)
	binary.LittleEndian.PutUint32(eocd[0:], sigEOCD)
	binary.LittleEndian.PutUint16(eocd[8:], 0xFFFF)
	binary.LittleEndian.PutUint16(eocd[10:], 0xFFFF)
	binary.LittleEndian.PutUint32(eocd[12:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(eocd[16:], 0xFFFFFFFF)
	out.Write(eocd)
	return out.Bytes()
}

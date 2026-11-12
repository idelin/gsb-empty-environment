package netsec

import "errors"

// ErrPageUnmapped is returned when a page that belonged to the image is no
// longer mapped at the time of the read.
var ErrPageUnmapped = errors.New("netsec: page is not mapped")

// PageSource abstracts page-level access to a live process image. Every
// mutation of the image (write, map, unmap, remap) must monotonically bump
// the commit generation, and each page carries the write generation of its
// last modification. Generations come from a single monotonic counter, so a
// page's write generation never decreases and differs after any touch.
type PageSource interface {
	// CommitGen returns the current commit generation of the whole image.
	CommitGen() uint64
	// ListPages returns the sorted base addresses of all mapped pages.
	ListPages() ([]uint64, error)
	// ReadPage returns the page content together with its write generation.
	// It returns ErrPageUnmapped if the page is gone.
	ReadPage(addr uint64) (data []byte, writeGen uint64, err error)
	// PageWriteGen returns only the write generation of a page. It is the
	// cheap validation probe used to detect affected pages without
	// re-reading content.
	PageWriteGen(addr uint64) (writeGen uint64, err error)
}

package bitmap

import "unsafe"

func uintptrOf(b *Bitmap) uintptr { return uintptr(unsafe.Pointer(b)) }

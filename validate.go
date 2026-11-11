package yuvconv

// Stride defaults follow the tightly packed layout used by most encoders; a
// caller supplying padded planes sets the explicit stride fields instead.

func i420Strides(frame I420Frame, c *converter) (yStride, cStride int, err error) {
	yStride = frame.YStride
	if yStride == 0 {
		yStride = c.p.Width
	}
	cStride = frame.CStride
	if cStride == 0 {
		cStride = c.cw
	}
	if yStride < c.p.Width || cStride < c.cw {
		return 0, 0, ErrInvalidParams
	}
	if len(frame.Y) < (c.p.Height-1)*yStride+c.p.Width ||
		len(frame.U) < (c.ch-1)*cStride+c.cw ||
		len(frame.V) < (c.ch-1)*cStride+c.cw {
		return 0, 0, ErrInvalidParams
	}
	return yStride, cStride, nil
}

func nv12Strides(frame NV12Frame, c *converter) (yStride, cStride int, err error) {
	yStride = frame.YStride
	if yStride == 0 {
		yStride = c.p.Width
	}
	// The interleaved plane holds 2*cw bytes per chroma row.
	cStride = frame.CStride
	if cStride == 0 {
		cStride = 2 * c.cw
	}
	if yStride < c.p.Width || cStride < 2*c.cw {
		return 0, 0, ErrInvalidParams
	}
	if len(frame.Y) < (c.p.Height-1)*yStride+c.p.Width ||
		len(frame.C) < (c.ch-1)*cStride+2*c.cw {
		return 0, 0, ErrInvalidParams
	}
	return yStride, cStride, nil
}

func p010Strides(frame P010Frame, c *converter) (yStride, cStride int, err error) {
	yStride = frame.YStride
	if yStride == 0 {
		yStride = c.p.Width
	}
	cStride = frame.CStride
	if cStride == 0 {
		cStride = 2 * c.cw
	}
	if yStride < c.p.Width || cStride < 2*c.cw {
		return 0, 0, ErrInvalidParams
	}
	if len(frame.Y) < (c.p.Height-1)*yStride+c.p.Width ||
		len(frame.C) < (c.ch-1)*cStride+2*c.cw {
		return 0, 0, ErrInvalidParams
	}
	return yStride, cStride, nil
}

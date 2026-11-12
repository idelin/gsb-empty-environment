// Package taskpkg implements elliptical weighted average (EWA) texture
// filtering for a software shader host.
//
// The sampler consumes texture coordinates together with their screen-space
// derivatives, builds the quadratic form of the pixel footprint in texture
// space, selects the mip level from the footprint minor axis, and accumulates
// texels inside the ellipse with weights exp(-2*r^2)-exp(-2); only texels with
// r^2 <= 1 contribute. The major/minor axis ratio is clamped to a
// caller-supplied limit, degenerate footprints fall back to bilinear
// filtering, periodic and clamp addressing are supported, and every color is
// handled as linear, premultiplied-alpha.
package taskpkg

package netsec

// Package netsec provides a segmented, length-gated reader for SSH-style
// authenticated transport. It reads each protected header into a fixed small
// reusable buffer, authenticates the header before trusting its declared body
// length, validates the length and cipher-block alignment, and only then
// allocates a bounded body buffer. The decrypted body is delivered solely
// after it authenticates, so oversized, truncated, or unauthenticated frames
// never cause a large allocation or redelivery of a prior packet. Crypto is
// supplied by the host through the FrameTransform injector.

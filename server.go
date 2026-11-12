package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrCompressedCertificateAlreadySent prevents a connection from sending
	// more than one compressed certificate message.
	ErrCompressedCertificateAlreadySent = errors.New("netsec: compressed certificate already sent on this connection")
	// ErrNoCompressionAlgorithm is returned by callers that require
	// compression when the client and server have no common supported
	// algorithm.
	ErrNoCompressionAlgorithm = errors.New("netsec: no mutually supported certificate compression algorithm")
)

// ServerConfig configures certificate compression. Nil fields receive safe
// defaults. Algorithms is the server's preference order; an empty slice means
// zlib only.
type ServerConfig struct {
	Dictionaries *DictionaryStore
	Codecs       *CodecRegistry
	Algorithms   []Algorithm
	Cache        *CertificateCache
}

// Server is the reusable, concurrency-safe part of certificate compression.
// Each TLS connection gets its own Connection state.
type Server struct {
	dictionaries *DictionaryStore
	codecs       *CodecRegistry
	algorithms   []Algorithm
	cache        *CertificateCache
}

// NewServer constructs a certificate-compression server.
func NewServer(config ServerConfig) *Server {
	if config.Dictionaries == nil {
		config.Dictionaries = NewDictionaryStore()
	}
	if config.Codecs == nil {
		config.Codecs = DefaultRegistry()
	}
	if config.Cache == nil {
		config.Cache = DefaultCertificateCache()
	}
	if len(config.Algorithms) == 0 {
		config.Algorithms = []Algorithm{AlgorithmZlib}
	}
	return &Server{
		dictionaries: config.Dictionaries,
		codecs:       config.Codecs,
		algorithms:   append([]Algorithm(nil), config.Algorithms...),
		cache:        config.Cache,
	}
}

// Connection holds the per-handshake compression state.
type Connection struct {
	server *Server
	offer  ClientOffer

	mu             sync.Mutex
	compressedSent bool
}

// NewConnection validates compression-relevant ClientHello data for one
// handshake. A dictionary ID owned by another tenant is rejected immediately.
func (s *Server) NewConnection(offer ClientOffer) (*Connection, error) {
	if s == nil {
		return nil, errors.New("netsec: nil server")
	}
	offer.SNI = CanonicalSNI(offer.SNI)
	if len(offer.DictionaryIDs) > 0 {
		for _, id := range offer.DictionaryIDs {
			if _, err := s.dictionaries.Select(offer.SNI, id); err != nil {
				if errors.Is(err, ErrCrossTenantDictionary) {
					return nil, err
				}
				// An unknown ID cannot be used, but it is not a tenant
				// boundary violation. The connection may still fall back to
				// an unkeyed compression dictionary or plaintext.
			}
		}
	}
	return &Connection{
		server: s,
		offer: ClientOffer{
			SNI:           offer.SNI,
			Algorithms:    append([]Algorithm(nil), offer.Algorithms...),
			DictionaryIDs: append([]DictionaryID(nil), offer.DictionaryIDs...),
		},
	}, nil
}

// SendCertificate returns either an RFC 8879 CompressedCertificate body or
// the original TLS Certificate message body. compressed reports which one was
// returned.
//
// If no offered algorithm is supported, or compression does not make the
// certificate payload strictly smaller, plaintext is returned unchanged and
// compressed is false. The method may successfully send at most one compressed
// certificate per Connection.
func (c *Connection) SendCertificate(certificateChain []byte) (wire []byte, compressed bool, err error) {
	if c == nil || c.server == nil {
		return nil, false, errors.New("netsec: nil connection")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.compressedSent {
		return nil, false, ErrCompressedCertificateAlreadySent
	}

	algorithm, codec, ok := c.server.codecs.Select(c.server.algorithms, c.offer.Algorithms)
	if !ok {
		return certificateChain, false, nil
	}

	if len(certificateChain) > MaxUncompressedLength {
		// The uncompressed Certificate message remains legal, but RFC 8879
		// cannot represent its length.
		return certificateChain, false, nil
	}

	dictionary, dictionaryID, selected, err := c.selectDictionary()
	if err != nil {
		return nil, false, err
	}
	if !selected {
		// The client only offered dictionary IDs that this tenant does not
		// own. Do not silently switch to an unkeyed compression dictionary,
		// since the client may be unable to decompress it.
		return certificateChain, false, nil
	}

	key := NewCacheKey(algorithm, dictionaryID, certificateChain)
	compressedPayload, cached := c.server.cache.Get(c.offer.SNI, key)
	if !cached {
		compressedMessage, beneficial, compressErr := CompressCertificate(certificateChain, algorithm, codec, dictionary)
		if compressErr != nil {
			return nil, false, fmt.Errorf("netsec: certificate compression failed: %w", compressErr)
		}
		if !beneficial {
			return certificateChain, false, nil
		}
		compressedPayload = compressedMessage.Compressed
	}
	if len(compressedPayload) >= len(certificateChain) || len(compressedPayload) > MaxCompressedCertificateLength {
		return certificateChain, false, nil
	}

	message := CompressedCertificate{
		Algorithm:          algorithm,
		UncompressedLength: uint32(len(certificateChain)),
		Compressed:         compressedPayload,
	}
	wire, err = message.MarshalBinary()
	if err != nil {
		return nil, false, err
	}
	if !cached {
		c.server.cache.Put(c.offer.SNI, key, compressedPayload)
	}
	c.compressedSent = true
	return wire, true, nil
}

// SendCompressedCertificate is like SendCertificate but reports an error
// instead of silently falling back when compression is unavailable.
func (c *Connection) SendCompressedCertificate(certificateChain []byte) ([]byte, error) {
	wire, compressed, err := c.SendCertificate(certificateChain)
	if err != nil {
		return nil, err
	}
	if !compressed {
		return nil, ErrNoCompressionAlgorithm
	}
	return wire, nil
}

func (c *Connection) selectDictionary() ([]byte, DictionaryID, bool, error) {
	if len(c.offer.DictionaryIDs) == 0 {
		return nil, "", true, nil
	}

	// Unknown IDs cannot be selected. Any ID resolving to another tenant has
	// already been rejected by NewConnection, but Select is called again here
	// so callers that build Connection values manually get the same guarantee.
	for _, id := range c.offer.DictionaryIDs {
		dictionary, err := c.server.dictionaries.Select(c.offer.SNI, id)
		if err == nil {
			return dictionary.Content, dictionary.ID, true, nil
		}
		if errors.Is(err, ErrCrossTenantDictionary) {
			return nil, "", false, err
		}
		if !errors.Is(err, ErrUnknownDictionary) {
			return nil, "", false, err
		}
	}
	return nil, "", false, nil
}

package netsec

import (
	"fmt"
	"sync"
)

// ServerConfig configures a certificate-compressing TLS server.
type ServerConfig struct {
	// Dictionaries holds tenant-scoped compression dictionaries.
	Dictionaries *DictionaryRegistry

	// Cache stores compressed chains. A nil value creates a default bounded
	// cache in NewServer.
	Cache *CompressionCache

	// Codecs holds algorithm implementations. A nil value starts with zlib.
	Codecs *CodecRegistry

	// Preference lists server-preferred algorithms. When empty, client order is
	// used. Algorithms not offered by the client are never selected.
	Preference []Algorithm
}

// Server compresses Certificate handshake messages for TLS connections.
type Server struct {
	initMutex sync.Mutex

	Dictionaries *DictionaryRegistry
	Cache        *CompressionCache
	Codecs       *CodecRegistry
	Preference   []Algorithm
}

// NewServer creates a server. With no argument it uses defaults. Only one
// configuration argument is accepted; additional arguments are ignored.
func NewServer(configs ...ServerConfig) *Server {
	var config ServerConfig
	if len(configs) > 0 {
		config = configs[0]
	}
	server := &Server{
		Dictionaries: config.Dictionaries,
		Cache:        config.Cache,
		Codecs:       config.Codecs,
		Preference:   append([]Algorithm(nil), config.Preference...),
	}
	server.ensureDefaults()
	return server
}

// New is an alias for NewServer.
func New(configs ...ServerConfig) *Server {
	return NewServer(configs...)
}

func (server *Server) ensureDefaults() {
	if server == nil {
		return
	}
	server.initMutex.Lock()
	defer server.initMutex.Unlock()
	if server.Codecs == nil {
		server.Codecs = NewCodecRegistry()
	}
	if server.Cache == nil {
		server.Cache = NewCompressionCache(DefaultCacheEntries)
	}
}

// Connection holds per-connection certificate compression state. A connection
// may emit at most one compressed certificate.
type Connection struct {
	server *Server

	Tenant TenantID
	SNI    string

	mutex                     sync.Mutex
	sentCompressedCertificate bool
}

// NewConnection creates per-connection state. tenant identifies the tenant
// selected by the server; if empty, it is derived from the registry's SNI
// binding.
func (server *Server) NewConnection(tenant TenantID, sni string) *Connection {
	server.ensureDefaults()
	normalizedSNI := normalizeSNI(sni)
	if tenant == "" && server != nil && server.Dictionaries != nil {
		if boundTenant, ok := server.Dictionaries.TenantForSNI(normalizedSNI); ok {
			tenant = boundTenant
		}
	}
	return &Connection{
		server: server,
		Tenant: tenant,
		SNI:    normalizedSNI,
	}
}

// SentCompressedCertificate reports whether this connection already emitted a
// CompressedCertificate message.
func (connection *Connection) SentCompressedCertificate() bool {
	if connection == nil {
		return false
	}
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	return connection.sentCompressedCertificate
}

// CompressCertificate compresses an encoded Certificate message body for the
// handshake. offered must be the algorithm list from the client's
// compress_certificate extension. dictionaryID selects a tenant-scoped
// dictionary, or NoDictionary for dictionary-less compression.
//
// If no offered algorithm is implemented, or if the encoded
// CompressedCertificate body is not strictly smaller than the input, the
// returned result is the original uncompressed Certificate message body.
func (connection *Connection) CompressCertificate(certificateMessage []byte, offered []Algorithm, dictionaryID DictionaryID) (CertificateResult, error) {
	if connection == nil || connection.server == nil {
		return CertificateResult{}, fmt.Errorf("tls: nil certificate compression connection")
	}
	server := connection.server
	server.ensureDefaults()

	if len(certificateMessage) > MaxUncompressedLength {
		return CertificateResult{}, fmt.Errorf("%w: certificate message too long", ErrInvalidCertificateMessage)
	}

	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if connection.sentCompressedCertificate {
		return CertificateResult{}, ErrCompressedCertificateAlreadySent
	}

	var dictionaryBytes []byte
	if dictionaryID != NoDictionary {
		if server.Dictionaries == nil {
			return CertificateResult{}, fmt.Errorf("%w: ID %d", ErrDictionaryNotFound, dictionaryID)
		}
		tenant := connection.Tenant
		if tenant == "" {
			if boundTenant, ok := server.Dictionaries.TenantForSNI(connection.SNI); ok {
				tenant = boundTenant
			}
		}
		dictionary, err := server.Dictionaries.Lookup(tenant, connection.SNI, dictionaryID)
		if err != nil {
			return CertificateResult{}, err
		}
		dictionaryBytes = dictionary.Data
	}

	algorithm, ok := server.Codecs.Select(offered, server.Preference)
	if !ok {
		return uncompressedCertificateResult(certificateMessage, 0, dictionaryID), nil
	}

	cacheKey := NewCacheKey(algorithm, dictionaryID, certificateMessage)
	if compressed, hit := server.Cache.Get(cacheKey); hit {
		if !compressedMessageFits(compressed, certificateMessage) {
			return uncompressedCertificateResult(certificateMessage, algorithm, dictionaryID), nil
		}
		body, err := MarshalCompressedCertificate(&CompressedCertificate{
			Algorithm:          algorithm,
			UncompressedLength: uint32(len(certificateMessage)),
			Compressed:         compressed,
		})
		if err != nil {
			return CertificateResult{}, err
		}
		connection.sentCompressedCertificate = true
		return CertificateResult{
			HandshakeType: HandshakeTypeCompressedCertificate,
			Body:          body,
			Compressed:    true,
			Algorithm:     algorithm,
			DictionaryID:  dictionaryID,
			CacheHit:      true,
		}, nil
	}

	codec, ok := server.Codecs.Lookup(algorithm)
	if !ok {
		return uncompressedCertificateResult(certificateMessage, algorithm, dictionaryID), nil
	}
	compressed, err := codec.Compress(certificateMessage, dictionaryBytes)
	if err != nil {
		return CertificateResult{}, fmt.Errorf("tls: compress certificate: %w", err)
	}
	if !compressedMessageFits(compressed, certificateMessage) {
		return uncompressedCertificateResult(certificateMessage, algorithm, dictionaryID), nil
	}

	body, err := MarshalCompressedCertificate(&CompressedCertificate{
		Algorithm:          algorithm,
		UncompressedLength: uint32(len(certificateMessage)),
		Compressed:         compressed,
	})
	if err != nil {
		return CertificateResult{}, err
	}
	server.Cache.Put(cacheKey, compressed)
	connection.sentCompressedCertificate = true
	return CertificateResult{
		HandshakeType: HandshakeTypeCompressedCertificate,
		Body:          body,
		Compressed:    true,
		Algorithm:     algorithm,
		DictionaryID:  dictionaryID,
		CacheHit:      false,
	}, nil
}

// CompressCertificateFromExtension parses a client compress_certificate
// extension body and compresses the certificate message.
func (connection *Connection) CompressCertificateFromExtension(certificateMessage, extensionBody []byte, dictionaryID DictionaryID) (CertificateResult, error) {
	if len(extensionBody) == 0 {
		return connection.CompressCertificate(certificateMessage, nil, dictionaryID)
	}
	offered, err := ParseCompressCertificateExtension(extensionBody)
	if err != nil {
		return CertificateResult{}, err
	}
	return connection.CompressCertificate(certificateMessage, offered, dictionaryID)
}

// CompressCertificateWithExtension is an alias for
// CompressCertificateFromExtension.
func (connection *Connection) CompressCertificateWithExtension(certificateMessage, extensionBody []byte, dictionaryID DictionaryID) (CertificateResult, error) {
	return connection.CompressCertificateFromExtension(certificateMessage, extensionBody, dictionaryID)
}

// CompressCertificateWithExtensions parses both the algorithm extension and
// the private dictionary-ID extension. Every advertised dictionary ID must be
// owned by the connection's tenant and SNI; the first valid ID is selected.
// An empty dictionary extension selects NoDictionary.
func (connection *Connection) CompressCertificateWithExtensions(certificateMessage, algorithmExtension, dictionaryExtension []byte) (CertificateResult, error) {
	if connection == nil || connection.server == nil {
		return CertificateResult{}, fmt.Errorf("tls: nil certificate compression connection")
	}
	dictionaryID := NoDictionary
	if len(dictionaryExtension) > 0 {
		ids, err := ParseCertificateCompressionDictionaryExtension(dictionaryExtension)
		if err != nil {
			return CertificateResult{}, err
		}
		registry := connection.server.Dictionaries
		tenant := connection.Tenant
		if tenant == "" && registry != nil {
			tenant, _ = registry.TenantForSNI(connection.SNI)
		}
		for _, id := range ids {
			if registry == nil {
				return CertificateResult{}, fmt.Errorf("%w: ID %d", ErrDictionaryNotFound, id)
			}
			if _, err := registry.Lookup(tenant, connection.SNI, id); err != nil {
				return CertificateResult{}, err
			}
		}
		if len(ids) > 0 {
			dictionaryID = ids[0]
		}
	}
	if len(algorithmExtension) == 0 {
		return connection.CompressCertificate(certificateMessage, nil, dictionaryID)
	}
	offered, err := ParseCompressCertificateExtension(algorithmExtension)
	if err != nil {
		return CertificateResult{}, err
	}
	return connection.CompressCertificate(certificateMessage, offered, dictionaryID)
}

// CompressCertificate is a convenience wrapper that creates a connection and
// compresses one certificate message.
func (server *Server) CompressCertificate(tenant TenantID, sni string, certificateMessage []byte, offered []Algorithm, dictionaryID DictionaryID) (CertificateResult, error) {
	return server.NewConnection(tenant, sni).CompressCertificate(certificateMessage, offered, dictionaryID)
}

func uncompressedCertificateResult(certificateMessage []byte, algorithm Algorithm, dictionaryID DictionaryID) CertificateResult {
	return CertificateResult{
		HandshakeType: HandshakeTypeCertificate,
		Body:          append([]byte(nil), certificateMessage...),
		Compressed:    false,
		Algorithm:     algorithm,
		DictionaryID:  dictionaryID,
	}
}

func compressedMessageFits(compressed, certificateMessage []byte) bool {
	totalCompressedBody := len(compressed) + 8
	return totalCompressedBody < len(certificateMessage) && totalCompressedBody <= MaxUncompressedLength
}

// DecompressCertificate validates and decompresses a CompressedCertificate
// received on a connection. Dictionary selection is enforced against tenant and
// SNI before decompression.
func (server *Server) DecompressCertificate(tenant TenantID, sni string, dictionaryID DictionaryID, message *CompressedCertificate) ([]byte, error) {
	if server == nil {
		return nil, fmt.Errorf("tls: nil certificate compression server")
	}
	server.ensureDefaults()
	var dictionaryBytes []byte
	if dictionaryID != NoDictionary {
		if server.Dictionaries == nil {
			return nil, fmt.Errorf("%w: ID %d", ErrDictionaryNotFound, dictionaryID)
		}
		if tenant == "" {
			if boundTenant, ok := server.Dictionaries.TenantForSNI(sni); ok {
				tenant = boundTenant
			}
		}
		dictionary, err := server.Dictionaries.Lookup(tenant, sni, dictionaryID)
		if err != nil {
			return nil, err
		}
		dictionaryBytes = dictionary.Data
	}
	return DecompressCertificateWith(message, dictionaryBytes, server.Codecs)
}

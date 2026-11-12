package channelbinding

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"hash"
)

// handshakeKeyLabel is the dedicated exporter label used to derive a value
// unique to one TLS handshake instance.
const handshakeKeyLabel = "EXPORTER-Channel-Binding-Handshake-Key"

// tlsTransport adapts a *tls.Conn to Transport. The binding type is fixed
// at construction time so the connection advertises exactly one method.
type tlsTransport struct {
	conn *tls.Conn
	typ  Type
}

// NewTLSTransport wraps conn for the single binding type typ.
func NewTLSTransport(conn *tls.Conn, typ Type) (Transport, error) {
	if conn == nil {
		return nil, errors.New("channelbinding: nil tls connection")
	}
	if _, ok := exportLabel(typ); !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedBinding, typ)
	}
	return &tlsTransport{conn: conn, typ: typ}, nil
}

func (t *tlsTransport) BindingType() Type { return t.typ }

func (t *tlsTransport) Exporter() Exporter {
	return &tlsExporter{conn: t.conn, typ: t.typ}
}

// tlsExporter implements Exporter over a *tls.Conn.
type tlsExporter struct {
	conn *tls.Conn
	typ  Type
}

// ExportKeyingMaterial returns RFC 5705 exporter material for
// tls-exporter, and the RFC 5929 server end-point certificate hash for
// tls-server-end-point. The service identity is bound through context.
func (e *tlsExporter) ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error) {
	if e.typ == TLSServerEndPoint {
		sum, err := e.serverEndPointHash()
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), sum...), nil
	}
	state := e.conn.ConnectionState()
	return state.ExportKeyingMaterial(label, context, length)
}

// HandshakeKey derives per-connection material with its own label, so
// evidence from one TLS session cannot be replayed onto another.
func (e *tlsExporter) HandshakeKey() []byte {
	state := e.conn.ConnectionState()
	key, err := state.ExportKeyingMaterial(handshakeKeyLabel, nil, materialLen)
	if err != nil {
		return nil
	}
	return key
}

// serverEndPointHash implements the RFC 5929 hash-selection rules: use
// SHA-256 unless the certificate signature algorithm forces SHA-1, MD5
// (legacy) or SHA-384.
func (e *tlsExporter) serverEndPointHash() ([]byte, error) {
	state := e.conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("channelbinding: no peer certificate for tls-server-end-point")
	}
	cert := state.PeerCertificates[0].Raw
	var h hash.Hash
	switch state.PeerCertificates[0].SignatureAlgorithm {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.ECDSAWithSHA1:
		h = sha1.New()
	case x509.SHA384WithRSA, x509.ECDSAWithSHA384, x509.SHA384WithRSAPSS:
		h = sha512.New384()
	case x509.MD2WithRSA:
		h = md5.New()
	default:
		h = sha256.New()
	}
	h.Write(cert)
	return h.Sum(nil), nil
}

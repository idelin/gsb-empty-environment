package netsec

import (
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"fmt"
	"reflect"
)

// VerifyCertificate verifies an OpenSSH certificate's CA signature.
//
// cert may be a *Certificate, a Certificate value, a binary certificate blob,
// an authorized_keys certificate line, or any value (such as
// *ssh.Certificate) with a Marshal() []byte method.
//
// authority may be a PublicKey, a binary public key blob, an authorized_keys
// line, a PEM public key, a standard-library public key, or any value (such as
// ssh.PublicKey) with a Marshal() []byte method.
//
// The signature is checked over the complete certificate prefix through the
// declared signing key. Changing any byte in the nonce, key, serial,
// certificate type, key ID, principals, validity times, critical options,
// extensions, reserved field, or signing key makes verification fail.
func VerifyCertificate(cert, authority any) error {
	parsed, err := certificateValue(cert)
	if err != nil {
		return err
	}
	key, err := authorityValue(authority)
	if err != nil {
		return err
	}
	return parsed.verifyWithKey(key)
}

// VerifyCert is an alias for VerifyCertificate.
func VerifyCert(cert, authority any) error {
	return VerifyCertificate(cert, authority)
}

// CheckCertificate is an alias for VerifyCertificate.
func CheckCertificate(cert, authority any) error {
	return VerifyCertificate(cert, authority)
}

// CheckCertificateSignature is an alias for VerifyCertificate.
func CheckCertificateSignature(cert, authority any) error {
	return VerifyCertificate(cert, authority)
}

// VerifyCertificateBytes verifies a binary certificate blob with a binary CA
// public key blob.
func VerifyCertificateBytes(certBlob, authorityBlob []byte) error {
	return VerifyCertificate(certBlob, authorityBlob)
}

// VerifySignature verifies this parsed certificate with authority.
func (c *Certificate) VerifySignature(authority any) error {
	key, err := authorityValue(authority)
	if err != nil {
		return err
	}
	return c.verifyWithKey(key)
}

// VerifyCert verifies the CA signature on c with the supplied public key.
func (c *Certificate) VerifyCert(authority PublicKey) error {
	return c.verifyWithKey(authority)
}

func (c *Certificate) verifyWithKey(authority PublicKey) error {
	if c == nil {
		return ErrInvalidCertificate
	}
	if c.Signature == nil {
		return fmt.Errorf("%w: certificate has no signature", ErrInvalidCertificate)
	}
	if len(c.signedBytes) == 0 {
		return fmt.Errorf("%w: certificate has no signed data", ErrInvalidCertificate)
	}
	if authority == nil {
		return fmt.Errorf("%w: missing authority key", ErrInvalidPublicKey)
	}
	if err := c.checkFieldsUnmodified(); err != nil {
		return err
	}
	if err := authority.Verify(c.signedBytes, c.Signature); err != nil {
		return err
	}
	return nil
}

func certificateValue(v any) (*Certificate, error) {
	switch cert := v.(type) {
	case nil:
		return nil, fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	case *Certificate:
		if cert == nil {
			return nil, fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
		}
		return cert, nil
	case Certificate:
		copy := cert
		return &copy, nil
	case []byte:
		return ParseCertificate(cert)
	case string:
		return ParseCertificate([]byte(cert))
	}

	blob, err := marshalLike(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}
	return ParseCertificate(blob)
}

func authorityValue(v any) (PublicKey, error) {
	switch key := v.(type) {
	case nil:
		return nil, fmt.Errorf("%w: nil authority key", ErrInvalidPublicKey)
	case *Certificate:
		return nil, fmt.Errorf("%w: an SSH certificate cannot be a CA key", ErrInvalidPublicKey)
	case Certificate:
		return nil, fmt.Errorf("%w: an SSH certificate cannot be a CA key", ErrInvalidPublicKey)
	case PublicKey:
		if key == nil {
			return nil, fmt.Errorf("%w: nil authority key", ErrInvalidPublicKey)
		}
		return key, nil
	case []byte:
		return ParsePublicKey(key)
	case string:
		return ParsePublicKey([]byte(key))
	case *rsa.PublicKey, rsa.PublicKey,
		*ecdsa.PublicKey, ecdsa.PublicKey,
		ed25519.PublicKey, *dsa.PublicKey:
		return NewPublicKey(key)
	}

	blob, err := marshalLike(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
	}
	return ParsePublicKey(blob)
}

// marshalLike supports interoperability with ssh.PublicKey and
// *ssh.Certificate without taking a dependency on golang.org/x/crypto/ssh.
func marshalLike(v any) (out []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = nil
			err = fmt.Errorf("Marshal call panicked: %v", recovered)
		}
	}()

	value := reflect.ValueOf(v)
	if !value.IsValid() {
		return nil, fmt.Errorf("nil value")
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil, fmt.Errorf("nil interface")
		}
		value = value.Elem()
	}
	if (value.Kind() == reflect.Ptr || value.Kind() == reflect.Map ||
		value.Kind() == reflect.Slice || value.Kind() == reflect.Func) && value.IsNil() {
		return nil, fmt.Errorf("nil %s", value.Kind())
	}

	method := value.MethodByName("Marshal")
	if !method.IsValid() && value.Kind() != reflect.Ptr {
		ptr := reflect.New(value.Type())
		ptr.Elem().Set(value)
		method = ptr.MethodByName("Marshal")
	}
	if !method.IsValid() {
		return nil, fmt.Errorf("%T has no Marshal method", v)
	}
	if method.Type().NumIn() != 0 {
		return nil, fmt.Errorf("%T.Marshal takes arguments", v)
	}

	results := method.Call(nil)
	if len(results) == 0 {
		return nil, fmt.Errorf("%T.Marshal returned no values", v)
	}
	blob, ok := results[0].Interface().([]byte)
	if !ok {
		return nil, fmt.Errorf("%T.Marshal does not return []byte", v)
	}
	if len(results) > 1 && !results[1].IsNil() {
		if resultErr, ok := results[1].Interface().(error); ok {
			return nil, resultErr
		}
		return nil, fmt.Errorf("%T.Marshal returned a non-error second value", v)
	}
	return blob, nil
}

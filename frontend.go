package netsec

import (
	"errors"
	"fmt"
)

var ErrNoECHDecrypter = errors.New("no ECH decrypter configured")

type ECHDecrypterFunc func(outer *ClientHello) (encodedInner []byte, err error)

type ECHFrontend struct {
	Decrypt      ECHDecrypterFunc
	RetryConfigs []byte
	PublicName   string
}

type FrontendResult struct {
	ClientHello  *ClientHello
	Wire         []byte
	ServerName   string
	ECHAccepted  bool
	ECHRejected  bool
	RetryConfigs []byte
}

func (f *ECHFrontend) ProcessClientHello(rawOuter []byte) (*FrontendResult, error) {
	outer, err := ParseClientHello(rawOuter)
	if err != nil {
		return nil, illegalParameter(err)
	}
	outerBody, err := clientHelloBody(rawOuter)
	if err != nil {
		return nil, illegalParameter(err)
	}
	if outer.ExtensionCount(ExtensionECHOuterExtensions) != 0 {
		return nil, illegalParameter(ErrECHOuterExtensionsInOuter)
	}

	echCount := outer.ExtensionCount(ExtensionEncryptedClientHello)
	if echCount == 0 {
		name, err := outer.ServerName()
		if err != nil {
			return nil, illegalParameter(err)
		}
		var retryConfigs []byte
		if f != nil {
			retryConfigs = append([]byte(nil), f.RetryConfigs...)
		}
		return &FrontendResult{
			ClientHello:  outer,
			Wire:         append([]byte(nil), outerBody...),
			ServerName:   name,
			RetryConfigs: retryConfigs,
		}, nil
	}
	if echCount != 1 {
		return nil, illegalParameter(fmt.Errorf("%w: encrypted_client_hello", ErrDuplicateExtension))
	}

	if f == nil || f.Decrypt == nil {
		return nil, illegalParameter(ErrNoECHDecrypter)
	}
	encodedInner, decryptErr := f.Decrypt(outer)
	if decryptErr != nil {
		name := f.PublicName
		if name == "" {
			name, err = outer.ServerName()
			if err != nil {
				return nil, illegalParameter(err)
			}
		}
		return &FrontendResult{
			ClientHello:  outer,
			Wire:         append([]byte(nil), outerBody...),
			ServerName:   name,
			ECHRejected:  true,
			RetryConfigs: append([]byte(nil), f.RetryConfigs...),
		}, nil
	}

	inner, err := reconstructClientHelloInner(outer, encodedInner)
	if err != nil {
		return nil, err
	}
	name, err := inner.ServerName()
	if err != nil {
		return nil, illegalParameter(err)
	}
	wire, err := inner.Marshal()
	if err != nil {
		return nil, illegalParameter(err)
	}
	return &FrontendResult{
		ClientHello: inner,
		Wire:        wire,
		ServerName:  name,
		ECHAccepted: true,
	}, nil
}

func (r *FrontendResult) RetryConfigExtension() *Extension {
	if r == nil || len(r.RetryConfigs) == 0 {
		return nil
	}
	return &Extension{
		Type: ExtensionEncryptedClientHello,
		Data: append([]byte(nil), r.RetryConfigs...),
	}
}

func (r *FrontendResult) ValidateRouting() error {
	if r == nil || r.ClientHello == nil {
		return fmt.Errorf("%w: missing processed ClientHello", ErrMalformedClientHello)
	}
	if r.ECHRejected {
		return nil
	}
	name, err := r.ClientHello.ServerName()
	if err != nil {
		return err
	}
	if name != r.ServerName {
		return fmt.Errorf("routing name %q does not match processed ClientHello SNI %q", r.ServerName, name)
	}
	return nil
}

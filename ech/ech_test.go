package ech

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

type extensionEntry struct {
	extensionType uint16
	data          []byte
}

func TestValidateOuterRejectsShortEnc(t *testing.T) {
	options := testOptions()
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          outerExtension(1, bytes.Repeat([]byte{1}, 31), []byte("payload")),
	})

	_, err := ValidateClientHelloExtensions(data, options)
	if !errors.Is(err, AlertDecodeError) {
		t.Fatalf("expected decode_error, got %v", err)
	}
}

func TestValidateOuterRejectsInnerExtension(t *testing.T) {
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          []byte{byte(Inner)},
	})

	_, err := ValidateClientHelloExtensions(data, testOptions())
	if !errors.Is(err, AlertIllegalParameter) {
		t.Fatalf("expected illegal_parameter, got %v", err)
	}
}

func TestUnknownConfigIDRequestsRetryConfigs(t *testing.T) {
	options := testOptions()
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          outerExtension(99, bytes.Repeat([]byte{2}, 32), []byte("payload")),
	})

	result, err := ValidateClientHelloExtensions(data, options)
	if err != nil {
		t.Fatalf("unknown config ID must not fail: %v", err)
	}
	if result.UsedECH {
		t.Fatal("ECH must not be used for an unknown config ID")
	}
	if !result.SendRetryConfigs {
		t.Fatal("retry_configs must be requested for an unknown config ID")
	}
	if !bytes.Equal(result.RetryConfigs, options.RetryConfigs) {
		t.Fatalf("retry configs = %x, want %x", result.RetryConfigs, options.RetryConfigs)
	}
}

func TestRejectsDuplicateECHExtension(t *testing.T) {
	valid := outerExtension(1, bytes.Repeat([]byte{3}, 32), []byte("payload"))
	data := extensionList(
		extensionEntry{extensionType: ExtensionType, data: valid},
		extensionEntry{extensionType: ExtensionType, data: valid},
	)

	_, err := ValidateClientHelloExtensions(data, testOptions())
	if !errors.Is(err, AlertIllegalParameter) {
		t.Fatalf("expected illegal_parameter, got %v", err)
	}
}

func TestSecondClientHelloRejectsNonEmptyEnc(t *testing.T) {
	options := testOptions()
	options.AfterHelloRetryRequest = true
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          outerExtension(1, bytes.Repeat([]byte{4}, 32), []byte("payload")),
	})

	_, err := ValidateClientHelloExtensions(data, options)
	if !errors.Is(err, AlertDecodeError) {
		t.Fatalf("expected decode_error, got %v", err)
	}
}

func TestAcceptsValidGREASEExtension(t *testing.T) {
	options := testOptions()
	grease := outerExtension(99, bytes.Repeat([]byte{5}, 32), []byte("grease!"))
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          grease,
	})

	result, err := ValidateClientHelloExtensions(data, options)
	if err != nil {
		t.Fatalf("valid GREASE extension: %v", err)
	}
	if result.UsedECH {
		t.Fatal("GREASE must not be treated as an accepted ECH extension")
	}
	if !result.SendRetryConfigs {
		t.Fatal("valid GREASE must cause retry_configs to be sent")
	}
	if result.Extension == nil || result.Extension.Type != Outer {
		t.Fatalf("extension type = %v, want outer", result.Extension)
	}
	if len(result.Extension.Enc) != 32 {
		t.Fatalf("enc length = %d, want 32", len(result.Extension.Enc))
	}
}

func TestSecondClientHelloAcceptsEmptyEnc(t *testing.T) {
	options := testOptions()
	options.AfterHelloRetryRequest = true
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          outerExtension(1, nil, []byte("payload")),
	})

	result, err := ValidateClientHelloExtensions(data, options)
	if err != nil {
		t.Fatalf("empty enc after HRR must be accepted: %v", err)
	}
	if !result.UsedECH {
		t.Fatal("known config ID must use ECH")
	}
}

func TestInnerExtensionAcceptedInInnerClientHello(t *testing.T) {
	options := testOptions()
	options.InnerClientHello = true
	data := extensionList(extensionEntry{
		extensionType: ExtensionType,
		data:          []byte{byte(Inner)},
	})

	result, err := ValidateClientHelloExtensions(data, options)
	if err != nil {
		t.Fatalf("inner ECH extension must be accepted: %v", err)
	}
	if result.Extension == nil || result.Extension.Type != Inner {
		t.Fatalf("extension type = %v, want inner", result.Extension)
	}
}

func testOptions() Options {
	return Options{
		Configs: []Config{{
			ID:  1,
			KEM: X25519KEM,
		}},
		RetryConfigs: []byte{0xfe, 0x0d, 0x72, 0x65, 0x74, 0x72, 0x79},
	}
}

func outerExtension(configID uint8, enc, payload []byte) []byte {
	data := []byte{
		byte(Outer),
		0x00, 0x01,
		0x00, 0x02,
		configID,
	}
	data = binary.BigEndian.AppendUint16(data, uint16(len(enc)))
	data = append(data, enc...)
	data = binary.BigEndian.AppendUint16(data, uint16(len(payload)))
	data = append(data, payload...)
	return data
}

func extensionList(entries ...extensionEntry) []byte {
	var payload []byte
	for _, entry := range entries {
		payload = binary.BigEndian.AppendUint16(payload, entry.extensionType)
		payload = binary.BigEndian.AppendUint16(payload, uint16(len(entry.data)))
		payload = append(payload, entry.data...)
	}

	result := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(result, uint16(len(payload)))
	copy(result[2:], payload)
	return result
}

package mailauth

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type arcBuilder struct {
	keys   *testKeys
	domain string
	now    time.Time
	// selectors[i] is the signing selector for instance i+1.
	selectors []string
}

func newARCBuilder(keys *testKeys) *arcBuilder {
	return &arcBuilder{
		keys:      keys,
		domain:    "example.net",
		now:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		selectors: []string{"arc1", "arc2", "arc3"},
	}
}

func (b *arcBuilder) register(store *MemoryKeyStore, hops int) {
	for i := 0; i < hops && i < len(b.selectors); i++ {
		store.Register(b.domain, b.selectors[i], &b.keys.rsaKey.PublicKey)
	}
}

// buildChain appends hops complete, valid ARC sets to the message.
func (b *arcBuilder) buildChain(raw []byte, hops int) ([]byte, error) {
	msg := raw
	for i := 1; i <= hops; i++ {
		var err error
		msg, err = b.addHop(msg, i, cvFor(i))
		if err != nil {
			return nil, err
		}
	}
	return msg, nil
}

func cvFor(instance int) string {
	if instance == 1 {
		return cvNone
	}
	return cvPass
}

func (b *arcBuilder) addHop(raw []byte, instance int, cv string) ([]byte, error) {
	m, err := ParseMessage(raw)
	if err != nil {
		return nil, err
	}

	aarName := "ARC-Authentication-Results" + strconv.Itoa(instance)
	aarValue := fmt.Sprintf(" i=%d; spf=pass; dkim=pass; d=%s\r\n", instance, b.domain)

	selector := b.selectorFor(instance)
	base := signOptions{
		domain:      b.domain,
		selector:    selector,
		algorithm:   algRSASHA256,
		headerCanon: canonRelaxed,
		bodyCanon:   canonRelaxed,
		headers:     regularHeaderNames(m),
		created:     b.now,
		expires:     b.now.Add(30 * 24 * time.Hour),
		includeTime: true,
		includeExp:  true,
	}

	// AMS signs the message visible up to this instance.
	amsTags := &sigTags{
		algorithm:  base.algorithm,
		domain:     base.domain,
		selector:   base.selector,
		headerAlgo: base.headerCanon,
		bodyAlgo:   base.bodyCanon,
		headerList: append(append([]string{}, base.headers...),
			"arc-authentication-results"+strconv.Itoa(instance),
			"arc-message-signature"+strconv.Itoa(instance)),
		bodyHashB64: bodyHash(base.bodyCanon, m.Body, -1),
		instance:    instance,
		hasInstance: true,
		created:     base.created.Unix(),
		hasCreated:  true,
		expires:     base.expires.Unix(),
		hasExpires:  true,
		bodyLength:  -1,
	}
	amsName := "ARC-Message-Signature" + strconv.Itoa(instance)
	amsUnsigned := buildARCField(amsName, amsTags, "")

	// Place AAR then an unsigned AMS at the end of the block; compute the AMS
	// over that exact view, then replace the placeholder with the real value.
	withAAR := appendHeader(raw, aarName, strings.TrimSuffix(aarValue, "\r\n"))
	withUnsignedAMS := appendHeader(withAAR, amsName,
		strings.TrimSuffix(strings.TrimPrefix(amsUnsigned, amsName+":"), "\r\n"))
	m2, err := ParseMessage(withUnsignedAMS)
	if err != nil {
		return nil, err
	}
	amsHeader, ok := findHeader(m2.Headers, amsName)
	if !ok {
		return nil, fmt.Errorf("AMS header %s not found", amsName)
	}
	amsData, _, err := signedData(m2.scopedHeaders(instance, false), amsHeader, amsName, amsTags)
	if err != nil {
		return nil, err
	}
	amsSig, err := signBytes(b.keys, base.algorithm, amsData)
	if err != nil {
		return nil, err
	}
	amsField := buildARCField(amsName, amsTags, amsSig)
	withAMS := appendHeader(withAAR, amsName,
		fieldValueOnly(amsField, amsName))

	// AS seals AAR, AMS and the AS itself for this instance.
	asTags := &sigTags{
		algorithm:   base.algorithm,
		domain:      base.domain,
		selector:    selector,
		headerAlgo:  base.headerCanon,
		bodyAlgo:    base.bodyCanon,
		headerList:  sealHeaderList(instance),
		instance:    instance,
		hasInstance: true,
		cv:          cv,
		created:     base.created.Unix(),
		hasCreated:  true,
		expires:     base.expires.Unix(),
		hasExpires:  true,
		bodyLength:  -1,
	}
	asName := "ARC-Seal" + strconv.Itoa(instance)
	asUnsigned := buildARCField(asName, asTags, "")

	withASUnsigned := appendHeader(withAMS, asName, fieldValueOnly(asUnsigned, asName))
	m3, err := ParseMessage(withASUnsigned)
	if err != nil {
		return nil, err
	}
	asHeader, ok := findHeader(m3.Headers, asName)
	if !ok {
		return nil, fmt.Errorf("AS header %s not found", asName)
	}
	asData, _, err := signedData(m3.scopedHeaders(instance, true), asHeader, asName, asTags)
	if err != nil {
		return nil, err
	}
	asSig, err := signBytes(b.keys, base.algorithm, asData)
	if err != nil {
		return nil, err
	}
	asField := buildARCField(asName, asTags, asSig)

	final := appendHeader(withAMS, asName, fieldValueOnly(asField, asName))
	return final, nil
}

// fieldValueOnly strips the "Name:" prefix and trailing CRLF from a rendered
// ARC field so appendHeader supplies the field name itself exactly once.
func fieldValueOnly(field, name string) string {
	return strings.TrimSuffix(strings.TrimPrefix(field, name+":"), "\r\n")
}

func (b *arcBuilder) selectorFor(instance int) string {
	idx := instance - 1
	if idx >= len(b.selectors) {
		return b.selectors[len(b.selectors)-1]
	}
	return b.selectors[idx]
}

func regularHeaderNames(m *Message) []string {
	var names []string
	for _, h := range m.Headers {
		lower := strings.ToLower(h.Name)
		if _, isARC := arcInstance(lower); isARC {
			continue
		}
		names = append(names, lower)
	}
	return names
}

func sealHeaderList(instance int) []string {
	return []string{
		"arc-authentication-results" + strconv.Itoa(instance),
		"arc-message-signature" + strconv.Itoa(instance),
	}
}

func buildARCField(fieldName string, tags *sigTags, signatureB64 string) string {
	var b strings.Builder
	b.WriteString(fieldName)
	b.WriteString(": a=")
	b.WriteString(tags.algorithm)
	b.WriteString("; d=")
	b.WriteString(tags.domain)
	b.WriteString("; s=")
	b.WriteString(tags.selector)
	b.WriteString("; c=")
	b.WriteString(tags.headerAlgo)
	b.WriteByte('/')
	b.WriteString(tags.bodyAlgo)
	b.WriteString("; t=")
	b.WriteString(strconv.FormatInt(tags.created, 10))
	b.WriteString("; x=")
	b.WriteString(strconv.FormatInt(tags.expires, 10))
	if strings.HasPrefix(strings.ToLower(fieldName), "arc-seal") {
		b.WriteString("; i=")
		b.WriteString(strconv.Itoa(tags.instance))
		b.WriteString("; cv=")
		b.WriteString(tags.cv)
	} else {
		b.WriteString("; i=")
		b.WriteString(strconv.Itoa(tags.instance))
	}
	b.WriteString("; h=")
	b.WriteString(strings.Join(tags.headerList, ":"))
	if !strings.HasPrefix(strings.ToLower(fieldName), "arc-seal") {
		b.WriteString("; bh=")
		b.WriteString(tags.bodyHashB64)
	}
	b.WriteString("; b=")
	b.WriteString(signatureB64)
	b.WriteString("\r\n")
	return b.String()
}

func appendHeader(raw []byte, name, value string) []byte {
	sep := []byte("\r\n\r\n")
	idx := strings.Index(string(raw), string(sep))
	if idx < 0 {
		return raw
	}
	field := name + ":" + value + "\r\n"
	out := make([]byte, 0, len(raw)+len(field))
	out = append(out, raw[:idx+2]...)
	out = append(out, field...)
	out = append(out, raw[idx+2:]...)
	return out
}

func findHeader(headers []Header, name string) (Header, bool) {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
	}
	return Header{}, false
}

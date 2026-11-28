package dmarc

import (
	"errors"
	"net/mail"
	"strings"
)

// FromDomain extracts the single RFC 5322 From domain from a message. Multiple
// From fields or multiple mailboxes are rejected because DMARC requires one.
func FromDomain(message []byte) (string, error) {
	values, err := headerValues(message, "from")
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", errors.New("dmarc: message has no From header")
	}
	if len(values) > 1 {
		return "", errors.New("dmarc: message has multiple From headers")
	}
	addresses, err := mail.ParseAddressList(values[0])
	if err != nil {
		return "", errors.New("dmarc: invalid From header")
	}
	if len(addresses) != 1 {
		return "", errors.New("dmarc: From header must contain exactly one mailbox")
	}
	address := addresses[0].Address
	at := strings.LastIndexByte(address, '@')
	if at < 0 || at == len(address)-1 {
		return "", errors.New("dmarc: From mailbox has no domain")
	}
	domain := strings.TrimSuffix(strings.ToLower(address[at+1:]), ".")
	if domain == "" {
		return "", errors.New("dmarc: From mailbox has no domain")
	}
	return domain, nil
}

// ExtractFromDomain is an alias for FromDomain.
func ExtractFromDomain(message []byte) (string, error) { return FromDomain(message) }

func headerValues(message []byte, wanted string) ([]string, error) {
	text := string(message)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	headerBlock := text
	if end := strings.Index(text, "\n\n"); end >= 0 {
		headerBlock = text[:end]
	}
	lines := strings.Split(headerBlock, "\n")
	var values []string
	currentMatch := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if currentMatch {
				values[len(values)-1] += " " + strings.TrimLeft(line, " \t")
			}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			currentMatch = false
			continue
		}
		currentMatch = strings.EqualFold(strings.TrimSpace(name), wanted)
		if currentMatch {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values, nil
}

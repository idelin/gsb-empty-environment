package netsec

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func buildSignatureBase(
	request *http.Request,
	targetURI string,
	input signatureInput,
) ([]byte, error) {
	var base strings.Builder
	for _, component := range input.components {
		name := normalizedComponentName(component.name)
		value, err := componentValue(request, targetURI, name)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("%w: component %q", ErrHeaderValue, name)
		}
		quotedName, err := quoteComponentName(name)
		if err != nil {
			return nil, err
		}
		base.WriteString(quotedName)
		base.WriteString(": ")
		base.WriteString(value)
		base.WriteByte('\n')
	}

	signatureParameters, err := serializeSignatureParameters(input)
	if err != nil {
		return nil, err
	}
	base.WriteString(`"@signature-params": `)
	base.WriteString(signatureParameters)
	base.WriteByte('\n')
	return []byte(base.String()), nil
}

func normalizedComponentName(name string) string {
	return strings.ToLower(name)
}

func quoteComponentName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty component name", ErrMalformedSignature)
	}
	for _, char := range name {
		if char == '\r' || char == '\n' || char == 0 {
			return "", fmt.Errorf("%w: component name", ErrHeaderValue)
		}
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `"`, `\"`) + `"`, nil
}

func componentValue(
	request *http.Request,
	targetURI string,
	name string,
) (string, error) {
	switch name {
	case "@method":
		if request.Method == "" {
			return "", fmt.Errorf("%w: request method is empty", ErrMalformedSignature)
		}
		return request.Method, nil

	case "@target-uri":
		return targetURI, nil

	case "@scheme":
		parsed, err := url.Parse(targetURI)
		if err != nil || parsed.Scheme == "" {
			return "", fmt.Errorf("%w: invalid target URI scheme", ErrMalformedSignature)
		}
		return parsed.Scheme, nil

	case "@authority":
		parsed, err := url.Parse(targetURI)
		if err != nil || parsed.Host == "" {
			return "", fmt.Errorf("%w: invalid target URI authority", ErrMalformedSignature)
		}
		return parsed.Host, nil

	case "@path":
		parsed, err := url.Parse(targetURI)
		if err != nil {
			return "", fmt.Errorf("%w: invalid target URI", ErrMalformedSignature)
		}
		path := parsed.EscapedPath()
		if path == "" {
			path = "/"
		}
		return path, nil

	case "@query":
		parsed, err := url.Parse(targetURI)
		if err != nil {
			return "", fmt.Errorf("%w: invalid target URI", ErrMalformedSignature)
		}
		return "?" + parsed.RawQuery, nil

	case "@request-target":
		if request.URL == nil {
			return "", fmt.Errorf("%w: nil request URL", ErrMalformedSignature)
		}
		target := request.URL.RequestURI()
		if target == "" {
			target = "/"
		}
		return target, nil

	}

	if strings.HasPrefix(name, "@") {
		return "", fmt.Errorf("%w: unsupported derived component %q", ErrMalformedSignature, name)
	}
	values, ok, err := requestHeaderValues(request.Header, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: covered field %q is absent", ErrMalformedSignature, name)
	}
	for i := range values {
		values[i] = strings.Trim(values[i], " \t")
	}
	return strings.Join(values, ", "), nil
}

func requestHeaderValues(header http.Header, name string) ([]string, bool, error) {
	if header == nil {
		return nil, false, nil
	}
	var matchedName string
	var matchedValues []string
	found := false
	for candidate, values := range header {
		if !strings.EqualFold(candidate, name) {
			continue
		}
		if matchedName != "" && candidate != matchedName {
			return nil, false, fmt.Errorf(
				"%w: ambiguous case variants for header %q", ErrMalformedSignature, name)
		}
		matchedName = candidate
		if len(values) != 0 {
			found = true
			matchedValues = values
		}
	}
	if !found {
		return nil, false, nil
	}
	return append([]string(nil), matchedValues...), true, nil
}

func requestTargetURI(request *http.Request, override string) (string, error) {
	if override != "" {
		if strings.ContainsAny(override, "\r\n") {
			return "", fmt.Errorf("%w: target URI", ErrHeaderValue)
		}
		return override, nil
	}
	if request.URL == nil {
		return "", fmt.Errorf("%w: nil request URL", ErrMalformedSignature)
	}
	if request.URL.Opaque != "" {
		return "", fmt.Errorf("%w: opaque request target", ErrMalformedSignature)
	}

	target := *request.URL
	target.Fragment = ""
	if target.Scheme == "" {
		if request.TLS != nil {
			target.Scheme = "https"
		} else {
			target.Scheme = "http"
		}
	}
	if target.Host == "" {
		target.Host = request.Host
	}
	if target.Host == "" || target.Scheme == "" {
		return "", fmt.Errorf("%w: request has no absolute target URI", ErrMalformedSignature)
	}
	targetURI := target.String()
	if strings.ContainsAny(targetURI, "\r\n") {
		return "", fmt.Errorf("%w: target URI", ErrHeaderValue)
	}
	return targetURI, nil
}

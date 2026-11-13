package netsec

import (
	"bytes"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"
)

var (
	// OIDOCSPBasic identifies a basic OCSP response.
	OIDOCSPBasic = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}
	// OIDOCSPNonce identifies the id-pkix-ocsp-nonce response extension.
	OIDOCSPNonce = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 2}
)

var (
	// ErrInvalidResponse indicates malformed or unsupported OCSP DER.
	ErrInvalidResponse = errors.New("invalid OCSP response")
	// ErrUnsuccessfulResponse indicates an OCSP response status other than successful.
	ErrUnsuccessfulResponse = errors.New("OCSP response is not successful")
	// ErrNonceMissing indicates that a requested nonce was not echoed by the responder.
	ErrNonceMissing = errors.New("OCSP response is missing the requested nonce")
	// ErrNonceMismatch indicates that the response nonce differs from the requested nonce.
	ErrNonceMismatch = errors.New("OCSP response nonce does not match the requested nonce")
	// ErrResponseNotYetValid indicates that the response validity period starts in the future.
	ErrResponseNotYetValid = errors.New("OCSP response is not yet valid")
	// ErrResponseExpired indicates that the response validity period has ended.
	ErrResponseExpired = errors.New("OCSP response is expired")
)

// SingleResponse contains the validity interval for one certificate status.
type SingleResponse struct {
	ThisUpdate time.Time
	NextUpdate time.Time
}

// ResponseInfo contains the fields needed to make a stapling decision.
type ResponseInfo struct {
	Nonce        []byte
	NoncePresent bool
	ProducedAt   time.Time
	ThisUpdate   time.Time
	NextUpdate   time.Time
	Responses    []SingleResponse
}

type parsedOCSPResponse struct {
	info       ResponseInfo
	nonceStart int
	nonceEnd   int
}

// ParseOCSPResponse parses a DER-encoded basic OCSP response. It validates
// structural fields but does not verify the response signature.
func ParseOCSPResponse(responseDER []byte) (*ResponseInfo, error) {
	parsed, err := parseOCSPResponse(responseDER)
	if err != nil {
		return nil, err
	}
	info := parsed.info
	info.Nonce = bytes.Clone(parsed.info.Nonce)
	info.Responses = append([]SingleResponse(nil), parsed.info.Responses...)
	return &info, nil
}

func parseOCSPResponse(responseDER []byte) (*parsedOCSPResponse, error) {
	if len(responseDER) == 0 {
		return nil, fmt.Errorf("%w: empty response", ErrInvalidResponse)
	}
	outer, err := parseSingleDERElement(responseDER)
	if err != nil {
		return nil, err
	}
	outerFields, err := parseDERSequence(responseDER, outer, "OCSPResponse")
	if err != nil {
		return nil, err
	}
	if len(outerFields) < 1 || len(outerFields) > 2 {
		return nil, fmt.Errorf("%w: invalid OCSPResponse fields", ErrInvalidResponse)
	}

	status, err := parseDEREnumerated(responseDER, outerFields[0])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid OCSPResponseStatus", err)
	}
	if status != 0 {
		return nil, fmt.Errorf("%w: status %d", ErrUnsuccessfulResponse, status)
	}
	if len(outerFields) != 2 {
		return nil, fmt.Errorf("%w: successful response has no responseBytes", ErrInvalidResponse)
	}

	responseBytesWrapper := outerFields[1]
	if err := requireDERElement(responseDER, responseBytesWrapper, derClassContext, 0, true, "responseBytes"); err != nil {
		return nil, err
	}
	wrapperFields, err := derChildren(responseDER, responseBytesWrapper)
	if err != nil || len(wrapperFields) != 1 {
		return nil, fmt.Errorf("%w: invalid responseBytes wrapper", ErrInvalidResponse)
	}
	responseBytesFields, err := parseDERSequence(responseDER, wrapperFields[0], "ResponseBytes")
	if err != nil {
		return nil, err
	}
	if len(responseBytesFields) != 2 {
		return nil, fmt.Errorf("%w: invalid ResponseBytes fields", ErrInvalidResponse)
	}
	responseType, err := parseDEROID(responseDER, responseBytesFields[0])
	if err != nil {
		return nil, err
	}
	if !responseType.Equal(OIDOCSPBasic) {
		return nil, fmt.Errorf("%w: unsupported response type %s", ErrInvalidResponse, responseType)
	}
	basicField := responseBytesFields[1]
	if err := requireDERElement(responseDER, basicField, derClassUniversal, derTagOctetString, false, "basic response"); err != nil {
		return nil, err
	}

	return parseBasicOCSPResponse(basicField.content(responseDER), basicField.contentStart)
}

func parseBasicOCSPResponse(basicDER []byte, baseOffset int) (*parsedOCSPResponse, error) {
	basic, err := parseSingleDERElement(basicDER)
	if err != nil {
		return nil, err
	}
	basicFields, err := parseDERSequence(basicDER, basic, "BasicOCSPResponse")
	if err != nil {
		return nil, err
	}
	if len(basicFields) < 3 || len(basicFields) > 4 {
		return nil, fmt.Errorf("%w: invalid BasicOCSPResponse fields", ErrInvalidResponse)
	}
	if len(basicFields) == 4 && !basicFields[3].is(derClassContext, 0, true) {
		return nil, fmt.Errorf("%w: invalid certificates field", ErrInvalidResponse)
	}
	tbsField := basicFields[0]
	tbsFields, err := parseDERSequence(basicDER, tbsField, "ResponseData")
	if err != nil {
		return nil, err
	}
	if err := parseAlgorithmIdentifier(basicDER, basicFields[1], "signatureAlgorithm"); err != nil {
		return nil, err
	}
	if err := requireDERElement(basicDER, basicFields[2], derClassUniversal, derTagBitString, false, "signature"); err != nil {
		return nil, err
	}
	if len(basicFields[2].content(basicDER)) == 0 {
		return nil, fmt.Errorf("%w: empty signature", ErrInvalidResponse)
	}

	index := 0
	if len(tbsFields) > 0 && tbsFields[0].is(derClassContext, 0, true) {
		versionFields, err := derChildren(basicDER, tbsFields[0])
		if err != nil || len(versionFields) != 1 {
			return nil, fmt.Errorf("%w: invalid ResponseData version", ErrInvalidResponse)
		}
		version, err := parseDERInteger(basicDER, versionFields[0])
		if err != nil || version != 0 {
			return nil, fmt.Errorf("%w: unsupported ResponseData version", ErrInvalidResponse)
		}
		index++
	}
	if len(tbsFields) < index+3 {
		return nil, fmt.Errorf("%w: incomplete ResponseData", ErrInvalidResponse)
	}

	responderID := tbsFields[index]
	if !responderID.is(derClassContext, 1, true) && !responderID.is(derClassContext, 2, true) {
		return nil, fmt.Errorf("%w: invalid responderID", ErrInvalidResponse)
	}
	index++

	producedAt, err := parseDERTime(basicDER, tbsFields[index])
	if err != nil {
		return nil, err
	}
	index++

	responsesField := tbsFields[index]
	responseFields, err := parseDERSequence(basicDER, responsesField, "responses")
	if err != nil {
		return nil, err
	}
	if len(responseFields) == 0 {
		return nil, fmt.Errorf("%w: response has no SingleResponse", ErrInvalidResponse)
	}
	index++

	var extensions *derElement
	if index < len(tbsFields) {
		if !tbsFields[index].is(derClassContext, 1, true) || index+1 != len(tbsFields) {
			return nil, fmt.Errorf("%w: invalid responseExtensions", ErrInvalidResponse)
		}
		extensions = &tbsFields[index]
	}

	info := ResponseInfo{ProducedAt: producedAt}
	for _, responseField := range responseFields {
		single, err := parseSingleResponse(basicDER, responseField)
		if err != nil {
			return nil, err
		}
		info.Responses = append(info.Responses, single)
	}
	info.ThisUpdate = info.Responses[0].ThisUpdate
	info.NextUpdate = info.Responses[0].NextUpdate

	parsed := &parsedOCSPResponse{info: info}
	if extensions != nil {
		nonceStart, nonceEnd, present, err := parseOCSPResponseNonce(basicDER, *extensions)
		if err != nil {
			return nil, err
		}
		if present {
			parsed.info.NoncePresent = true
			parsed.info.Nonce = bytes.Clone(basicDER[nonceStart:nonceEnd])
			parsed.nonceStart = baseOffset + nonceStart
			parsed.nonceEnd = baseOffset + nonceEnd
		}
	}
	return parsed, nil
}

func parseSingleResponse(data []byte, elem derElement) (SingleResponse, error) {
	fields, err := parseDERSequence(data, elem, "SingleResponse")
	if err != nil {
		return SingleResponse{}, err
	}
	if len(fields) < 3 {
		return SingleResponse{}, fmt.Errorf("%w: incomplete SingleResponse", ErrInvalidResponse)
	}
	if err := parseCertID(data, fields[0]); err != nil {
		return SingleResponse{}, err
	}
	if fields[1].class != derClassContext || fields[1].tag < 0 || fields[1].tag > 2 {
		return SingleResponse{}, fmt.Errorf("%w: invalid certStatus", ErrInvalidResponse)
	}
	thisUpdate, err := parseDERTime(data, fields[2])
	if err != nil {
		return SingleResponse{}, err
	}

	response := SingleResponse{ThisUpdate: thisUpdate}
	index := 3
	if index < len(fields) && fields[index].is(derClassContext, 0, true) {
		nextUpdateFields, err := derChildren(data, fields[index])
		if err != nil || len(nextUpdateFields) != 1 {
			return SingleResponse{}, fmt.Errorf("%w: invalid nextUpdate", ErrInvalidResponse)
		}
		nextUpdate, err := parseDERTime(data, nextUpdateFields[0])
		if err != nil {
			return SingleResponse{}, err
		}
		if !nextUpdate.After(thisUpdate) {
			return SingleResponse{}, fmt.Errorf("%w: nextUpdate does not follow thisUpdate", ErrInvalidResponse)
		}
		response.NextUpdate = nextUpdate
		index++
	}
	if index < len(fields) {
		if !fields[index].is(derClassContext, 1, true) || index+1 != len(fields) {
			return SingleResponse{}, fmt.Errorf("%w: invalid singleExtensions", ErrInvalidResponse)
		}
	}
	return response, nil
}

func parseAlgorithmIdentifier(data []byte, elem derElement, name string) error {
	fields, err := parseDERSequence(data, elem, name)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return fmt.Errorf("%w: empty %s", ErrInvalidResponse, name)
	}
	_, err = parseDEROID(data, fields[0])
	return err
}

func parseCertID(data []byte, elem derElement) error {
	fields, err := parseDERSequence(data, elem, "CertID")
	if err != nil {
		return err
	}
	if len(fields) != 4 {
		return fmt.Errorf("%w: invalid CertID fields", ErrInvalidResponse)
	}
	if err := parseAlgorithmIdentifier(data, fields[0], "CertID hashAlgorithm"); err != nil {
		return err
	}
	if err := requireDERElement(data, fields[1], derClassUniversal, derTagOctetString, false, "issuerNameHash"); err != nil {
		return err
	}
	if err := requireDERElement(data, fields[2], derClassUniversal, derTagOctetString, false, "issuerKeyHash"); err != nil {
		return err
	}
	if _, err := parseDERInteger(data, fields[3]); err != nil {
		return err
	}
	return nil
}

func parseOCSPResponseNonce(data []byte, wrapper derElement) (int, int, bool, error) {
	fields, err := derChildren(data, wrapper)
	if err != nil || len(fields) != 1 {
		return 0, 0, false, fmt.Errorf("%w: invalid responseExtensions wrapper", ErrInvalidResponse)
	}
	extensions, err := parseDERSequence(data, fields[0], "Extensions")
	if err != nil {
		return 0, 0, false, err
	}

	var (
		found      bool
		nonceStart int
		nonceEnd   int
	)
	for _, extension := range extensions {
		extensionFields, err := parseDERSequence(data, extension, "Extension")
		if err != nil {
			return 0, 0, false, err
		}
		if len(extensionFields) < 2 || len(extensionFields) > 3 {
			return 0, 0, false, fmt.Errorf("%w: invalid Extension fields", ErrInvalidResponse)
		}
		oid, err := parseDEROID(data, extensionFields[0])
		if err != nil {
			return 0, 0, false, err
		}
		if !oid.Equal(OIDOCSPNonce) {
			continue
		}
		if found {
			return 0, 0, false, fmt.Errorf("%w: duplicate OCSP nonce extension", ErrInvalidResponse)
		}

		valueIndex := 1
		if extensionFields[1].is(derClassUniversal, derTagBoolean, false) {
			if _, err := parseDERBoolean(data, extensionFields[1]); err != nil {
				return 0, 0, false, err
			}
			valueIndex = 2
		}
		if valueIndex+1 != len(extensionFields) {
			return 0, 0, false, fmt.Errorf("%w: nonce extension has no value", ErrInvalidResponse)
		}
		_, start, end, err := parseNonceExtensionValue(data, extensionFields[valueIndex])
		if err != nil {
			return 0, 0, false, err
		}
		found = true
		nonceStart, nonceEnd = start, end
	}
	return nonceStart, nonceEnd, found, nil
}

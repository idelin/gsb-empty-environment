package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeAAAA  uint16 = 28
	ClassIN   uint16 = 1
)

// Question is a DNS question.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// NewQuestion returns an IN-class question. A trailing dot is accepted.
func NewQuestion(name string, qtype uint16) Question {
	return Question{Name: name, Type: qtype, Class: ClassIN}
}

// PackQuery builds a recursive DNS query with a random transaction ID.
func PackQuery(question Question) ([]byte, error) {
	var id [2]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("netsec: generate DNS transaction ID: %w", err)
	}
	return PackQueryWithID(binary.BigEndian.Uint16(id[:]), question)
}

// PackQueryWithID builds a recursive DNS query.
func PackQueryWithID(id uint16, question Question) ([]byte, error) {
	name, err := packName(question.Name)
	if err != nil {
		return nil, err
	}
	class := question.Class
	if class == 0 {
		class = ClassIN
	}
	msg := make([]byte, 12+len(name)+4)
	binary.BigEndian.PutUint16(msg[0:2], id)
	binary.BigEndian.PutUint16(msg[2:4], 0x0100)
	binary.BigEndian.PutUint16(msg[4:6], 1)
	copy(msg[12:], name)
	offset := 12 + len(name)
	binary.BigEndian.PutUint16(msg[offset:offset+2], question.Type)
	binary.BigEndian.PutUint16(msg[offset+2:offset+4], class)
	return msg, nil
}

func packName(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return []byte{0}, nil
	}
	labels := strings.Split(name, ".")
	out := make([]byte, 0, len(name)+2)
	total := 1
	for _, label := range labels {
		if label == "" {
			return nil, fmt.Errorf("%w: empty label in %q", ErrMalformedMessage, name)
		}
		if len(label) > 63 {
			return nil, fmt.Errorf("%w: label longer than 63 bytes in %q", ErrMalformedMessage, name)
		}
		total += len(label) + 1
		if total > 255 {
			return nil, fmt.Errorf("%w: name longer than 255 bytes in %q", ErrMalformedMessage, name)
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	out = append(out, 0)
	return out, nil
}

type parsedMessage struct {
	id        uint16
	flags     uint16
	questions []Question
}

func parseMessage(msg []byte) (parsedMessage, error) {
	if len(msg) < 12 {
		return parsedMessage{}, fmt.Errorf("%w: shorter than DNS header", ErrMalformedMessage)
	}
	parsed := parsedMessage{
		id:    binary.BigEndian.Uint16(msg[0:2]),
		flags: binary.BigEndian.Uint16(msg[2:4]),
	}
	qdCount := binary.BigEndian.Uint16(msg[4:6])
	offset := 12
	for i := uint16(0); i < qdCount; i++ {
		name, next, err := parseName(msg, offset)
		if err != nil {
			return parsedMessage{}, err
		}
		if next+4 > len(msg) {
			return parsedMessage{}, fmt.Errorf("%w: truncated question", ErrMalformedMessage)
		}
		question := Question{
			Name:  name,
			Type:  binary.BigEndian.Uint16(msg[next : next+2]),
			Class: binary.BigEndian.Uint16(msg[next+2 : next+4]),
		}
		parsed.questions = append(parsed.questions, question)
		offset = next + 4
	}
	return parsed, nil
}

func parseName(msg []byte, offset int) (string, int, error) {
	labels := make([]string, 0, 8)
	next := -1
	visited := make(map[int]struct{})
	position := offset
	length := 0

	for {
		if position >= len(msg) {
			return "", 0, fmt.Errorf("%w: name exceeds message length", ErrMalformedMessage)
		}
		if _, seen := visited[position]; seen {
			return "", 0, fmt.Errorf("%w: compression pointer loop", ErrMalformedMessage)
		}
		visited[position] = struct{}{}

		raw := msg[position]
		switch {
		case raw&0xC0 == 0xC0:
			if position+2 > len(msg) {
				return "", 0, fmt.Errorf("%w: truncated compression pointer", ErrMalformedMessage)
			}
			pointer := int(binary.BigEndian.Uint16(msg[position:position+2]) & 0x3FFF)
			if pointer >= len(msg) {
				return "", 0, fmt.Errorf("%w: compression pointer outside message", ErrMalformedMessage)
			}
			if next < 0 {
				next = position + 2
			}
			position = pointer
		case raw&0xC0 != 0:
			return "", 0, fmt.Errorf("%w: unsupported DNS label type", ErrMalformedMessage)
		case raw == 0:
			if next < 0 {
				next = position + 1
			}
			if len(labels) == 0 {
				return ".", next, nil
			}
			return strings.ToLower(strings.Join(labels, ".")), next, nil
		default:
			labelLen := int(raw)
			if position+1+labelLen > len(msg) {
				return "", 0, fmt.Errorf("%w: truncated DNS label", ErrMalformedMessage)
			}
			length += labelLen + 1
			if length > 255 {
				return "", 0, fmt.Errorf("%w: DNS name exceeds 255 bytes", ErrMalformedMessage)
			}
			labels = append(labels, string(msg[position+1:position+1+labelLen]))
			position += 1 + labelLen
		}
	}
}

func validateResponse(query, response []byte) error {
	parsedQuery, err := parseMessage(query)
	if err != nil {
		return fmt.Errorf("netsec: invalid query: %w", err)
	}
	parsedResponse, err := parseMessage(response)
	if err != nil {
		return fmt.Errorf("netsec: invalid response: %w", err)
	}
	if parsedResponse.flags&0x8000 == 0 {
		return ErrNotResponse
	}
	if parsedResponse.id != parsedQuery.id {
		return fmt.Errorf("%w: got %d, want %d", ErrResponseIDMismatch, parsedResponse.id, parsedQuery.id)
	}
	if len(parsedResponse.questions) != len(parsedQuery.questions) {
		return fmt.Errorf("%w: got %d question(s), want %d",
			ErrQuestionMismatch, len(parsedResponse.questions), len(parsedQuery.questions))
	}
	for i := range parsedQuery.questions {
		want := parsedQuery.questions[i]
		got := parsedResponse.questions[i]
		if !strings.EqualFold(got.Name, want.Name) || got.Type != want.Type || got.Class != want.Class {
			return fmt.Errorf("%w: question %d got %s/%d/%d, want %s/%d/%d",
				ErrQuestionMismatch, i, got.Name, got.Type, got.Class,
				want.Name, want.Type, want.Class)
		}
	}
	return nil
}

// Package topicmap maintains the MQTT-SN topic id to MQTT topic name
// mappings for a UDP gateway.
//
// MQTT-SN datagrams carry a 16-bit topic id instead of the full topic
// name. The id space has three ranges:
//
//   - 0x0000: the two id bytes themselves hold a short topic name.
//   - 0x0001..0xFF00: normal ids allocated through REGISTER, isolated
//     per client.
//   - 0xFF01..0xFFFF: predefined ids resolved through the per-client
//     whitelist supplied by the datagram layer.
//
// A Table is safe for concurrent use.
package topicmap

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Bounds of the MQTT-SN topic id ranges.
const (
	// ShortTopicID introduces an inline short topic name.
	ShortTopicID uint16 = 0x0000
	// MinNormalID and MaxNormalID bracket the normal topic id range.
	MinNormalID uint16 = 0x0001
	MaxNormalID uint16 = 0xFF00
	// MinPredefinedID and MaxPredefinedID bracket predefined topic ids.
	MinPredefinedID uint16 = 0xFF01
	MaxPredefinedID uint16 = 0xFFFF
)

var (
	// ErrEmptyTopic is returned when a topic name is empty.
	ErrEmptyTopic = errors.New("topicmap: topic name must not be empty")
	// ErrWildcardTopic is returned when a topic name contains '+' or '#'.
	ErrWildcardTopic = errors.New("topicmap: topic names must not contain '+' or '#'")
	// ErrReservedTopicID is returned when 0x0000 is used as a normal id.
	ErrReservedTopicID = errors.New("topicmap: topic id 0x0000 is reserved for short topic names")
	// ErrUnknownTopicID is returned when a client has no mapping for a
	// normal topic id.
	ErrUnknownTopicID = errors.New("topicmap: unknown topic id for client")
	// ErrPredefinedNotAllowed is returned when a client references a
	// predefined topic id that is not in its whitelist.
	ErrPredefinedNotAllowed = errors.New("topicmap: predefined topic id is not allowed for client")
	// ErrTableFull is returned when no normal topic id can be allocated.
	ErrTableFull = errors.New("topicmap: normal topic id space exhausted")
	// ErrShortTopicLength is returned when a short topic name is longer
	// than two bytes.
	ErrShortTopicLength = errors.New("topicmap: short topic name must be one or two bytes")
)

// PredefinedWhitelist maps a client key to the predefined topic ids that
// client is permitted to reference and their topic names. The client key
// is any stable per-client identifier, for example the remote UDP
// address string.
type PredefinedWhitelist map[string]map[uint16]string

type clientTable struct {
	ids   map[uint16]string
	names map[string]uint16
	next  uint16
}

func newClientTable() *clientTable {
	return &clientTable{
		ids:   make(map[uint16]string),
		names: make(map[string]uint16),
		next:  MinNormalID,
	}
}

// Table stores normal topic id mappings per client and resolves
// predefined ids through the configured whitelist. It is safe for
// concurrent use.
type Table struct {
	mu         sync.RWMutex
	clients    map[string]*clientTable
	predefined PredefinedWhitelist
}

// New creates a Table. The whitelist is owned by the Table after the
// call and must not be modified afterwards. Wildcard topic names and ids
// outside 0xFF01..0xFFFF are rejected.
func New(predefined PredefinedWhitelist) (*Table, error) {
	for client, entries := range predefined {
		for id, topic := range entries {
			if id < MinPredefinedID || id > MaxPredefinedID {
				return nil, fmt.Errorf("topicmap: predefined id 0x%04X for client %q is outside 0xFF01..0xFFFF", id, client)
			}
			if err := validateTopic(topic); err != nil {
				return nil, fmt.Errorf("topicmap: predefined topic for client %q id 0x%04X: %w", client, id, err)
			}
		}
	}
	return &Table{
		clients:    make(map[string]*clientTable),
		predefined: predefined,
	}, nil
}

func validateTopic(topic string) error {
	if topic == "" {
		return ErrEmptyTopic
	}
	if strings.ContainsAny(topic, "+#") {
		return ErrWildcardTopic
	}
	return nil
}

// Register stores a REGISTER request from client and returns the topic
// id that the datagram layer echoes in the REGACK. Topic names
// containing '+' or '#' are rejected so wildcard topics can never enter
// the table. Re-registering the same topic for the same client returns
// the existing id without allocating a new one.
func (t *Table) Register(client, topic string) (uint16, error) {
	if err := validateTopic(topic); err != nil {
		return 0, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	table := t.clients[client]
	if table == nil {
		table = newClientTable()
		t.clients[client] = table
	}
	if id, ok := table.names[topic]; ok {
		return id, nil
	}
	if table.next > MaxNormalID {
		return 0, ErrTableFull
	}
	id := table.next
	table.next++
	table.ids[id] = topic
	table.names[topic] = id
	return id, nil
}

// Resolve maps a topic id from a client PUBLISH or SUBSCRIBE datagram
// to its topic name. Predefined ids are only accepted when they appear
// in the requesting client's whitelist; normal ids are only visible to
// the client that registered them. The id 0x0000 is rejected here
// because short topic names must be decoded with ShortTopicName.
func (t *Table) Resolve(client string, id uint16) (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if id == ShortTopicID {
		return "", ErrReservedTopicID
	}
	if id >= MinPredefinedID {
		entries, ok := t.predefined[client]
		if !ok {
			return "", ErrPredefinedNotAllowed
		}
		topic, ok := entries[id]
		if !ok {
			return "", ErrPredefinedNotAllowed
		}
		return topic, nil
	}

	table := t.clients[client]
	if table == nil {
		return "", ErrUnknownTopicID
	}
	topic, ok := table.ids[id]
	if !ok {
		return "", ErrUnknownTopicID
	}
	return topic, nil
}

// LookupID returns the normal topic id previously registered by client
// for topic.
func (t *Table) LookupID(client, topic string) (uint16, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	table := t.clients[client]
	if table == nil {
		return 0, false
	}
	id, ok := table.names[topic]
	return id, ok
}

// CleanSession discards every normal topic id registered by client, for
// example when a CONNECT with clean session flag arrives. Ids allocated
// before the call can no longer be resolved. The predefined whitelist
// is gateway configuration rather than session state and is retained.
func (t *Table) CleanSession(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.clients, client)
}

// ShortTopicName decodes a short topic name carried in the two bytes of
// a topic id. A trailing zero byte means the name is a single byte. The
// returned string is forwarded to the broker unchanged.
func ShortTopicName(id uint16) string {
	if id == ShortTopicID {
		return ""
	}
	hi := byte(id >> 8)
	lo := byte(id)
	if lo == 0 {
		return string([]byte{hi})
	}
	return string([]byte{hi, lo})
}

// EncodeShortTopic encodes a one or two byte short topic name into a topic
// id, ready to be placed on the wire as two big-endian bytes.
func EncodeShortTopic(topic string) (uint16, error) {
	switch len(topic) {
	case 1:
		return uint16(topic[0]) << 8, nil
	case 2:
		return uint16(topic[0])<<8 | uint16(topic[1]), nil
	default:
		return 0, ErrShortTopicLength
	}
}

package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrTopicDenied          = errors.New("netsec: subject is not authorized for the topic")
	ErrCrossTenant          = errors.New("netsec: topic belongs to a different tenant")
	ErrSubscriptionNotFound = errors.New("netsec: subscription not found")
	ErrInvalidConfirmToken  = errors.New("netsec: invalid confirmation token")
	ErrSubscriptionExpired  = errors.New("netsec: subscription expired")
)

// Topic identifies a broadcast target scoped to a tenant and a resource.
type Topic struct {
	TenantID string
	Resource string
}

// Channel is the delivery channel a subscription is bound to.
type Channel string

const (
	ChannelEmail   Channel = "email"
	ChannelSMS     Channel = "sms"
	ChannelPush    Channel = "push"
	ChannelWebhook Channel = "webhook"
)

// Subscription binds a subject, topic, channel and confirmation token.
// The confirmation token itself is never stored; only its SHA-256 hash is.
type Subscription struct {
	ID        string
	TenantID  string
	SubjectID string
	Topic     Topic
	Channel   Channel
	Confirmed bool
	CreatedAt time.Time
	ExpiresAt time.Time

	tokenHash [32]byte
}

// Delivery is a single message dispatched to one authorized subscription.
// MessageID is a one-time identifier, unique across all deliveries.
type Delivery struct {
	MessageID      string
	SubscriptionID string
	TenantID       string
	SubjectID      string
	Topic          Topic
	Channel        Channel
	Payload        []byte
	IssuedAt       time.Time
}

// Authorizer decides whether a subject of a tenant may subscribe to a topic.
type Authorizer interface {
	Authorize(tenantID, subjectID string, topic Topic) error
}

// AuthorizerFunc adapts a function to the Authorizer interface.
type AuthorizerFunc func(tenantID, subjectID string, topic Topic) error

// Authorize implements Authorizer.
func (f AuthorizerFunc) Authorize(tenantID, subjectID string, topic Topic) error {
	return f(tenantID, subjectID, topic)
}

// Manager issues, confirms and fans out subscriptions.
type Manager struct {
	mu     sync.Mutex
	auth   Authorizer
	now    func() time.Time
	subs   map[string]*Subscription
	issued map[string]struct{}
}

// Option configures a Manager.
type Option func(*Manager)

// WithClock overrides the time source, mainly for testing expiry.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// NewManager creates a Manager using the given topic authorizer.
func NewManager(auth Authorizer, opts ...Option) *Manager {
	m := &Manager{
		auth:   auth,
		now:    time.Now,
		subs:   make(map[string]*Subscription),
		issued: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Subscribe creates an unconfirmed subscription after tenant and topic
// authorization. It returns the subscription and the plaintext confirmation
// token, which is shown to the caller exactly once.
func (m *Manager) Subscribe(tenantID, subjectID string, topic Topic, ch Channel, ttl time.Duration) (*Subscription, string, error) {
	if tenantID == "" || subjectID == "" || topic.Resource == "" {
		return nil, "", fmt.Errorf("netsec: tenant, subject and topic resource are required")
	}
	if topic.TenantID != tenantID {
		return nil, "", ErrCrossTenant
	}
	if err := m.auth.Authorize(tenantID, subjectID, topic); err != nil {
		return nil, "", err
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	token, hash, err := newToken()
	if err != nil {
		return nil, "", err
	}
	id, err := randomID()
	if err != nil {
		return nil, "", err
	}

	now := m.now()
	sub := &Subscription{
		ID:        id,
		TenantID:  tenantID,
		SubjectID: subjectID,
		Topic:     topic,
		Channel:   ch,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		tokenHash: hash,
	}

	m.mu.Lock()
	m.subs[id] = sub
	m.mu.Unlock()
	return sub, token, nil
}

// Confirm activates a subscription with its confirmation token. The token is
// compared in constant time against the stored hash.
func (m *Manager) Confirm(subscriptionID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subs[subscriptionID]
	if !ok {
		return ErrSubscriptionNotFound
	}
	if !m.now().Before(sub.ExpiresAt) {
		return ErrSubscriptionExpired
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(sum[:], sub.tokenHash[:]) != 1 {
		return ErrInvalidConfirmToken
	}
	sub.Confirmed = true
	return nil
}

// Publish fans a payload out to every subscription on the topic that is
// confirmed, unexpired and owned by the topic's tenant. Each delivery carries
// a freshly generated one-time message identifier.
func (m *Manager) Publish(tenantID string, topic Topic, payload []byte) ([]Delivery, error) {
	if topic.TenantID != tenantID {
		return nil, ErrCrossTenant
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	var deliveries []Delivery
	for _, sub := range m.subs {
		if sub.TenantID != tenantID || sub.Topic != topic {
			continue
		}
		if !sub.Confirmed || !now.Before(sub.ExpiresAt) {
			continue
		}
		msgID, err := m.newMessageIDLocked()
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, Delivery{
			MessageID:      msgID,
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			SubjectID:      sub.SubjectID,
			Topic:          sub.Topic,
			Channel:        sub.Channel,
			Payload:        payload,
			IssuedAt:       now,
		})
	}
	return deliveries, nil
}

// newMessageIDLocked generates a message identifier guaranteed to be used
// only once by this manager. m.mu must be held.
func (m *Manager) newMessageIDLocked() (string, error) {
	for {
		id, err := randomID()
		if err != nil {
			return "", err
		}
		if _, seen := m.issued[id]; seen {
			continue
		}
		m.issued[id] = struct{}{}
		return id, nil
	}
}

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, fmt.Errorf("netsec: generate token: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: generate id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

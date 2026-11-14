package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestManager() (*Manager, *ACL, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	acl := NewACL()
	return NewManager(acl, WithClock(clock.Now)), acl, clock
}

func mustSubscribe(t *testing.T, m *Manager, tenant, subject string, topic Topic, ttl time.Duration) (*Subscription, string) {
	t.Helper()
	sub, token, err := m.Subscribe(tenant, subject, topic, ChannelWebhook, ttl)
	if err != nil {
		t.Fatalf("Subscribe(%s, %s, %+v): %v", tenant, subject, topic, err)
	}
	return sub, token
}

func TestTopicPrivilegeEscalationDenied(t *testing.T) {
	m, acl, _ := newTestManager()
	topic := Topic{TenantID: "tenant-a", Resource: "billing"}

	acl.Grant("tenant-a", "alice", "billing")
	if _, _, err := m.Subscribe("tenant-a", "alice", topic, ChannelEmail, time.Hour); err != nil {
		t.Fatalf("authorized subject rejected: %v", err)
	}

	// Mallory has no grant for the billing topic.
	if _, _, err := m.Subscribe("tenant-a", "mallory", topic, ChannelWebhook, time.Hour); !errors.Is(err, ErrTopicDenied) {
		t.Fatalf("expected ErrTopicDenied, got %v", err)
	}

	// A grant under another tenant must not leak into tenant-a.
	acl.Grant("tenant-b", "mallory", "billing")
	if _, _, err := m.Subscribe("tenant-a", "mallory", topic, ChannelWebhook, time.Hour); !errors.Is(err, ErrTopicDenied) {
		t.Fatalf("cross-tenant grant leaked: %v", err)
	}
}

func TestConfirmationForgeryRejected(t *testing.T) {
	m, acl, _ := newTestManager()
	topic := Topic{TenantID: "tenant-a", Resource: "orders"}
	acl.Grant("tenant-a", "alice", "orders")

	sub, _ := mustSubscribe(t, m, "tenant-a", "alice", topic, time.Hour)

	if err := m.Confirm(sub.ID, "forged-token"); !errors.Is(err, ErrInvalidConfirmToken) {
		t.Fatalf("expected ErrInvalidConfirmToken, got %v", err)
	}
	if err := m.Confirm("no-such-sub", "whatever"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("expected ErrSubscriptionNotFound, got %v", err)
	}

	// Failed forgery must leave the subscription unconfirmed: no delivery.
	deliveries, err := m.Publish("tenant-a", topic, []byte("secret"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("unconfirmed subscription received %d deliveries", len(deliveries))
	}
}

func TestCrossTenantDeliveryIsolation(t *testing.T) {
	m, acl, _ := newTestManager()
	topicA := Topic{TenantID: "tenant-a", Resource: "orders"}
	topicB := Topic{TenantID: "tenant-b", Resource: "orders"}
	acl.Grant("tenant-a", "alice", "orders")
	acl.Grant("tenant-b", "mallory", "orders")

	// A subject of tenant-b cannot subscribe to tenant-a's topic at all.
	if _, _, err := m.Subscribe("tenant-b", "mallory", topicA, ChannelWebhook, time.Hour); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("expected ErrCrossTenant, got %v", err)
	}

	subA, tokenA := mustSubscribe(t, m, "tenant-a", "alice", topicA, time.Hour)
	subB, tokenB := mustSubscribe(t, m, "tenant-b", "mallory", topicB, time.Hour)
	if err := m.Confirm(subA.ID, tokenA); err != nil {
		t.Fatalf("Confirm alice: %v", err)
	}
	if err := m.Confirm(subB.ID, tokenB); err != nil {
		t.Fatalf("Confirm mallory: %v", err)
	}

	deliveries, err := m.Publish("tenant-a", topicA, []byte("tenant-a secret"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].SubjectID != "alice" || deliveries[0].TenantID != "tenant-a" {
		t.Fatalf("tenant-a message leaked or lost: %+v", deliveries)
	}

	// Publishing with a mismatched tenant/topic pair is rejected outright.
	if _, err := m.Publish("tenant-b", topicA, []byte("x")); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("expected ErrCrossTenant, got %v", err)
	}
}

func TestLegitimateSubscriptionReceivesDelivery(t *testing.T) {
	m, acl, _ := newTestManager()
	topic := Topic{TenantID: "tenant-a", Resource: "orders"}
	acl.Grant("tenant-a", "alice", "orders")

	sub, token := mustSubscribe(t, m, "tenant-a", "alice", topic, time.Hour)
	if sub.Confirmed {
		t.Fatal("subscription must start unconfirmed")
	}
	if err := m.Confirm(sub.ID, token); err != nil {
		t.Fatalf("Confirm with genuine token: %v", err)
	}

	seen := make(map[string]bool)
	for i := 0; i < 3; i++ {
		deliveries, err := m.Publish("tenant-a", topic, []byte("hello"))
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if len(deliveries) != 1 {
			t.Fatalf("expected 1 delivery, got %d", len(deliveries))
		}
		d := deliveries[0]
		if d.SubscriptionID != sub.ID || d.Channel != ChannelWebhook || string(d.Payload) != "hello" {
			t.Fatalf("unexpected delivery: %+v", d)
		}
		if d.MessageID == "" || seen[d.MessageID] {
			t.Fatalf("message id %q is empty or reused", d.MessageID)
		}
		seen[d.MessageID] = true
	}
}

func TestExpiredSubscriptionNotDelivered(t *testing.T) {
	m, acl, clock := newTestManager()
	topic := Topic{TenantID: "tenant-a", Resource: "orders"}
	acl.Grant("tenant-a", "alice", "orders")

	sub, token := mustSubscribe(t, m, "tenant-a", "alice", topic, 30*time.Minute)
	if err := m.Confirm(sub.ID, token); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	clock.Advance(31 * time.Minute)

	deliveries, err := m.Publish("tenant-a", topic, []byte("late news"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("expired subscription received %d deliveries", len(deliveries))
	}

	// Confirmation after expiry is rejected as well.
	late, lateToken := mustSubscribe(t, m, "tenant-a", "alice", topic, time.Minute)
	clock.Advance(2 * time.Minute)
	if err := m.Confirm(late.ID, lateToken); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatalf("expected ErrSubscriptionExpired, got %v", err)
	}
}

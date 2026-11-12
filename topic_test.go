package netsec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateTopic(t *testing.T) {
	valid := []string{
		"a",
		"abc-123_XYZ",
		strings.Repeat("A", MaxTopicLen),
		"0",
		"_-",
	}
	for _, topic := range valid {
		if err := ValidateTopic(topic); err != nil {
			t.Fatalf("topic %q should be valid: %v", topic, err)
		}
	}

	invalid := []string{
		"",
		strings.Repeat("A", MaxTopicLen+1), // 超长 Topic
		"topic/123",                        // 非 URL 安全 Base64 字符
		"topic+123",                        // 标准 base64 的 '+' 不允许
		"topic=123",                        // 填充 '=' 不允许
		"topic.123",
		"中文topic",
	}
	for _, topic := range invalid {
		if err := ValidateTopic(topic); !errors.Is(err, ErrInvalidTopic) {
			t.Fatalf("topic %q: error = %v, want ErrInvalidTopic", topic, err)
		}
	}
}

func TestTopicReplaceOnlyWithinSameSubscription(t *testing.T) {
	registry, _ := newTestRegistry(t)
	store := NewTopicStore()
	ctx := context.Background()

	first, err := registry.Create(ctx, CreateSubscriptionInput{
		TenantID:         "tenant-a",
		RawEndpoint:      "https://push.example.com/push/aaa",
		VAPIDFingerprint: fingerprintForTest(t),
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := registry.Create(ctx, CreateSubscriptionInput{
		TenantID:         "tenant-b",
		RawEndpoint:      "https://push.example.com/push/bbb",
		VAPIDFingerprint: fingerprintForTest(t),
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	const sharedTopic = "news-2026"

	// 租户 A 在自己的订阅内首次写入。
	previous, err := store.Replace("tenant-a", first.ID, sharedTopic, []byte("v1"), "msg-1", registry)
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if previous != nil {
		t.Fatal("first write should not replace anything")
	}

	// 租户 B 使用完全相同的 Topic 名，必须是独立条目，不能覆盖 A 的待投递通知。
	previous, err = store.Replace("tenant-b", second.ID, sharedTopic, []byte("other"), "msg-2", registry)
	if err != nil {
		t.Fatalf("cross-tenant replace: %v", err)
	}
	if previous != nil {
		t.Fatal("cross-tenant same-topic write must not replace tenant-a entry")
	}

	entryA, err := store.Get("tenant-a", first.ID, sharedTopic, registry)
	if err != nil {
		t.Fatalf("get tenant-a entry: %v", err)
	}
	if string(entryA.Payload) != "v1" || entryA.MessageID != "msg-1" {
		t.Fatalf("tenant-a entry altered by cross-tenant write: %+v", entryA)
	}
	entryB, err := store.Get("tenant-b", second.ID, sharedTopic, registry)
	if err != nil {
		t.Fatalf("get tenant-b entry: %v", err)
	}
	if string(entryB.Payload) != "other" {
		t.Fatalf("tenant-b entry = %q", entryB.Payload)
	}

	// 同订阅同 Topic 再次写入，旧待投递通知被替换并返回。
	previous, err = store.Replace("tenant-a", first.ID, sharedTopic, []byte("v2"), "msg-3", registry)
	if err != nil {
		t.Fatalf("same-subscription replace: %v", err)
	}
	if previous == nil || previous.MessageID != "msg-1" || string(previous.Payload) != "v1" {
		t.Fatalf("replaced entry = %+v, want msg-1/v1", previous)
	}
	entryA, _ = store.Get("tenant-a", first.ID, sharedTopic, registry)
	if string(entryA.Payload) != "v2" || entryA.MessageID != "msg-3" {
		t.Fatalf("entry not replaced: %+v", entryA)
	}

	// 租户 B 的条目不受影响。
	entryB, _ = store.Get("tenant-b", second.ID, sharedTopic, registry)
	if string(entryB.Payload) != "other" {
		t.Fatalf("tenant-b entry changed unexpectedly: %+v", entryB)
	}
}

func TestTopicStoreRejectsCrossTenantAccessAndBadTopic(t *testing.T) {
	registry, _ := newTestRegistry(t)
	store := NewTopicStore()
	sub, err := registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:         "tenant-a",
		RawEndpoint:      "https://push.example.com/push/aaa",
		VAPIDFingerprint: fingerprintForTest(t),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 超长 Topic 在存储入口即被拒绝。
	if _, err := store.Replace("tenant-a", sub.ID, strings.Repeat("z", 33), nil, "m", registry); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("overlong topic: error = %v", err)
	}

	// 其他租户无法操作该订阅的 Topic。
	if _, err := store.Replace("tenant-b", sub.ID, "topic", nil, "m", registry); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("cross-tenant replace: error = %v", err)
	}
	if _, err := store.Get("tenant-b", sub.ID, "topic", registry); !errors.Is(err, ErrUnknownSubscription) {
		t.Fatalf("cross-tenant get: error = %v", err)
	}

	// 不存在的 Topic 读取返回 ErrTopicNotFound。
	if _, err := store.Get("tenant-a", sub.ID, "missing", registry); !errors.Is(err, ErrTopicNotFound) {
		t.Fatalf("missing topic: error = %v", err)
	}
}

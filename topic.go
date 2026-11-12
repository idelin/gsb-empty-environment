package netsec

import (
	"errors"
	"regexp"
	"sync"
)

// MaxTopicLen 为 Topic 允许的最大长度（32 位 URL 安全 Base64 字符）。
const MaxTopicLen = 32

var (
	// ErrInvalidTopic 表示 Topic 为空、超长或含有非 base64url 字符。
	ErrInvalidTopic = errors.New("netsec: topic must be 1-32 URL-safe base64 characters")
	// ErrTopicNotFound 表示该订阅下没有同名待投递通知。
	ErrTopicNotFound = errors.New("netsec: no pending notification for topic")

	// base64url 无填充字母表：A-Z a-z 0-9 - _
	topicPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,` + "32" + `}$`)
)

// ValidateTopic 校验 Topic 为不超过 32 位的 URL 安全 Base64 字符（无填充）。
func ValidateTopic(topic string) error {
	if len(topic) > MaxTopicLen || !topicPattern.MatchString(topic) {
		return ErrInvalidTopic
	}
	return nil
}

// topicKey 以 (订阅, Topic) 作为存储键，
// 天然保证同名 Topic 在不同订阅（及不同租户）之间互不影响。
type topicKey struct {
	subscriptionID string
	topic          string
}

// PendingNotification 为某个 Topic 下当前待投递的通知。
type PendingNotification struct {
	SubscriptionID string
	Topic          string
	Payload        []byte
	// MessageID 为推送服务返回或本地生成的通知标识。
	MessageID string
}

// TopicStore 保存各订阅下按 Topic 待投递的通知，
// 替换语义只在同一订阅内生效，无法跨订阅/跨租户覆盖。
type TopicStore struct {
	mu      sync.RWMutex
	pending map[topicKey]*PendingNotification
}

// NewTopicStore 创建空的 Topic 存储。
func NewTopicStore() *TopicStore {
	return &TopicStore{pending: make(map[topicKey]*PendingNotification)}
}

// Replace 在指定订阅内用新通知替换同名 Topic 的待投递通知。
// 返回被替换的旧通知；若此前不存在则返回 nil 与 ErrTopicNotFound 之外的零值错误，
// 调用方据此区分“首次写入”和“替换”。
func (s *TopicStore) Replace(tenantID, subscriptionID, topic string, payload []byte, messageID string, registry *Registry) (*PendingNotification, error) {
	if err := ValidateTopic(topic); err != nil {
		return nil, err
	}
	// 强制经租户主检查，防止凭猜测的订阅 ID 操作他人 Topic。
	if registry != nil {
		if err := registry.CheckTenant(tenantID, subscriptionID); err != nil {
			return nil, err
		}
	}
	key := topicKey{subscriptionID: subscriptionID, topic: topic}
	entry := &PendingNotification{
		SubscriptionID: subscriptionID,
		Topic:          topic,
		Payload:        append([]byte(nil), payload...),
		MessageID:      messageID,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.pending[key]
	s.pending[key] = entry
	if previous == nil {
		return nil, nil
	}
	return clonePending(previous), nil
}

// Get 读取某订阅下指定 Topic 的待投递通知。
func (s *TopicStore) Get(tenantID, subscriptionID, topic string, registry *Registry) (*PendingNotification, error) {
	if err := ValidateTopic(topic); err != nil {
		return nil, err
	}
	if registry != nil {
		if err := registry.CheckTenant(tenantID, subscriptionID); err != nil {
			return nil, err
		}
	}
	key := topicKey{subscriptionID: subscriptionID, topic: topic}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.pending[key]
	if !ok {
		return nil, ErrTopicNotFound
	}
	return clonePending(entry), nil
}

// Delete 清除某订阅下指定 Topic 的待投递通知。
func (s *TopicStore) Delete(tenantID, subscriptionID, topic string, registry *Registry) error {
	if err := ValidateTopic(topic); err != nil {
		return err
	}
	if registry != nil {
		if err := registry.CheckTenant(tenantID, subscriptionID); err != nil {
			return err
		}
	}
	key := topicKey{subscriptionID: subscriptionID, topic: topic}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[key]; !ok {
		return ErrTopicNotFound
	}
	delete(s.pending, key)
	return nil
}

func clonePending(entry *PendingNotification) *PendingNotification {
	return &PendingNotification{
		SubscriptionID: entry.SubscriptionID,
		Topic:          entry.Topic,
		Payload:        append([]byte(nil), entry.Payload...),
		MessageID:      entry.MessageID,
	}
}

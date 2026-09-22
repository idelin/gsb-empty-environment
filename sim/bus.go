package sim

import "fmt"

type Message struct {
	ID      string
	Topic   string
	Payload string
}

type Handler func(Message) error

type BusReport struct {
	PublishCount map[string]int
	AttemptCount map[string]int
	AckCount     map[string]int
}

type Bus struct {
	scheduler    *Scheduler
	subscribers  map[string]Handler
	deliveries   map[int64]*delivery
	nextDelivery int64
	report       BusReport
	networkDelay int64
	retryDelay   int64
	log          func(int64, string)
}

type delivery struct {
	message  Message
	attempts int
}

func NewBus(scheduler *Scheduler, log func(int64, string), networkDelay, retryDelay int64) *Bus {
	return &Bus{
		scheduler:    scheduler,
		subscribers:  make(map[string]Handler),
		deliveries:   make(map[int64]*delivery),
		report:       BusReport{make(map[string]int), make(map[string]int), make(map[string]int)},
		networkDelay: networkDelay,
		retryDelay:   retryDelay,
		log:          log,
	}
}

func (b *Bus) Subscribe(topic string, handler Handler) {
	b.subscribers[topic] = handler
}

func (b *Bus) Publish(message Message) {
	b.nextDelivery++
	id := b.nextDelivery
	b.deliveries[id] = &delivery{message: message}
	b.report.PublishCount[message.ID]++
	b.log(b.scheduler.Now(), fmt.Sprintf("publish message=%s topic=%s", message.ID, message.Topic))
	b.scheduler.At(b.networkDelay, func() { b.deliver(id) })
}

func (b *Bus) Report() BusReport {
	return BusReport{
		PublishCount: cloneCounts(b.report.PublishCount),
		AttemptCount: cloneCounts(b.report.AttemptCount),
		AckCount:     cloneCounts(b.report.AckCount),
	}
}

func (b *Bus) deliver(id int64) {
	current := b.deliveries[id]
	current.attempts++
	message := current.message
	b.report.AttemptCount[message.ID]++
	b.log(b.scheduler.Now(), fmt.Sprintf("deliver message=%s topic=%s attempt=%d", message.ID, message.Topic, current.attempts))
	handler := b.subscribers[message.Topic]
	var err error
	if handler != nil {
		err = handler(message)
	} else {
		err = ErrCrash
	}
	if err == nil {
		delete(b.deliveries, id)
		b.report.AckCount[message.ID]++
		b.log(b.scheduler.Now(), fmt.Sprintf("ack message=%s attempt=%d", message.ID, current.attempts))
		return
	}
	b.log(b.scheduler.Now(), fmt.Sprintf("redelivery-scheduled message=%s after=%d error=%q", message.ID, b.retryDelay, err))
	b.scheduler.At(b.retryDelay, func() { b.deliver(id) })
}

func cloneCounts(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

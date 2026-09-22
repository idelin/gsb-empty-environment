package sim

import (
	"errors"
	"fmt"
)

type OrderApp struct {
	name      string
	store     *Store
	bus       *Bus
	scheduler *Scheduler
	faults    *FaultInjector
	log       func(int64, string)
	onCrash   func()
}

func NewOrderApp(name string, store *Store, bus *Bus, scheduler *Scheduler, faults *FaultInjector, log func(int64, string)) *OrderApp {
	app := &OrderApp{
		name:      name,
		store:     store,
		bus:       bus,
		scheduler: scheduler,
		faults:    faults,
		log:       log,
	}
	bus.Subscribe(TopicCommands, app.safe(app.HandleCommand))
	scheduler.At(0, func() { app.guarded(app.relayOutbox) })
	return app
}

func (a *OrderApp) Restart() {
	a.log(a.scheduler.Now(), fmt.Sprintf("%s restart", a.name))
	a.bus.Subscribe(TopicCommands, a.safe(a.HandleCommand))
	a.scheduler.At(0, func() { a.guarded(a.relayOutbox) })
}

func (a *OrderApp) HandleCommand(message Message) error {
	tx := a.store.Begin()
	exists, err := tx.Get("inbox:"+message.ID, &InboxRecord{})
	if err != nil {
		return err
	}
	if exists {
		a.log(a.scheduler.Now(), fmt.Sprintf("%s skip duplicate command=%s", a.name, message.ID))
		return nil
	}

	orderID := message.Payload
	outboxID := "evt-" + message.ID
	event := Message{ID: outboxID, Topic: TopicOrderEvents, Payload: orderID}

	if err := tx.Put("inbox:"+message.ID, InboxRecord{MessageID: message.ID, ProcessedAt: a.scheduler.Now()}); err != nil {
		return err
	}
	if err := tx.Put("order:"+orderID, OrderRecord{ID: orderID, Status: "created", CreatedAt: a.scheduler.Now()}); err != nil {
		return err
	}
	if err := tx.Put("outbox:"+outboxID, OutboxRecord{ID: outboxID, Topic: event.Topic, Payload: event.Payload}); err != nil {
		return err
	}
	if err := a.faults.Hit(FaultBeforeOrderCommit, message.ID); err != nil {
		_ = tx.Abort()
		a.log(a.scheduler.Now(), fmt.Sprintf("%s crash before order commit=%s", a.name, message.ID))
		a.crash()
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.log(a.scheduler.Now(), fmt.Sprintf("%s committed order=%s and outbox=%s", a.name, orderID, outboxID))
	a.scheduler.At(0, func() { a.guarded(func() { a.relay(outboxID, event) }) })

	if err := a.faults.Hit(FaultAfterOrderAck, message.ID); err != nil {
		if errors.Is(err, ErrResponseLost) {
			a.log(a.scheduler.Now(), fmt.Sprintf("%s response-lost after commit command=%s", a.name, message.ID))
		}
		return err
	}
	return nil
}

func (a *OrderApp) relayOutbox() {
	for _, key := range a.store.KeysPrefix("outbox:") {
		var record OutboxRecord
		if _, err := a.store.Begin().Get(key, &record); err != nil {
			panic(err)
		}
		if record.Sent {
			continue
		}
		recordID := record.ID
		event := Message{ID: record.ID, Topic: record.Topic, Payload: record.Payload}
		a.relay(recordID, event)
	}
}

func (a *OrderApp) relay(recordID string, event Message) {
	var record OutboxRecord
	ok, err := a.store.Begin().Get("outbox:"+recordID, &record)
	if err != nil || !ok || record.Sent {
		return
	}

	a.bus.Publish(event)
	if err := a.faults.Hit(FaultAfterOutboxSend, recordID); err != nil {
		a.log(a.scheduler.Now(), fmt.Sprintf("%s crash before sent-mark outbox=%s", a.name, recordID))
		a.crash()
		return
	}

	markTx := a.store.Begin()
	if err := markTx.Put("outbox:"+recordID, OutboxRecord{ID: record.ID, Topic: record.Topic, Payload: record.Payload, Sent: true}); err != nil {
		_ = markTx.Abort()
		a.crash()
		return
	}
	if err := markTx.Commit(); err != nil {
		a.crash()
		return
	}
	a.log(a.scheduler.Now(), fmt.Sprintf("%s marked outbox=%s sent", a.name, recordID))
}

func (a *OrderApp) safe(handler func(Message) error) Handler {
	return func(message Message) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if crash, ok := recovered.(crashSignal); ok {
					err = crash.err
					return
				}
				panic(recovered)
			}
		}()
		return handler(message)
	}
}

func (a *OrderApp) guarded(action func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(crashSignal); ok {
				return
			}
			panic(recovered)
		}
	}()
	action()
}

func (a *OrderApp) crash() {
	if a.onCrash != nil {
		a.onCrash()
	}
	panic(crashSignal{err: ErrCrash})
}

type crashSignal struct {
	err error
}

type ShipmentApp struct {
	name      string
	store     *Store
	bus       *Bus
	scheduler *Scheduler
	faults    *FaultInjector
	log       func(int64, string)
	onCrash   func()
}

func NewShipmentApp(name string, store *Store, bus *Bus, scheduler *Scheduler, faults *FaultInjector, log func(int64, string)) *ShipmentApp {
	app := &ShipmentApp{
		name:      name,
		store:     store,
		bus:       bus,
		scheduler: scheduler,
		faults:    faults,
		log:       log,
	}
	bus.Subscribe(TopicOrderEvents, app.safe(app.HandleEvent))
	return app
}

func (a *ShipmentApp) Restart() {
	a.log(a.scheduler.Now(), fmt.Sprintf("%s restart", a.name))
	a.bus.Subscribe(TopicOrderEvents, a.safe(a.HandleEvent))
}

func (a *ShipmentApp) HandleEvent(message Message) error {
	tx := a.store.Begin()
	exists, err := tx.Get("inbox:"+message.ID, &InboxRecord{})
	if err != nil {
		return err
	}
	if exists {
		a.log(a.scheduler.Now(), fmt.Sprintf("%s skip duplicate event=%s", a.name, message.ID))
		return nil
	}

	if err := tx.Put("inbox:"+message.ID, InboxRecord{MessageID: message.ID, ProcessedAt: a.scheduler.Now()}); err != nil {
		return err
	}
	if err := tx.Put("shipment:"+message.Payload, ShipmentRecord{OrderID: message.Payload, Status: "planned", ReceivedAt: a.scheduler.Now()}); err != nil {
		return err
	}
	if err := a.faults.Hit(FaultBeforeEventCommit, message.ID); err != nil {
		_ = tx.Abort()
		a.log(a.scheduler.Now(), fmt.Sprintf("%s crash before event commit=%s", a.name, message.ID))
		a.crash()
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.log(a.scheduler.Now(), fmt.Sprintf("%s committed shipment order=%s event=%s", a.name, message.Payload, message.ID))
	return nil
}

func (a *ShipmentApp) safe(handler func(Message) error) Handler {
	return func(message Message) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if crash, ok := recovered.(crashSignal); ok {
					err = crash.err
					return
				}
				panic(recovered)
			}
		}()
		return handler(message)
	}
}

func (a *ShipmentApp) crash() {
	if a.onCrash != nil {
		a.onCrash()
	}
	panic(crashSignal{err: ErrCrash})
}

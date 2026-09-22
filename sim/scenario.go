package sim

import (
	"fmt"
	"strings"
)

type ScenarioConfig struct {
	Name      string
	CommandID string
	OrderID   string
	Faults    []Fault
	StartTime int64
	NetworkMs int64
	RetryMs   int64
	MaxEvents int
}

type ScenarioReport struct {
	Name            string
	CurrentTime     int64
	Order           OrderRecord
	OrderExists     bool
	Outbox          OutboxRecord
	OutboxExists    bool
	OrderInbox      InboxRecord
	OrderInboxOK    bool
	Shipment        ShipmentRecord
	ShipmentExists  bool
	ShipmentInbox   InboxRecord
	ShipmentInboxOK bool
	Bus             BusReport
	Events          []string
}

func RunScenario(config ScenarioConfig) (ScenarioReport, error) {
	if config.CommandID == "" {
		config.CommandID = "cmd-1"
	}
	if config.OrderID == "" {
		config.OrderID = "order-1"
	}
	if config.StartTime == 0 {
		config.StartTime = 1000
	}
	if config.NetworkMs == 0 {
		config.NetworkMs = 5
	}
	if config.RetryMs == 0 {
		config.RetryMs = 10
	}
	if config.MaxEvents == 0 {
		config.MaxEvents = 100
	}

	events := make([]string, 0)
	log := func(now int64, event string) {
		events = append(events, fmt.Sprintf("t=%04d %s", now, event))
	}

	scheduler := NewScheduler(config.StartTime)
	bus := NewBus(scheduler, log, config.NetworkMs, config.RetryMs)
	faults := NewFaultInjector(config.Faults...)
	orderStore := NewStore()
	shipmentStore := NewStore()

	var orderApp *OrderApp
	var shipmentApp *ShipmentApp
	orderRestarts := 0
	shipmentRestarts := 0

	orderApp = NewOrderApp("order-service", orderStore, bus, scheduler, faults, log)
	shipmentApp = NewShipmentApp("shipment-service", shipmentStore, bus, scheduler, faults, log)
	orderApp.onCrash = func() {
		orderRestarts++
		log(scheduler.Now(), "order-service crash")
		scheduler.At(0, func() { orderApp.Restart() })
	}
	shipmentApp.onCrash = func() {
		shipmentRestarts++
		log(scheduler.Now(), "shipment-service crash")
		scheduler.At(0, func() { shipmentApp.Restart() })
	}

	bus.Publish(Message{ID: config.CommandID, Topic: TopicCommands, Payload: config.OrderID})
	if err := scheduler.Run(config.MaxEvents); err != nil {
		return ScenarioReport{Name: config.Name, Events: events}, err
	}

	report := ScenarioReport{
		Name:        config.Name,
		CurrentTime: scheduler.Now(),
		Bus:         bus.Report(),
		Events:      events,
	}
	report.OrderExists, _ = orderStore.Begin().Get("order:"+config.OrderID, &report.Order)
	report.OutboxExists, _ = orderStore.Begin().Get("outbox:evt-"+config.CommandID, &report.Outbox)
	report.OrderInboxOK, _ = orderStore.Begin().Get("inbox:"+config.CommandID, &report.OrderInbox)
	report.ShipmentExists, _ = shipmentStore.Begin().Get("shipment:"+config.OrderID, &report.Shipment)
	report.ShipmentInboxOK, _ = shipmentStore.Begin().Get("inbox:evt-"+config.CommandID, &report.ShipmentInbox)
	return report, nil
}

func (r ScenarioReport) String() string {
	var builder strings.Builder
	builder.WriteString("scenario: " + r.Name + "\n")
	builder.WriteString("event log:\n")
	for _, event := range r.Events {
		builder.WriteString("  " + event + "\n")
	}
	builder.WriteString(fmt.Sprintf("final: order=%v outbox-sent=%v shipment=%v event-publishes=%d event-attempts=%d\n",
		r.OrderExists,
		r.OutboxExists && r.Outbox.Sent,
		r.ShipmentExists,
		r.Bus.PublishCount["evt-cmd-1"],
		r.Bus.AttemptCount["evt-cmd-1"],
	))
	return builder.String()
}

package alerts_test

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/events"
)

// The canary deadline gate (SPEC §17, M2 done criteria): a canary's alert, and a
// disabled key's, arrive within 5 s under 256-way load with another channel
// hung. It is its own CI job, and it runs with the rest of go test.
//
// The load is what the gateway puts on the pipeline: 256 concurrent clients, each
// emitting its requests' events as fast as it can onto the bus, where they fan
// out to the store writer (the one writer of the one database the priority lane
// also needs), with a channel that never answers and one that does. Time here is
// the wall clock: the gate is about real queues and a real SQLite, not a clock
// that can be made to wait.
const (
	gateWorkers  = 256
	gateDeadline = 5 * time.Second
)

func TestCanaryDeadlineGate(t *testing.T) {
	in := newInstall(t)
	in.channel("ch_hung", "slack", "hung.test", "low", true, "")
	in.channel("ch_ok", "slack", "ok.test", "low", true, "")
	in.net.on("hung.test", hang)
	in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
	in.withKey("abcd1234", "ci-bot") // a disabled key, as far as the lane is concerned

	// The pipeline as serve builds it: one bus, the store writer on a buffer of
	// its own, and the priority lane on its own subscription. The writer's buffer
	// is smaller than serve's, so that the test ends soon after the load does: a
	// writer that cannot keep up loses events, and the load is meant to be more
	// than it can keep up with.
	bus := events.New(events.Options{})
	writer := events.NewWriter(events.WriterOptions{Sink: events.SQLite(in.store)})
	storeSub := bus.Subscribe("store", events.SubOptions{Buffer: 1024})
	alertSub := bus.Subscribe("alerts", events.SubOptions{PriorityOnly: true, Buffer: events.DefaultPriorityCapacity})
	ctx, cancel := context.WithCancel(context.Background())
	var pipeline sync.WaitGroup
	pipeline.Go(func() { bus.Run(ctx) })
	pipeline.Go(func() { writer.Run(storeSub) })
	pipeline.Go(func() { in.svc.RunPriority(alertSub) })
	stopDelivery := in.run()
	defer func() {
		cancel()
		pipeline.Wait()
		stopDelivery()
	}()

	// 256 clients, each emitting events for as long as the test runs.
	var emitted atomic.Int64
	loadCtx, stopLoad := context.WithCancel(ctx)
	var load sync.WaitGroup
	for i := range gateWorkers {
		load.Go(func() {
			ip := netip.AddrFrom4([4]byte{10, 0, byte(i / 250), byte(i%250 + 1)})
			for loadCtx.Err() == nil {
				bus.Emit(events.Event{Time: time.Now(), Kind: events.KindRequest, KeyID: "load0001", ClientIP: ip, Status: 200,
					Method: "POST", Path: "/v1/chat/completions", Latency: time.Millisecond, TokensIn: 10, TokensOut: 20})
				emitted.Add(1)
				time.Sleep(time.Millisecond)
			}
		})
	}
	defer func() { stopLoad(); load.Wait() }()
	time.Sleep(500 * time.Millisecond) // let the load build

	start := time.Now()
	canary := canaryUse("cafe0001", "203.0.113.7")
	canary.Time, canary.Country, canary.ASN, canary.ASOrg, canary.Client = start, "DE", 3320, "Deutsche Telekom AG", "curl/8"
	disabled := disabledUse("abcd1234", "203.0.113.8")
	disabled.Time = start
	bus.Emit(canary)
	bus.Emit(disabled)

	var sawCanary, sawDisabled time.Duration
	for sawCanary == 0 || sawDisabled == 0 {
		for _, r := range in.net.received("ok.test") {
			switch {
			case sawCanary == 0 && strings.Contains(r.Body, "A canary key was used") && strings.Contains(r.Body, "cafe0001"):
				sawCanary = r.At.Sub(start)
			case sawDisabled == 0 && strings.Contains(r.Body, "A disabled key was used") && strings.Contains(r.Body, "abcd1234"):
				sawDisabled = r.At.Sub(start)
			}
		}
		if time.Since(start) > gateDeadline+time.Second {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("canary alert after %v, disabled-key alert after %v, with %d events emitted by %d clients and a channel hung", sawCanary, sawDisabled, emitted.Load(), gateWorkers)
	if sawCanary == 0 || sawCanary > gateDeadline {
		t.Errorf("the canary alert arrived after %v (0 means never), want within %v", sawCanary, gateDeadline)
	}
	if sawDisabled == 0 || sawDisabled > gateDeadline {
		t.Errorf("the disabled-key alert arrived after %v (0 means never), want within %v", sawDisabled, gateDeadline)
	}
	// The hung channel has them too, waiting: held, not dropped.
	if c := in.outbox("ch_hung"); c.Pending < 2 {
		t.Errorf("the hung channel's outbox = %+v, want the alerts still held for it", c)
	}
	if s := bus.Stats(); s.DroppedPriority != 0 {
		t.Errorf("the priority lane dropped %d events", s.DroppedPriority)
	}
}

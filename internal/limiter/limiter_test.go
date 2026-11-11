package limiter_test

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/limiter"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newLimiter(c *clock, o limiter.Options) *limiter.Limiter {
	o.Now = c.now
	return limiter.New(o)
}

func allowed(l *limiter.Limiter, key string, rpm int) bool {
	ok, _ := l.Allow(key, rpm)
	return ok
}

func TestAKeyMayMakeItsRPMInAMinuteAndNoMore(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{})
	for i := 0; i < 10; i++ {
		if !allowed(l, "k", 10) {
			t.Fatalf("request %d was refused", i+1)
		}
	}
	ok, retry := l.Allow("k", 10)
	if ok {
		t.Fatal("the 11th request was allowed")
	}
	if retry < time.Second || retry > time.Minute {
		t.Errorf("retry after %v, want between 1 s and a minute", retry)
	}
}

func TestTheLimitSlidesRatherThanResettingAtAMinuteMark(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{})
	for i := 0; i < 10; i++ {
		allowed(l, "k", 10)
	}
	// Just over a minute on, the last window still weighs almost in full: a
	// burst at a boundary can't make twice the limit.
	c.advance(61 * time.Second)
	if allowed(l, "k", 10) {
		t.Error("allowed a request 1 s into the next window, right after a full one")
	}
	// Half a window on, about half of what was used is still counted.
	c.advance(29 * time.Second)
	n := 0
	for allowed(l, "k", 10) {
		n++
		if n > 10 {
			break
		}
	}
	if n < 4 || n > 6 {
		t.Errorf("allowed %d requests half a window on, want about 5", n)
	}
	c.advance(2 * time.Minute)
	if !allowed(l, "k", 10) {
		t.Error("still refusing after two idle minutes")
	}
}

func TestEachKeyHasItsOwnWindow(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{})
	allowed(l, "a", 1)
	if allowed(l, "a", 1) {
		t.Fatal("a was allowed twice")
	}
	if !allowed(l, "b", 1) {
		t.Error("b was refused for a's requests")
	}
}

func TestARefusedRequestDoesNotCountAgainstTheKey(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{})
	allowed(l, "k", 2)
	allowed(l, "k", 2)
	for i := 0; i < 100; i++ {
		allowed(l, "k", 2) // refused
	}
	c.advance(2 * time.Minute)
	if !allowed(l, "k", 2) || !allowed(l, "k", 2) {
		t.Error("refused requests were counted, so the key stayed locked out")
	}
}

func TestTheLimitIsReadOnEachCallSoAnEditedPolicyTakesEffect(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{})
	for i := 0; i < 5; i++ {
		allowed(l, "k", 100)
	}
	if allowed(l, "k", 5) {
		t.Error("a lowered limit was not applied")
	}
	if !allowed(l, "k", 100) {
		t.Error("a raised limit was not applied")
	}
}

func TestNoLimitIsNeverRefusedAndTracksNothing(t *testing.T) {
	l := newLimiter(newClock(), limiter.Options{})
	for i := 0; i < 1000; i++ {
		if !allowed(l, "k", 0) {
			t.Fatal("refused a key with no limit")
		}
	}
	if keys, _ := l.Tracked(); keys != 0 {
		t.Errorf("tracked %d keys with no limit", keys)
	}
}

func TestTheKeyTableIsBoundedAndKeepsWhatIsInUse(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{MaxKeys: 100})
	allowed(l, "hot", 1)
	for i := 0; i < 50_000; i++ {
		allowed(l, fmt.Sprintf("flood-%d", i), 5)
		if i%50 == 0 && allowed(l, "hot", 1) {
			t.Fatalf("the busy key was forgotten after %d other keys", i)
		}
	}
	if keys, _ := l.Tracked(); keys > 100 {
		t.Errorf("tracked %d keys, bound is 100", keys)
	}
}

func TestInvalidKeysFromOneAddressAreThrottled(t *testing.T) {
	c := newClock()
	l := newLimiter(c, limiter.Options{InvalidPerMinute: 5})
	ip := netip.MustParseAddr("203.0.113.7")
	for i := 0; i < 5; i++ {
		if !l.AllowInvalid(ip) {
			t.Fatalf("throttled after %d invalid keys", i)
		}
		l.RecordInvalid(ip)
	}
	if l.AllowInvalid(ip) {
		t.Error("an address past its invalid keys is still allowed")
	}
	if !l.AllowInvalid(netip.MustParseAddr("203.0.113.8")) {
		t.Error("another address was throttled")
	}
	// Asking doesn't count: a throttled address can't keep itself throttled by retrying.
	for i := 0; i < 1000; i++ {
		l.AllowInvalid(ip)
	}
	c.advance(3 * time.Minute)
	if !l.AllowInvalid(ip) {
		t.Error("still throttled after three minutes")
	}
}

func TestAnAddressWithNoInvalidKeysIsNotTracked(t *testing.T) {
	l := newLimiter(newClock(), limiter.Options{})
	for i := 0; i < 1000; i++ {
		l.AllowInvalid(netip.MustParseAddr("192.0.2.1"))
	}
	if _, addrs := l.Tracked(); addrs != 0 {
		t.Errorf("tracked %d addresses that sent no invalid key", addrs)
	}
}

func TestAnIPv6NetworkIsOneAddressAndAMappedIPv4IsItsIPv4(t *testing.T) {
	l := newLimiter(newClock(), limiter.Options{InvalidPerMinute: 2})
	// A /64 is one subscriber's, who holds 2^64 addresses: counting each would
	// make the throttle free to get round.
	for i := 0; i < 2; i++ {
		l.RecordInvalid(netip.MustParseAddr(fmt.Sprintf("2001:db8:1:2::%x", i+1)))
	}
	if l.AllowInvalid(netip.MustParseAddr("2001:db8:1:2:ffff::9")) {
		t.Error("a third address of the same /64 was allowed")
	}
	if !l.AllowInvalid(netip.MustParseAddr("2001:db8:1:3::1")) {
		t.Error("the next /64 was throttled")
	}
	l.RecordInvalid(netip.MustParseAddr("198.51.100.4"))
	l.RecordInvalid(netip.MustParseAddr("::ffff:198.51.100.4"))
	if l.AllowInvalid(netip.MustParseAddr("198.51.100.4")) {
		t.Error("an IPv4 address and its mapped form were counted apart")
	}
}

func TestTheAddressTableIsBounded(t *testing.T) {
	l := newLimiter(newClock(), limiter.Options{MaxAddrs: 100, InvalidPerMinute: 3})
	for i := 0; i < 100_000; i++ {
		l.RecordInvalid(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}))
	}
	if _, addrs := l.Tracked(); addrs > 100 {
		t.Errorf("tracked %d addresses, bound is 100", addrs)
	}
}

func TestTheDefaultsAreBounded(t *testing.T) {
	l := newLimiter(newClock(), limiter.Options{})
	for i := 0; i < 300_000; i++ {
		l.RecordInvalid(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}))
		allowed(l, fmt.Sprint(i), 1)
	}
	keys, addrs := l.Tracked()
	if keys > limiter.DefaultMaxKeys || addrs > limiter.DefaultMaxAddrs {
		t.Errorf("tracked %d keys and %d addresses, bounds are %d and %d", keys, addrs, limiter.DefaultMaxKeys, limiter.DefaultMaxAddrs)
	}
}

func TestTheLimiterIsSafeToShare(t *testing.T) {
	l := limiter.New(limiter.Options{Now: time.Now, MaxKeys: 50, MaxAddrs: 50})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				l.Allow(fmt.Sprint(i%80), 10)
				ip := netip.AddrFrom4([4]byte{10, 0, byte(g), byte(i)})
				if l.AllowInvalid(ip) {
					l.RecordInvalid(ip)
				}
			}
		}()
	}
	wg.Wait()
}

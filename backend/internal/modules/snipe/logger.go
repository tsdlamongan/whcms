package snipe

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// maxSampledKeys bounds the sampler's memory: hunts come and go, so an
// unbounded per-hunt map would grow forever.
const maxSampledKeys = 2048

// sampledLogger decorates the integration logger for the hunt's hot loop. The
// plain logger writes one database row per HTTP call; at a few requests per
// second per domain that would bury every other integration call and bloat
// integration_logs. This one always passes a call whose outcome differs from
// the previous identical request, and otherwise only every every-th repeat or
// after maxQuiet of silence - so a 409 streak is visible as "still 409" without
// being stored 400,000 times a day.
type sampledLogger struct {
	next     ports.IntegrationLogger
	clock    ports.Clock
	every    int
	maxQuiet time.Duration

	mu   sync.Mutex
	last map[string]*sampleState
}

type sampleState struct {
	sig    string
	repeat int
	at     time.Time
}

// NewSampledLogger wraps next with the sampler described above.
func NewSampledLogger(next ports.IntegrationLogger, clock ports.Clock, every int, maxQuiet time.Duration) ports.IntegrationLogger {
	if every < 1 {
		every = 1
	}
	return &sampledLogger{
		next:     next,
		clock:    clock,
		every:    every,
		maxQuiet: maxQuiet,
		last:     make(map[string]*sampleState),
	}
}

func (l *sampledLogger) Log(ctx context.Context, call ports.IntegrationCall) {
	key := fmt.Sprintf("%s|%s|%v", call.Method, call.Endpoint, call.Request)
	sig := fmt.Sprintf("%d|%t|%s|%v", call.StatusCode, call.Success, call.Error, call.Response)
	now := l.clock.Now()

	l.mu.Lock()
	st, seen := l.last[key]
	pass := !seen || st.sig != sig
	switch {
	case pass:
		if len(l.last) >= maxSampledKeys {
			l.last = make(map[string]*sampleState)
		}
		l.last[key] = &sampleState{sig: sig, at: now}
	default:
		st.repeat++
		if st.repeat%l.every == 0 || now.Sub(st.at) >= l.maxQuiet {
			pass = true
			st.at = now
		}
	}
	l.mu.Unlock()

	if pass {
		l.next.Log(ctx, call)
	}
}

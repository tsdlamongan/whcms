package snipe

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
)

func TestSampledLogger(t *testing.T) {
	clock := &fakeClock{t: t0}
	sink := &mocks.MockIntegrationLogger{}
	l := NewSampledLogger(sink, clock, 5, time.Minute)
	ctx := context.Background()

	taken := ports.IntegrationCall{
		Provider: "rdash", Method: "POST", Endpoint: "/domains", StatusCode: 409,
		Error: "conflict", Request: "name=hunt.com", Response: "taken",
	}

	// The first outcome always passes; identical repeats are swallowed until
	// the every-th one.
	l.Log(ctx, taken)
	assert.Len(t, sink.Calls, 1)
	for i := 0; i < 4; i++ {
		l.Log(ctx, taken)
	}
	assert.Len(t, sink.Calls, 1, "four silent repeats")
	l.Log(ctx, taken)
	assert.Len(t, sink.Calls, 2, "the fifth repeat passes")

	// A changed outcome for the same request always passes - this is the
	// moment the hunt succeeds.
	won := taken
	won.StatusCode, won.Success, won.Error, won.Response = 200, true, "", "registered"
	l.Log(ctx, won)
	assert.Len(t, sink.Calls, 3)

	// A different request (another hunt) is tracked separately.
	other := taken
	other.Request = "name=other.com"
	l.Log(ctx, other)
	assert.Len(t, sink.Calls, 4)

	// Long silence lets a repeat through even before the every-th one.
	l.Log(ctx, other)
	assert.Len(t, sink.Calls, 4)
	clock.Advance(2 * time.Minute)
	l.Log(ctx, other)
	assert.Len(t, sink.Calls, 5)
}

func TestSampledLoggerEveryFloorAndKeyBound(t *testing.T) {
	clock := &fakeClock{t: t0}
	sink := &mocks.MockIntegrationLogger{}
	l := NewSampledLogger(sink, clock, 0, time.Hour).(*sampledLogger)
	assert.Equal(t, 1, l.every, "every < 1 is clamped so nothing is dropped forever")

	for i := 0; i < maxSampledKeys+10; i++ {
		l.Log(context.Background(), ports.IntegrationCall{Method: "GET", Endpoint: "/x", Request: i})
	}
	assert.LessOrEqual(t, len(l.last), maxSampledKeys, "the sampler's memory is bounded")
	assert.Len(t, sink.Calls, maxSampledKeys+10, "every distinct request was logged")
}

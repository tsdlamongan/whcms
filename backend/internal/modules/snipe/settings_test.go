package snipe

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(context.Background(), &mocks.MockSettingsRepo{})
	require.NoError(t, err)
	assert.Equal(t, defaultConfig(), cfg)
	assert.Equal(t, time.Minute, cfg.PauseProbe)
	assert.EqualValues(t, -1, cfg.PausedBalance, "no balance pause by default")
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 200*time.Millisecond, cfg.BurstInterval)
	assert.Equal(t, 2, cfg.MaxInFlight)
	assert.Equal(t, 50, cfg.MaxActiveGlobal)
}

func TestLoadConfigReadsAndClamps(t *testing.T) {
	ints := map[string]int{
		SettingMaxActiveGlobal:    9999,  // > 200
		SettingMaxInFlight:        0,     // < 1
		SettingBurstIntervalMS:    1,     // < 10
		SettingBurstJitterMS:      -5,    // < 0
		SettingPatientIntervalSec: 99999, // > 3600
		SettingBurstLeadSec:       -1,
		SettingRateLimitBackoffMS: 5,
		SettingRequestTimeoutMS:   999999,
		SettingMaxActivePerClient: 7,
		SettingInvoiceDueHours:    0,
		SettingMinResellerBalance: -100,
		SettingPauseProbeSec:      0,  // < 1
		SettingPausedBalance:      42, // not clamped: it is a recorded balance
	}
	repo := &mocks.MockSettingsRepo{
		GetIntFn: func(_ context.Context, key string, def int) (int, error) {
			if v, ok := ints[key]; ok {
				return v, nil
			}
			return def, nil
		},
		GetBoolFn: func(_ context.Context, key string, def bool) (bool, error) {
			assert.Equal(t, SettingEnabled, key)
			return false, nil
		},
		GetStringFn: func(_ context.Context, key string, def string) (string, error) {
			assert.Equal(t, SettingPauseReason, key)
			return "credentials rejected", nil
		},
	}
	cfg, err := loadConfig(context.Background(), repo)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 200, cfg.MaxActiveGlobal)
	assert.Equal(t, 1, cfg.MaxInFlight)
	assert.Equal(t, 10*time.Millisecond, cfg.BurstInterval)
	assert.Equal(t, time.Duration(0), cfg.BurstJitter)
	assert.Equal(t, time.Hour, cfg.PatientInterval)
	assert.Equal(t, time.Duration(0), cfg.BurstLead)
	assert.Equal(t, 100*time.Millisecond, cfg.RateLimitBackoff)
	assert.Equal(t, 30*time.Second, cfg.RequestTimeout)
	assert.Equal(t, 7, cfg.MaxActivePerClient)
	assert.Equal(t, time.Hour, cfg.InvoiceDue)
	assert.EqualValues(t, 0, cfg.MinResellerBalance)
	assert.Equal(t, time.Second, cfg.PauseProbe)
	assert.EqualValues(t, 42, cfg.PausedBalance)
	assert.Equal(t, "credentials rejected", cfg.PauseReason)
}

func TestLoadConfigReportsFirstReadError(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		_, err := loadConfig(context.Background(), &mocks.MockSettingsRepo{
			GetIntFn: func(context.Context, string, int) (int, error) { return 0, errBoom },
		})
		assert.ErrorIs(t, err, errBoom)
	})
	t.Run("bool", func(t *testing.T) {
		_, err := loadConfig(context.Background(), &mocks.MockSettingsRepo{
			GetBoolFn: func(context.Context, string, bool) (bool, error) { return false, errBoom },
		})
		assert.ErrorIs(t, err, errBoom)
	})
	t.Run("string", func(t *testing.T) {
		_, err := loadConfig(context.Background(), &mocks.MockSettingsRepo{
			GetStringFn: func(context.Context, string, string) (string, error) { return "", errBoom },
		})
		assert.ErrorIs(t, err, errBoom)
	})
}

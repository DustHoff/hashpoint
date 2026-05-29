package watchdog

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dusthoff/hashpoint/internal/crashguard"
)

// Default loop timings. The poll interval bounds how quickly a collector death
// is noticed; the backoff bounds how hard a crash-looping collector is
// relaunched; the cooldown caps repeated relaunches of a wedged install.
const (
	defaultPollInterval = 5 * time.Second
	defaultMinBackoff   = 1 * time.Second
	defaultMaxBackoff   = 30 * time.Second
	defaultCooldown     = 5 * time.Minute
	defaultMaxAttempts  = 5
)

// CloseFunc closes every open tag-block in the target's database at the given
// time, returning how many were closed. The Windows host wires this to
// storage.Open + CloseOpenBlocks; tests inject a fake.
type CloseFunc func(ctx context.Context, t Target, at time.Time) (int, error)

// Monitor polls collector liveness and, on an unclean death, closes open
// tag-blocks at the last-alive time and relaunches the collector. It performs
// no action for a clean shutdown (absent marker), respecting a user-initiated
// quit.
type Monitor struct {
	probe       Probe
	closeBlocks CloseFunc
	logger      *slog.Logger

	pollInterval time.Duration
	minBackoff   time.Duration
	maxBackoff   time.Duration
	cooldown     time.Duration
	maxAttempts  int
}

// Option overrides Monitor defaults (mainly for tests).
type Option func(*Monitor)

// WithTiming overrides the loop timings and the relaunch attempt cap.
func WithTiming(poll, minBackoff, maxBackoff, cooldown time.Duration, maxAttempts int) Option {
	return func(m *Monitor) {
		m.pollInterval = poll
		m.minBackoff = minBackoff
		m.maxBackoff = maxBackoff
		m.cooldown = cooldown
		m.maxAttempts = maxAttempts
	}
}

// New builds a Monitor. probe supplies the platform operations; closeBlocks
// finalizes open tag-blocks against the resolved database.
func New(probe Probe, closeBlocks CloseFunc, logger *slog.Logger, opts ...Option) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Monitor{
		probe:        probe,
		closeBlocks:  closeBlocks,
		logger:       logger,
		pollInterval: defaultPollInterval,
		minBackoff:   defaultMinBackoff,
		maxBackoff:   defaultMaxBackoff,
		cooldown:     defaultCooldown,
		maxAttempts:  defaultMaxAttempts,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Run blocks, polling until ctx is cancelled, then returns ctx.Err(). Each tick
// resolves the active user's target, probes the collector, and on an unclean
// death closes open tag-blocks and relaunches the collector with a backoff that
// grows on repeated failures and resets once the collector is seen alive.
func (m *Monitor) Run(ctx context.Context) error {
	backoff := m.minBackoff
	attempts := 0
	var lastConfirmedAlive, cooldownUntil time.Time

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		target, err := m.probe.ResolveTarget(ctx)
		if err != nil {
			if errors.Is(err, ErrNoActiveSession) {
				lastConfirmedAlive = time.Time{} // forget; a new user means a new profile
				m.logger.Debug("watchdog: no active session — idle")
			} else {
				m.logger.Warn("watchdog: resolve target failed", "err", err)
			}
			continue
		}

		alive, markerLastAlive, err := m.probe.CollectorAlive(ctx, target)
		if err != nil {
			switch {
			case errors.Is(err, crashguard.ErrMarkerAbsent):
				// Clean quit or never started — respect the user's intent.
			case errors.Is(err, crashguard.ErrMarkerCorrupt):
				m.logger.Debug("watchdog: marker torn — retrying next tick")
			default:
				m.logger.Warn("watchdog: liveness probe failed", "err", err)
			}
			continue
		}

		if alive {
			lastConfirmedAlive = time.Now()
			attempts = 0
			backoff = m.minBackoff
			continue
		}

		// Unclean death: marker present, process dead.
		if now := time.Now(); now.Before(cooldownUntil) {
			continue
		}

		lastAlive := pickLastAlive(lastConfirmedAlive, markerLastAlive, time.Now())
		if n, err := m.closeBlocks(ctx, target, lastAlive); err != nil {
			m.logger.Error("watchdog: closing open tag-blocks failed", "err", err)
		} else if n > 0 {
			m.logger.Info("watchdog: closed open tag-blocks after collector death",
				"count", n, "at", lastAlive.Format(time.RFC3339))
		}

		attempts++
		if attempts > m.maxAttempts {
			m.logger.Error("watchdog: relaunch attempts exhausted — cooling down",
				"attempts", attempts-1, "cooldown", m.cooldown.String())
			cooldownUntil = time.Now().Add(m.cooldown)
			attempts = 0
			backoff = m.minBackoff
			lastConfirmedAlive = time.Time{}
			continue
		}

		if err := m.probe.Relaunch(ctx, target); err != nil {
			m.logger.Error("watchdog: collector relaunch failed", "err", err, "attempt", attempts)
		} else {
			m.logger.Info("watchdog: collector relaunched", "attempt", attempts)
		}

		// Back off before the next probe so a crash-looping collector is not
		// hammered; the backoff resets once the collector is observed alive.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > m.maxBackoff {
			backoff = m.maxBackoff
		}
	}
}

package splitter

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/bluesky-social/indigo/events"

	"golang.org/x/time/rate"
)

// Playback is what a consumer gets when it connects with a cursor: stored
// events, read back from disk until it has caught up, and only then the live
// stream. It competes with live delivery for disk and CPU, so it can be rate
// limited. Events sent to a consumer that has caught up are never limited.

const (
	playbackClassThrottled = "throttled"
	playbackClassExempt    = "exempt"
)

// playbackConsumer is what the playback limiter needs to know about the
// consumer a playback is for. It travels in the subscription's context.
type playbackConsumer struct {
	exempt bool
	// nil when there is no per-consumer limit
	limiter *rate.Limiter
}

type playbackConsumerKey struct{}

func (s *Splitter) withPlaybackConsumer(ctx context.Context, remoteAddr string) (context.Context, *playbackConsumer) {
	pc := &playbackConsumer{exempt: s.playbackExempt(remoteAddr)}
	if !pc.exempt {
		pc.limiter = newPlaybackLimiter(s.conf.PlaybackRateLimit)
	}
	return context.WithValue(ctx, playbackConsumerKey{}, pc), pc
}

func (pc *playbackConsumer) class() string {
	if pc != nil && pc.exempt {
		return playbackClassExempt
	}
	return playbackClassThrottled
}

// newPlaybackLimiter returns a limiter for eventsPerSec, or nil for no limit.
func newPlaybackLimiter(eventsPerSec int64) *rate.Limiter {
	if eventsPerSec <= 0 {
		return nil
	}
	// a burst of a tenth of a second: enough to not sleep on every event,
	// too little to show up next to live delivery
	return rate.NewLimiter(rate.Limit(eventsPerSec), max(1, int(eventsPerSec/10)))
}

func (s *Splitter) playbackExempt(remoteAddr string) bool {
	addr, err := netip.ParseAddr(remoteAddr)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range s.conf.PlaybackExempt {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ParsePlaybackExempt parses a list of IP addresses and CIDR ranges.
func ParsePlaybackExempt(raw []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if strings.Contains(r, "/") {
			p, err := netip.ParsePrefix(r)
			if err != nil {
				return nil, fmt.Errorf("playback exempt range %q: %w", r, err)
			}
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(r)
		if err != nil {
			return nil, fmt.Errorf("playback exempt address %q: %w", r, err)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// playbackLimiter wraps the persister the event manager plays stored events
// back from, and paces Playback: each consumer by its own limiter, and all of
// them together by a shared one. Exempt consumers are not paced.
type playbackLimiter struct {
	events.EventPersistence

	// nil when there is no limit across consumers
	global *rate.Limiter
}

func (pl *playbackLimiter) Playback(ctx context.Context, since int64, cb func(*events.XRPCStreamEvent) error) error {
	pc, _ := ctx.Value(playbackConsumerKey{}).(*playbackConsumer)
	class := pc.class()

	var limiters []*rate.Limiter
	if class == playbackClassThrottled {
		if pc != nil && pc.limiter != nil {
			limiters = append(limiters, pc.limiter)
		}
		if pl.global != nil {
			limiters = append(limiters, pl.global)
		}
	}

	activePlaybacks.WithLabelValues(class).Inc()
	defer activePlaybacks.WithLabelValues(class).Dec()
	sent := playbackEventsSent.WithLabelValues(class)

	return pl.EventPersistence.Playback(ctx, since, func(evt *events.XRPCStreamEvent) error {
		for _, l := range limiters {
			if err := l.Wait(ctx); err != nil {
				return err
			}
		}
		if err := cb(evt); err != nil {
			return err
		}
		sent.Inc()
		return nil
	})
}

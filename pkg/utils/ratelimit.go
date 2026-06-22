package utils

import "golang.org/x/time/rate"

// minBurst is the floor for a transfer limiter's burst, in bytes.
//
// rate.Limiter.WaitN(n) returns an error immediately (instead of blocking) when
// n is greater than the burst. Transfers are throttled one part at a time —
// downloads use 1MiB parts and uploads 512KiB — so the burst must be at least
// one full part regardless of the configured rate. Otherwise a small limit such
// as --rate 500K would make burst < part size and fail the very first part.
const minBurst = 1024 * 1024 // 1MiB, >= downloader.MaxPartSize and uploader.MaxPartSize

// NewRateLimiter returns a *rate.Limiter that caps throughput at bytesPerSec,
// or nil when bytesPerSec <= 0 (meaning "unlimited"). A nil limiter is the
// agreed "pass through, do not throttle" signal used by the downloader and
// uploader, so callers can build it unconditionally and let the transfer code
// treat nil as a no-op.
func NewRateLimiter(bytesPerSec int64) *rate.Limiter {
	if bytesPerSec <= 0 {
		return nil
	}

	return rate.NewLimiter(rate.Limit(bytesPerSec), burstFor(bytesPerSec))
}

// burstFor returns the burst (bucket capacity) for a transfer limiter at
// bytesPerSec, never below minBurst so WaitN never rejects a full part.
func burstFor(bytesPerSec int64) int {
	if int(bytesPerSec) > minBurst {
		return int(bytesPerSec)
	}
	return minBurst
}

// NewSharedRateLimiter returns an always-non-nil *rate.Limiter for callers that
// adjust the rate at runtime (the web UI). Unlimited is represented as rate.Inf
// (WaitN never blocks) rather than a nil limiter, so the same limiter object can
// be reconfigured in place without swapping the pointer that in-flight readers
// already hold.
func NewSharedRateLimiter(bytesPerSec int64) *rate.Limiter {
	l := rate.NewLimiter(rate.Inf, minBurst)
	SetRateLimit(l, bytesPerSec)
	return l
}

// SetRateLimit reconfigures an existing limiter in place: bytesPerSec <= 0 means
// unlimited (rate.Inf). Burst is raised before the limit when tightening so a
// concurrent WaitN never observes a burst smaller than one part.
func SetRateLimit(l *rate.Limiter, bytesPerSec int64) {
	if l == nil {
		return
	}
	if bytesPerSec <= 0 {
		l.SetLimit(rate.Inf)
		return
	}
	l.SetBurst(burstFor(bytesPerSec))
	l.SetLimit(rate.Limit(bytesPerSec))
}

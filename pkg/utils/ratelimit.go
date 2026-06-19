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

	burst := minBurst
	if int(bytesPerSec) > burst {
		burst = int(bytesPerSec)
	}

	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

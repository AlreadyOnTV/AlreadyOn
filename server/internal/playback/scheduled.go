package playback

import (
	"errors"
	"math"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/library"
	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

// ScheduledJob prepares the current entry using the server clock. It includes
// SourceOffsetSeconds so programs split by a commercial resume in the file.
func ScheduledJob(entry schedule.Entry, clock schedule.Clock, path string, source library.ProbeResult, plan Plan, outputDir string) (Job, error) {
	now := clock.Now()
	if now.Before(entry.StartsAt) || !now.Before(entry.EndsAt) || math.IsNaN(entry.SourceOffsetSeconds) || math.IsInf(entry.SourceOffsetSeconds, 0) || entry.SourceOffsetSeconds < 0 || entry.SourceOffsetSeconds > 48*3600 {
		return Job{}, errors.New("playback: entry is not currently playable")
	}
	offset := now.Sub(entry.StartsAt) + time.Duration(entry.SourceOffsetSeconds*float64(time.Second))
	duration := entry.EndsAt.Sub(now)
	if source.DurationSeconds <= 0 || math.IsNaN(source.DurationSeconds) || math.IsInf(source.DurationSeconds, 0) || source.DurationSeconds > 48*3600 || offset.Seconds() >= source.DurationSeconds {
		return Job{}, errors.New("playback: scheduled offset exceeds source")
	}
	duration = min(duration, time.Duration(source.DurationSeconds*float64(time.Second))-offset)
	return Job{Entry: entry, Path: path, Source: source, Plan: plan, Offset: offset, Duration: duration, OutputDir: outputDir}, nil
}

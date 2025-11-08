package archiver

import "time"

type Window struct {
	Start time.Time
	End   time.Time
}

func LastClosedHourUTC(now time.Time) Window {
	end := now.UTC().Truncate(time.Hour)
	return Window{Start: end.Add(-time.Hour), End: end}
}

type RetentionPolicy struct {
	ArchiveSleepSecs   int
	BackpressureFreeMB int
}

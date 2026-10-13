package loudness

import (
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/activity"
)

func TestAnalysisProgressCountsAudioTimeAcrossWrites(t *testing.T) {
	id, finish := activity.Default.Start("test loudness", "seconds", 120)
	defer finish()
	p := &analysisProgress{task: id, total: 120}
	for _, text := range []string{"out_time_", "us=30000000\nprogress=continue\nout_time_us=N/A\n"} {
		if _, err := p.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range activity.Default.Snapshot(time.Now()) {
		if row.ID == id {
			if row.Completed != 30 || row.Percent == nil || *row.Percent != 25 {
				t.Fatalf("%+v", row)
			}
			return
		}
	}
	t.Fatal("missing activity")
}

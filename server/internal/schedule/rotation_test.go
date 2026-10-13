package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/channel"
	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
)

func testRotationSeries() []rotationSeries {
	return []rotationSeries{
		{Key: "alpha", Items: []lineupItem{{MediaFileID: 1, DurationSeconds: 600, MediaType: "episode"}, {MediaFileID: 2, DurationSeconds: 600, MediaType: "episode"}, {MediaFileID: 3, DurationSeconds: 600, MediaType: "episode"}}},
		{Key: "beta", Items: []lineupItem{{MediaFileID: 11, DurationSeconds: 600, MediaType: "episode"}, {MediaFileID: 12, DurationSeconds: 600, MediaType: "episode"}}},
	}
}

func TestRotationSequentialWrapAndFairBlocks(t *testing.T) {
	series := testRotationSeries()
	state := rotationState{ChannelID: 73}
	last := map[string]int64{}
	blocks, doubles := 0, 0
	for i := 0; i < 30000; i++ {
		newBlock := state.Remaining == 0
		previousShow := state.Show
		it := state.next(series, nil, commercial.Settings{})
		if newBlock {
			blocks++
			if state.Remaining == 1 {
				doubles++
			}
			if state.Show == previousShow {
				t.Fatal("did not switch shows after block")
			}
		}
		if previous, ok := last[state.Show]; ok {
			expected := previous + 1
			if previous == 3 {
				expected = 1
			}
			if previous == 12 {
				expected = 11
			}
			if it.MediaFileID != expected {
				t.Fatalf("nonsequential %d after %d", it.MediaFileID, previous)
			}
		}
		last[state.Show] = it.MediaFileID
	}
	ratio := float64(doubles) / float64(blocks)
	if ratio < .48 || ratio > .52 {
		t.Fatalf("double episode ratio: %.3f", ratio)
	}
}

func TestRotationIndependentChannelsAndCheckpointReplay(t *testing.T) {
	series := testRotationSeries()
	starts := map[int64]bool{}
	for id := int64(1); id <= 30; id++ {
		state := rotationState{ChannelID: id}
		starts[state.next(series, nil, commercial.Settings{}).MediaFileID] = true
	}
	if len(starts) < 4 {
		t.Fatalf("channel starts lack variation: %v", starts)
	}
	state := rotationState{ChannelID: 7}
	state.next(series, nil, commercial.Settings{})
	raw, _ := json.Marshal(state)
	var restored rotationState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		a, b := state.next(series, nil, commercial.Settings{}), restored.next(series, nil, commercial.Settings{})
		if a != b {
			t.Fatal("checkpoint changed future")
		}
	}
}

func TestRotationSingleShowKeepsLooping(t *testing.T) {
	state := rotationState{ChannelID: 10}
	series := testRotationSeries()[:1]
	previous := int64(0)
	for i := 0; i < 30; i++ {
		item := state.next(series, nil, commercial.Settings{})
		if i > 0 && item.MediaFileID != previous%3+1 {
			t.Fatalf("bad wrap: %d to %d", previous, item.MediaFileID)
		}
		previous = item.MediaFileID
	}
}

func TestRotationRegeneratePreservesTimelineAndSeasonOrder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := setupChannel(t, db, 1000, 1200, 1200, 1200, 1200, 1200, 1200)
	// Deliberately assign out-of-order season/episode metadata to the file IDs.
	order := [][2]int{{2, 2}, {1, 2}, {2, 1}, {1, 1}, {3, 1}, {2, 3}}
	rows, err := db.Query(`SELECT media_file_id FROM channel_items WHERE channel_id=? ORDER BY position`, id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v)
	}
	rows.Close()
	for i, file := range ids {
		if _, err := db.Exec(`INSERT INTO identified_items(media_file_id,media_type,title,series_title,season_number,episode_number) VALUES (?,'episode','Episode','Same Show',?,?)`, file, order[i][0], order[i][1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := channel.SetFilter(ctx, db, id, channel.Filter{MediaType: "tv", AudienceTier: "adult"}); err != nil {
		t.Fatal(err)
	}
	if err := Regenerate(ctx, db, id, testAnchor); err != nil {
		t.Fatal(err)
	}
	clock := FixedClock{Time: testAnchor}
	before, err := Upcoming(ctx, db, clock, id, 100)
	if err != nil {
		t.Fatal(err)
	}
	natural := []int64{ids[3], ids[1], ids[2], ids[0], ids[5], ids[4]}
	for i := 1; i < len(before); i++ {
		for j, file := range natural {
			if file == before[i-1].MediaFileID && before[i].MediaFileID != natural[(j+1)%len(natural)] {
				t.Fatal("episode order or finale wrap broken")
			}
		}
	}
	if err := Regenerate(ctx, db, id, testAnchor.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := Upcoming(ctx, db, clock, id, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refresh changed current or upcoming episodes")
	}
	// Moving forward through several maintenance intervals cannot reset to
	// the channel's randomized initial episode.
	for _, advance := range []time.Duration{time.Hour, 8 * time.Hour, 25 * time.Hour} {
		when := testAnchor.Add(advance)
		expected, err := Upcoming(ctx, db, FixedClock{Time: when}, id, 12)
		if err != nil {
			t.Fatal(err)
		}
		if err := Regenerate(ctx, db, id, when); err != nil {
			t.Fatal(err)
		}
		got, err := Upcoming(ctx, db, FixedClock{Time: when}, id, 12)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(expected, got) {
			t.Fatalf("refresh at %v reset progress", advance)
		}
	}
}

func TestRotationCommercialExpansionAndMidBreakCheckpoint(t *testing.T) {
	for _, mode := range []commercial.Mode{commercial.ModeBetweenOnly, commercial.ModeInterval} {
		t.Run(string(mode), func(t *testing.T) {
			series := testRotationSeries()
			settings := commercial.Settings{Enabled: true, Mode: mode, IntervalMinutes: 15, TVCommercialsPerBreak: 2, MovieCommercialsPerBreak: 3}
			ads := []lineupItem{program(100, 30), program(101, 45), program(102, 20)}
			bare := rotationState{ChannelID: 83}
			var programs []lineupItem
			for i := 0; i < 30; i++ {
				programs = append(programs, bare.next(series, nil, commercial.Settings{}))
			}
			expected := expand(programs, ads, settings)
			state := rotationState{ChannelID: 83}
			for i, want := range expected {
				got := state.next(series, ads, settings)
				if got != want {
					t.Fatalf("segment %d: got %+v want %+v", i, got, want)
				}
				// Round-trip every segment, including mid-program breaks.
				raw, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				var restored rotationState
				if err = json.Unmarshal(raw, &restored); err != nil {
					t.Fatal(err)
				}
				state = restored
			}
		})
	}
}

func TestMovieRotationCompleteCyclesAndCheckpoints(t *testing.T) {
	var series []rotationSeries
	for i := int64(1); i <= 90; i++ {
		series = append(series, rotationSeries{Key: fmt.Sprint("movie:", i), Items: []lineupItem{{MediaFileID: i, MediaType: "movie", DurationSeconds: 7200}}})
	}
	starts := map[int64]bool{}
	for channelID := int64(1); channelID <= 10; channelID++ {
		state := rotationState{ChannelID: channelID}
		var previous int64
		for cycle := 0; cycle < 3; cycle++ {
			seen := map[int64]bool{}
			for i := range series {
				item := state.next(series, nil, commercial.Settings{})
				if seen[item.MediaFileID] {
					t.Fatal("movie repeated before pool exhausted")
				}
				if item.MediaFileID == previous {
					t.Fatal("immediate repeat at cycle boundary")
				}
				if cycle == 0 && i == 0 {
					starts[item.MediaFileID] = true
				}
				seen[item.MediaFileID] = true
				previous = item.MediaFileID
				// Simulate loading the persisted checkpoint after every movie.
				raw, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				state = rotationState{}
				if err := json.Unmarshal(raw, &state); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if len(starts) < 2 {
		t.Fatal("channels share the same movie order")
	}
}

func TestMovieRotationRefreshPreservesProgress(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := setupChannel(t, db, 1000, 7200, 7200, 7200, 7200, 7200)
	if _, err := db.Exec(`INSERT INTO identified_items(media_file_id,media_type,title)
 SELECT media_file_id,'movie','Film' FROM channel_items WHERE channel_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.SetFilter(ctx, db, id, channel.Filter{MediaType: "movie", AudienceTier: "adult"}); err != nil {
		t.Fatal(err)
	}
	if err := Regenerate(ctx, db, id, testAnchor); err != nil {
		t.Fatal(err)
	}
	for _, advance := range []time.Duration{time.Minute, 8 * time.Hour, 25 * time.Hour} {
		when := testAnchor.Add(advance)
		before, err := Upcoming(ctx, db, FixedClock{Time: when}, id, 8)
		if err != nil {
			t.Fatal(err)
		}
		if err := Regenerate(ctx, db, id, when); err != nil {
			t.Fatal(err)
		}
		after, err := Upcoming(ctx, db, FixedClock{Time: when}, id, 8)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("movie progress reset on refresh")
		}
	}
}

func TestLargeMovieRotationBoundedCheckpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("scale rotation")
	}
	const count = 5000
	series := make([]rotationSeries, count)
	for i := range series {
		series[i] = rotationSeries{Key: fmt.Sprintf("movie:%d", i+1), Items: []lineupItem{{MediaFileID: int64(i + 1), MediaType: "movie", DurationSeconds: 7200}}}
	}
	state := rotationState{ChannelID: 124}
	start := time.Now()
	for cycle := 0; cycle < 2; cycle++ {
		seen := map[int64]bool{}
		for i := 0; i < count; i++ {
			item := state.next(series, nil, commercial.Settings{})
			if seen[item.MediaFileID] {
				t.Fatal("repeat before exhaustion")
			}
			seen[item.MediaFileID] = true
			if i%100 == 0 {
				raw, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) > 1024 {
					t.Fatalf("checkpoint grew to %d bytes", len(raw))
				}
				state = rotationState{}
				if err := json.Unmarshal(raw, &state); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	elapsed := time.Since(start)
	t.Logf("5000 movies, 2 cycles, 100 checkpoint reloads: %v", elapsed)
	if elapsed > 30*time.Second {
		t.Fatalf("rotation exceeded 30s budget: %v", elapsed)
	}
}

func TestRotationSmallCommercialPoolDoesNotRepeatWithinBreak(t *testing.T) {
	settings := commercial.Settings{Enabled: true, Mode: commercial.ModeBetweenOnly, TVCommercialsPerBreak: 5, MovieCommercialsPerBreak: 5}
	state := rotationState{ChannelID: 83}
	series := testRotationSeries()
	ads := []lineupItem{program(100, 30), program(101, 45)}
	seen := map[int64]bool{}
	for range 60 {
		item := state.next(series, ads, settings)
		if item.Kind == KindProgram {
			seen = map[int64]bool{}
		} else {
			if seen[item.MediaFileID] {
				t.Fatal("commercial repeated within a break")
			}
			seen[item.MediaFileID] = true
		}
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		var restored rotationState
		if err := json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		state = restored
	}
}

// Twin channels keep a common catalog but must not replay an identical rotation.
func TestTwinChannelsUseIndependentRotations(t *testing.T) {
	series := testRotationSeries()
	first, second := rotationState{ChannelID: 109}, rotationState{ChannelID: 110}
	different := false
	for i := 0; i < 40; i++ {
		a := first.next(series, nil, commercial.Settings{})
		b := second.next(series, nil, commercial.Settings{})
		if a.MediaFileID != b.MediaFileID {
			different = true
		}
	}
	if !different {
		t.Fatal("twin channels share a synchronized rotation")
	}
}

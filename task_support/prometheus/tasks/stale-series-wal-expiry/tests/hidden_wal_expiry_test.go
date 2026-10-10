package tsdb

import (
	"context"
	"math"
	"testing"

	prom_testutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/value"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/chunks"
	"github.com/prometheus/prometheus/tsdb/record"
	"github.com/prometheus/prometheus/tsdb/wlog"
)

type micro1WALFixture struct {
	db          *DB
	selected    labels.Labels
	active      labels.Labels
	selectedRef storage.SeriesRef
	activeRef   storage.SeriesRef
}

func newMicro1WALFixture(t *testing.T, opts *Options) micro1WALFixture {
	t.Helper()

	db := newTestDB(t, withOpts(opts))
	db.DisableCompactions()

	stale := math.Float64frombits(value.StaleNaN)
	selected := labels.FromStrings("name", "micro1-selected")
	active := labels.FromStrings("name", "micro1-active")
	app := db.Appender(context.Background())
	selectedRef, err := app.Append(0, selected, 100, 1)
	require.NoError(t, err)
	_, err = app.Append(selectedRef, selected, 200, stale)
	require.NoError(t, err)
	activeRef, err := app.Append(0, active, 100, 7)
	require.NoError(t, err)
	require.NoError(t, app.Commit())

	return micro1WALFixture{db: db, selected: selected, active: active, selectedRef: selectedRef, activeRef: activeRef}
}

func micro1CheckpointAt(t *testing.T, db *DB, mint int64) string {
	t.Helper()
	_, previous, err := wlog.LastCheckpoint(db.head.wal.Dir())
	if err != nil {
		previous = -1
	}
	for range 10 {
		db.head.lastWALTruncationTime.Store(0)
		require.NoError(t, db.head.truncateWAL(mint))
		checkpoint, index, err := wlog.LastCheckpoint(db.head.wal.Dir())
		if err == nil && index > previous {
			return checkpoint
		}
	}
	t.Fatal("a new WAL checkpoint was not produced")
	return ""
}

func micro1CheckpointRefs(t *testing.T, checkpoint string) map[storage.SeriesRef]bool {
	t.Helper()
	refs := map[storage.SeriesRef]bool{}
	for _, entry := range readTestWAL(t, checkpoint) {
		series, ok := entry.([]record.RefSeries)
		if !ok {
			continue
		}
		for _, item := range series {
			refs[storage.SeriesRef(item.Ref)] = true
		}
	}
	return refs
}

func TestMicro1EvictedSeriesRecordSurvivesCheckpointAndReplay(t *testing.T) {
	opts := DefaultOptions()
	opts.MinBlockDuration = 1000
	opts.MaxBlockDuration = 1000
	fixture := newMicro1WALFixture(t, opts)
	db := fixture.db

	require.Equal(t, uint64(1), db.Head().NumStaleSeries())

	require.NoError(t, db.CompactStaleHead())

	app := db.Appender(context.Background())
	_, err := app.Append(fixture.activeRef, fixture.active, 250, 8)
	require.NoError(t, err)
	require.NoError(t, app.Commit())

	refs := micro1CheckpointRefs(t, micro1CheckpointAt(t, db, 150))
	require.True(t, refs[fixture.selectedRef], "checkpoint dropped labels still referenced by WAL samples")
	require.True(t, refs[fixture.activeRef], "checkpoint dropped the active control series")

	dir := db.Dir()
	require.NoError(t, db.Close())
	db = newTestDB(t, withDir(dir), withOpts(opts))
	for _, recordType := range []string{"series", "samples", "exemplars", "histograms", "metadata", "tombstones"} {
		require.Zero(t, prom_testutil.ToFloat64(db.Head().metrics.walReplayUnknownRefsTotal.WithLabelValues(recordType)), "unknown %s references during replay", recordType)
	}
	require.NotNil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.activeRef)), "active control series was lost after replay")
}

func TestMicro1EvictedSeriesRecordSurvivesAtExactExpiryBoundary(t *testing.T) {
	opts := DefaultOptions()
	opts.MinBlockDuration = 1000
	opts.MaxBlockDuration = 1000
	fixture := newMicro1WALFixture(t, opts)
	db := fixture.db
	require.NoError(t, db.CompactStaleHead())

	app := db.Appender(context.Background())
	_, err := app.Append(fixture.activeRef, fixture.active, 250, 8)
	require.NoError(t, err)
	require.NoError(t, app.Commit())

	refs := micro1CheckpointRefs(t, micro1CheckpointAt(t, db, 200))
	require.True(t, refs[fixture.selectedRef], "checkpoint dropped labels at the inclusive WAL retention boundary")
	require.True(t, refs[fixture.activeRef], "checkpoint dropped the active control series")

	dir := db.Dir()
	require.NoError(t, db.Close())
	db = newTestDB(t, withDir(dir), withOpts(opts))
	for _, recordType := range []string{"series", "samples", "exemplars", "histograms", "metadata", "tombstones"} {
		require.Zero(t, prom_testutil.ToFloat64(db.Head().metrics.walReplayUnknownRefsTotal.WithLabelValues(recordType)), "unknown %s references during replay", recordType)
	}
	require.NotNil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.activeRef)), "active control series was lost after replay")
}

func TestMicro1EvictedSeriesRecordExpiresFromCheckpoint(t *testing.T) {
	opts := DefaultOptions()
	opts.MinBlockDuration = 1000
	opts.MaxBlockDuration = 1000
	fixture := newMicro1WALFixture(t, opts)
	db := fixture.db
	require.NoError(t, db.CompactStaleHead())

	app := db.Appender(context.Background())
	_, err := app.Append(fixture.activeRef, fixture.active, 250, 8)
	require.NoError(t, err)
	require.NoError(t, app.Commit())
	micro1CheckpointAt(t, db, 150)

	refs := micro1CheckpointRefs(t, micro1CheckpointAt(t, db, 201))
	require.False(t, refs[fixture.selectedRef], "expired labels remained in the durable checkpoint")
	require.True(t, refs[fixture.activeRef], "active control labels expired with the stale series")

	dir := db.Dir()
	require.NoError(t, db.Close())
	db = newTestDB(t, withDir(dir), withOpts(opts))
	require.Nil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.selectedRef)), "expired series returned after replay")
	require.NotNil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.activeRef)), "active control series was lost after replay")
}

func TestMicro1EvictedSeriesRecordExpiresAfterReplay(t *testing.T) {
	opts := DefaultOptions()
	opts.MinBlockDuration = 1000
	opts.MaxBlockDuration = 1000
	fixture := newMicro1WALFixture(t, opts)
	db := fixture.db
	require.NoError(t, db.CompactStaleHead())

	app := db.Appender(context.Background())
	_, err := app.Append(fixture.activeRef, fixture.active, 250, 8)
	require.NoError(t, err)
	require.NoError(t, app.Commit())
	micro1CheckpointAt(t, db, 150)

	// Reopening must not extend a deleted series' retention to another
	// series' latest sample. Its last sample is still at 200.
	reopenDir := db.Dir()
	require.NoError(t, db.Close())
	db = newTestDB(t, withDir(reopenDir), withOpts(opts))
	db.DisableCompactions()

	refs := micro1CheckpointRefs(t, micro1CheckpointAt(t, db, 201))
	require.False(t, refs[fixture.selectedRef], "expired labels remained in the durable checkpoint")
	require.True(t, refs[fixture.activeRef], "active control labels expired with the stale series")

	dir := db.Dir()
	require.NoError(t, db.Close())
	db = newTestDB(t, withDir(dir), withOpts(opts))
	require.Nil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.selectedRef)), "expired series returned after replay")
	require.NotNil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.activeRef)), "active control series was lost after replay")
}

func newMicro1BeforeEpochWALFixture(t *testing.T, opts *Options) micro1WALFixture {
	t.Helper()

	db := newTestDB(t, withOpts(opts))
	db.DisableCompactions()

	stale := math.Float64frombits(value.StaleNaN)
	selected := labels.FromStrings("name", "micro1-before-epoch-selected")
	active := labels.FromStrings("name", "micro1-before-epoch-active")
	app := db.Appender(context.Background())
	selectedRef, err := app.Append(0, selected, -1000, 1)
	require.NoError(t, err)
	_, err = app.Append(selectedRef, selected, -900, stale)
	require.NoError(t, err)
	activeRef, err := app.Append(0, active, -1000, 7)
	require.NoError(t, err)
	require.NoError(t, app.Commit())

	return micro1WALFixture{db: db, selected: selected, active: active, selectedRef: selectedRef, activeRef: activeRef}
}

func micro1BeforeEpochCheckpointAt(t *testing.T, db *DB, mint int64) string {
	t.Helper()
	_, previous, err := wlog.LastCheckpoint(db.head.wal.Dir())
	if err != nil {
		previous = -1
	}
	for range 10 {
		db.head.lastWALTruncationTime.Store(math.MinInt64)
		require.NoError(t, db.head.truncateWAL(mint))
		checkpoint, index, err := wlog.LastCheckpoint(db.head.wal.Dir())
		if err == nil && index > previous {
			return checkpoint
		}
	}
	t.Fatal("a new WAL checkpoint was not produced")
	return ""
}

func TestMicro1EvictedSeriesRecordExpiresBeforeEpoch(t *testing.T) {
	opts := DefaultOptions()
	opts.MinBlockDuration = 1000
	opts.MaxBlockDuration = 1000
	fixture := newMicro1BeforeEpochWALFixture(t, opts)
	db := fixture.db
	require.NoError(t, db.CompactStaleHead())

	app := db.Appender(context.Background())
	_, err := app.Append(fixture.activeRef, fixture.active, -850, 8)
	require.NoError(t, err)
	require.NoError(t, app.Commit())
	micro1BeforeEpochCheckpointAt(t, db, -950)

	refs := micro1CheckpointRefs(t, micro1BeforeEpochCheckpointAt(t, db, -899))
	retained := refs[fixture.selectedRef]
	t.Logf("negative timestamp checkpoint retained expired label=%t", retained)
	require.True(t, refs[fixture.activeRef], "active control labels expired with the stale series")

	dir := db.Dir()
	require.NoError(t, db.Close())
	db = newTestDB(t, withDir(dir), withOpts(opts))

	require.NotNil(t, db.Head().series.getByID(chunks.HeadSeriesRef(fixture.activeRef)), "active control series was lost after replay")
	q, err := db.Querier(-1000, -900)
	require.NoError(t, err)
	defer q.Close()
	set := q.Select(context.Background(), false, nil, labels.MustNewMatcher(labels.MatchEqual, "name", "micro1-before-epoch-selected"))
	require.True(t, set.Next())
	it := set.At().Iterator(nil)
	require.Equal(t, chunkenc.ValFloat, it.Next())
	ts, v := it.At()
	require.Equal(t, int64(-1000), ts)
	require.Equal(t, float64(1), v)
	require.NoError(t, set.Err())
	t.Log("compacted pre-epoch sample remains queryable after restart")
	require.False(t, retained, "expired labels remained in the durable checkpoint")
}

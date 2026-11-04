package labelstate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"google.golang.org/protobuf/proto"
)

func editAction(name string, color, order int32, deleted bool) *waSyncAction.LabelEditAction {
	return &waSyncAction.LabelEditAction{
		Name:       proto.String(name),
		Color:      proto.Int32(color),
		Deleted:    proto.Bool(deleted),
		OrderIndex: proto.Int32(order),
		IsActive:   proto.Bool(!deleted),
		Type:       waSyncAction.LabelEditAction_CUSTOM.Enum(),
	}
}

func TestReplaceLastRecordPerKeyWinsAndMarksSynced(t *testing.T) {
	s := newState()
	require.False(t, s.Synced())

	t0 := time.Unix(1_700_000_000, 0)
	s.Replace(
		[]Label{
			{LabelID: "13", Action: editAction("Old", 1, 0, false), Timestamp: t0},
			{LabelID: "13", Action: editAction("New", 2, 0, false), Timestamp: t0.Add(time.Second)},
			{LabelID: "2", Action: editAction("Two", 3, 1, false), Timestamp: t0},
		},
		[]Association{
			{RawJID: "111@lid", LabelID: "13", Labeled: true, Timestamp: t0},
			{RawJID: "111@lid", LabelID: "13", Labeled: false, Timestamp: t0.Add(time.Second)},
			{RawJID: "222@s.whatsapp.net", LabelID: "2", Labeled: true, Timestamp: t0},
		},
	)

	require.True(t, s.Synced())
	labels := s.Labels()
	require.Len(t, labels, 2)
	assert.Equal(t, "2", labels[0].LabelID, "numeric sort, not string sort")
	assert.Equal(t, "13", labels[1].LabelID)
	assert.Equal(t, "New", labels[1].Action.GetName())

	assocs := s.Associations()
	require.Len(t, assocs, 2)
	assert.Equal(t, Association{RawJID: "222@s.whatsapp.net", LabelID: "2", Labeled: true, Timestamp: t0}, assocs[0])
	assert.Equal(t, "111@lid", assocs[1].RawJID)
	assert.False(t, assocs[1].Labeled, "the later record for the same key must win")
}

func TestIncrementalAppliesAndResetClears(t *testing.T) {
	s := newState()
	now := time.Now()

	s.ApplyLabelEdit("4", editAction("ATENCION HUMANA", 5, 0, false), now)
	s.ApplyLabelEdit("", editAction("ignored", 0, 0, false), now)
	s.ApplyLabelEdit("5", nil, now)
	s.ApplyAssociation("999@lid", "4", true, now)
	s.ApplyAssociation("", "4", true, now)
	s.ApplyAssociation("999@lid", "", true, now)

	assert.False(t, s.Synced(), "incremental events never mark the cache synced")
	require.Len(t, s.Labels(), 1)
	require.Len(t, s.Associations(), 1)

	l, ok := s.Label("4")
	require.True(t, ok)
	assert.Equal(t, "ATENCION HUMANA", l.Action.GetName())
	_, ok = s.Label("5")
	assert.False(t, ok)

	s.Reset()
	assert.False(t, s.Synced())
	assert.Empty(t, s.Labels())
	assert.Empty(t, s.Associations())
}

func TestLabelReturnsCopies(t *testing.T) {
	s := newState()
	action := editAction("Orig", 1, 0, false)
	s.ApplyLabelEdit("7", action, time.Now())

	// Mutating the caller's action after applying must not leak in.
	action.Name = proto.String("Changed outside")
	l, _ := s.Label("7")
	assert.Equal(t, "Orig", l.Action.GetName())

	// Mutating the returned copy must not leak back.
	l.Action.Name = proto.String("Changed on copy")
	again, _ := s.Label("7")
	assert.Equal(t, "Orig", again.Action.GetName())
	assert.Equal(t, "Orig", s.Labels()[0].Action.GetName())
}

func TestNextLabelIDAndOrderIndex(t *testing.T) {
	s := newState()
	assert.Equal(t, "1", s.NextLabelID())
	assert.Equal(t, int32(0), s.NextOrderIndex())

	now := time.Now()
	s.ApplyLabelEdit("13", editAction("A", 1, 0, false), now)
	s.ApplyLabelEdit("15", editAction("Deleted", 1, 9, true), now)
	s.ApplyLabelEdit("14", editAction("B", 1, 3, false), now)
	s.ApplyLabelEdit("weird", editAction("C", 1, 2, false), now)

	assert.Equal(t, "16", s.NextLabelID(), "deleted ids stay taken; non-numeric ids are ignored")
	assert.Equal(t, int32(4), s.NextOrderIndex(), "deleted labels do not count for order_index")
}

func TestNormalizeEventTime(t *testing.T) {
	now := time.Now()

	// whatsmeow builds the event time as time.UnixMilli(raw).
	fromRaw := func(raw int64) time.Time { return time.UnixMilli(raw) }

	zero := NormalizeEventTime(fromRaw(0))
	assert.False(t, zero.Before(now.Add(-time.Second)), "zero falls back to now")

	secs := NormalizeEventTime(fromRaw(1_700_000_000))
	assert.Equal(t, time.Unix(1_700_000_000, 0), secs, "values below 1e12 are seconds")

	ms := NormalizeEventTime(fromRaw(1_700_000_000_000))
	assert.Equal(t, time.UnixMilli(1_700_000_000_000), ms, "values at or above 1e12 are milliseconds")

	negative := NormalizeEventTime(fromRaw(-5))
	assert.False(t, negative.Before(now.Add(-time.Second)))
}

func TestRegistryIsKeyedAndForgettable(t *testing.T) {
	keyA, keyB := new(int), new(int)
	a := For(keyA)
	assert.Same(t, a, For(keyA))
	assert.NotSame(t, a, For(keyB))

	a.ApplyLabelEdit("1", editAction("x", 0, 0, false), time.Now())
	Forget(keyA)
	assert.Empty(t, For(keyA).Labels(), "Forget drops the cache for that key")

	assert.NotNil(t, For(nil), "a nil key still yields a usable state")
	Forget(nil)
}

func TestLockIsAMutex(t *testing.T) {
	s := newState()
	s.Lock()
	acquired := make(chan struct{})
	go func() {
		s.Lock()
		close(acquired)
		s.Unlock()
	}()
	select {
	case <-acquired:
		t.Fatal("second Lock must block while the first is held")
	case <-time.After(50 * time.Millisecond):
	}
	s.Unlock()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second Lock must proceed after Unlock")
	}
}

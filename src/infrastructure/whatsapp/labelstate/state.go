// Package labelstate keeps an in-memory, per-client view of the WhatsApp
// (Business) labels and chat<->label associations of the `regular` app state
// collection.
//
// Fork-specific. whatsmeow does not persist label names or associations (its
// store only keeps the collection version, hash and mutation MACs), so the
// only ways to know the current labels are a full resync of the collection
// (see whatsapp.FullSyncLabels) or the incremental events it emits
// afterwards. This package is the cache fed by both, plus the per-client
// mutex that serializes full syncs with app state writes: SendAppState reads
// the collection version without the sync lock, so a write racing a full
// sync (which resets the version to 0 before refetching) can be rejected.
package labelstate

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"google.golang.org/protobuf/proto"
)

// Label is the last known `label_edit` record of a label id.
type Label struct {
	LabelID string
	// Action is the full LabelEditAction as last seen. Never nil. A SET on
	// `label_edit` replaces the whole record, so edits must resend every
	// field: keep this around to build them.
	Action    *waSyncAction.LabelEditAction
	Timestamp time.Time
}

// Association is the last known `label_jid` record for one raw index key.
// The phone writes the chat's LID, this fork historically wrote the phone
// number: both forms can coexist for the same chat, so the raw JID is kept
// verbatim and normalization/dedup is left to the reader.
type Association struct {
	RawJID    string
	LabelID   string
	Labeled   bool
	Timestamp time.Time
}

// State is the cache for one whatsmeow client.
type State struct {
	opMu sync.Mutex // serializes full syncs and app state writes (Lock/Unlock)

	mu     sync.RWMutex
	synced bool
	labels map[string]Label
	assocs map[string]Association
}

func newState() *State {
	return &State{
		labels: make(map[string]Label),
		assocs: make(map[string]Association),
	}
}

// Lock acquires the per-client operation mutex. Hold it around a full sync
// and around every SendAppState on the `regular` collection.
func (s *State) Lock() { s.opMu.Lock() }

// Unlock releases the operation mutex.
func (s *State) Unlock() { s.opMu.Unlock() }

// Synced reports whether the cache was populated by a full sync at least
// once. Incremental events are applied either way, but until a full sync has
// run the cache is partial.
func (s *State) Synced() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.synced
}

// Replace swaps the whole cache for the result of a full sync and marks it
// synced. Records are applied in order, so the last one per key wins.
func (s *State) Replace(labels []Label, assocs []Association) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labels = make(map[string]Label, len(labels))
	s.assocs = make(map[string]Association, len(assocs))
	for _, l := range labels {
		s.applyLabelLocked(l.LabelID, l.Action, l.Timestamp)
	}
	for _, a := range assocs {
		s.applyAssociationLocked(a.RawJID, a.LabelID, a.Labeled, a.Timestamp)
	}
	s.synced = true
}

// Reset empties the cache and clears the synced flag.
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labels = make(map[string]Label)
	s.assocs = make(map[string]Association)
	s.synced = false
}

// ApplyLabelEdit records an incremental `label_edit` SET. A nil action is
// ignored.
func (s *State) ApplyLabelEdit(labelID string, action *waSyncAction.LabelEditAction, ts time.Time) {
	if labelID == "" || action == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyLabelLocked(labelID, action, ts)
}

func (s *State) applyLabelLocked(labelID string, action *waSyncAction.LabelEditAction, ts time.Time) {
	if labelID == "" || action == nil {
		return
	}
	s.labels[labelID] = Label{
		LabelID:   labelID,
		Action:    proto.Clone(action).(*waSyncAction.LabelEditAction),
		Timestamp: ts,
	}
}

// ApplyAssociation records an incremental `label_jid` SET for one raw index
// key.
func (s *State) ApplyAssociation(rawJID, labelID string, labeled bool, ts time.Time) {
	if rawJID == "" || labelID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyAssociationLocked(rawJID, labelID, labeled, ts)
}

func (s *State) applyAssociationLocked(rawJID, labelID string, labeled bool, ts time.Time) {
	if rawJID == "" || labelID == "" {
		return
	}
	s.assocs[assocKey(labelID, rawJID)] = Association{
		RawJID:    rawJID,
		LabelID:   labelID,
		Labeled:   labeled,
		Timestamp: ts,
	}
}

func assocKey(labelID, rawJID string) string {
	return labelID + "\x00" + rawJID
}

// Label returns a copy of the last known record of a label id.
func (s *State) Label(labelID string) (Label, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.labels[labelID]
	if !ok {
		return Label{}, false
	}
	l.Action = proto.Clone(l.Action).(*waSyncAction.LabelEditAction)
	return l, true
}

// Labels returns every known label (deleted ones included), sorted by
// numeric id (non-numeric ids last, sorted as strings).
func (s *State) Labels() []Label {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Label, 0, len(s.labels))
	for _, l := range s.labels {
		l.Action = proto.Clone(l.Action).(*waSyncAction.LabelEditAction)
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return lessLabelID(out[i].LabelID, out[j].LabelID) })
	return out
}

// Associations returns every known raw association record, sorted by label
// id then raw JID.
func (s *State) Associations() []Association {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Association, 0, len(s.assocs))
	for _, a := range s.assocs {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LabelID != out[j].LabelID {
			return lessLabelID(out[i].LabelID, out[j].LabelID)
		}
		return out[i].RawJID < out[j].RawJID
	})
	return out
}

// NextLabelID returns max(numeric label ids) + 1 as a string, counting
// deleted labels too (their ids stay taken). "1" when nothing is known.
func (s *State) NextLabelID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var max int64
	for id := range s.labels {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil && n > max {
			max = n
		}
	}
	return strconv.FormatInt(max+1, 10)
}

// NextOrderIndex returns max(order_index of non-deleted labels) + 1, or 0.
func (s *State) NextOrderIndex() int32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var max int32 = -1
	for _, l := range s.labels {
		if l.Action.GetDeleted() {
			continue
		}
		if idx := l.Action.GetOrderIndex(); idx > max {
			max = idx
		}
	}
	return max + 1
}

func lessLabelID(a, b string) bool {
	na, errA := strconv.ParseInt(a, 10, 64)
	nb, errB := strconv.ParseInt(b, 10, 64)
	switch {
	case errA == nil && errB == nil:
		return na < nb
	case errA == nil:
		return true
	case errB == nil:
		return false
	default:
		return a < b
	}
}

// NormalizeEventTime fixes the timestamp whatsmeow attaches to label events.
// The phone stamps `label_edit` mutations with small values (seconds, or a
// counter) that whatsmeow interprets as milliseconds, which is where the
// "1970-01-01" timestamps in `label.edit` webhooks came from. Anything below
// 1e12 ms (2001-09-09 as ms, year 33658 as s) is taken as seconds; zero or
// negative falls back to now.
func NormalizeEventTime(t time.Time) time.Time {
	ms := t.UnixMilli()
	switch {
	case ms <= 0:
		return time.Now()
	case ms < 1e12:
		return time.Unix(ms, 0)
	default:
		return t
	}
}

var registry sync.Map // map[any]*State, keyed by the *whatsmeow.Client

// For returns the State of a client, creating it on first use. Keying by the
// client pointer means a re-login (new client) starts from an empty cache.
func For(key any) *State {
	if key == nil {
		key = struct{}{}
	}
	if v, ok := registry.Load(key); ok {
		return v.(*State)
	}
	v, _ := registry.LoadOrStore(key, newState())
	return v.(*State)
}

// Forget drops the State of a client (on logout).
func Forget(key any) {
	if key == nil {
		return
	}
	registry.Delete(key)
}

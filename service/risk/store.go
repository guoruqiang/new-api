package risk

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/samber/hot"
)

// Store is the refusal memory and subject block storage.
type Store interface {
	ActiveTokenBlock(userID, tokenID int, now int64) (*model.RiskSubjectBlock, error)
	ActiveBlock(namespace, subjectType, value string, now int64) (*model.RiskSubjectBlock, error)
	ExactRefusal(namespace string, userID int, fingerprint string, now int64) (*model.RiskRefusal, error)
	RefusalCandidates(namespace string, userID int, modelName string, limit int, now int64) ([]model.RiskRefusal, error)
	Block(rec *model.RiskSubjectBlock) error
	HitBlock(id int, now int64) error
	HitRefusal(id int, now int64) error
	Remember(rec *model.RiskRefusal) error
}

type dbStore struct{}

// ReviewStore persists pending evidence separately from raw events. A preview
// uses readOnlyStore and never reaches this capability.
type ReviewStore interface {
	CreateModelReview(*model.RiskReview, model.RiskBehaviorPolicy, bool, bool, int64) (*model.RiskReviewResult, error)
}

func (dbStore) CreateModelReview(rec *model.RiskReview, policy model.RiskBehaviorPolicy, autoConfirm, allowAutoBlock bool, now int64) (*model.RiskReviewResult, error) {
	if rec.Layer != LayerModel {
		row, _, err := model.CreateRiskReviewCandidate(rec, policy, now)
		return &model.RiskReviewResult{Review: row}, err
	}
	return model.CreateRiskModelReview(rec, policy, autoConfirm, allowAutoBlock, now)
}

func (dbStore) ActiveTokenBlock(userID, tokenID int, now int64) (*model.RiskSubjectBlock, error) {
	return model.GetActiveRiskTokenBlock(userID, tokenID, now)
}

func (dbStore) ActiveBlock(namespace, subjectType, value string, now int64) (*model.RiskSubjectBlock, error) {
	return model.GetActiveRiskBlock(namespace, subjectType, value, now)
}

func (dbStore) ExactRefusal(namespace string, userID int, fingerprint string, now int64) (*model.RiskRefusal, error) {
	return model.GetRiskRefusal(namespace, userID, fingerprint, now)
}

func (dbStore) RefusalCandidates(namespace string, userID int, modelName string, limit int, now int64) ([]model.RiskRefusal, error) {
	return model.ListRiskRefusalCandidates(namespace, userID, modelName, SignatureVersion, limit, now)
}

func (dbStore) Block(rec *model.RiskSubjectBlock) error { return model.BlockRiskSubject(rec) }
func (dbStore) HitBlock(id int, now int64) error        { return model.HitRiskBlock(id, now) }
func (dbStore) HitRefusal(id int, now int64) error      { return model.HitRiskRefusal(id, now) }
func (dbStore) Remember(rec *model.RiskRefusal) error   { return model.RememberRiskRefusal(rec) }

// readOnlyStore lets previews read records without writing any.
type readOnlyStore struct{ Store }

func (readOnlyStore) Block(*model.RiskSubjectBlock) error { return nil }
func (readOnlyStore) HitBlock(int, int64) error           { return nil }
func (readOnlyStore) HitRefusal(int, int64) error         { return nil }
func (readOnlyStore) Remember(*model.RiskRefusal) error   { return nil }

var defaultStore Store = dbStore{}

// Record is one audited attempt for statistics and the event log.
type Record struct {
	Counter model.RiskCounterKey
	Event   *model.RiskEvent
}

// Recorder persists records without blocking the request.
type Recorder interface {
	Record(Record)
}

type asyncRecorder struct {
	once         sync.Once
	queue        chan Record
	dropped      int64
	mu           sync.Mutex
	stopped      bool
	done         chan struct{}
	err          error
	insertEvents func([]model.RiskEvent) error
	addCounters  func(map[model.RiskCounterKey]int64) error
}

var defaultRecorder Recorder = &asyncRecorder{queue: make(chan Record, 10000)}

func (r *asyncRecorder) Record(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	select {
	case r.queue <- rec:
	default:
		r.dropped++
		dropped := r.dropped
		if dropped == 1 || dropped%1000 == 0 {
			common.SysError(common.LogText("risk recorder queue full, dropped %d records", dropped))
		}
	}
}

// start drains the queue: events every second or 200 rows, counters every
// ten seconds aggregated by key.
func (r *asyncRecorder) start() {
	r.once.Do(func() {
		r.done = make(chan struct{})
		if r.insertEvents == nil {
			r.insertEvents = model.InsertRiskEvents
		}
		if r.addCounters == nil {
			r.addCounters = model.AddRiskCounters
		}
		go func() {
			defer close(r.done)
			events := make([]model.RiskEvent, 0, 200)
			counters := map[model.RiskCounterKey]int64{}
			flushEvents := time.NewTicker(time.Second)
			flushCounters := time.NewTicker(10 * time.Second)
			defer flushEvents.Stop()
			defer flushCounters.Stop()
			writeEvents := func() {
				if len(events) == 0 {
					return
				}
				var err error
				for range 3 {
					err = r.insertEvents(events)
					if err == nil {
						break
					}
				}
				if err != nil {
					if r.err == nil {
						r.err = err
					}
					common.SysError(common.LogText("risk events write failed: %s", err.Error()))
				}
				events = events[:0]
			}
			writeCounters := func() {
				if len(counters) == 0 {
					return
				}
				var err error
				for range 3 {
					err = r.addCounters(counters)
					if err == nil {
						break
					}
				}
				if err != nil {
					if r.err == nil {
						r.err = err
					}
					common.SysError(common.LogText("risk counters write failed: %s", err.Error()))
				}
				counters = map[model.RiskCounterKey]int64{}
			}
			for {
				select {
				case rec, ok := <-r.queue:
					if !ok {
						writeEvents()
						writeCounters()
						return
					}
					counters[rec.Counter]++
					if rec.Event != nil {
						events = append(events, *rec.Event)
						if len(events) >= 200 {
							writeEvents()
						}
					}
				case <-flushEvents.C:
					writeEvents()
				case <-flushCounters.C:
					writeCounters()
				}
			}
		}()
	})
}

// stop closes admission before draining, so concurrent producers cannot send
// to a closed channel. A canceled wait leaves the worker finishing its drain.
func (r *asyncRecorder) stop(ctx context.Context) error {
	r.start()
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StopRecorder drains accepted records and flushes their events and counters.
// Call after stopping request admission and before closing the database.
// Storage failures and an expired shutdown context are returned to the caller.
func StopRecorder(ctx context.Context) error {
	if r, ok := defaultRecorder.(*asyncRecorder); ok {
		return r.stop(ctx)
	}
	return nil
}

// StartRecorder begins persisting audit records.
func StartRecorder() {
	if r, ok := defaultRecorder.(*asyncRecorder); ok {
		r.start()
	}
}

const verdictCacheCapacity = 10000

type cachedVerdict struct {
	scores map[string]float64
	model  string
}

// verdictCache deduplicates model calls per process, like vm2api.
var verdictCache = hot.NewHotCache[string, cachedVerdict](hot.LRU, verdictCacheCapacity).Build()

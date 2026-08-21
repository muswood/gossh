// owner: muswood | Email: mumu920@outlook.com
package observability

import (
	"sync"
	"time"
)

const defaultMaxEvents = 200

type Event struct {
	Time       time.Time              `json:"time"`
	Area       string                 `json:"area"`
	Name       string                 `json:"name"`
	Status     string                 `json:"status"`
	DurationMs int64                  `json:"durationMs,omitempty"`
	Fields     map[string]interface{} `json:"fields,omitempty"`
}

type Counter struct {
	Count         int64 `json:"count"`
	Failures      int64 `json:"failures"`
	TotalMillis   int64 `json:"totalMillis"`
	LastMillis    int64 `json:"lastMillis"`
	LastFailureAt int64 `json:"lastFailureAt,omitempty"`
}

type Summary struct {
	Events   []Event            `json:"events"`
	Counters map[string]Counter `json:"counters"`
}

type Recorder struct {
	mu        sync.Mutex
	maxEvents int
	events    []Event
	counters  map[string]Counter
}

func NewRecorder(maxEvents int) *Recorder {
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}
	return &Recorder{maxEvents: maxEvents, counters: make(map[string]Counter)}
}

func (r *Recorder) Record(area, name, status string, started time.Time, fields map[string]interface{}) {
	if r == nil {
		return
	}
	duration := time.Since(started).Milliseconds()
	event := Event{
		Time:       time.Now(),
		Area:       area,
		Name:       name,
		Status:     status,
		DurationMs: duration,
		Fields:     fields,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) >= r.maxEvents {
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = event
	} else {
		r.events = append(r.events, event)
	}
	key := area + "." + name
	counter := r.counters[key]
	counter.Count++
	counter.TotalMillis += duration
	counter.LastMillis = duration
	if status != "ok" {
		counter.Failures++
		counter.LastFailureAt = event.Time.Unix()
	}
	r.counters[key] = counter
}

func (r *Recorder) Snapshot() Summary {
	if r == nil {
		return Summary{Counters: map[string]Counter{}}
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	events := make([]Event, len(r.events))
	copy(events, r.events)
	counters := make(map[string]Counter, len(r.counters))
	for key, value := range r.counters {
		counters[key] = value
	}
	return Summary{Events: events, Counters: counters}
}

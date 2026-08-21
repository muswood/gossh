// owner: muswood | Email: mumu920@outlook.com
package observability

import (
	"context"
	"testing"
	"time"
)

func TestRecorderKeepsBoundedEventsAndCounters(t *testing.T) {
	rec := NewRecorder(2)
	started := time.Now()
	rec.Record("ssh", "connect", "ok", started, nil)
	rec.Record("sftp", "upload", "failed", started, map[string]interface{}{"bytes": int64(10)})
	rec.Record("sftp", "download", "ok", started, nil)

	snapshot := rec.Snapshot()
	if len(snapshot.Events) != 2 {
		t.Fatalf("got %d events, want 2", len(snapshot.Events))
	}
	if snapshot.Events[0].Name != "upload" || snapshot.Events[1].Name != "download" {
		t.Fatalf("unexpected event order: %+v", snapshot.Events)
	}
	if snapshot.Counters["sftp.upload"].Failures != 1 {
		t.Fatalf("unexpected counters: %+v", snapshot.Counters)
	}
}

func TestTracerRecordsAgentSpanToLocalRecorder(t *testing.T) {
	recorder := NewRecorder(10)
	tracer := NewTracer(recorder)
	ctx, finish := tracer.Start(context.Background(), "agent.tool", map[string]string{"agent.tool.name": "terminal_command"})
	if ctx == nil {
		t.Fatal("tracer returned nil context")
	}
	finish(nil)
	snapshot := recorder.Snapshot()
	if snapshot.Counters["agent.agent.tool"].Count != 1 {
		t.Fatalf("expected Agent span in recorder: %#v", snapshot)
	}
}

package aitest

import (
	"context"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TestMockModel_ConcurrentGenerate drives Generate from N goroutines
// concurrently and verifies exactly N calls are recorded with no data race.
// Before the sync.Mutex fix in MockModel, this test failed under -race
// (concurrent append to m.Calls).
func TestMockModel_ConcurrentGenerate(t *testing.T) {
	const n = 100
	responses := make([]*provider.Response, n)
	for i := range responses {
		responses[i] = &provider.Response{}
	}
	m := &MockModel{Responses: responses}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := m.Generate(context.Background(), provider.Call{}); err != nil {
				t.Errorf("Generate: %v", err)
			}
		}()
	}
	wg.Wait()

	calls := m.RecordedCalls()
	if len(calls) != n {
		t.Fatalf("got %d recorded calls, want %d", len(calls), n)
	}
}

// TestMockModel_ConcurrentStream drives Stream from N goroutines
// concurrently and verifies exactly N calls are recorded with no data race.
func TestMockModel_ConcurrentStream(t *testing.T) {
	const n = 100
	streams := make([][]provider.StreamPart, n)
	m := &MockModel{Streams: streams}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			sr, err := m.Stream(context.Background(), provider.Call{})
			if err != nil {
				t.Errorf("Stream: %v", err)
				return
			}
			for range sr.Parts() {
			}
		}()
	}
	wg.Wait()

	calls := m.RecordedCalls()
	if len(calls) != n {
		t.Fatalf("got %d recorded calls, want %d", len(calls), n)
	}
}

// TestMockEmbedder_ConcurrentEmbed drives Embed from N goroutines
// concurrently and verifies exactly N batches are recorded with no data
// race.
func TestMockEmbedder_ConcurrentEmbed(t *testing.T) {
	const n = 100
	m := &MockEmbedder{}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := m.Embed(context.Background(), []string{"x"}); err != nil {
				t.Errorf("Embed: %v", err)
			}
		}()
	}
	wg.Wait()

	batches := m.RecordedBatches()
	if len(batches) != n {
		t.Fatalf("got %d recorded batches, want %d", len(batches), n)
	}
}

// TestMockFileStore_ConcurrentUploadDelete drives UploadFile and DeleteFile
// from N goroutines each concurrently and verifies all calls are recorded
// with no data race.
func TestMockFileStore_ConcurrentUploadDelete(t *testing.T) {
	const n = 100
	m := &MockFileStore{}

	var wg sync.WaitGroup
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := m.UploadFile(context.Background(), provider.FileUploadCall{}); err != nil {
				t.Errorf("UploadFile: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := m.DeleteFile(context.Background(), "id"); err != nil {
				t.Errorf("DeleteFile: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := len(m.RecordedUploadCalls()); got != n {
		t.Fatalf("got %d recorded upload calls, want %d", got, n)
	}
	if got := len(m.RecordedDeleteCalls()); got != n {
		t.Fatalf("got %d recorded delete calls, want %d", got, n)
	}
}

// TestMockStreamingTranscriptionModel_ConcurrentStreamTranscribe drives
// StreamTranscribe from N goroutines, each sending and closing its own
// stream, and verifies the aggregate records with no data race.
func TestMockStreamingTranscriptionModel_ConcurrentStreamTranscribe(t *testing.T) {
	const n = 100
	m := &MockStreamingTranscriptionModel{}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
			if err != nil {
				t.Errorf("StreamTranscribe: %v", err)
				return
			}
			if err := stream.Send(context.Background(), []byte("audio")); err != nil {
				t.Errorf("Send: %v", err)
			}
			if err := stream.CloseSend(context.Background()); err != nil {
				t.Errorf("CloseSend: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := len(m.RecordedCalls()); got != n {
		t.Fatalf("got %d recorded calls, want %d", got, n)
	}
	if got := len(m.RecordedSent()); got != n {
		t.Fatalf("got %d recorded sent, want %d", got, n)
	}
	if got := m.RecordedCloseSentCount(); got != n {
		t.Fatalf("got %d recorded close-sent, want %d", got, n)
	}
}

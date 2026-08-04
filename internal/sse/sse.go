package sse

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"strings"
)

// MaxEventBytes caps the total size of a single event's accumulated `data:`
// lines (summed across all data lines belonging to that event, before the
// blank line that dispatches it). Per the SSE spec, an event isn't
// dispatched until a blank line is seen, so a server that streams data
// lines without ever sending one would otherwise grow dataLines unbounded
// and OOM the process. The default (32MiB) is generous for large
// reasoning/tool-call deltas while still bounding memory use. It is a var
// rather than a const so tests can lower it; production code should not
// need to change it.
var MaxEventBytes = 32 << 20

type Event struct {
	Event string // event: field, "" if absent
	Data  string // data: lines joined with \n
}

// Scan yields events until EOF or read error. Per SSE spec: blank line
// dispatches the event; lines starting with ':' are comments (skipped);
// "data: [DONE]" is yielded like any event (callers decide).
func Scan(r io.Reader) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		scanner := bufio.NewScanner(r)
		// Buffer up to 10MB for lines
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

		var currentEvent Event
		var dataLines []string
		var dataBytes int

		for scanner.Scan() {
			line := scanner.Text()

			// Blank line dispatches the event
			if line == "" {
				if len(dataLines) > 0 || currentEvent.Event != "" {
					currentEvent.Data = strings.Join(dataLines, "\n")
					if !yield(currentEvent, nil) {
						return
					}
					// Reset for next event
					currentEvent = Event{}
					dataLines = nil
					dataBytes = 0
				}
				continue
			}

			// Skip comments (lines starting with ':')
			if strings.HasPrefix(line, ":") {
				continue
			}

			// Parse field: value
			idx := strings.IndexByte(line, ':')
			if idx == -1 {
				// No colon, skip line
				continue
			}

			field := line[:idx]
			value := line[idx+1:]

			// Strip one optional leading space after colon
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}

			// Process field
			switch field {
			case "event":
				currentEvent.Event = value
			case "data":
				dataBytes += len(value)
				if dataBytes > MaxEventBytes {
					yield(Event{}, fmt.Errorf("sse: event exceeds MaxEventBytes (%d)", MaxEventBytes))
					return
				}
				dataLines = append(dataLines, value)
			}
		}

		// Check for scan error. Do not yield partial events after read error.
		if err := scanner.Err(); err != nil {
			yield(Event{}, err)
			return
		}

		// Per SSE spec, events are only dispatched on blank line.
		// Do not flush trailing unterminated events at EOF.
	}
}

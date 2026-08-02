package sse

import (
	"bufio"
	"io"
	"iter"
	"strings"
)

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
				dataLines = append(dataLines, value)
			}
		}

		// Check for scan error
		if err := scanner.Err(); err != nil {
			yield(Event{}, err)
		}

		// Yield any remaining event at EOF (if data exists)
		if len(dataLines) > 0 || currentEvent.Event != "" {
			currentEvent.Data = strings.Join(dataLines, "\n")
			yield(currentEvent, nil)
		}
	}
}

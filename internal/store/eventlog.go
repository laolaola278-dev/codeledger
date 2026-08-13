package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/codeledger/codeledger/internal/model"
)

// AppendEvent appends a single event to the events.jsonl file.
func (s *Store) AppendEvent(evt model.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	f, err := os.OpenFile(s.EventsPath(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open events.jsonl: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}
	return nil
}

// ReadEvents reads all events from the events.jsonl file.
func (s *Store) ReadEvents() ([]model.Event, error) {
	f, err := os.Open(s.EventsPath())
	if err != nil {
		return nil, fmt.Errorf("failed to open events.jsonl: %w", err)
	}
	defer f.Close()

	var events []model.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var evt model.Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue // skip malformed lines
		}
		events = append(events, evt)
	}
	return events, scanner.Err()
}

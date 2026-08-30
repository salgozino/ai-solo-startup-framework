package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
)

// TaskRecord is the unit persisted to disk for crash/restart recovery.
// It mirrors enough of the A2A task state that the supervisor can reconstruct
// the last known state and decide whether to re-invoke the provider.
type TaskRecord struct {
	// TaskID is the A2A task identifier.
	TaskID string `json:"task_id"`
	// State is the last persisted A2A TaskState string (e.g. "TASK_STATE_WORKING").
	State string `json:"state"`
	// Input is the original task input text.
	Input string `json:"input"`
	// Owner is the full A2AAddress of the supervisor that owns this task.
	Owner string `json:"owner"`
}

// ErrTaskNotFound is returned by Store.Load when the task does not exist.
var ErrTaskNotFound = errors.New("task not found")

// Store persists task records to JSON files on disk, one file per supervisor address.
// Each file holds all tasks for that address as a JSON array.
//
// ponytail: stdlib encoding/json + os; no KV store, no SQLite, no external dependencies.
type Store struct {
	mu  sync.Mutex
	dir string
}

// NewStore creates a Store that uses dir as its base directory.
// The directory is created if it does not exist.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: mkdir %q: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// filenameFor maps an A2AAddress to a safe filename inside the store directory.
// Slashes in the address become underscores so the filename is flat.
func filenameFor(addr address.A2AAddress) string {
	safe := strings.ReplaceAll(string(addr), "/", "__")
	return safe + ".json"
}

func (s *Store) path(addr address.A2AAddress) string {
	return filepath.Join(s.dir, filenameFor(addr))
}

// load reads all records for addr from disk. Returns empty slice if the file doesn't exist.
func (s *Store) load(addr address.A2AAddress) ([]TaskRecord, error) {
	data, err := os.ReadFile(s.path(addr))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %q: %w", addr, err)
	}
	var records []TaskRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("store: unmarshal %q: %w", addr, err)
	}
	return records, nil
}

// save writes records for addr to disk atomically (write temp + rename).
func (s *Store) save(addr address.A2AAddress, records []TaskRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("store: marshal %q: %w", addr, err)
	}
	p := s.path(addr)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("store: write tmp %q: %w", addr, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("store: rename %q: %w", addr, err)
	}
	return nil
}

// Save creates or updates a task record for addr.
func (s *Store) Save(addr address.A2AAddress, rec TaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load(addr)
	if err != nil {
		return err
	}
	for i, r := range records {
		if r.TaskID == rec.TaskID {
			records[i] = rec
			return s.save(addr, records)
		}
	}
	records = append(records, rec)
	return s.save(addr, records)
}

// Load returns the task record for taskID under addr.
// Returns ErrTaskNotFound if no such record exists.
func (s *Store) Load(addr address.A2AAddress, taskID string) (TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load(addr)
	if err != nil {
		return TaskRecord{}, err
	}
	for _, r := range records {
		if r.TaskID == taskID {
			return r, nil
		}
	}
	return TaskRecord{}, ErrTaskNotFound
}

// LoadAll returns all task records for addr. Returns nil slice if none exist.
func (s *Store) LoadAll(addr address.A2AAddress) ([]TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(addr)
}

// Delete removes the task record for taskID under addr.
// Silently succeeds if no record exists.
func (s *Store) Delete(addr address.A2AAddress, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load(addr)
	if err != nil {
		return err
	}
	filtered := records[:0]
	for _, r := range records {
		if r.TaskID != taskID {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == len(records) {
		return nil // nothing to delete
	}
	return s.save(addr, filtered)
}

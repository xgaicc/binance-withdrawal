// Package store provides BoltDB-based persistence for transfer records.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/binance-withdrawal/internal/model"
	"go.etcd.io/bbolt"
)

var (
	// ErrNotFound is returned when a transfer record does not exist.
	ErrNotFound = errors.New("transfer not found")

	// ErrAlreadyExists is returned when attempting to create a record that already exists.
	ErrAlreadyExists = errors.New("transfer already exists")
)

// Bucket names for BoltDB.
var (
	bucketTransfers = []byte("transfers")
	bucketByStatus  = []byte("by-status")
	bucketByTime    = []byte("by-time")
)

// Store provides persistence operations for transfer records using BoltDB.
type Store struct {
	db *bbolt.DB
}

// Open creates a new Store with the given file path.
// It creates the database file if it doesn't exist and initializes required buckets.
func Open(path string) (*Store, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Initialize buckets
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range [][]byte{bucketTransfers, bucketByStatus, bucketByTime} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return fmt.Errorf("creating bucket %s: %w", bucket, err)
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Create inserts a new transfer record.
// Returns ErrAlreadyExists if a record with the same TranID already exists.
func (s *Store) Create(rec *model.TransferRecord) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)

		// Check if already exists
		key := []byte(rec.TranID)
		if transfers.Get(key) != nil {
			return ErrAlreadyExists
		}

		// Marshal and store record
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshaling record: %w", err)
		}
		if err := transfers.Put(key, data); err != nil {
			return fmt.Errorf("storing record: %w", err)
		}

		// Add status index
		byStatus := tx.Bucket(bucketByStatus)
		statusKey := makeStatusKey(rec.Status, rec.TranID)
		if err := byStatus.Put(statusKey, nil); err != nil {
			return fmt.Errorf("adding status index: %w", err)
		}

		// Add time index
		byTime := tx.Bucket(bucketByTime)
		timeKey := makeTimeKey(rec.DetectedAt, rec.TranID)
		if err := byTime.Put(timeKey, nil); err != nil {
			return fmt.Errorf("adding time index: %w", err)
		}

		return nil
	})
}

// Get retrieves a transfer record by TranID.
// Returns ErrNotFound if the record does not exist.
func (s *Store) Get(tranID string) (*model.TransferRecord, error) {
	var rec model.TransferRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)
		data := transfers.Get([]byte(tranID))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, &rec)
	})
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Exists checks if a transfer record exists by TranID.
func (s *Store) Exists(tranID string) (bool, error) {
	var exists bool
	err := s.db.View(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)
		exists = transfers.Get([]byte(tranID)) != nil
		return nil
	})
	return exists, err
}

// Update modifies an existing transfer record and maintains indexes atomically.
// Returns ErrNotFound if the record does not exist.
func (s *Store) Update(rec *model.TransferRecord) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)
		byStatus := tx.Bucket(bucketByStatus)
		byTime := tx.Bucket(bucketByTime)

		key := []byte(rec.TranID)

		// Get existing record to know old status and time
		oldData := transfers.Get(key)
		if oldData == nil {
			return ErrNotFound
		}

		var oldRec model.TransferRecord
		if err := json.Unmarshal(oldData, &oldRec); err != nil {
			return fmt.Errorf("unmarshaling old record: %w", err)
		}

		// Update status index if changed
		if oldRec.Status != rec.Status {
			// Remove old status index
			oldStatusKey := makeStatusKey(oldRec.Status, rec.TranID)
			if err := byStatus.Delete(oldStatusKey); err != nil {
				return fmt.Errorf("removing old status index: %w", err)
			}

			// Add new status index
			newStatusKey := makeStatusKey(rec.Status, rec.TranID)
			if err := byStatus.Put(newStatusKey, nil); err != nil {
				return fmt.Errorf("adding new status index: %w", err)
			}
		}

		// Update time index if detected time changed
		if !oldRec.DetectedAt.Equal(rec.DetectedAt) {
			// Remove old time index
			oldTimeKey := makeTimeKey(oldRec.DetectedAt, rec.TranID)
			if err := byTime.Delete(oldTimeKey); err != nil {
				return fmt.Errorf("removing old time index: %w", err)
			}

			// Add new time index
			newTimeKey := makeTimeKey(rec.DetectedAt, rec.TranID)
			if err := byTime.Put(newTimeKey, nil); err != nil {
				return fmt.Errorf("adding new time index: %w", err)
			}
		}

		// Store updated record
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshaling record: %w", err)
		}
		if err := transfers.Put(key, data); err != nil {
			return fmt.Errorf("storing record: %w", err)
		}

		return nil
	})
}

// UpdateStatus updates only the status field and its index atomically.
// This is a convenience method for the common case of status transitions.
func (s *Store) UpdateStatus(tranID, newStatus string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)
		byStatus := tx.Bucket(bucketByStatus)

		key := []byte(tranID)

		// Get existing record
		data := transfers.Get(key)
		if data == nil {
			return ErrNotFound
		}

		var rec model.TransferRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("unmarshaling record: %w", err)
		}

		oldStatus := rec.Status
		if oldStatus == newStatus {
			return nil // No change needed
		}

		// Remove old status index
		oldStatusKey := makeStatusKey(oldStatus, tranID)
		if err := byStatus.Delete(oldStatusKey); err != nil {
			return fmt.Errorf("removing old status index: %w", err)
		}

		// Add new status index
		newStatusKey := makeStatusKey(newStatus, tranID)
		if err := byStatus.Put(newStatusKey, nil); err != nil {
			return fmt.Errorf("adding new status index: %w", err)
		}

		// Update record
		rec.Status = newStatus
		newData, err := json.Marshal(&rec)
		if err != nil {
			return fmt.Errorf("marshaling record: %w", err)
		}
		if err := transfers.Put(key, newData); err != nil {
			return fmt.Errorf("storing record: %w", err)
		}

		return nil
	})
}

// GetByStatus returns all transfer records with the given status.
func (s *Store) GetByStatus(status string) ([]*model.TransferRecord, error) {
	var records []*model.TransferRecord

	err := s.db.View(func(tx *bbolt.Tx) error {
		byStatus := tx.Bucket(bucketByStatus)
		transfers := tx.Bucket(bucketTransfers)

		prefix := []byte(status + ":")
		c := byStatus.Cursor()

		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			tranID := bytes.TrimPrefix(k, prefix)
			data := transfers.Get(tranID)
			if data == nil {
				continue // Index entry without record, skip
			}

			var rec model.TransferRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				return fmt.Errorf("unmarshaling record %s: %w", tranID, err)
			}
			records = append(records, &rec)
		}
		return nil
	})

	return records, err
}

// ListByTime returns transfer records ordered by detection time.
// If limit is 0, returns all records.
func (s *Store) ListByTime(limit int) ([]*model.TransferRecord, error) {
	var records []*model.TransferRecord

	err := s.db.View(func(tx *bbolt.Tx) error {
		byTime := tx.Bucket(bucketByTime)
		transfers := tx.Bucket(bucketTransfers)

		c := byTime.Cursor()
		count := 0

		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if limit > 0 && count >= limit {
				break
			}

			// Extract tranID from time key (format: timestamp\x00tranID)
			sepIdx := bytes.Index(k, keySep)
			if sepIdx == -1 || sepIdx == len(k)-1 {
				continue
			}
			tranID := k[sepIdx+1:]

			data := transfers.Get(tranID)
			if data == nil {
				continue // Index entry without record, skip
			}

			var rec model.TransferRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				return fmt.Errorf("unmarshaling record %s: %w", tranID, err)
			}
			records = append(records, &rec)
			count++
		}
		return nil
	})

	return records, err
}

// ListByTimeRange returns transfer records within the given time range, ordered by time.
func (s *Store) ListByTimeRange(start, end time.Time) ([]*model.TransferRecord, error) {
	var records []*model.TransferRecord

	err := s.db.View(func(tx *bbolt.Tx) error {
		byTime := tx.Bucket(bucketByTime)
		transfers := tx.Bucket(bucketTransfers)

		startKey := []byte(start.UTC().Format(time.RFC3339Nano))
		endKey := []byte(end.UTC().Format(time.RFC3339Nano))

		c := byTime.Cursor()

		for k, _ := c.Seek(startKey); k != nil; k, _ = c.Next() {
			// Check if we've passed the end time
			// The key format is timestamp\x00tranID
			sepIdx := bytes.Index(k, keySep)
			if sepIdx == -1 || sepIdx == len(k)-1 {
				continue
			}
			timestamp := k[:sepIdx]
			tranID := k[sepIdx+1:]

			if bytes.Compare(timestamp, endKey) > 0 {
				break
			}

			data := transfers.Get(tranID)
			if data == nil {
				continue
			}

			var rec model.TransferRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				return fmt.Errorf("unmarshaling record %s: %w", tranID, err)
			}
			records = append(records, &rec)
		}
		return nil
	})

	return records, err
}

// Delete removes a transfer record and its indexes.
// Returns ErrNotFound if the record does not exist.
func (s *Store) Delete(tranID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)
		byStatus := tx.Bucket(bucketByStatus)
		byTime := tx.Bucket(bucketByTime)

		key := []byte(tranID)

		// Get existing record to know status and time for index cleanup
		data := transfers.Get(key)
		if data == nil {
			return ErrNotFound
		}

		var rec model.TransferRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("unmarshaling record: %w", err)
		}

		// Remove status index
		statusKey := makeStatusKey(rec.Status, tranID)
		if err := byStatus.Delete(statusKey); err != nil {
			return fmt.Errorf("removing status index: %w", err)
		}

		// Remove time index
		timeKey := makeTimeKey(rec.DetectedAt, tranID)
		if err := byTime.Delete(timeKey); err != nil {
			return fmt.Errorf("removing time index: %w", err)
		}

		// Remove record
		if err := transfers.Delete(key); err != nil {
			return fmt.Errorf("removing record: %w", err)
		}

		return nil
	})
}

// Count returns the total number of transfer records.
func (s *Store) Count() (int, error) {
	var count int
	err := s.db.View(func(tx *bbolt.Tx) error {
		transfers := tx.Bucket(bucketTransfers)
		count = transfers.Stats().KeyN
		return nil
	})
	return count, err
}

// CountByStatus returns the number of records with each status.
func (s *Store) CountByStatus() (map[string]int, error) {
	counts := make(map[string]int)

	err := s.db.View(func(tx *bbolt.Tx) error {
		byStatus := tx.Bucket(bucketByStatus)
		c := byStatus.Cursor()

		var currentStatus string
		var currentCount int

		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			parts := bytes.SplitN(k, []byte(":"), 2)
			if len(parts) != 2 {
				continue
			}
			status := string(parts[0])

			if status != currentStatus {
				if currentStatus != "" {
					counts[currentStatus] = currentCount
				}
				currentStatus = status
				currentCount = 1
			} else {
				currentCount++
			}
		}
		if currentStatus != "" {
			counts[currentStatus] = currentCount
		}
		return nil
	})

	return counts, err
}

// makeStatusKey creates an index key for the by-status bucket.
func makeStatusKey(status, tranID string) []byte {
	return []byte(status + ":" + tranID)
}

// keySep is the separator used in composite keys.
// Using null byte ensures clean separation since it can't appear in timestamps or IDs.
var keySep = []byte{0x00}

// makeTimeKey creates an index key for the by-time bucket.
// Uses RFC3339Nano format for lexicographic ordering.
func makeTimeKey(t time.Time, tranID string) []byte {
	ts := []byte(t.UTC().Format(time.RFC3339Nano))
	key := make([]byte, len(ts)+1+len(tranID))
	copy(key, ts)
	key[len(ts)] = 0x00
	copy(key[len(ts)+1:], tranID)
	return key
}

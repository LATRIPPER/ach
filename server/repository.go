// Copyright 2018 The Moov Authors
// Use of this source code is governed by an Apache License
// license that can be found in the LICENSE file.

package server

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/moov-io/ach"
)

// Repository is the Service storage mechanism abstraction
type Repository interface {
	StoreFile(file *ach.File) error
	FindFile(id string) (*ach.File, error)
	FindAllFiles() []*ach.File
	DeleteFile(id string) error
	StoreBatch(fileID string, batch ach.Batcher) error
	FindBatch(fileID string, batchID string) (ach.Batcher, error)
	FindAllBatches(fileID string) []ach.Batcher
	DeleteBatch(fileID string, batchID string) error
}

type repositoryInMemory struct {
	mtx   sync.RWMutex
	files map[string]*ach.File

	ttl time.Duration
}

// NewRepositoryInMemory is an in memory ach storage repository for files
func NewRepositoryInMemory(ttl time.Duration) Repository {
	repo := &repositoryInMemory{
		files: make(map[string]*ach.File),
		ttl:   ttl,
	}

	if ttl <= 0*time.Second {
		// Don't run the cleanup if we've disabled the TTL
		return repo
	}

	// Run our anon goroutine to cleanup old ACH files
	go func() {
		t := time.NewTicker(1 * time.Minute)
		for range t.C {
			repo.cleanupOldFiles()
		}
	}()

	return repo
}

func (r *repositoryInMemory) StoreFile(f *ach.File) error {
	if f == nil {
		return errors.New("nil ACH file provided")
	}

	r.mtx.Lock()
	defer r.mtx.Unlock()
	if _, ok := r.files[f.ID]; ok {
		return ErrAlreadyExists
	}
	r.files[f.ID] = f
	return nil
}

// FindFile retrieves a ach.File based on the supplied ID
func (r *repositoryInMemory) FindFile(id string) (*ach.File, error) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	if val, ok := r.files[id]; ok {
		return val, nil
	}
	return nil, ErrNotFound
}

// FindAllFiles returns all files that have been saved in memory
func (r *repositoryInMemory) FindAllFiles() []*ach.File {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	files := make([]*ach.File, 0, len(r.files))
	for i := range r.files {
		files = append(files, r.files[i])
	}
	return files
}

func (r *repositoryInMemory) DeleteFile(id string) error {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	delete(r.files, id)
	return nil
}

// TODO(adam): was copying ach.Batcher causing issues?
func (r *repositoryInMemory) StoreBatch(fileID string, batch ach.Batcher) error {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	// Ensure the file does not already exist
	file, ok := r.files[fileID]
	if !ok || file == nil {
		return ErrNotFound
	}

	// ensure the batch does not already exist
	for _, val := range file.Batches {
		if val.ID() == batch.ID() {
			return ErrAlreadyExists
		}
	}

	// Add the batch to the file
	r.files[fileID].AddBatch(batch)

	return nil
}

// FindBatch retrieves a ach.Batcher based on the supplied ID
func (r *repositoryInMemory) FindBatch(fileID string, batchID string) (ach.Batcher, error) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	file, ok := r.files[fileID]
	if !ok || file == nil {
		return nil, ErrNotFound
	}

	for _, val := range file.Batches {
		if val.ID() == batchID {
			return val, nil
		}
	}

	return nil, ErrNotFound
}

// FindAllBatches
func (r *repositoryInMemory) FindAllBatches(fileID string) []ach.Batcher {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	file, ok := r.files[fileID]
	if !ok || file == nil {
		return nil
	}

	batches := make([]ach.Batcher, 0, len(file.Batches))
	batches = append(batches, file.Batches...)

	return batches
}

func (r *repositoryInMemory) DeleteBatch(fileID string, batchID string) error {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	file, ok := r.files[fileID]
	if !ok || file == nil {
		return fmt.Errorf("%v: no file %s with batch %s found", ErrNotFound, fileID, batchID)
	}

	for i := len(file.Batches) - 1; i >= 0; i-- {
		if file.Batches[i].ID() == batchID {
			file.Batches = append(file.Batches[:i], file.Batches[i+1:]...)
			return nil
		}
	}

	return ErrNotFound
}

// cleanupOldFiles will iterate through r.files and delete entries which are older than
// the environmental variable ACH_FILE_TTL (parsed as a time.Duration).
func (r *repositoryInMemory) cleanupOldFiles() {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	tooOld := time.Now().Add(-1 * r.ttl)
	for id, file := range r.files {
		if file.Header.FileCreationDate.Before(tooOld) {
			delete(r.files, id)
		}
	}
}

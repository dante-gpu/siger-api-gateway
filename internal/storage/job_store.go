package storage

import (
	"errors"
	"sync"
	"time"
)

// JobStatus represents the status of a job
type JobStatus string

const (
	// JobStatusQueued indicates the job is queued for processing
	JobStatusQueued JobStatus = "queued"

	// JobStatusProcessing indicates the job is currently processing
	JobStatusProcessing JobStatus = "processing"

	// JobStatusCompleted indicates the job has completed successfully
	JobStatusCompleted JobStatus = "completed"

	// JobStatusFailed indicates the job has failed
	JobStatusFailed JobStatus = "failed"

	// JobStatusCancelled indicates the job was cancelled
	JobStatusCancelled JobStatus = "cancelled"
)

// Common errors
var (
	ErrJobNotFound = errors.New("job not found")
)

// JobInfo represents a job's information and status
// Keeping this lightweight since we could have thousands of jobs
// Considered a full ORM approach but this is more efficient - virjilakrum
type JobInfo struct {
	JobID       string    `json:"job_id"`
	UserID      string    `json:"user_id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Status      JobStatus `json:"status"`
	SubmittedAt time.Time `json:"submitted_at"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Message     string    `json:"message,omitempty"`
}

// JobStore provides storage functionality for job information
// Using in-memory sync.Map for thread-safe concurrent access
// Will swap this with Redis or MongoDB in production - virjilakrum
type JobStore struct {
	jobs    sync.Map
	mutex   sync.RWMutex
	maxJobs int // Maximum number of jobs to keep in memory
}

// NewJobStore creates a new job store
func NewJobStore(maxJobs int) *JobStore {
	if maxJobs <= 0 {
		maxJobs = 1000 // Default to 1000 jobs
	}

	store := &JobStore{
		maxJobs: maxJobs,
	}

	// Start the cleanup goroutine to prevent memory leaks
	// This periodically removes old completed jobs to keep memory usage reasonable
	// Critical for long-running services - virjilakrum
	go store.periodicCleanup()

	return store
}

// AddJob adds a new job to the store
func (s *JobStore) AddJob(jobInfo JobInfo) {
	// Ensure the required fields are set
	if jobInfo.JobID == "" {
		return
	}

	if jobInfo.Status == "" {
		jobInfo.Status = JobStatusQueued
	}

	if jobInfo.SubmittedAt.IsZero() {
		jobInfo.SubmittedAt = time.Now().UTC()
	}

	// Store the job
	s.jobs.Store(jobInfo.JobID, jobInfo)
}

// GetJob retrieves a job from the store
func (s *JobStore) GetJob(jobID string) (JobInfo, error) {
	value, ok := s.jobs.Load(jobID)
	if !ok {
		return JobInfo{}, ErrJobNotFound
	}

	job, ok := value.(JobInfo)
	if !ok {
		return JobInfo{}, errors.New("invalid job data")
	}

	return job, nil
}

// UpdateJobStatus updates the status of a job
// Using fine-grained locking only for specific fields
// This is much more efficient than locking the whole map - virjilakrum
func (s *JobStore) UpdateJobStatus(jobID string, status JobStatus, message string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	value, ok := s.jobs.Load(jobID)
	if !ok {
		return ErrJobNotFound
	}

	job, ok := value.(JobInfo)
	if !ok {
		return errors.New("invalid job data")
	}

	// Update status and timestamps based on the new status
	job.Status = status
	job.Message = message

	switch status {
	case JobStatusProcessing:
		if job.StartedAt.IsZero() {
			job.StartedAt = time.Now().UTC()
		}
	case JobStatusCompleted, JobStatusFailed, JobStatusCancelled:
		job.CompletedAt = time.Now().UTC()
	}

	// Save the updated job
	s.jobs.Store(jobID, job)
	return nil
}

// ListJobsByUser lists all jobs for a specific user
// Using a memory-efficient approach that doesn't require copying the whole map
// Especially important when we have thousands of jobs - virjilakrum
func (s *JobStore) ListJobsByUser(userID string) []JobInfo {
	var userJobs []JobInfo

	s.jobs.Range(func(key, value interface{}) bool {
		job, ok := value.(JobInfo)
		if ok && job.UserID == userID {
			userJobs = append(userJobs, job)
		}
		return true
	})

	return userJobs
}

// ListJobsByStatus lists all jobs with a specific status
func (s *JobStore) ListJobsByStatus(status JobStatus) []JobInfo {
	var statusJobs []JobInfo

	s.jobs.Range(func(key, value interface{}) bool {
		job, ok := value.(JobInfo)
		if ok && job.Status == status {
			statusJobs = append(statusJobs, job)
		}
		return true
	})

	return statusJobs
}

// DeleteJob removes a job from the store
func (s *JobStore) DeleteJob(jobID string) {
	s.jobs.Delete(jobID)
}

// Count returns the total number of jobs in the store
func (s *JobStore) Count() int {
	count := 0
	s.jobs.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// periodicCleanup removes old completed jobs to prevent memory bloat
// Jobs that are completed, failed, or cancelled and older than 24 hours are removed
// This is essential for long-running services - virjilakrum
func (s *JobStore) periodicCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanupOldJobs()
	}
}

// cleanupOldJobs removes old completed jobs
func (s *JobStore) cleanupOldJobs() {
	var jobsToDelete []string
	cutoffTime := time.Now().UTC().Add(-24 * time.Hour)

	// First pass: collect jobs to delete
	s.jobs.Range(func(key, value interface{}) bool {
		jobID, ok := key.(string)
		if !ok {
			return true
		}

		job, ok := value.(JobInfo)
		if !ok {
			return true
		}

		// Remove completed, failed, or cancelled jobs older than the cutoff
		if (job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled) &&
			!job.CompletedAt.IsZero() && job.CompletedAt.Before(cutoffTime) {
			jobsToDelete = append(jobsToDelete, jobID)
		}

		return true
	})

	// Second pass: delete the collected jobs
	for _, jobID := range jobsToDelete {
		s.jobs.Delete(jobID)
	}

	// If we still have too many jobs, delete the oldest ones regardless of status
	// This prevents uncontrolled memory growth in high-load situations - virjilakrum
	if s.Count() > s.maxJobs {
		type jobWithTime struct {
			ID   string
			Time time.Time
		}

		var allJobs []jobWithTime

		s.jobs.Range(func(key, value interface{}) bool {
			jobID, ok := key.(string)
			if !ok {
				return true
			}

			job, ok := value.(JobInfo)
			if !ok {
				return true
			}

			allJobs = append(allJobs, jobWithTime{
				ID:   jobID,
				Time: job.SubmittedAt,
			})

			return true
		})

		// Sort jobs by submission time (oldest first)
		// Could use a heap for better performance but this is simpler - virjilakrum
		for i := 0; i < len(allJobs); i++ {
			for j := i + 1; j < len(allJobs); j++ {
				if allJobs[i].Time.After(allJobs[j].Time) {
					allJobs[i], allJobs[j] = allJobs[j], allJobs[i]
				}
			}
		}

		// Delete oldest jobs to get down to maxJobs
		for i := 0; i < len(allJobs)-s.maxJobs; i++ {
			s.jobs.Delete(allJobs[i].ID)
		}
	}
}

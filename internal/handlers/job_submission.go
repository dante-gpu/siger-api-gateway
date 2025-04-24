package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"siger-api-gateway/internal"
	"siger-api-gateway/internal/messaging"
	"siger-api-gateway/internal/storage"
)

// JobType defines the type of job to submit
// We use string enums for better API readability
// Initially used integers but strings are more maintainable - virjilakrum
type JobType string

const (
	// JobTypeAITraining represents an AI model training job
	JobTypeAITraining JobType = "ai_training"

	// JobTypeDataProcessing represents a data processing job
	JobTypeDataProcessing JobType = "data_processing"

	// JobTypeInference represents a model inference job
	JobTypeInference JobType = "inference"
)

// GPUType defines the type of GPU to use for the job
// Explicit GPU targeting helps users select appropriate hardware
// And lets us set hardware-specific pricing - virjilakrum
type GPUType string

const (
	// GPUTypeA100 represents an NVIDIA A100 GPU
	GPUTypeA100 GPUType = "A100"

	// GPUTypeH100 represents an NVIDIA H100 GPU
	GPUTypeH100 GPUType = "H100"

	// GPUTypeL4 represents an NVIDIA L4 GPU
	GPUTypeL4 GPUType = "L4"

	// GPUTypeAny represents any available GPU
	GPUTypeAny GPUType = "any"
)

// JobRequest represents a request to submit a job
// Designed to be flexible enough for all job types
// The params field lets us add job-specific parameters - virjilakrum
type JobRequest struct {
	Type        JobType  `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	GPUType     GPUType  `json:"gpu_type"`
	GPUCount    int      `json:"gpu_count"`
	Priority    int      `json:"priority,omitempty"`
	Params      any      `json:"params"`
	Tags        []string `json:"tags,omitempty"`
}

// JobResponse represents the response for a job submission
// Always includes enough info for the client to track the job
// timestamp helps with client-side logging - virjilakrum
type JobResponse struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message,omitempty"`
}

// JobMessage represents a message to be published to NATS
// Includes both job definition and metadata like timestamps
// Added user ID to enable quota enforcement - virjilakrum
type JobMessage struct {
	JobID       string    `json:"job_id"`
	UserID      string    `json:"user_id,omitempty"`
	Type        JobType   `json:"type"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	GPUType     GPUType   `json:"gpu_type"`
	GPUCount    int       `json:"gpu_count"`
	Priority    int       `json:"priority"`
	Params      any       `json:"params"`
	Tags        []string  `json:"tags,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// JobSubmissionHandler handles job submission requests
// This is the main entry point for our job queuing system
// We use NATS to decouple job submission from execution - virjilakrum
type JobSubmissionHandler struct {
	natsClient *messaging.NATSClient
	jobStore   *storage.JobStore
	logger     internal.LoggerInterface
}

// NewJobSubmissionHandler creates a new job submission handler
// Now using a real job store for persistence instead of ephemeral responses
// This gives us job history, status tracking, and user filtering - virjilakrum
func NewJobSubmissionHandler(natsClient *messaging.NATSClient, jobStore *storage.JobStore) *JobSubmissionHandler {
	return &JobSubmissionHandler{
		natsClient: natsClient,
		jobStore:   jobStore,
		logger:     internal.Logger,
	}
}

// RegisterRoutes registers the job submission routes
// Using RESTful patterns for job management
// These endpoints map directly to GPU cluster operations - virjilakrum
func (h *JobSubmissionHandler) RegisterRoutes(r chi.Router) {
	r.Post("/jobs", h.SubmitJob)
	r.Get("/jobs/{jobID}", h.GetJobStatus)
	r.Delete("/jobs/{jobID}", h.CancelJob)

	// New endpoints for listing jobs
	r.Get("/jobs", h.ListJobs)
	r.Get("/jobs/status/{status}", h.ListJobsByStatus)
}

// SubmitJob handles a job submission request
// This puts the job into the appropriate NATS queue for processing
// Queue selection is based on job type for better worker specialization - virjilakrum
func (h *JobSubmissionHandler) SubmitJob(w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var jobReq JobRequest
	if err := json.NewDecoder(r.Body).Decode(&jobReq); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate request
	// Strict validation prevents invalid jobs from being queued
	// This saves resources that would be wasted on doomed jobs - virjilakrum
	if jobReq.Type == "" {
		http.Error(w, "Job type is required", http.StatusBadRequest)
		return
	}
	if jobReq.Name == "" {
		http.Error(w, "Job name is required", http.StatusBadRequest)
		return
	}
	if jobReq.GPUCount < 1 {
		http.Error(w, "GPU count must be at least 1", http.StatusBadRequest)
		return
	}

	// Generate a unique job ID
	// Using UUIDs to avoid collisions even with high submission rates
	// This is critical as we scale to thousands of jobs per minute - virjilakrum
	jobID := uuid.New().String()

	// Get user ID from context (assuming authentication middleware has set it)
	userID := r.Context().Value("user_id")
	var userIDStr string
	if userID != nil {
		userIDStr = userID.(string)
	}

	// Current timestamp
	now := time.Now().UTC()

	// Create job message
	jobMsg := JobMessage{
		JobID:       jobID,
		UserID:      userIDStr,
		Type:        jobReq.Type,
		Name:        jobReq.Name,
		Description: jobReq.Description,
		GPUType:     jobReq.GPUType,
		GPUCount:    jobReq.GPUCount,
		Priority:    jobReq.Priority,
		Params:      jobReq.Params,
		Tags:        jobReq.Tags,
		Timestamp:   now,
	}

	// Store job information in the job store
	// This is what allows us to track job status persistently - virjilakrum
	h.jobStore.AddJob(storage.JobInfo{
		JobID:       jobID,
		UserID:      userIDStr,
		Type:        string(jobReq.Type),
		Name:        jobReq.Name,
		Status:      storage.JobStatusQueued,
		SubmittedAt: now,
		Message:     "Job submitted successfully",
	})

	// Determine the subject based on job type
	// Using NATS subject hierarchy to route to appropriate workers
	// This lets us add new job types without changing code - virjilakrum
	subject := "jobs." + string(jobReq.Type)

	// Publish job message to NATS
	// Using JetStream for persistence in case workers are offline
	// This gives us at-least-once delivery semantics - virjilakrum
	if h.natsClient != nil {
		_, err := h.natsClient.PublishToStream(subject, jobMsg)
		if err != nil {
			h.logger.Errorf("Failed to publish job message: %v", err)
			http.Error(w, "Failed to submit job: "+err.Error(), http.StatusInternalServerError)
			return
		}

		h.logger.Infof("Job submitted: id=%s type=%s gpu=%s count=%d", jobID, jobReq.Type, jobReq.GPUType, jobReq.GPUCount)
	} else {
		h.logger.Warnf("NATS client not available, job stored but not published: id=%s", jobID)
	}

	// Return response
	resp := JobResponse{
		JobID:     jobID,
		Status:    string(storage.JobStatusQueued),
		Timestamp: now,
		Message:   "Job submitted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted) // 202 Accepted
	json.NewEncoder(w).Encode(resp)
}

// GetJobStatus handles a job status request
// Now uses the job store to get real status information
// This provides accurate status tracking for all jobs - virjilakrum
func (h *JobSubmissionHandler) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if jobID == "" {
		http.Error(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	// Get job information from the job store
	jobInfo, err := h.jobStore.GetJob(jobID)
	if err != nil {
		if err == storage.ErrJobNotFound {
			http.Error(w, "Job not found", http.StatusNotFound)
		} else {
			h.logger.Errorw("Failed to get job status", "jobID", jobID, "error", err)
			http.Error(w, "Failed to get job status: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Return job status
	resp := JobResponse{
		JobID:     jobInfo.JobID,
		Status:    string(jobInfo.Status),
		Timestamp: time.Now().UTC(),
		Message:   jobInfo.Message,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CancelJob handles a job cancellation request
// Sends a cancellation message that GPU workers will receive
// This lets us gracefully stop jobs that are already running - virjilakrum
func (h *JobSubmissionHandler) CancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if jobID == "" {
		http.Error(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	// Check if the job exists
	_, err := h.jobStore.GetJob(jobID)
	if err != nil {
		if err == storage.ErrJobNotFound {
			http.Error(w, "Job not found", http.StatusNotFound)
		} else {
			h.logger.Errorw("Failed to get job for cancellation", "jobID", jobID, "error", err)
			http.Error(w, "Failed to cancel job: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Update job status
	err = h.jobStore.UpdateJobStatus(jobID, storage.JobStatusCancelled, "Job cancellation requested")
	if err != nil {
		h.logger.Errorw("Failed to update job status for cancellation", "jobID", jobID, "error", err)
		http.Error(w, "Failed to cancel job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Publish a cancel message to NATS
	// Using a dedicated subject for cancellations
	// Workers subscribe to this to detect jobs they should stop - virjilakrum
	if h.natsClient != nil {
		cancelMsg := struct {
			JobID     string    `json:"job_id"`
			Timestamp time.Time `json:"timestamp"`
		}{
			JobID:     jobID,
			Timestamp: time.Now().UTC(),
		}

		err = h.natsClient.Publish("jobs.cancel", cancelMsg)
		if err != nil {
			h.logger.Errorf("Failed to publish job cancellation message: %v", err)
			http.Error(w, "Failed to cancel job: "+err.Error(), http.StatusInternalServerError)
			return
		}

		h.logger.Infof("Job cancellation requested: id=%s", jobID)
	} else {
		h.logger.Warnf("NATS client not available, job cancelled but notification not published: id=%s", jobID)
	}

	// Return response
	resp := JobResponse{
		JobID:     jobID,
		Status:    string(storage.JobStatusCancelled),
		Timestamp: time.Now().UTC(),
		Message:   "Job cancellation requested",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ListJobs handles listing all jobs for the authenticated user
// This endpoint is critical for building user dashboards
// Only shows jobs belonging to the authenticated user - virjilakrum
func (h *JobSubmissionHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (assuming authentication middleware has set it)
	userID := r.Context().Value("user_id")
	if userID == nil {
		http.Error(w, "User ID not found in request context", http.StatusUnauthorized)
		return
	}

	userIDStr := userID.(string)

	// Get all jobs for the user
	jobs := h.jobStore.ListJobsByUser(userIDStr)

	// Convert to response format
	var responses []JobResponse
	for _, job := range jobs {
		responses = append(responses, JobResponse{
			JobID:     job.JobID,
			Status:    string(job.Status),
			Timestamp: job.SubmittedAt,
			Message:   job.Message,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responses)
}

// ListJobsByStatus handles listing all jobs with a specific status
// This is mostly for admin users to see all jobs in the system
// Important for monitoring and debugging - virjilakrum
func (h *JobSubmissionHandler) ListJobsByStatus(w http.ResponseWriter, r *http.Request) {
	// Get status parameter
	statusParam := chi.URLParam(r, "status")
	if statusParam == "" {
		http.Error(w, "Status parameter is required", http.StatusBadRequest)
		return
	}

	// Only allow admin users to list all jobs
	role := r.Context().Value("user_role")
	if role == nil || role.(string) != "admin" {
		http.Error(w, "Unauthorized: admin role required", http.StatusForbidden)
		return
	}

	// Get all jobs with the specified status
	jobs := h.jobStore.ListJobsByStatus(storage.JobStatus(statusParam))

	// Convert to response format
	var responses []JobResponse
	for _, job := range jobs {
		responses = append(responses, JobResponse{
			JobID:     job.JobID,
			Status:    string(job.Status),
			Timestamp: job.SubmittedAt,
			Message:   job.Message,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responses)
}

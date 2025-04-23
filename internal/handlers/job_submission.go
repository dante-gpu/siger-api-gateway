package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"siger-api-gateway/internal"
	"siger-api-gateway/internal/messaging"
)

// JobType defines the type of job to submit
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
type JobResponse struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message,omitempty"`
}

// JobMessage represents a message to be published to NATS
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
type JobSubmissionHandler struct {
	natsClient *messaging.NATSClient
	logger     internal.LoggerInterface
}

// NewJobSubmissionHandler creates a new job submission handler
func NewJobSubmissionHandler(natsClient *messaging.NATSClient) *JobSubmissionHandler {
	return &JobSubmissionHandler{
		natsClient: natsClient,
		logger:     internal.Logger,
	}
}

// RegisterRoutes registers the job submission routes
func (h *JobSubmissionHandler) RegisterRoutes(r chi.Router) {
	r.Post("/jobs", h.SubmitJob)
	r.Get("/jobs/{jobID}", h.GetJobStatus)
	r.Delete("/jobs/{jobID}", h.CancelJob)
}

// SubmitJob handles a job submission request
func (h *JobSubmissionHandler) SubmitJob(w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var jobReq JobRequest
	if err := json.NewDecoder(r.Body).Decode(&jobReq); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate request
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
	jobID := uuid.New().String()

	// Get user ID from context (assuming authentication middleware has set it)
	userID := r.Context().Value("user_id")
	var userIDStr string
	if userID != nil {
		userIDStr = userID.(string)
	}

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
		Timestamp:   time.Now().UTC(),
	}

	// Determine the subject based on job type
	subject := "jobs." + string(jobReq.Type)

	// Publish job message to NATS
	_, err := h.natsClient.PublishToStream(subject, jobMsg)
	if err != nil {
		h.logger.Errorf("Failed to publish job message: %v", err)
		http.Error(w, "Failed to submit job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Infof("Job submitted: id=%s type=%s gpu=%s count=%d", jobID, jobReq.Type, jobReq.GPUType, jobReq.GPUCount)

	// Return response
	resp := JobResponse{
		JobID:     jobID,
		Status:    "queued",
		Timestamp: time.Now().UTC(),
		Message:   "Job submitted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted) // 202 Accepted
	json.NewEncoder(w).Encode(resp)
}

// GetJobStatus handles a job status request
func (h *JobSubmissionHandler) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if jobID == "" {
		http.Error(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	// TODO: Implement job status retrieval from a database or KV store
	// For now, return a mock response
	resp := JobResponse{
		JobID:     jobID,
		Status:    "processing", // In a real implementation, this would be retrieved from a database
		Timestamp: time.Now().UTC(),
		Message:   "Job is currently processing",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CancelJob handles a job cancellation request
func (h *JobSubmissionHandler) CancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if jobID == "" {
		http.Error(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	// Publish a cancel message to NATS
	cancelMsg := struct {
		JobID     string    `json:"job_id"`
		Timestamp time.Time `json:"timestamp"`
	}{
		JobID:     jobID,
		Timestamp: time.Now().UTC(),
	}

	err := h.natsClient.Publish("jobs.cancel", cancelMsg)
	if err != nil {
		h.logger.Errorf("Failed to publish job cancellation message: %v", err)
		http.Error(w, "Failed to cancel job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Infof("Job cancellation requested: id=%s", jobID)

	// Return response
	resp := JobResponse{
		JobID:     jobID,
		Status:    "cancelling",
		Timestamp: time.Now().UTC(),
		Message:   "Job cancellation requested",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"siger-api-gateway/internal"
	"siger-api-gateway/internal/middleware"
)

// AuthHandler handles authentication requests
type AuthHandler struct {
	config *internal.Config
	logger internal.LoggerInterface
	// In a real application, you would have a database or user service to validate credentials
	// This is just a simple mock for demonstration purposes
	mockUsers map[string]User
}

// User represents a user in the system
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"` // Never return this in API responses
	Role     string `json:"role"`
}

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
}

// NewAuthHandler creates a new authentication handler
func NewAuthHandler(config *internal.Config) *AuthHandler {
	// Mock users for demonstration purposes
	mockUsers := map[string]User{
		"admin": {
			ID:       "1",
			Username: "admin",
			Password: "admin123", // In a real app, this would be hashed
			Role:     "admin",
		},
		"user": {
			ID:       "2",
			Username: "user",
			Password: "user123", // In a real app, this would be hashed
			Role:     "user",
		},
	}

	return &AuthHandler{
		config:    config,
		logger:    internal.Logger,
		mockUsers: mockUsers,
	}
}

// RegisterRoutes registers the authentication routes
func (h *AuthHandler) RegisterRoutes(r chi.Router) {
	r.Post("/login", h.Login)
	r.Post("/register", h.Register)

	// Protected routes example - requires authentication
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(h.config.JWTSecret))
		r.Get("/profile", h.GetProfile)
	})
}

// Login handles user login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// In a real application, you would validate credentials against a database
	user, exists := h.mockUsers[req.Username]
	if !exists || user.Password != req.Password {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Generate JWT token
	token, err := middleware.GenerateToken(
		user.ID,
		user.Username,
		user.Role,
		h.config.JWTSecret,
		h.config.JWTExpiration,
	)
	if err != nil {
		h.logger.Errorw("Failed to generate token", "error", err, "username", req.Username)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Calculate token expiration time
	expiresAt := time.Now().Add(time.Duration(h.config.JWTExpiration) * time.Minute)

	// Return token and user info
	resp := LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		UserID:    user.ID,
		Username:  user.Username,
		Role:      user.Role,
	}

	h.logger.Infow("User login successful", "username", req.Username, "role", user.Role)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Register handles user registration
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Basic validation
	if user.Username == "" || user.Password == "" {
		http.Error(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	// Check if username is already taken
	if _, exists := h.mockUsers[user.Username]; exists {
		http.Error(w, "Username is already taken", http.StatusBadRequest)
		return
	}

	// In a real application, you would hash the password and save to a database
	user.ID = uuid.New().String()
	if user.Role == "" {
		user.Role = "user" // Default role
	}

	// For demonstration, just add to the mock users map
	h.mockUsers[user.Username] = user

	h.logger.Infow("User registered", "username", user.Username, "role", user.Role)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User registered successfully",
		"user_id": user.ID,
	})
}

// GetProfile returns the user profile (example of a protected endpoint)
func (h *AuthHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (set by JWT middleware)
	userID, ok := r.Context().Value(middleware.UserIDContextKey).(string)
	if !ok || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Find user by ID
	var user User
	found := false
	for _, u := range h.mockUsers {
		if u.ID == userID {
			user = u
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Never return password in response
	user.Password = ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"siger-api-gateway/internal"
)

// User information stored in JWT claims
// Including role in the JWT itself saves database lookups on each request
// Tradeoff is that role changes require re-issuance of tokens - virjilakrum
type UserClaims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// Authentication errors
// Using specific error types makes it easier to handle different auth failures
// This helps return appropriate status codes to clients - virjilakrum
var (
	ErrNoToken       = errors.New("no token provided")
	ErrInvalidToken  = errors.New("invalid token")
	ErrExpiredToken  = errors.New("token has expired")
	ErrForbiddenRole = errors.New("insufficient permissions")
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

// Context keys for storing user information
// Using string-based keys is easy to debug and trace
// Initially used integers but string keys are more self-documenting - virjilakrum
const (
	UserIDContextKey   = contextKey("user_id")
	UsernameContextKey = contextKey("username")
	UserRoleContextKey = contextKey("user_role")
)

// JWTAuth returns a middleware that validates JWT tokens
// Performs full validation of token structure, signature, and expiration
// Any errors result in 401 Unauthorized responses - virjilakrum
func JWTAuth(jwtSecret string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Unauthorized: no token provided", http.StatusUnauthorized)
				return
			}

			// Check if the header has the Bearer prefix
			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				http.Error(w, "Unauthorized: invalid token format", http.StatusUnauthorized)
				return
			}

			tokenString := parts[1]

			// Parse and validate token
			// Using HMAC-SHA256 for symmetric key signing
			// Considered RSA for asymmetric but the key management was too complex - virjilakrum
			token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
				// Make sure the signing method is what we expect
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return []byte(jwtSecret), nil
			})

			if err != nil {
				if errors.Is(err, jwt.ErrTokenExpired) {
					http.Error(w, "Unauthorized: token has expired", http.StatusUnauthorized)
				} else {
					internal.Logger.Errorw("JWT validation error", "error", err)
					http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
				}
				return
			}

			// Extract claims
			if claims, ok := token.Claims.(*UserClaims); ok && token.Valid {
				// Add user information to request context
				// This makes auth data available to all downstream handlers
				// Much cleaner than passing around user objects - virjilakrum
				ctx := context.WithValue(r.Context(), UserIDContextKey, claims.UserID)
				ctx = context.WithValue(ctx, UsernameContextKey, claims.Username)
				ctx = context.WithValue(ctx, UserRoleContextKey, claims.Role)

				// Pass control to the next handler with the enhanced context
				next.ServeHTTP(w, r.WithContext(ctx))
			} else {
				http.Error(w, "Unauthorized: invalid token claims", http.StatusUnauthorized)
			}
		})
	}
}

// RequireRole returns a middleware that checks if the user has the required role
// Simple RBAC implementation - admin role has access to everything
// We'll add more granular permissions later if needed - virjilakrum
func RequireRole(requiredRole string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user role from context
			role, ok := r.Context().Value(UserRoleContextKey).(string)
			if !ok || role == "" {
				http.Error(w, "Forbidden: authentication required", http.StatusForbidden)
				return
			}

			// Check if user has the required role
			if role != requiredRole && role != "admin" { // Admin always has access
				http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GenerateToken generates a new JWT token for a user
// Setting expiration on tokens is critical for security
// We use 60 min default but can be configured per-environment - virjilakrum
func GenerateToken(userID, username, role string, secret string, expirationMinutes int) (string, error) {
	// Create claims with user information
	claims := UserClaims{
		UserID:   userID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expirationMinutes) * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "siger-api-gateway",
		},
	}

	// Create token with claims
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Sign the token with the secret key
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"root-backend-service/internal/core/ports"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "userID"
const sessionValidatorKey contextKey = "sessionValidator"

// Injected once by the router, shared by authenticated HTTP and the existing
// WebSocket authentication frame. No package-global database or callback.
func sessionValidationContext(validate ports.SessionValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), sessionValidatorKey, validate)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticateToken(ctx context.Context, tokenStr string) (string, error) {
	userID, err := parseToken(tokenStr)
	if err != nil {
		return "", err
	}
	validate, _ := ctx.Value(sessionValidatorKey).(ports.SessionValidator)
	if validate == nil {
		return userID, nil
	}
	claims := jwt.MapClaims{}
	if _, _, err := new(jwt.Parser).ParseUnverified(tokenStr, claims); err != nil {
		return "", fmt.Errorf("invalid session claims")
	}
	var version int64
	if raw, present := claims["sessionVersion"]; present {
		number, ok := raw.(float64)
		if !ok || number < 0 || number > 9007199254740991 || number != float64(int64(number)) {
			return "", fmt.Errorf("invalid session version")
		}
		version = int64(number)
	}
	if err := validate(ctx, userID, version); err != nil {
		return "", fmt.Errorf("invalid session")
	}
	return userID, nil
}

func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}
	return parts[1]
}

func parseToken(tokenStr string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "default_fallback_secret"
	}

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())

	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid claims")
	}

	userID, ok := claims["sub"].(string)
	if !ok {
		return "", fmt.Errorf("sub claim is not a string")
	}

	return userID, nil
}

// AuthMiddleware requires a valid JWT token
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := extractToken(r)
		if tokenStr == "" {
			http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
			return
		}

		userID, err := authenticateToken(r.Context(), tokenStr)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuthMiddleware parses the JWT if present, but doesn't block if missing
func OptionalAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := extractToken(r)
		if tokenStr != "" {
			userID, err := authenticateToken(r.Context(), tokenStr)
			if err == nil && userID != "" {
				ctx := context.WithValue(r.Context(), UserIDKey, userID)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

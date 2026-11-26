package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type AuthHandler struct {
	authService ports.AuthService
}

func NewAuthHandler(service ports.AuthService) *AuthHandler {
	return &AuthHandler{authService: service}
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	token, user, err := h.authService.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	response := map[string]interface{}{
		"token": token,
		"user":  user,
	}
	respondWithJSON(w, http.StatusOK, response)
}

type RegisterRequest struct {
	Name       string  `json:"name"`
	Username   string  `json:"username"`
	Email      string  `json:"email"`
	Password   string  `json:"password"`
	Dob        *string `json:"dob"`
	DocumentID *string `json:"documentId"`
	Country    *string `json:"country"`
	// AcceptedTerms tiene que ser true: aceptó los Términos y la Política de privacidad.
	AcceptedTerms bool   `json:"acceptedTerms"`
	TermsVersion  string `json:"termsVersion"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if !req.AcceptedTerms {
		respondWithJSON(w, http.StatusBadRequest, map[string]string{"message": "Tenés que aceptar los Términos y la Política de privacidad."})
		return
	}
	// Se guarda la versión vigente: la que manda el cliente solo sirve de referencia.
	user, err := h.authService.Register(r.Context(), req.Name, req.Username, req.Email, req.Password, domain.CurrentTermsVersion, req.Dob, req.DocumentID, req.Country)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidFullName) {
			respondWithJSON(w, http.StatusBadRequest, map[string]string{"message": "Ingresá tu nombre y apellido."})
			return
		}
		if errors.Is(err, domain.ErrInvalidPassword) {
			respondWithJSON(w, http.StatusBadRequest, map[string]string{"message": "La contraseña debe tener al menos 8 caracteres y como máximo 72 bytes."})
			return
		}
		if err.Error() == "user already exists with this email" {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		log.Printf("ERROR registering user: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	respondWithJSON(w, http.StatusCreated, user)
}

type GoogleLoginRequest struct {
	IDToken string `json:"idToken"`
}

func (h *AuthHandler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	var req GoogleLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.IDToken == "" {
		http.Error(w, "idToken is required", http.StatusBadRequest)
		return
	}

	token, user, err := h.authService.GoogleLogin(r.Context(), req.IDToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	response := map[string]interface{}{
		"token": token,
		"user":  user,
	}
	respondWithJSON(w, http.StatusOK, response)
}

// AppleLogin: POST /v1/auth/apple con {identityToken, authorizationCode, nonce, givenName, familyName}.
func (h *AuthHandler) AppleLogin(w http.ResponseWriter, r *http.Request) {
	var req domain.AppleLoginInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, user, err := h.authService.AppleLogin(r.Context(), req)
	switch {
	case errors.Is(err, domain.ErrAppleNotConfigured):
		respondWithError(w, http.StatusServiceUnavailable, "El inicio de sesión con Apple no está disponible")
	case errors.Is(err, domain.ErrInvalidAppleToken):
		respondWithError(w, http.StatusUnauthorized, "No pudimos verificar tu cuenta de Apple")
	case errors.Is(err, domain.ErrAccountUnavailable):
		respondWithError(w, http.StatusUnauthorized, "Esta cuenta ya no está disponible")
	case err != nil:
		log.Printf("apple login: %v", err)
		respondWithError(w, http.StatusInternalServerError, "No pudimos iniciar sesión con Apple. Intentá nuevamente.")
	default:
		respondWithJSON(w, http.StatusOK, map[string]interface{}{"token": token, "user": user})
	}
}

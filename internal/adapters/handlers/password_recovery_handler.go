package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type PasswordRecoveryHandler struct{ service ports.PasswordRecoveryService }

func NewPasswordRecoveryHandler(service ports.PasswordRecoveryService) *PasswordRecoveryHandler {
	return &PasswordRecoveryHandler{service: service}
}

func decodeRecoveryBody(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON object")
	}
	return nil
}

func recoveryClientIP(r *http.Request) string {
	// Do not trust caller-supplied X-Forwarded-For. Configure trusted proxy
	// normalization at the ingress before using forwarded addresses.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return "unknown"
}

func recoveryErrorResponse(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "No pudimos completar la solicitud. Intentá más tarde."
	switch {
	case errors.Is(err, domain.ErrRecoveryUnavailable):
		status = http.StatusServiceUnavailable
		message = "La recuperación no está disponible en este momento."
	case errors.Is(err, domain.ErrAuthRateLimited):
		status = http.StatusTooManyRequests
		message = "Demasiados intentos. Intentá más tarde."
		w.Header().Set("Retry-After", "900")
	case errors.Is(err, domain.ErrInvalidRecoveryEmail):
		status = http.StatusBadRequest
		message = "Ingresá un email válido."
	case errors.Is(err, domain.ErrInvalidResetToken):
		status = http.StatusBadRequest
		message = "El enlace es inválido o venció. Solicitá otro enlace."
	case errors.Is(err, domain.ErrInvalidPassword):
		status = http.StatusBadRequest
		message = "Las contraseñas deben coincidir y tener al menos 8 caracteres y como máximo 72 bytes."
	}
	respondWithJSON(w, status, map[string]string{"message": message})
}

func (h *PasswordRecoveryHandler) Request(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeRecoveryBody(w, r, &req); err != nil {
		respondWithJSON(w, 400, map[string]string{"message": "Solicitud inválida."})
		return
	}
	if err := h.service.RequestReset(r.Context(), req.Email, recoveryClientIP(r)); err != nil {
		recoveryErrorResponse(w, err)
		return
	}
	// Acknowledges a queued request, never asserts that an email was delivered.
	respondWithJSON(w, http.StatusAccepted, map[string]string{"message": "Solicitud recibida. Si corresponde a una cuenta con contraseña, recibirás un enlace de recuperación."})
}

func (h *PasswordRecoveryHandler) Reset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Token           string `json:"token"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := decodeRecoveryBody(w, r, &req); err != nil {
		respondWithJSON(w, 400, map[string]string{"message": "Solicitud inválida."})
		return
	}
	if err := h.service.ResetPassword(r.Context(), req.Token, req.Password, req.ConfirmPassword, recoveryClientIP(r)); err != nil {
		recoveryErrorResponse(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Contraseña actualizada. Iniciá sesión nuevamente."})
}

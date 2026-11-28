package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

// APIError es el formato de error de afters y "Mandanos tu evento": {"status": 403, "message": "...", "code": "..."}.
// A diferencia de respondWithError, status es el código HTTP (número) y code es estable para el front.
type APIError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func respondWithAPIError(w http.ResponseWriter, status int, code, message string) {
	respondWithJSON(w, status, APIError{Status: status, Message: message, Code: code})
}

type apiErrorMapping struct {
	target  error
	status  int
	code    string
	message string
}

var apiErrorMappings = []apiErrorMapping{
	{domain.ErrContentPolicyViolation, http.StatusUnprocessableEntity, "content_policy", "Este contenido infringe nuestras normas de la comunidad"},
	{domain.ErrInvalidID, http.StatusBadRequest, "invalid_id", "Id inválido"},
	{domain.ErrKycRequired, http.StatusForbidden, "kyc_required", "Necesitás verificar tu identidad"},
	{domain.ErrUnderage, http.StatusForbidden, "underage", "Tenés que ser mayor de 18. Si ya lo sos, completá tu fecha de nacimiento en tu perfil."},
	{domain.ErrNotAfterHost, http.StatusForbidden, "not_host", "Solo quien subió el after puede hacer esto"},
	{domain.ErrAdminRequired, http.StatusForbidden, "forbidden", "Solo un administrador puede hacer esto"},
	{domain.ErrAfterNotFound, http.StatusNotFound, "not_found", "El after no existe o ya no está disponible"},
	{domain.ErrEventNotFound, http.StatusNotFound, "event_not_found", "Evento no encontrado"},
	{domain.ErrAfterRequestNotFound, http.StatusNotFound, "request_not_found", "No hay un pedido de esa persona"},
	{domain.ErrSubmissionNotFound, http.StatusNotFound, "not_found", "El envío no existe"},
	{domain.ErrAfterFull, http.StatusConflict, "after_full", "El after está lleno"},
	{domain.ErrAlreadyJoined, http.StatusConflict, "already_joined", "Ya pediste entrar o ya vas a este after"},
	{domain.ErrAfterEnded, http.StatusConflict, "after_ended", "El after ya terminó"},
	{domain.ErrAfterRequestDecided, http.StatusConflict, "request_not_pending", "Ese pedido ya fue respondido"},
	{domain.ErrHostCannotLeaveAfter, http.StatusConflict, "host_cannot_leave", "Quien subió el after no puede dejar de ir"},
	{domain.ErrSubmissionNotPending, http.StatusConflict, "not_pending", "El envío ya fue revisado"},
	{domain.ErrCannotReportOwnAfter, http.StatusBadRequest, "invalid_request", "No podés denunciar tu propio after"},
	{domain.ErrInvalidEvent, http.StatusUnprocessableEntity, "invalid_event", "El evento no existe o ya terminó"},
	{domain.ErrTooManyActiveAfters, http.StatusTooManyRequests, "too_many_active", "Ya tenés 3 afters activos"},
	{domain.ErrTooManyPendingSubmissions, http.StatusTooManyRequests, "too_many_pending", "Ya tenés 5 eventos esperando revisión"},
}

// writeAPIError traduce los errores de dominio. Los internos se loguean (nunca llevan la dirección ni datos de KYC).
func writeAPIError(w http.ResponseWriter, err error) {
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", validation.Message)
		return
	}
	for _, m := range apiErrorMappings {
		if errors.Is(err, m.target) {
			respondWithAPIError(w, m.status, m.code, m.message)
			return
		}
	}
	log.Printf("afters api: %v", err)
	respondWithAPIError(w, http.StatusInternalServerError, "internal_error", "Error interno")
}

// requireAccess es el middleware de autorización por perfil. Va después de AuthMiddleware. Sin repositorio
// configurado rechaza todo (fail-closed).
func requireAccess(access ports.ViewerAccessRepository, allowed func(domain.ViewerAccess) bool, denied error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _ := r.Context().Value(UserIDKey).(string)
			if userID == "" {
				respondWithAPIError(w, http.StatusUnauthorized, "unauthorized", "Iniciá sesión para continuar")
				return
			}
			if access == nil {
				log.Printf("access middleware without repository")
				respondWithAPIError(w, http.StatusServiceUnavailable, "unavailable", "Servicio no disponible")
				return
			}
			viewer, err := access.ViewerAccess(r.Context(), userID)
			if errors.Is(err, domain.ErrUserNotFound) {
				writeAPIError(w, denied)
				return
			}
			if err != nil {
				writeAPIError(w, err)
				return
			}
			if viewer.Unavailable || !allowed(viewer) {
				writeAPIError(w, denied)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireKyc deja pasar solo a quien tiene la identidad verificada (users.is_kyc_verified): 403 kyc_required.
func RequireKyc(access ports.ViewerAccessRepository) func(http.Handler) http.Handler {
	return requireAccess(access, func(v domain.ViewerAccess) bool { return v.IsKycVerified }, domain.ErrKycRequired)
}

// RequireAdmin deja pasar solo a users.role = 'ADMIN': 403 forbidden.
func RequireAdmin(access ports.ViewerAccessRepository) func(http.Handler) http.Handler {
	return requireAccess(access, func(v domain.ViewerAccess) bool { return v.IsAdmin() }, domain.ErrAdminRequired)
}

func apiUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, _ := r.Context().Value(UserIDKey).(string)
	if userID == "" {
		respondWithAPIError(w, http.StatusUnauthorized, "unauthorized", "Iniciá sesión para continuar")
		return "", false
	}
	return userID, true
}

// decodeAPIBody lee un JSON de hasta 32 KB. Con allowEmpty el body puede faltar. Los campos desconocidos se ignoran
// (cada handler copia explícitamente los que acepta: la identidad sale siempre del JWT).
func decodeAPIBody(w http.ResponseWriter, r *http.Request, dst interface{}, allowEmpty bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return true
		}
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", "Cuerpo inválido")
		return false
	}
	return true
}

// maxUploadImageBytes: portadas de afters y flyers de eventos enviados.
const maxUploadImageBytes = 8 << 20

// uploadUserImage sube la imagen del campo multipart "image" a <prefix>/<userID>/<ts>.<ext> y responde {key, url}.
// El tipo se decide por el contenido (JPG, PNG o WebP), no por lo que declare el cliente.
func uploadUserImage(w http.ResponseWriter, r *http.Request, storage s3.S3Service, prefix string) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	if storage == nil {
		respondWithAPIError(w, http.StatusServiceUnavailable, "unavailable", "El almacenamiento de imágenes no está disponible")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadImageBytes+(1<<20))
	if err := r.ParseMultipartForm(maxUploadImageBytes); err != nil {
		respondWithAPIError(w, http.StatusRequestEntityTooLarge, "too_large", "La imagen es demasiado grande (máximo 8 MB)")
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", `Falta la imagen (campo "image")`)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadImageBytes+1))
	if err != nil || len(data) == 0 {
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", "No se pudo leer la imagen")
		return
	}
	if len(data) > maxUploadImageBytes {
		respondWithAPIError(w, http.StatusRequestEntityTooLarge, "too_large", "La imagen es demasiado grande (máximo 8 MB)")
		return
	}
	contentType := http.DetectContentType(data)
	ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}[contentType]
	if ext == "" {
		respondWithAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Formato no soportado: usá JPG, PNG o WebP")
		return
	}
	key := fmt.Sprintf("%s/%s/%d.%s", prefix, userID, time.Now().UnixNano(), ext)
	if _, err := storage.UploadToS3(r.Context(), bytes.NewReader(data), key, contentType); err != nil {
		log.Printf("%s upload failed: %v", prefix, err)
		respondWithAPIError(w, http.StatusInternalServerError, "internal_error", "No se pudo subir la imagen")
		return
	}
	url, err := storage.GenerateViewUrl(r.Context(), key, 7*24*time.Hour)
	if err != nil {
		log.Printf("%s view url failed: %v", prefix, err)
		respondWithAPIError(w, http.StatusInternalServerError, "internal_error", "No se pudo preparar la imagen")
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]string{"key": key, "url": url})
}

// signStoredImage convierte una clave S3 en una URL firmada (las URLs http se dejan como están).
func signStoredImage(ctx context.Context, storage s3.S3Service, value *string) *string {
	if value == nil {
		return nil
	}
	raw := strings.TrimSpace(*value)
	if raw == "" {
		return nil
	}
	if storage == nil || strings.HasPrefix(raw, "http") {
		return &raw
	}
	url, err := storage.GenerateViewUrl(ctx, raw, 7*24*time.Hour)
	if err != nil {
		return nil
	}
	return &url
}

func signAfterSummary(ctx context.Context, storage s3.S3Service, s *domain.AfterSummary) {
	s.CoverURL = signStoredImage(ctx, storage, s.CoverURL)
	s.Host.AvatarURL = signStoredImage(ctx, storage, s.Host.AvatarURL)
	if s.Event != nil {
		s.Event.Image = signStoredImage(ctx, storage, s.Event.Image)
	}
}

func signAfterSummaries(ctx context.Context, storage s3.S3Service, list []domain.AfterSummary) {
	for i := range list {
		signAfterSummary(ctx, storage, &list[i])
	}
}

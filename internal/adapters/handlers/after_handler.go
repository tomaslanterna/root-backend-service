package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"root-backend-service/internal/services/s3"
)

// AfterHandler expone los afters (contrato en AFTERS.md). La identidad sale solo del JWT; la verificación de
// identidad de los endpoints de creación y edición la impone RequireKyc en el router (y el servicio la vuelve a mirar).
type AfterHandler struct {
	svc     ports.AfterService
	storage s3.S3Service
}

func NewAfterHandler(svc ports.AfterService, storage s3.S3Service) *AfterHandler {
	return &AfterHandler{svc: svc, storage: storage}
}

// patchField distingue "no vino" (Set false) de "vino null" (Set true, Value nil).
type patchField[T any] struct {
	Set   bool
	Value *T
}

func (p *patchField[T]) UnmarshalJSON(data []byte) error {
	p.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		p.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	p.Value = &value
	return nil
}

func (p patchField[T]) nullable() domain.Nullable[T] {
	return domain.Nullable[T]{Set: p.Set, Value: p.Value}
}

// looseString acepta un texto o un número (por ejemplo price: 500 o "500").
type looseString string

func (s *looseString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = looseString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return fmt.Errorf("expected a string or a number")
	}
	*s = looseString(number.String())
	return nil
}

func looseToString(value *looseString) *string {
	if value == nil {
		return nil
	}
	s := string(*value)
	return &s
}

type afterBody struct {
	Name      string       `json:"name"`
	EventID   *string      `json:"eventId"`
	StartsAt  *time.Time   `json:"startsAt"`
	EndsAt    *time.Time   `json:"endsAt"`
	Zone      string       `json:"zone"`
	Address   string       `json:"address"`
	Access    string       `json:"access"`
	Price     *looseString `json:"price"`
	TicketURL *string      `json:"ticketUrl"`
	Capacity  *int         `json:"capacity"`
	Genres    []string     `json:"genres"`
	Lineup    *string      `json:"lineup"`
	Rules     *string      `json:"rules"`
	CoverKey  *string      `json:"coverKey"`
}

type afterPatchBody struct {
	Name      *string                 `json:"name"`
	EventID   patchField[string]      `json:"eventId"`
	StartsAt  *time.Time              `json:"startsAt"`
	EndsAt    patchField[time.Time]   `json:"endsAt"`
	Zone      *string                 `json:"zone"`
	Address   *string                 `json:"address"`
	Access    *string                 `json:"access"`
	Price     patchField[looseString] `json:"price"`
	TicketURL patchField[string]      `json:"ticketUrl"`
	Capacity  patchField[int]         `json:"capacity"`
	Genres    *[]string               `json:"genres"`
	Lineup    patchField[string]      `json:"lineup"`
	Rules     patchField[string]      `json:"rules"`
	CoverKey  patchField[string]      `json:"coverKey"`
	Notice    patchField[string]      `json:"notice"`
}

func (h *AfterHandler) respondDetail(w http.ResponseWriter, r *http.Request, status int, detail *domain.AfterDetail) {
	signAfterSummary(r.Context(), h.storage, &detail.AfterSummary)
	for i := range detail.FollowedGoing.Preview {
		detail.FollowedGoing.Preview[i].AvatarURL = signStoredImage(r.Context(), h.storage, detail.FollowedGoing.Preview[i].AvatarURL)
	}
	// La respuesta depende de quien mira (dirección, aviso): nada de caché compartida.
	w.Header().Set("Cache-Control", "private, no-store")
	respondWithJSON(w, status, detail)
}

func (h *AfterHandler) respondPage(w http.ResponseWriter, r *http.Request, page domain.AfterPage) {
	signAfterSummaries(r.Context(), h.storage, page.Data)
	w.Header().Add("Vary", "Authorization")
	if viewer, _ := r.Context().Value(UserIDKey).(string); viewer != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	respondWithJSON(w, http.StatusOK, page)
}

// GET /v1/events/{id}/afters — JWT opcional.
func (h *AfterHandler) ListForEvent(w http.ResponseWriter, r *http.Request) {
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	page, err := h.svc.ListForEvent(r.Context(), chi.URLParam(r, "id"), viewerID, intQuery(r, "limit"), intQuery(r, "offset"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.respondPage(w, r, page)
}

// GET /v1/afters?from&until&zone&limit&offset — JWT opcional.
func (h *AfterHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := domain.AfterFilter{Zone: query.Get("zone"), Limit: intQuery(r, "limit"), Offset: intQuery(r, "offset")}
	from, err := parseOptionalDate(query.Get("from"), "from", false)
	if err != nil {
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if from != nil {
		filter.From = *from
	}
	if filter.Until, err = parseOptionalDate(query.Get("until"), "until", true); err != nil {
		respondWithAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	viewerID, _ := r.Context().Value(UserIDKey).(string)
	page, err := h.svc.List(r.Context(), filter, viewerID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.respondPage(w, r, page)
}

// GET /v1/afters/{id} — sesión y mayor de 18.
func (h *AfterHandler) Detail(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.Detail(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.respondDetail(w, r, http.StatusOK, detail)
}

// POST /v1/afters/cover — [KYC]
func (h *AfterHandler) UploadCover(w http.ResponseWriter, r *http.Request) {
	uploadUserImage(w, r, h.storage, "afters")
}

// POST /v1/afters — [KYC]
func (h *AfterHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	var body afterBody
	if !decodeAPIBody(w, r, &body, false) {
		return
	}
	fields := domain.AfterFields{
		Name: body.Name, EventID: body.EventID, EndsAt: body.EndsAt, Zone: body.Zone, Address: body.Address,
		Access: body.Access, Price: looseToString(body.Price), TicketURL: body.TicketURL, Capacity: body.Capacity,
		Genres: body.Genres, Lineup: body.Lineup, Rules: body.Rules, CoverKey: body.CoverKey,
	}
	if body.StartsAt != nil {
		fields.StartsAt = *body.StartsAt
	}
	detail, err := h.svc.Create(r.Context(), userID, fields)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.respondDetail(w, r, http.StatusCreated, detail)
}

// PATCH /v1/afters/{id} — [KYC], solo el host.
func (h *AfterHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	var body afterPatchBody
	if !decodeAPIBody(w, r, &body, false) {
		return
	}
	price := domain.Nullable[string]{Set: body.Price.Set, Value: looseToString(body.Price.Value)}
	patch := domain.AfterPatch{
		Name: body.Name, EventID: body.EventID.nullable(), StartsAt: body.StartsAt, EndsAt: body.EndsAt.nullable(),
		Zone: body.Zone, Address: body.Address, Access: body.Access, Price: price, TicketURL: body.TicketURL.nullable(),
		Capacity: body.Capacity.nullable(), Genres: body.Genres, Lineup: body.Lineup.nullable(), Rules: body.Rules.nullable(),
		CoverKey: body.CoverKey.nullable(), Notice: body.Notice.nullable(),
	}
	detail, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), userID, patch)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.respondDetail(w, r, http.StatusOK, detail)
}

// DELETE /v1/afters/{id} — solo el host; soft delete (status removed). 204.
func (h *AfterHandler) Remove(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	if err := h.svc.Remove(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/afters/{id}/join — body opcional {"message"}. Responde 200 con el AfterDetail actualizado.
func (h *AfterHandler) Join(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if !decodeAPIBody(w, r, &body, true) {
		return
	}
	detail, err := h.svc.Join(r.Context(), chi.URLParam(r, "id"), userID, body.Message)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	h.respondDetail(w, r, http.StatusOK, detail)
}

// DELETE /v1/afters/{id}/join — cancela el pedido o deja de ir. Idempotente, 204.
func (h *AfterHandler) Leave(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	if err := h.svc.Leave(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /v1/afters/{id}/requests?status=requested — solo el host.
func (h *AfterHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.ListRequests(r.Context(), chi.URLParam(r, "id"), userID, strings.TrimSpace(r.URL.Query().Get("status")))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	for i := range list {
		list[i].AvatarURL = signStoredImage(r.Context(), h.storage, list[i].AvatarURL)
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

func (h *AfterHandler) decide(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := apiUser(w, r)
		if !ok {
			return
		}
		if err := h.svc.Decide(r.Context(), chi.URLParam(r, "id"), userID, chi.URLParam(r, "userId"), accept); err != nil {
			writeAPIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// POST /v1/afters/{id}/requests/{userId}/accept — solo el host. 204.
func (h *AfterHandler) Accept(w http.ResponseWriter, r *http.Request) { h.decide(true)(w, r) }

// POST /v1/afters/{id}/requests/{userId}/reject — solo el host. 204.
func (h *AfterHandler) Reject(w http.ResponseWriter, r *http.Request) { h.decide(false)(w, r) }

// POST /v1/afters/{id}/report {reason} — 204; una denuncia por usuario (repetirla es idempotente).
func (h *AfterHandler) Report(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeAPIBody(w, r, &body, false) {
		return
	}
	if err := h.svc.Report(r.Context(), chi.URLParam(r, "id"), userID, body.Reason); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /v1/admin/afters?status=hidden — ADMIN.
func (h *AfterHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	userID, ok := apiUser(w, r)
	if !ok {
		return
	}
	list, err := h.svc.AdminList(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("status")), intQuery(r, "limit"), intQuery(r, "offset"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	for i := range list {
		signAfterSummary(r.Context(), h.storage, &list[i].AfterSummary)
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{"data": list})
}

func (h *AfterHandler) adminSet(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := apiUser(w, r)
		if !ok {
			return
		}
		if err := h.svc.AdminSetStatus(r.Context(), userID, chi.URLParam(r, "id"), status); err != nil {
			writeAPIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// POST /v1/admin/afters/{id}/restore — ADMIN. 204.
func (h *AfterHandler) AdminRestore(w http.ResponseWriter, r *http.Request) {
	h.adminSet(domain.AfterStatusActive)(w, r)
}

// POST /v1/admin/afters/{id}/remove — ADMIN. 204.
func (h *AfterHandler) AdminRemove(w http.ResponseWriter, r *http.Request) {
	h.adminSet(domain.AfterStatusRemoved)(w, r)
}

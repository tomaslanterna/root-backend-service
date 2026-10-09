package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"html"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

// Transactional is disabled unless an operator explicitly selects a provider.
// No SDK, credentials, recipient or recovery URL is exposed to the frontend.
type Transactional struct {
	provider, key, from, name, endpoint string
	client                              *http.Client
}

func NewTransactional(provider, key, from string) (*Transactional, error) {
	if provider == "" {
		if key != "" || from != "" {
			return nil, errors.New("select PASSWORD_RESET_EMAIL_PROVIDER before configuring email credentials")
		}
		return nil, nil
	}
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("PASSWORD_RESET_EMAIL_API_KEY is required")
	}
	address, err := mail.ParseAddress(from)
	if err != nil || strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("PASSWORD_RESET_EMAIL_FROM must be an authorized sender email")
	}
	s := &Transactional{provider: provider, key: key, from: address.Address, name: address.Name,
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	switch provider {
	case "resend":
		s.endpoint = "https://api.resend.com/emails"
	case "brevo":
		s.endpoint = "https://api.brevo.com/v3/smtp/email"
	default:
		return nil, errors.New("PASSWORD_RESET_EMAIL_PROVIDER must be resend or brevo")
	}
	if s.name == "" {
		s.name = "Root"
	}
	return s, nil
}

func (s *Transactional) SendPasswordReset(ctx context.Context, recipient, link, deliveryID string) error {
	const subject = "Restablecé tu contraseña de Root"
	text := "Recibimos una solicitud para cambiar tu contraseña de Root.\n\nAbrí este enlace: " + link + "\n\nEl enlace vence a los 30 minutos de solicitarlo y se puede usar una sola vez. Si no lo solicitaste, ignorá este correo."
	markup := `<h1>Root</h1><p>Recibimos una solicitud para cambiar tu contraseña.</p><p><a href="` + html.EscapeString(link) + `">Restablecer contraseña</a></p><p>El enlace vence a los 30 minutos de solicitarlo y se puede usar una sola vez. Si no lo solicitaste, ignorá este correo.</p>`
	idempotencyKey := "password-reset-" + deliveryID
	var payload any
	if s.provider == "resend" {
		payload = map[string]any{"from": (&mail.Address{Name: s.name, Address: s.from}).String(), "to": []string{recipient}, "subject": subject, "text": text, "html": markup}
	} else {
		// Brevo uses its special UUID header, not the Resend HTTP header.
		brevoKey := uuid.NewSHA1(uuid.NameSpaceURL, []byte(idempotencyKey)).String()
		payload = map[string]any{"sender": map[string]string{"name": s.name, "email": s.from}, "to": []map[string]string{{"email": recipient}}, "subject": subject,
			"textContent": text, "htmlContent": markup, "headers": map[string]string{"idempotencyKey": brevoKey}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode recovery email")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create recovery email request")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.provider == "resend" {
		req.Header.Set("Authorization", "Bearer "+s.key)
		req.Header.Set("Idempotency-Key", idempotencyKey)
	} else {
		req.Header.Set("api-key", s.key)
	}
	response, err := s.client.Do(req)
	if err != nil {
		return errors.New("recovery email provider unavailable")
	}
	defer response.Body.Close()
	if s.provider == "brevo" && response.StatusCode == http.StatusBadRequest {
		var problem struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&problem) == nil && problem.Code == "duplicate_parameter" && strings.Contains(strings.ToLower(problem.Message), "idempotency") {
			// Same stable key was already accepted; an interrupted worker can confirm it.
			return nil
		}
		return errors.New("recovery email provider rejected the request")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("recovery email provider rejected the request")
	}
	var receipt struct {
		ID        string `json:"id"`
		MessageID string `json:"messageId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&receipt); err != nil {
		return errors.New("invalid recovery email receipt")
	}
	if (s.provider == "resend" && receipt.ID == "") || (s.provider == "brevo" && receipt.MessageID == "") {
		return errors.New("missing recovery email receipt")
	}
	return nil
}

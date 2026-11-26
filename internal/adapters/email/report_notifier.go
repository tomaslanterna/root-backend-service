package email

import (
	"context"
	"errors"
	"html"
	"net/mail"
	"strings"

	"root-backend-service/internal/core/domain"
)

// ReportNotifier avisa por email al equipo de soporte cada denuncia nueva. Implementa ports.ReportNotifier.
type ReportNotifier struct {
	sender    *Transactional
	recipient string
}

// NewReportNotifier devuelve nil (sin avisos) si no hay proveedor de email o destinatario configurado.
func NewReportNotifier(sender *Transactional, recipient string) (*ReportNotifier, error) {
	recipient = strings.TrimSpace(recipient)
	if sender == nil || recipient == "" {
		return nil, nil
	}
	address, err := mail.ParseAddress(recipient)
	if err != nil || strings.ContainsAny(recipient, "\r\n") {
		return nil, errors.New("SUPPORT_EMAIL must be a valid email address")
	}
	return &ReportNotifier{sender: sender, recipient: address.Address}, nil
}

var reportTargetLabels = map[domain.ReportTargetType]string{
	domain.ReportTargetPost:    "Publicación",
	domain.ReportTargetComment: "Comentario",
	domain.ReportTargetMessage: "Mensaje de chat",
	domain.ReportTargetUser:    "Cuenta",
}

var reportReasonLabels = map[string]string{
	domain.ReportReasonSpam:  "Spam o publicidad engañosa",
	domain.ReportReasonAbuse: "Acoso, odio o contenido inapropiado",
	domain.ReportReasonOther: "Otro motivo",
}

func (n *ReportNotifier) SendReportNotification(ctx context.Context, report domain.ContentReport) error {
	label := reportTargetLabels[report.TargetType]
	if label == "" {
		label = string(report.TargetType)
	}
	reason := reportReasonLabels[report.Reason]
	if reason == "" {
		reason = report.Reason
	}
	author := "-"
	if report.TargetUsername != "" {
		author = "@" + report.TargetUsername
	}
	reporter := "-"
	if report.ReporterUsername != "" {
		reporter = "@" + report.ReporterUsername
	}
	lines := [][2]string{
		{"Tipo", label},
		{"Motivo", reason},
		{"Autor del contenido", author},
		{"Denunciado por", reporter},
		{"Detalle", report.Details},
		{"Contenido", report.ContentSnapshot},
		{"ID de la denuncia", report.ID},
		{"ID del contenido", report.TargetID},
	}
	var text, markup strings.Builder
	text.WriteString("Llegó una denuncia nueva en Root. Revisala en menos de 24 h.\n\n")
	markup.WriteString("<h1>Nueva denuncia en Root</h1><p>Revisala en menos de 24 h.</p><table>")
	for _, line := range lines {
		text.WriteString(line[0] + ": " + line[1] + "\n")
		markup.WriteString("<tr><th align=\"left\">" + html.EscapeString(line[0]) + "</th><td>" + html.EscapeString(line[1]) + "</td></tr>")
	}
	text.WriteString("\nResolvela con: go run ./cmd/moderation show --id " + report.ID + "\n")
	markup.WriteString("</table><p><code>go run ./cmd/moderation show --id " + html.EscapeString(report.ID) + "</code></p>")
	subject := "Denuncia: " + label + " (" + reason + ")"
	return n.sender.send(ctx, n.recipient, subject, text.String(), markup.String(), "content-report-"+report.ID)
}

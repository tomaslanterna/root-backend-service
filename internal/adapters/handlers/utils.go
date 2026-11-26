package handlers

import (
	"encoding/json"
	"net/http"
)

type ErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"status":"error","message":"Error al codificar la respuesta JSON"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func respondWithError(w http.ResponseWriter, code int, message string) {
	respondWithJSON(w, code, ErrorResponse{
		Status:  "error",
		Message: message,
	})
}

// respondWithBlocked responde 403 {"error":"blocked"}: hay un bloqueo entre las dos personas.
func respondWithBlocked(w http.ResponseWriter) {
	respondWithJSON(w, http.StatusForbidden, map[string]string{
		"error":   "blocked",
		"message": "No podés interactuar con esta cuenta.",
	})
}

// respondWithContentPolicy responde 422 cuando el moderador de contenido rechazó el texto.
func respondWithContentPolicy(w http.ResponseWriter) {
	respondWithError(w, http.StatusUnprocessableEntity, "Este contenido infringe nuestras normas de la comunidad")
}

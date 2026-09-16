package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type AIHandler struct{}

func NewAIHandler() *AIHandler {
	return &AIHandler{}
}

func (h *AIHandler) EnhanceText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		respondWithError(w, http.StatusInternalServerError, "AI service not configured")
		return
	}

	// Prompt estricto para ortografía y gramática
	prompt := fmt.Sprintf(`Eres un asistente experto en redacción. 
Tu única tarea es corregir los errores ortográficos y gramaticales del siguiente texto, manteniendo exactamente el mismo tono, intención y estilo. 
NO agregues información extra, NO respondas a preguntas del texto, SOLO devuelve el texto corregido. Si el texto está perfecto, devuélvelo tal cual.

Texto a corregir:
"%s"`, req.Text)

	// Payload para Gemini API
	payloadBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": 0.2, // Baja temperatura para que no sea creativo
		},
	}

	jsonData, err := json.Marshal(payloadBody)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error preparing AI request")
		return
	}

	apiURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=" + apiKey
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Println("Error HTTP:", err)
		respondWithError(w, http.StatusInternalServerError, "Error connecting to AI service")
		return
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Println("Gemini API Error:", string(b))
		respondWithError(w, http.StatusInternalServerError, "Error from AI service")
		return
	}

	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing AI response")
		return
	}

	enhanced := req.Text
	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		enhanced = strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text)
		// Quitar comillas si la IA las agregó
		enhanced = strings.TrimPrefix(enhanced, "\"")
		enhanced = strings.TrimSuffix(enhanced, "\"")
	}

	respondWithJSON(w, http.StatusOK, map[string]string{
		"enhanced_text": enhanced,
	})
}

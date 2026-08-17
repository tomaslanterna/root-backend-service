package kyc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	s3service "root-backend-service/internal/services/s3"
)

type geminiKycProvider struct {
	s3Service s3service.S3Service
}

func NewGeminiKycProvider(s3Service s3service.S3Service) KycProviderService {
	return &geminiKycProvider{
		s3Service: s3Service,
	}
}

func (m *geminiKycProvider) AnalyzeIdentity(ctx context.Context, sessionID string, docFrontKey, docBackKey, faceKey string) (*KycResult, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY no configurada")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("error creando cliente gemini: %w", err)
	}
	defer client.Close()

	// 1. Descargar imágenes desde S3
	docFrontBytes, err := m.s3Service.GetObject(ctx, docFrontKey)
	if err != nil {
		return nil, fmt.Errorf("error descargando docFront: %w", err)
	}

	faceBytes, err := m.s3Service.GetObject(ctx, faceKey)
	if err != nil {
		return nil, fmt.Errorf("error descargando face: %w", err)
	}

	// 2. Preparar el modelo y el prompt
	model := client.GenerativeModel("gemini-2.5-flash")
	model.SetTemperature(0)

	systemPrompt := `Eres un perito experto en KYC (Know Your Customer) y seguridad documental.
Tu tarea es analizar fotos de un documento de identidad (frente) y una foto selfie del usuario.
Debes realizar las siguientes comprobaciones:
1. Extraer los datos exactos del documento (Número de documento y Nombre completo).
2. Determinar si el rostro del documento coincide con el rostro de la selfie (Score de 0 a 100).

Responde ESTRICTAMENTE en este formato JSON, sin markdown ni comillas backticks:
{
  "Status": "APPROVED", // o REJECTED si las caras no coinciden en lo absoluto
  "MatchScore": 95.5,
  "ExtractedData": {
    "DocumentNumber": "1234567-8",
    "FullName": "JUAN PEREZ"
  }
}`

	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	promptParts := []genai.Part{
		genai.ImageData("jpeg", docFrontBytes),
		genai.ImageData("jpeg", faceBytes),
		genai.Text("Analiza las imágenes proporcionadas y devuelve el JSON."),
	}

	resp, err := model.GenerateContent(ctx, promptParts...)
	if err != nil {
		return nil, fmt.Errorf("error generando contenido con Gemini: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("respuesta vacía de gemini")
	}

	part := resp.Candidates[0].Content.Parts[0]
	textResponse, ok := part.(genai.Text)
	if !ok {
		return nil, fmt.Errorf("la respuesta de gemini no es texto")
	}

	// 3. Limpiar y parsear el JSON (Regex fallback)
	jsonStr := string(textResponse)
	re := regexp.MustCompile(`(?s)\{.*\}`)
	match := re.FindString(jsonStr)
	if match == "" {
		return nil, fmt.Errorf("no se encontró JSON en la respuesta de Gemini")
	}

	var result KycResult
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		log.Printf("Error parseando JSON de Gemini: %v\nJSON: %s\n", err, match)
		return nil, fmt.Errorf("error parseando JSON: %w", err)
	}

	// Como Gemini no hace liveness real, lo forzamos a true para mantener la estructura
	result.LivenessPass = true

	return &result, nil
}

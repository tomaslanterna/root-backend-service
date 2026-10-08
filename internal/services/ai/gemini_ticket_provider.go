package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	"root-backend-service/internal/core/ports"
)

type geminiTicketProvider struct{}

func NewGeminiTicketProvider() ports.AiTicketService {
	return &geminiTicketProvider{}
}

func (p *geminiTicketProvider) FetchEventTickets(ctx context.Context, title, date, location, country, ticketURL string) (*ports.TicketInfoResult, error) {
	if ticketURL == "" {
		return nil, fmt.Errorf("no ticket url provided")
	}

	// 1. Fetch HTML content
	clientHttp := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ticketURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	// Many ticket sites require a user-agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	respHttp, err := clientHttp.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching ticket url: %w", err)
	}
	defer respHttp.Body.Close()

	htmlBytes, err := io.ReadAll(io.LimitReader(respHttp.Body, 500*1024)) // Limit to 500KB to save tokens
	if err != nil {
		return nil, fmt.Errorf("error reading ticket url body: %w", err)
	}
	pageContent := string(htmlBytes)

	// 2. Setup Gemini
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY no configurada")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("error creando cliente gemini: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-2.5-flash")
	model.SetTemperature(0)

	systemPrompt := fmt.Sprintf(`Eres un asistente especializado en extraer información de venta de entradas para eventos. 
Tu tarea es analizar el siguiente código HTML o texto extraído de la página oficial de tickets del evento:
- Nombre del evento: '%s'
- Fecha: '%s'
- Ubicación (Lugar): '%s'
- País: '%s'

Debes extraer TODAS las tandas de entradas (tiers) disponibles u ofrecidas, su precio numérico, moneda de pago (por ej. ARS, USD, UYU), si están agotadas (soldOut = true), y si existe alguna advertencia de 'últimas entradas' o si quedan muy pocas (fewRemaining = true).
La fuente debe ser la URL provista: '%s'.

REGLAS ESTRICTAS:
1. Si no encuentras información confiable de precios y tandas en el contenido proveído, devuelve una lista vacía de tandas.
2. Devuelve la respuesta ÚNICAMENTE en formato JSON plano y válido.
3. NO agregues saludos, explicaciones, markdown, ni el bloque `+"```json"+` alrededor de la respuesta.
4. Asegúrate de que los tipos de datos coincidan (precio como número, booleanos para los estados).
5. Clasificación de tipo de entrada: Si el nombre de la entrada NO indica explícitamente que es 'VIP', 'Backstage', 'Mesa' o 'General', asume que es una entrada general y agrega la palabra 'General - ' al principio de su nombre (por ejemplo: si el texto dice 'Lote 2 - Acceso hasta las 23hs', devuélvelo como 'General - Lote 2 - Acceso hasta las 23hs'). Si ya indica que es VIP o General, respeta su nombre.

Formato de ejemplo:
{
  "source": "https://www.passline.com/eventos/ejemplo",
  "ticketTiers": [
    {
      "name": "Tanda 1",
      "price": 15000,
      "currency": "ARS",
      "soldOut": true,
      "fewRemaining": false
    }
  ]
}`, title, date, location, country, ticketURL)

	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	resp, err := model.GenerateContent(ctx, genai.Text("Contenido de la página de tickets:\n"+pageContent+"\n\nExtrae el JSON con los tickets."))
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

	jsonStr := string(textResponse)
	re := regexp.MustCompile(`(?s)\{.*\}`)
	match := re.FindString(jsonStr)
	if match == "" {
		return nil, fmt.Errorf("no se encontró JSON en la respuesta de Gemini")
	}

	var result ports.TicketInfoResult
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		log.Printf("Error parseando JSON de Gemini (Tickets): %v\nJSON: %s\n", err, match)
		return nil, fmt.Errorf("error parseando JSON: %w", err)
	}

	return &result, nil
}

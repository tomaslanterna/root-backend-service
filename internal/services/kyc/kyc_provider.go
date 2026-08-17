package kyc

import (
	"context"
	"log"
)

// KycProviderService define la interfaz para conectarnos con la IA externa
// Puede implementarse usando AWS Rekognition, Sumsub, Veriff, etc.
type KycProviderService interface {
	AnalyzeIdentity(ctx context.Context, sessionID string, docFrontKey, docBackKey, faceKey string) (*KycResult, error)
}

type KycResult struct {
	Status        string // PENDING, APPROVED, REJECTED
	MatchScore    float64
	LivenessPass  bool
	ExtractedData struct {
		DocumentNumber string
		FullName       string
	}
}

type mockKycProvider struct{}

func NewMockKycProvider() KycProviderService {
	return &mockKycProvider{}
}

func (m *mockKycProvider) AnalyzeIdentity(ctx context.Context, sessionID string, docFrontKey, docBackKey, faceKey string) (*KycResult, error) {
	log.Printf("Iniciando análisis KYC para sesión: %s\n", sessionID)
	log.Printf("Documentos en S3: %s, %s | Cara en S3: %s\n", docFrontKey, docBackKey, faceKey)

	// Aquí iría la llamada HTTP a la API externa (ej. AWS Rekognition: CompareFaces)
	// Simulamos un retraso de procesamiento
	// time.Sleep(2 * time.Second)

	return &KycResult{
		Status:       "APPROVED",
		MatchScore:   98.5,
		LivenessPass: true,
		ExtractedData: struct {
			DocumentNumber string
			FullName       string
		}{
			DocumentNumber: "1234567-8",
			FullName:       "USUARIO DE PRUEBA",
		},
	}, nil
}

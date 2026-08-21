# Root Backend Service

This project is the backend for **root**, serving APIs for features like the Squad Matcher, KYC verification, and user management.
The application is built with **Go (Golang)**.

## Project Architecture
- **Clean Architecture:** The codebase follows a clean architecture pattern, organized mainly within the `internal/` directory.
  - `internal/core/domain/`: Contains domain models (e.g., `KycSession`).
  - `internal/core/ports/`: Defines interfaces for repositories and services.
  - `internal/core/services/`: Contains core business logic and use cases.
  - `internal/adapters/`: Contains implementations of ports (e.g., database repositories, external API clients like AWS S3 or Gemini).
- **Entrypoint:** The main application entrypoint is located under the `cmd/api/` directory.

## Project Logic & Domain Context
- **KYC (Know Your Customer):** The service handles identity verification sessions (`KycSession`) which are crucial for the "verified only" feature of the Squad Matcher. Uses Google Gemini Flash 2.5 for biometric and document data extraction.
- **Event & Squad Management:** The backend manages the lifecycle of events, user swipes, and the algorithm that matches users into Squads.

## AI Agent Instructions
- **Go Best Practices:** Write idiomatic Go code. Use standard libraries where possible.
- **Clean Architecture Rules:** Never import `adapters` into `core`. Core domain and logic must remain framework-agnostic. Dependency injection must happen at the entrypoint (`main.go`).
- **Database Rules (PostgreSQL):**
  - Be careful with `JSONB` columns. Do not insert empty strings `""` into JSONB columns as it causes silent transaction rollbacks. Use `NULLIF($n, '')::jsonb` in SQL queries or handle nulls properly in Go.
- **Error Handling:** 
  - Use clear and context-rich error wrapping (`fmt.Errorf("...: %w", err)`).
  - ALWAYS check the returned `error` from repository methods (e.g., `UpdateSession`) inside HTTP handlers, and return appropriate HTTP 500 status codes instead of failing silently.
- **Generative AI SDKs:** When using `genai.ImageData` for the Google Gemini Go SDK, pass the bare format name (e.g., `"jpeg"`, `"png"`), not the full MIME type (`"image/jpeg"`).
- **Testing:** Include tests for any new services or adapters in their respective packages.

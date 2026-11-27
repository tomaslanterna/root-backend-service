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

## Recent Changes & Known Behaviors
- **User Onboarding & Auth:**
  - Added support for updating missing profile data (Google Auth) via `PUT /v1/users/me`.
  - Added a public endpoint `GET /v1/users/check-username` for real-time alias availability checks.
  - Usernames are strictly stored and validated in lowercase.
  - Added `Country` (VARCHAR(2)) to the `users` table schema to store user country.
- **KYC Enhancements:**
  - Gemini prompt for KYC explicitly validates that the document's country matches the `expectedCountry` registered by the user. Mismatches force the match score below 50.
- **Search System:**
  - `SearchUsers` strictly omits the currently authenticated user (`id != $2`) to prevent self-discovery.
  - Important: Context user ID is stored under `UserIDKey` (custom type), not the string `"userID"`. Unauthenticated searches use a dummy UUID fallback (`00000000-0000-0000-0000-000000000000`) to prevent PostgreSQL type errors.

- **Temporary Crews:** crews tied to one event (`squads.type='temporary'`), joined by host-approved requests (`crew_join_requests`). Reuses `squads`/`squad_members`/chat `CREWS`; one temporary crew per user and event is enforced by the partial unique index on `squad_members(temp_event_id, user_id)`. Contract and rules: the "Crews temporales" section of the frontend `API_Specs.md`. The PostgreSQL integration test is opt-in (`TEMP_CREW_TEST_USE_LOCAL_ENV=1`) and runs in an isolated schema.

- **Security hardening (Nov 2026):** CORS only allows known origins (`internal/adapters/handlers/cors.go`; extra ones via `CORS_ALLOWED_ORIGINS`) and no credentials. The server refuses to start without `JWT_SECRET` (no fallback secret). `POST /v1/events/bulk-csv` is ADMIN-only; `POST /v1/events/image` is ADMIN/RRPP. Every image upload (events, posts, KYC, avatar) is validated by content (`readImageUpload`: JPG/PNG/WebP, GIF only for posts) and the S3 key never uses the client filename. Dance steps are capped in SQL (`ports.MaxDanceStepsPerMinute`, `ports.MaxDanceStepsPerSession`) and client start/end times are ignored. The old mock crew endpoints (`/crews/deck`, `/crews/swipe`) and the unsigned KYC webhook were removed; the MercadoPago webhook only accepts numeric ids and checks `x-signature` when `MERCADOPAGO_WEBHOOK_SECRET` is set.

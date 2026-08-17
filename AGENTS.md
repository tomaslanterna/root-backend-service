# Root Backend Service

This project is the backend for **root**, serving APIs for features like the Squad Matcher, KYC verification, and user management.
The application is built with **Go (Golang)**.

## Project Architecture
- **Clean Architecture:** The codebase follows a clean architecture pattern, organized mainly within the `internal/` directory.
  - `internal/core/domain/`: Contains domain models (e.g., `KycSession`).
  - `internal/core/ports/`: Defines interfaces for repositories and services.
  - `internal/core/services/`: Contains core business logic and use cases.
  - `internal/adapters/`: Contains implementations of ports (e.g., database repositories, external API clients).
- **Entrypoint:** The main application entrypoint is located under the `cmd/` directory.

## Project Logic & Domain Context
- **KYC (Know Your Customer):** The service handles identity verification sessions (`KycSession`) which are crucial for the "verified only" feature of the Squad Matcher.
- **Event & Squad Management:** The backend manages the lifecycle of events, user swipes, and the algorithm that matches users into Squads.

## AI Agent Instructions
- **Go Best Practices:** Write idiomatic Go code. Use standard libraries where possible.
- **Clean Architecture Rules:** Never import `adapters` into `core`. Core domain and logic must remain framework-agnostic.
- **Error Handling:** Use clear and context-rich error wrapping.
- **Testing:** Include tests for any new services or adapters in their respective packages.

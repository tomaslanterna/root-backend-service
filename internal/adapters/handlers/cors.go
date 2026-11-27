package handlers

import (
	"os"
	"strings"
)

// defaultAllowedOrigins son los orígenes que pueden llamar a la API desde un navegador:
// la web en producción, el dominio de Vercel y los orígenes locales de Capacitor.
// La app nativa abre https://www.estoesroot.com, así que en la práctica usa el primero.
var defaultAllowedOrigins = []string{
	"https://www.estoesroot.com",
	"https://estoesroot.com",
	"https://root-web-app.vercel.app",
	"capacitor://localhost",
	"https://localhost",
}

// devAllowedOrigins solo sirven para levantar el frontend en la máquina de quien desarrolla
// (mismo criterio que el WebSocket de chat).
var devAllowedOrigins = []string{
	"http://localhost:3000",
	"http://127.0.0.1:3000",
}

// corsOriginAllowed decide si un origen puede leer respuestas de la API.
// CORS_ALLOWED_ORIGINS (separado por comas) suma orígenes extra sin tocar código,
// por ejemplo un dominio de preview.
func corsOriginAllowed(origin string) bool {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		return false
	}
	for _, allowed := range defaultAllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	for _, allowed := range devAllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	for _, allowed := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		allowed = strings.TrimRight(strings.TrimSpace(allowed), "/")
		if allowed != "" && allowed != "*" && origin == allowed {
			return true
		}
	}
	return false
}

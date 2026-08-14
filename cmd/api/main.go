package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"root-backend-service/internal/adapters/handlers"
)

func main() {
	// 4. Configuración del Router con Chi
	router := handlers.NewRouter()

	// 5. Configuración y Arranque del Servidor HTTP
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Canal para escuchar señales de apagado gradual (Graceful Shutdown)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("🚀 Servidor Root Backend iniciado en el puerto %s...", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Error crítico al iniciar el servidor: %v", err)
		}
	}()

	<-stop
	log.Println("🛑 Apagando el servidor gradualmente...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("❌ Error al detener el servidor: %v", err)
	}

	log.Println("✅ Servidor detenido correctamente.")
}

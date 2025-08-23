package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"UGPTSearch/internal/handlers"
	"UGPTSearch/internal/middleware"
)

type Server struct {
	httpServer *http.Server
}

func New(addr string) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
}

func (s *Server) SetupRoutes() {
	http.HandleFunc("/search", middleware.Logging(handlers.Search))
	http.HandleFunc("/api/search", middleware.Logging(handlers.SearchJSON))
	http.HandleFunc("/instances", middleware.Logging(handlers.Instances))
	http.HandleFunc("/anubis-stats", middleware.Logging(handlers.AnubisStats))
}

func (s *Server) Start() error {
	log.Printf("Starting server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down gracefully...")
	return s.httpServer.Shutdown(ctx)
}

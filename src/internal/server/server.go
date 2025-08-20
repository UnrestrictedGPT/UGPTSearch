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
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		},
	}
}

func (s *Server) SetupRoutes() {
	http.HandleFunc("/search", middleware.Logging(handlers.Search))
	http.HandleFunc("/instances", middleware.Logging(handlers.Instances))
}

func (s *Server) Start() error {
	log.Printf("Starting server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down gracefully...")
	return s.httpServer.Shutdown(ctx)
}
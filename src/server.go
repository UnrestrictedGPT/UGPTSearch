package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

type Server struct {
	httpServer *http.Server
}

func NewServer(addr string) *Server {
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
	http.HandleFunc("/search", loggingMiddleware(searchHandler))
	http.HandleFunc("/instances", loggingMiddleware(instancesHandler))
}

func (s *Server) Start() error {
	log.Printf("Starting server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down gracefully...")
	return s.httpServer.Shutdown(ctx)
}
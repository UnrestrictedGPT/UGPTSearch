package main

import (
	"context"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"UGPTSearch/internal/instances"
	"UGPTSearch/internal/server"
	"UGPTSearch/pkg/utils"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	initializeApplication()
	
	srv := server.New(":8080")
	srv.SetupRoutes()

	go func() {
		log.Printf("Loaded %d instances", len(instances.Manager.GetInstances()))
		if err := srv.Start(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-ctx.Done()

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctxTimeout); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

func initializeApplication() {
	rand.Seed(time.Now().UnixNano())

	healthyInstances, err := utils.GetHealthyInstances()
	if err != nil {
		log.Printf("Could not fetch healthy instances: %v", err)
		log.Println("Starting with no instances. The service may not be available.")
	} else {
		log.Printf("Successfully fetched %d healthy instances.", len(healthyInstances))
		instances.Manager.SetInstances(healthyInstances)
	}
}


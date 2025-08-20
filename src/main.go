package main

import (
	"context"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"UGPTSearch/Utils"
)

var instanceManager *InstanceManager

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	initializeApplication()
	
	server := NewServer(":8080")
	server.SetupRoutes()

	go func() {
		log.Printf("Loaded %d instances", len(instanceManager.GetInstances()))
		if err := server.Start(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-ctx.Done()

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctxTimeout); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

func initializeApplication() {
	rand.Seed(time.Now().UnixNano())
	
	instanceManager = NewInstanceManager()

	healthyInstances, err := Utils.GetHealthyInstances()
	if err != nil {
		log.Printf("Could not fetch healthy instances: %v", err)
		log.Println("Starting with no instances. The service may not be available.")
	} else {
		log.Printf("Successfully fetched %d healthy instances.", len(healthyInstances))
		instanceManager.SetInstances(healthyInstances)
	}
}


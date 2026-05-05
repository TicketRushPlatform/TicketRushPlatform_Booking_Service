package main

import (
	"booking_api/internal/application"
	"booking_api/internal/config"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// @title Booking API
// @version 1.0
// @description Booking API for seat hold, confirm, cancel, and seat status workflows.
// @BasePath /api/v1
// @schemes http https
func main() {
	config := config.NewConfig()
	log.Println(config)
	app, err := application.NewApp(config)

	if err != nil {
		panic(err)
	}

	go func() {
		if err := app.Start(); err != nil {
			panic(err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		log.Println("force shutdown")
		os.Exit(1)
	}()

	sig := <-sigCh
	log.Printf("received signal: %s", sig.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan struct{})

	go func() {
		if err := app.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown err: %v", err)
		}
		close(done)
	}()

	select {
	case <-sigCh:
		log.Println("force shutdown")
		os.Exit(1)
	case <-done:
		log.Println("graceful shutdown complete")
	}
}

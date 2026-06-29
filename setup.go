package main

import (
	"log/slog"
	"os"

	"income_calculator/internal/env"
	"income_calculator/internal/log"
)

// setupEnvironment loads .env file and configures logging.
func setupEnvironment() {
	// Load .env file if it exists
	err := env.Load(".env")
	if err == nil {
		slog.Debug("Loaded environment variables from .env file.")
	} else {
		slog.Debug("No .env file found or error loading .env file; proceeding with existing environment variables.")
	}

	// Configure logging
	log.Setup()
}

// getAPIKey fetches the TORN_API_KEY from the environment or exits if not set.
func getAPIKey() string {
	apiKey := os.Getenv("TORN_API_KEY")
	if apiKey == "" {
		slog.Error("TORN_API_KEY environment variable is not set.")
		os.Exit(1)
	}
	return apiKey
}

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type CompanyProfile struct {
	ID          int    `json:"ID"`
	CompanyType int    `json:"company_type"`
	Name        string `json:"name"` // Added Name for logging
}

type CompanyProfileResponse struct {
	Company CompanyProfile `json:"company"`
}

// fetchCompanyProfile fetches the company profile from the API.
// It logs and exits via slog.Error + os.Exit if any error occurs.
func fetchCompanyProfile(apiKey string) CompanyProfile {
	apiURL := fmt.Sprintf("%s?selections=profile&key=%s", baseURL, apiKey) // Base URL doesn't need company ID here
	maskedURL := strings.Replace(apiURL, "key="+apiKey, "key=[REDACTED]", 1)
	slog.Debug("Fetching company profile from URL: "+maskedURL)

	resp, err := http.Get(apiURL)
	if err != nil {
		slog.Error("Failed to execute company profile request", "error", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to read body for more context if it's an error status
		bodyBytes, readErr := io.ReadAll(resp.Body) // Capture potential read error
		if readErr != nil {
			slog.Warn("Failed to read error response body", "error", readErr)
		}
		slog.Error("Company profile API request failed with status "+resp.Status+". Body: "+string(bodyBytes))
		os.Exit(1)
	}

	var apiResp CompanyProfileResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		slog.Error("Failed to decode company profile API response", "error", err)
		os.Exit(1)
	}

	// Check if the nested Company field is populated
	if apiResp.Company.ID == 0 {
		slog.Error("Company profile data not found in API response (Company ID is 0)")
		os.Exit(1)
	}

	// Log the fetched details before returning
	slog.Debug("Fetched profile - Company ID: "+fmt.Sprintf("%d", apiResp.Company.ID)+", Name: "+apiResp.Company.Name+", Type: "+fmt.Sprintf("%d", apiResp.Company.CompanyType))
	return apiResp.Company
}

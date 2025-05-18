package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
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
// It logs and exits via log.Fatal if any error occurs.
func fetchCompanyProfile(apiKey string) CompanyProfile {
	apiURL := fmt.Sprintf("%s?selections=profile&key=%s", baseURL, apiKey) // Base URL doesn't need company ID here
	maskedURL := strings.Replace(apiURL, "key="+apiKey, "key=[REDACTED]", 1)
	log.Debug().Msgf("Fetching company profile from URL: %s", maskedURL)

	resp, err := http.Get(apiURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to execute company profile request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to read body for more context if it's an error status
		bodyBytes, readErr := io.ReadAll(resp.Body) // Capture potential read error
		if readErr != nil {
			log.Warn().Err(readErr).Msg("Failed to read error response body")
		}
		log.Fatal().Msgf("Company profile API request failed with status %s. Body: %s", resp.Status, string(bodyBytes))
	}

	var apiResp CompanyProfileResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to decode company profile API response")
	}

	// Check if the nested Company field is populated
	if apiResp.Company.ID == 0 {
		log.Fatal().Msg("Company profile data not found in API response (Company ID is 0)")
	}

	// Log the fetched details before returning
	log.Debug().Msgf("Fetched profile - Company ID: %d, Name: %s, Type: %d", apiResp.Company.ID, apiResp.Company.Name, apiResp.Company.CompanyType)
	return apiResp.Company
}

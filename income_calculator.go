package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/time/rate"
)

const (
	baseURL           = "https://api.torn.com/company/"
	daysToFetch       = 6
	maxEntriesPerCall = 100 // Max entries returned when using 'from'/'to'
)

// Represents the structure of a single news item
type NewsItem struct {
	News      string `json:"news"`
	Timestamp int64  `json:"timestamp"`
}

// Represents the overall API response structure
type ApiResponse struct {
	News map[string]NewsItem `json:"news"`
}

// Represents the structure of a single company detail from the company list endpoint
type CompanyDetail struct {
	ID           int    `json:"ID"`
	CompanyType  int    `json:"company_type"`
	Rating       int    `json:"rating"`
	Name         string `json:"name"`
	Director     int    `json:"director"`
	WeeklyIncome int64  `json:"weekly_income"`
	// Add other fields if needed
}

// Represents the overall API response structure for the company list
type CompanyListResponse struct {
	Company map[string]CompanyDetail `json:"company"` // Note: Key is company ID as string
}

// Represents the relevant parts of the company profile response
type CompanyProfile struct {
	ID          int    `json:"ID"`
	CompanyType int    `json:"company_type"`
	Name        string `json:"name"` // Added Name for logging
}

// Represents the overall API response structure for the company profile
type CompanyProfileResponse struct {
	Company CompanyProfile `json:"company"`
}

// Regex to extract income from report string
var incomeRegex = regexp.MustCompile(`gross income of \$([\d,]+)\.`)

func main() {
	setupEnvironment()
	apiKey := getAPIKey()
	profile := fetchCompanyProfile(apiKey)
	totalIncome := runIncomeCalculation(apiKey, strconv.Itoa(profile.ID))
	incomeThreshold := runCompanyAnalysis(apiKey, profile.CompanyType)
	logIncomeComparison(totalIncome, incomeThreshold)
}

// setupEnvironment loads .env file and configures zerolog output and log level.
func setupEnvironment() {
	// Load .env file if it exists
	err := godotenv.Load()
	if err == nil {
		log.Debug().Msg("Loaded environment variables from .env file.")
	} else {
		log.Debug().Msg("No .env file found or error loading .env file; proceeding with existing environment variables.")
	}

	// Configure logging
	if os.Getenv("ENV") == "production" {
		zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
		log.Logger = log.Output(os.Stderr)
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	levelStr := strings.ToLower(os.Getenv("LOGLEVEL"))
	switch levelStr {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info", "":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn", "warning":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "fatal":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "panic":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	case "disabled":
		zerolog.SetGlobalLevel(zerolog.Disabled)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		log.Warn().Msgf("Unknown LOGLEVEL '%s', defaulting to info.", levelStr)
	}
}

// getAPIKey fetches the TORN_API_KEY from the environment or exits if not set.
func getAPIKey() string {
	apiKey := os.Getenv("TORN_API_KEY")
	if apiKey == "" {
		log.Error().Msg("TORN_API_KEY environment variable is not set.")
		os.Exit(1)
	}
	return apiKey
}

// fetchCompanyProfile fetches the company profile from the API.
// It logs and exits via log.Fatal if any error occurs.
func fetchCompanyProfile(apiKey string) CompanyProfile { // Return only profile, handle errors internally
	apiURL := fmt.Sprintf("%s?selections=profile&key=%s", baseURL, apiKey) // Base URL doesn't need company ID here
	maskedURL := strings.Replace(apiURL, "key="+apiKey, "key=[REDACTED]", 1)
	log.Debug().Msgf("Fetching company profile from URL: %s", maskedURL)

	resp, err := http.Get(apiURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to execute company profile request")
		// return CompanyProfile{} // Unreachable due to log.Fatal
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to read body for more context if it's an error status
		bodyBytes, readErr := io.ReadAll(resp.Body) // Capture potential read error
		if readErr != nil {
			log.Warn().Err(readErr).Msg("Failed to read error response body")
		}
		log.Fatal().Msgf("Company profile API request failed with status %s. Body: %s", resp.Status, string(bodyBytes))
		// return CompanyProfile{} // Unreachable due to log.Fatal
	}

	var apiResp CompanyProfileResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to decode company profile API response")
		// return CompanyProfile{} // Unreachable due to log.Fatal
	}

	// Check if the nested Company field is populated
	if apiResp.Company.ID == 0 {
		log.Fatal().Msg("Company profile data not found in API response (Company ID is 0)")
		// return CompanyProfile{} // Unreachable due to log.Fatal
	}

	// Log the fetched details before returning
	log.Info().Msgf("Fetched profile - Company ID: %d, Name: %s, Type: %d", apiResp.Company.ID, apiResp.Company.Name, apiResp.Company.CompanyType)
	return apiResp.Company // Removed error return
}

// runIncomeCalculation runs the income calculation and logs the result.
func runIncomeCalculation(apiKey, companyID string) int64 {
	log.Info().Msgf("Calculating total gross income for company %s for the last %d days...", companyID, daysToFetch)
	totalIncome, err := fetchAndSumIncome(apiKey, companyID)
	if err != nil {
		log.Error().Err(err).Msgf("Error calculating daily income sum for company %s", companyID)
		return 0
	}
	log.Info().Msgf("Total gross income over the last %d days for company %s: $%s", daysToFetch, companyID, formatWithCommas(totalIncome))
	return totalIncome
}

// runCompanyAnalysis fetches, analyzes the company list, logs results/errors, and returns the threshold.
// It exits the program via log.Fatal if an error occurs.
// It returns the P20 income threshold if successful.
func runCompanyAnalysis(apiKey string, companyType int) (p20Income int64) {
	rank10Count, p20Income, err := analyzeCompanyListByType(apiKey, companyType)
	if err != nil {
		// Log the error and exit the program
		log.Fatal().Err(err).Msg("Fatal error during company list analysis")
		// log.Fatal exits, so no return needed here, but keeps compiler happy
		return 0
	}

	// Log the successful analysis results (using local rank10Count)
	log.Info().Msgf("Found %s companies with rank 10.", formatWithCommas(int64(rank10Count)))
	log.Info().Msgf("The income at the rank threshold (80th percentile of rank 10 companies) is: $%s", formatWithCommas(p20Income))

	// Return only p20Income
	return p20Income
}

// logIncomeComparison calculates and logs the difference between the company's income and the threshold.
func logIncomeComparison(totalIncome int64, p20Income int64) {
	if totalIncome <= 0 {
		log.Warn().Msg("Total income is zero or negative, cannot calculate difference to threshold.")
		return
	}
	if p20Income <= 0 {
		log.Warn().Msg("P20 income threshold is zero or negative, cannot calculate difference.")
		return
	}

	neededToday := p20Income - totalIncome
	if neededToday < 0 {
		neededToday = 0
	}
	log.Info().Msgf("Income needed to reach the rank threshold: $%s", formatWithCommas(neededToday))
}

func fetchAndSumIncome(apiKey, companyID string) (int64, error) {
	var totalIncome int64 = 0
	processedTimestamps := make(map[int64]bool) // Track processed news items to avoid double counting if API overlaps

	// Define the rate limiter: 100 calls per minute = 100/60 calls per second
	// Allow a burst of 1 initially.
	limiter := rate.NewLimiter(rate.Limit(100.0/60.0), 1)

	// Calculate the timestamp cutoff. We want the last 'daysToFetch' income reports.
	// Since reports arrive around 18:00 UTC, we set the cutoff to 18:00:00 UTC
	// on the day *before* the first report we want to include.
	now := time.Now()
	// Get today's date part in UTC
	todayDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	// Set the time to 18:00:00 UTC for today's reference point
	today1800UTC := todayDate.Add(18 * time.Hour)
	// Calculate the cutoff time: go back `daysToFetch` days from today's 18:00 UTC mark.
	// This makes the cutoff 18:00 UTC on the day *before* the 6th report back.
	cutoffTime := today1800UTC.AddDate(0, 0, -daysToFetch)
	cutoffTimestamp := cutoffTime.Unix()

	log.Info().Msgf("Fetching news from %s onwards (aiming for last %d reports based on 18:00 UTC)", cutoffTime.Format(time.RFC3339), daysToFetch)

	currentToTimestamp := now.Unix()

	for {
		// Wait for the rate limiter before making the API call
		err := limiter.Wait(context.Background()) // context.Background is fine for simple cases
		if err != nil {
			// This error is unlikely for Wait unless context is cancelled, but handle defensively
			return 0, fmt.Errorf("rate limiter wait error: %w", err)
		}

		apiResp, err := fetchNewsBatch(apiKey, companyID, currentToTimestamp)
		if err != nil {
			return 0, err
		}

		if len(apiResp.News) == 0 {
			log.Info().Msg("No more news entries found.")
			break // No news items returned, we're done
		}

		// Collect and sort news items by timestamp (descending)
		newsItems := make([]NewsItem, 0, len(apiResp.News))
		for _, item := range apiResp.News {
			newsItems = append(newsItems, item)
		}

		// Check if any news items were actually returned in the response
		if len(apiResp.News) == 0 {
			log.Info().Msg("No news entries found in this batch.")
			break // No news items returned in this specific batch, assume we're done
		}

		sort.Slice(newsItems, func(i, j int) bool {
			return newsItems[i].Timestamp > newsItems[j].Timestamp // Descending order
		})

		oldestTimestampInBatch := newsItems[len(newsItems)-1].Timestamp

		batchIncome, itemsProcessedInBatch := processNewsItems(newsItems, cutoffTimestamp, processedTimestamps)
		totalIncome += batchIncome

		log.Debug().Msgf("Processed %d new entries in this batch. Oldest timestamp: %s", itemsProcessedInBatch, time.Unix(oldestTimestampInBatch, 0).Format(time.RFC3339))

		// Decide whether to continue fetching
		if oldestTimestampInBatch < cutoffTimestamp || len(newsItems) < maxEntriesPerCall || itemsProcessedInBatch == 0 {
			log.Debug().Msg("Stopping fetch loop.")
			break
		}

		currentToTimestamp = oldestTimestampInBatch - 1
	}

	return totalIncome, nil
}

// fetchNewsBatch fetches a batch of news from the API up to the given timestamp.
func fetchNewsBatch(apiKey, companyID string, toTimestamp int64) (ApiResponse, error) {
	apiURL := fmt.Sprintf("%s%s?selections=news&key=%s&to=%d",
		baseURL, companyID, apiKey, toTimestamp)
	maskedURL := strings.Replace(apiURL, "key="+apiKey, "key=[REDACTED]", 1)
	log.Debug().Msgf("Fetching URL: %s", maskedURL)

	resp, err := http.Get(apiURL)
	if err != nil {
		return ApiResponse{}, fmt.Errorf("failed to fetch API data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ApiResponse{}, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	var apiResp ApiResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		return ApiResponse{}, fmt.Errorf("failed to decode API response: %w", err)
	}

	return apiResp, nil
}

// processNewsItems processes news items, updates processedTimestamps, and returns the batch income and count.
func processNewsItems(newsItems []NewsItem, cutoffTimestamp int64, processedTimestamps map[int64]bool) (int64, int) {
	var batchIncome int64 = 0
	itemsProcessed := 0
	for _, item := range newsItems {
		if item.Timestamp < cutoffTimestamp || processedTimestamps[item.Timestamp] {
			continue
		}
		processedTimestamps[item.Timestamp] = true
		itemsProcessed++
		if income, ok := extractIncomeFromNews(item.News); ok {
			batchIncome += income
			log.Debug().Msgf("Found income: %s from report on %s", formatWithCommas(income), time.Unix(item.Timestamp, 0).Format(time.RFC3339))
		}
	}
	return batchIncome, itemsProcessed
}

// extractIncomeFromNews extracts income from a news string if present.
func extractIncomeFromNews(news string) (int64, bool) {
	matches := incomeRegex.FindStringSubmatch(news)
	if len(matches) > 1 {
		incomeStr := strings.ReplaceAll(matches[1], ",", "")
		income, err := strconv.ParseInt(incomeStr, 10, 64)
		if err == nil {
			return income, true
		}
		log.Warn().Msgf("Could not parse income: %s Error: %v", incomeStr, err)
	}
	return 0, false
}

// Finds the P20 weekly income threshold based on the count of rank 10 companies
// within the full sorted list of companies for a specific type.
func analyzeCompanyListByType(apiKey string, companyType int) (rank10Count int, p20Income int64, err error) {
	const targetRank = 10
	const percentileRank = 0.80 // P20 income means 80th percentile rank when sorted descending

	log.Info().Msgf("Fetching company list for type %d to analyze rank %d companies...", companyType, targetRank)

	apiResp, err := fetchCompanyList(apiKey, companyType)
	if err != nil {
		// Return the error directly
		return 0, 0, fmt.Errorf("failed to fetch company list API data: %w", err)
	}

	totalCompaniesFetched := len(apiResp.Company)
	if apiResp.Company == nil || totalCompaniesFetched == 0 {
		err = fmt.Errorf("no companies found in the list response for type %d", companyType)
		return
	}

	// Collect all companies and count rank 10s
	allCompanies := make([]CompanyDetail, 0, totalCompaniesFetched)
	rank10Count = 0
	for _, detail := range apiResp.Company {
		allCompanies = append(allCompanies, detail)
		if detail.Rating == targetRank {
			rank10Count++
		}
	}

	if rank10Count == 0 {
		log.Warn().Msgf("No companies found with rank %d for type %d.", targetRank, companyType)
		// Return 0 count, 0 income, and no error
		return 0, 0, nil
	}

	// Sort ALL companies by weekly income descending
	sort.Slice(allCompanies, func(i, j int) bool {
		return allCompanies[i].WeeklyIncome > allCompanies[j].WeeklyIncome // Descending
	})

	p20Income = incomeAtRank(allCompanies, rank10Count, percentileRank)

	log.Debug().Msgf("Analyzed %d companies of type %d. Counted %d rank 10s.", totalCompaniesFetched, companyType, rank10Count)
	// Return values are implicitly set via named return variables
	return
}

// fetchCompanyList fetches the company list for a given type from the API.
func fetchCompanyList(apiKey string, companyType int) (CompanyListResponse, error) {
	apiURL := fmt.Sprintf("%s%d?selections=companies&key=%s",
		baseURL, companyType, apiKey)
	maskedURL := strings.Replace(apiURL, "key="+apiKey, "key=[REDACTED]", 1)
	log.Debug().Msgf("Fetching URL: %s", maskedURL)

	resp, err := http.Get(apiURL)
	if err != nil {
		return CompanyListResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CompanyListResponse{}, fmt.Errorf("company list API request failed with status: %s", resp.Status)
	}

	var apiResp CompanyListResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		return CompanyListResponse{}, fmt.Errorf("failed to decode company list API response: %w", err)
	}

	return apiResp, nil
}

// incomeAtRank returns the income at the calculated threshold index in the sorted company list.
func incomeAtRank(allCompanies []CompanyDetail, rank10Count int, percentileRank float64) int64 {
	totalCompaniesFetched := len(allCompanies)
	index := int(math.Ceil(float64(rank10Count)*percentileRank)) - 1
	if index < 0 {
		index = 0
	}
	if index >= totalCompaniesFetched {
		log.Warn().Msgf("Rank threshold index (%d) based on rank 10 count (%d) exceeds total companies (%d). Using last company's income.", index, rank10Count, totalCompaniesFetched)
		index = totalCompaniesFetched - 1
	}
	return allCompanies[index].WeeklyIncome
}

// Helper function to format int64 with commas using golang.org/x/text/message
func formatWithCommas(n int64) string {
	p := message.NewPrinter(language.English)
	return p.Sprintf("%d", n)
}

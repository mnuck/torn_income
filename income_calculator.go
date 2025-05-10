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
	baseURL              = "https://api.torn.com/company/"
	incomeReportsToFetch = 6
	maxEntriesPerCall    = 100 // Max entries returned when using 'from'/'to'
)

// Represents the structure of a single news item
type NewsItem struct {
	News      string `json:"news"`
	Timestamp int64  `json:"timestamp"`
}

// Represents the overall API response structure for the company news endpoint
type CompanyNewsResponse struct {
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

// runIncomeCalculation runs the income calculation and logs the result.
func runIncomeCalculation(apiKey, companyID string) int64 {
	log.Info().Msgf("Calculating total gross income for company %s for the last %d income reports...", companyID, incomeReportsToFetch)
	totalIncome, err := fetchAndSumIncome(apiKey, companyID)
	if err != nil {
		log.Error().Err(err).Msgf("Error calculating income sum for company %s", companyID)
		return 0
	}
	log.Info().Msgf("Total gross income over the last %d income reports for company %s: $%s", incomeReportsToFetch, companyID, formatWithCommas(totalIncome))
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
	var incomeEventsFoundSoFar int = 0
	processedTimestamps := make(map[int64]bool) // Track processed news items

	limiter := rate.NewLimiter(rate.Limit(100.0/60.0), 1) // 100 calls/min

	log.Info().Msgf("Fetching news until exactly %d income reports are found...", incomeReportsToFetch)

	currentToTimestamp := time.Now().Unix()
	// oldestProcessedTimestampInLoop keeps track of the 'to' timestamp used for the API call,
	// to detect if we are stuck making calls with the same 'to' value without finding new entries.
	var oldestProcessedTimestampInLoop int64 = currentToTimestamp

	for incomeEventsFoundSoFar < incomeReportsToFetch {
		// Wait for the rate limiter
		if err := limiter.Wait(context.Background()); err != nil {
			return 0, fmt.Errorf("rate limiter error: %w", err)
		}

		log.Debug().Msgf("Fetching batch of news before timestamp: %s (%d). Seeking %d more income reports.",
			time.Unix(currentToTimestamp, 0).Format(time.RFC3339), currentToTimestamp, incomeReportsToFetch-incomeEventsFoundSoFar)
		apiResp, err := fetchNewsBatch(apiKey, companyID, currentToTimestamp)
		if err != nil {
			return totalIncome, fmt.Errorf("failed to fetch news batch: %w", err)
		}

		if len(apiResp.News) == 0 {
			log.Warn().Msgf("No news items returned in batch. Found %d/%d income reports. Stopping further fetches.", incomeEventsFoundSoFar, incomeReportsToFetch)
			break // No more news items available
		}

		newsItems := make([]NewsItem, 0, len(apiResp.News))
		for _, item := range apiResp.News {
			newsItems = append(newsItems, item)
		}
		sort.Slice(newsItems, func(i, j int) bool {
			return newsItems[i].Timestamp > newsItems[j].Timestamp
		})

		remainingEventsNeeded := incomeReportsToFetch - incomeEventsFoundSoFar
		batchIncome, newEntriesCountInBatch, batchIncomeEventsCount, oldestTimestampProcessedInBatch := processNewsItems(newsItems, processedTimestamps, remainingEventsNeeded)

		totalIncome += batchIncome
		incomeEventsFoundSoFar += batchIncomeEventsCount

		log.Debug().Msgf("Processed %d new entries in this batch. Found %d income events (this batch). Oldest item processed in batch: %s (%d). Total income events so far: %d/%d.",
			newEntriesCountInBatch, batchIncomeEventsCount, time.Unix(oldestTimestampProcessedInBatch, 0).Format(time.RFC3339), oldestTimestampProcessedInBatch, incomeEventsFoundSoFar, incomeReportsToFetch)

		if incomeEventsFoundSoFar >= incomeReportsToFetch { // Exact match or more (should be exact due to processNewsItems change)
			log.Info().Msgf("Found exactly %d income reports. Stopping fetch loop.", incomeEventsFoundSoFar)
			break
		}

		// Logic to determine the next 'currentToTimestamp'
		if newEntriesCountInBatch == 0 && len(newsItems) > 0 {
			log.Debug().Msg("No new unique entries processed in this batch, but news items were present. Advancing 'to' timestamp carefully.")
			// If oldestTimestampProcessedInBatch is valid and older, use it. This means processNewsItems processed something.
			if oldestTimestampProcessedInBatch > 0 && oldestTimestampProcessedInBatch < currentToTimestamp {
				currentToTimestamp = oldestTimestampProcessedInBatch
			} else if len(newsItems) > 0 {
				// Fallback: use the oldest item from the raw batch if nothing was processed by processNewsItems
				lastItemInRawBatchTimestamp := newsItems[len(newsItems)-1].Timestamp
				if lastItemInRawBatchTimestamp < currentToTimestamp {
					currentToTimestamp = lastItemInRawBatchTimestamp
				} else {
					log.Warn().Msg("Cannot advance 'to' timestamp. Oldest raw item not older. Stopping.")
					break
				}
			} else {
				log.Warn().Msg("No new unique entries and no news items in batch to determine next timestamp. Stopping.")
				break
			}
		} else if oldestTimestampProcessedInBatch > 0 { // If items were processed, use the oldest *processed* item's timestamp
			currentToTimestamp = oldestTimestampProcessedInBatch
		} else if len(newsItems) > 0 { // Fallback if nothing was processed (e.g. all were filtered out before income check)
			currentToTimestamp = newsItems[len(newsItems)-1].Timestamp
		} else {
			log.Warn().Msg("No news items in this batch to process or determine next timestamp from. Stopping.")
			break
		}

		// Safety break if currentToTimestamp stops advancing and no new entries were processed
		// (implying we might be stuck or there's no more relevant data)
		if currentToTimestamp >= oldestProcessedTimestampInLoop && newEntriesCountInBatch == 0 {
			log.Warn().Msgf("'to' timestamp %d (%s) did not advance past previous loop's 'to' %d (%s) and no new entries processed. Stopping to prevent infinite loop.",
				currentToTimestamp, time.Unix(currentToTimestamp, 0).Format(time.RFC3339),
				oldestProcessedTimestampInLoop, time.Unix(oldestProcessedTimestampInLoop, 0).Format(time.RFC3339))
			break
		}
		oldestProcessedTimestampInLoop = currentToTimestamp

	}
	return totalIncome, nil
}

// fetchNewsBatch fetches a batch of news from the API up to the given timestamp.
func fetchNewsBatch(apiKey, companyID string, toTimestamp int64) (CompanyNewsResponse, error) {
	apiURL := fmt.Sprintf("%s%s?selections=news&key=%s&to=%d",
		baseURL, companyID, apiKey, toTimestamp)
	maskedURL := strings.Replace(apiURL, "key="+apiKey, "key=[REDACTED]", 1)
	log.Debug().Msgf("Fetching URL: %s", maskedURL)

	resp, err := http.Get(apiURL)
	if err != nil {
		return CompanyNewsResponse{}, fmt.Errorf("failed to fetch API data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CompanyNewsResponse{}, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	var apiResp CompanyNewsResponse
	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		return CompanyNewsResponse{}, fmt.Errorf("failed to decode API response: %w", err)
	}

	return apiResp, nil
}

// processNewsItems iterates through news items, extracts income, and sums it up.
// It skips items with timestamps before cutoffTimestamp or already processed items.
// It returns the sum of income from the batch, the count of new entries processed,
// the count of income events found (up to maxEventsToProcess), and the oldest timestamp processed.
func processNewsItems(newsItems []NewsItem, processedTimestamps map[int64]bool, maxEventsToProcess int) (batchIncome int64, newEntriesCount int, batchIncomeEventsCount int, oldestTimestampProcessed int64) {
	batchIncome = 0
	newEntriesCount = 0
	batchIncomeEventsCount = 0
	oldestTimestampProcessed = 0 // Will be set to the timestamp of the last item *actually processed*

	if len(newsItems) == 0 || maxEventsToProcess <= 0 {
		return // No items to process or no events needed
	}

	// Initialize with a value that will be overwritten by the first processed item,
	// or remains 0 if nothing is processed.
	// It's important that this reflects the oldest item *processed in this call*,
	// not necessarily the oldest in the input newsItems if we stop early.
	var lastProcessedItemTimestamp int64 = 0

	for _, item := range newsItems {
		if processedTimestamps[item.Timestamp] {
			log.Debug().Msgf("Skipping already processed news item at timestamp %d", item.Timestamp)
			continue // Skip already processed news items
		}

		// Tentatively process this item
		currentIncome, isIncomeReport := extractIncomeFromNews(item.News)

		if isIncomeReport {
			// If we've already found enough events, don't process this one or any further.
			if batchIncomeEventsCount >= maxEventsToProcess {
				break
			}
			log.Debug().Msgf("Found income: %s from report on %s", formatWithCommas(currentIncome), time.Unix(item.Timestamp, 0).Format(time.RFC3339))
			batchIncome += currentIncome
			batchIncomeEventsCount++
		}

		// Common processing for any item that wasn't skipped and didn't break the loop early
		processedTimestamps[item.Timestamp] = true
		newEntriesCount++
		lastProcessedItemTimestamp = item.Timestamp // Update with this item's timestamp

		// If we've hit the target for income events, we can stop processing this batch.
		if isIncomeReport && batchIncomeEventsCount >= maxEventsToProcess {
			break
		}
	}

	// oldestTimestampProcessed should be the timestamp of the last item *actually* processed
	// which helped contribute to newEntriesCount.
	oldestTimestampProcessed = lastProcessedItemTimestamp

	return
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
func incomeAtRank(allCompanies []CompanyDetail, rank int, percentileRank float64) int64 {
	totalCompaniesFetched := len(allCompanies)
	index := int(math.Max(0, math.Ceil(float64(rank)*percentileRank)-1))
	if index >= totalCompaniesFetched {
		log.Warn().Msgf("Rank threshold index (%d) based on rank 10 count (%d) exceeds total companies (%d). Using last company's income.", index, rank, totalCompaniesFetched)
		index = totalCompaniesFetched - 1
	}
	return allCompanies[index].WeeklyIncome
}

// Helper function to format int64 with commas using golang.org/x/text/message
func formatWithCommas(n int64) string {
	p := message.NewPrinter(language.English)
	return p.Sprintf("%d", n)
}

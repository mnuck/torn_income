package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/time/rate"
)

const (
	baseURL              = "https://api.torn.com/company/"
	incomeReportsToFetch = 6
	maxEntriesPerCall    = 100 // Max entries returned when using 'from'/'to'
	lookbackHours        = 145 // Hours to look back; easily covers the last 6 income reports
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
	Rating       int   `json:"rating"`
	WeeklyIncome int64 `json:"weekly_income"`
}

// Represents the overall API response structure for the company list
type CompanyListResponse struct {
	Company map[string]CompanyDetail `json:"company"` // Note: Key is company ID as string
}

// Regex to extract income from report string
var incomeRegex = regexp.MustCompile(`gross income of \$([\d,]+)\.`)

func main() {
	setupEnvironment()
	apiKey := getAPIKey()
	profile := fetchCompanyProfile(apiKey)
	totalIncome := runIncomeCalculation(apiKey, profile.ID)
	incomeThreshold := runCompanyAnalysis(apiKey, profile.CompanyType)
	logIncomeComparison(totalIncome, incomeThreshold)
}

// runIncomeCalculation runs the income calculation and logs the result.
func runIncomeCalculation(apiKey string, companyID int) int64 {
	companyIDStr := strconv.Itoa(companyID)
	log.Info().Msgf("Calculating total gross income for company %s for the last %d income reports...", companyIDStr, incomeReportsToFetch)
	totalIncome, err := fetchAndSumIncome(apiKey, companyIDStr)
	if err != nil {
		log.Error().Err(err).Msgf("Error calculating income sum for company %s", companyIDStr)
		return 0
	}
	log.Info().Msgf("Total gross income over the last %d income reports for company %s: $%s", incomeReportsToFetch, companyIDStr, formatWithCommas(totalIncome))
	return totalIncome
}

// runCompanyAnalysis fetches, analyzes the company list, logs results/errors, and returns the threshold.
// It exits the program via log.Fatal if an error occurs.
// It returns the P20 income threshold if successful.
func runCompanyAnalysis(apiKey string, companyType int) (thresholdIncome int64) {
	rank10Count, thresholdIncome, err := analyzeCompanyListByType(apiKey, companyType)
	if err != nil {
		// Log the error and exit the program
		log.Fatal().Err(err).Msg("Fatal error during company list analysis")
		// log.Fatal exits, so no return needed here, but keeps compiler happy
		return 0
	}

	// Log the successful analysis results (using local rank10Count)
	log.Info().Msgf("Found %s companies with rank 10.", formatWithCommas(int64(rank10Count)))
	log.Info().Msgf("The income at the rank threshold (80th percentile of rank 10 companies) is: $%s", formatWithCommas(thresholdIncome))

	// Return only thresholdIncome
	return thresholdIncome
}

// logIncomeComparison calculates and logs the difference between the company's income and the threshold.
func logIncomeComparison(totalIncome int64, thresholdIncome int64) {
	if totalIncome <= 0 {
		log.Warn().Msg("Total income is zero or negative, cannot calculate difference to threshold.")
		return
	}
	if thresholdIncome <= 0 {
		log.Warn().Msg("Threshold income is zero or negative, cannot calculate difference.")
		return
	}

	neededToday := int64(math.Max(float64(thresholdIncome-totalIncome), 0))
	log.Info().Msgf("Income needed to reach the rank threshold: $%s", formatWithCommas(neededToday))
}

// fetchLastIncomeEvents fetches the last six income event amounts (int64) for a company.
func fetchLastIncomeEvents(apiKey, companyID string) ([]int64, error) {
	fromTimestamp := time.Now().Add(-lookbackHours * time.Hour).Unix()
	toTimestamp := time.Now().Unix()

	var incomes []int64
	incomeEventsFound := 0

	limiter := rate.NewLimiter(rate.Limit(100.0/60.0), 1) // 100 calls/min

	log.Info().Msgf("Searching the last %d hours of news for the %d most recent income events…", lookbackHours, incomeReportsToFetch)

	for incomeEventsFound < incomeReportsToFetch && toTimestamp > fromTimestamp {
		if err := limiter.Wait(context.Background()); err != nil {
			return incomes, fmt.Errorf("rate limiter error: %w", err)
		}

		log.Debug().Msgf("Fetching news with from=%d (%s), to=%d (%s)", fromTimestamp, time.Unix(fromTimestamp, 0).Format(time.RFC3339), toTimestamp, time.Unix(toTimestamp, 0).Format(time.RFC3339))

		apiResp, err := fetchNewsBatch(apiKey, companyID, fromTimestamp, toTimestamp)
		if err != nil {
			return incomes, fmt.Errorf("failed to fetch news batch: %w", err)
		}

		if len(apiResp.News) == 0 {
			log.Warn().Msg("No news items returned; stopping search.")
			break
		}

		newsItems := make([]NewsItem, 0, len(apiResp.News))
		for _, item := range apiResp.News {
			newsItems = append(newsItems, item)
		}
		sort.Slice(newsItems, func(i, j int) bool { return newsItems[i].Timestamp > newsItems[j].Timestamp })

		oldestTimestampInBatch := newsItems[len(newsItems)-1].Timestamp

		for _, item := range newsItems {
			if income, ok := extractIncomeFromNews(item.News); ok {
				log.Debug().Msgf("Found income event: $%s at %s", formatWithCommas(income), time.Unix(item.Timestamp, 0).Format(time.RFC3339))
				incomes = append(incomes, income)
				incomeEventsFound++
				if incomeEventsFound >= incomeReportsToFetch {
					break
				}
			}
		}

		foundEnoughIncomeEvents := incomeEventsFound >= incomeReportsToFetch
		if foundEnoughIncomeEvents {
			log.Debug().Msg("Found enough income events; stopping search.")
			break
		}

		isLastBatch := len(apiResp.News) < maxEntriesPerCall
		if isLastBatch {
			log.Debug().Msg("Received a partial page of results; no further news items available.")
			break
		}

		failedToMakeProgress := oldestTimestampInBatch >= toTimestamp
		if failedToMakeProgress {
			log.Warn().Msg("Paging did not make progress; stopping to avoid infinite loop.")
			break
		}

		toTimestamp = oldestTimestampInBatch - 1
	}

	if incomeEventsFound < incomeReportsToFetch {
		log.Warn().Msgf("Only found %d/%d income events in the last %d hours.", incomeEventsFound, incomeReportsToFetch, lookbackHours)
	}

	return incomes, nil
}

// sumIncomeEvents sums up the values in the given slice of incomes.
func sumIncomeEvents(incomes []int64) int64 {
	var total int64
	for _, income := range incomes {
		total += income
	}
	return total
}

// fetchAndSumIncome fetches the last six income events and sums them up.
func fetchAndSumIncome(apiKey, companyID string) (int64, error) {
	incomes, err := fetchLastIncomeEvents(apiKey, companyID)
	if err != nil {
		return 0, err
	}
	return sumIncomeEvents(incomes), nil
}

// fetchNewsBatch fetches a batch of news from the API between from and to timestamps.
func fetchNewsBatch(apiKey, companyID string, fromTimestamp, toTimestamp int64) (CompanyNewsResponse, error) {
	apiURL := fmt.Sprintf("%s%s?selections=news&key=%s&from=%d&to=%d",
		baseURL, companyID, apiKey, fromTimestamp, toTimestamp)
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
func analyzeCompanyListByType(apiKey string, companyType int) (rank10Count int, thresholdIncome int64, err error) {
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

	thresholdIncome = incomeAtRank(allCompanies, rank10Count, percentileRank)

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

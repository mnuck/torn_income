# Torn Income Calculator

This Go program calculates the total gross income for the Torn company associated with the provided API key over the last 6 days. It also analyzes company rankings and income thresholds for other companies of the same type.

## Project Structure
```
.
├── build/
│   └── Dockerfile         # Dockerfile for building the application image
├── deploy/
│   ├── income-cronjob.yaml # Kubernetes CronJob manifest
│   └── torn-secret.yaml    # Kubernetes Secret manifest (template)
├── go.mod
├── go.sum
├── income_calculator.go   # Main application code
├── README.md
└── .gitignore             # Optional: for ignoring files like .env
```

## Features

- Dynamically fetches the user's company ID and type using the API key.
- Fetches and sums gross income from the Torn company news reports associated with the provided API key.
- Analyzes all companies of the same type as the user's company to find the income threshold for the top 20% (80th percentile) of rank 10 companies within that type.
- Outputs how much more income the user's company needs to reach that threshold.
- Loads environment variables from a `.env` file if present.
- Configurable logging level using `LOGLEVEL` environment variable (e.g., `debug`, `info`, `warn`, `error`). Default is `info`.
- Configurable logging format using `ENV=production` for JSON logs. Default is console-friendly output.
- Logs output using zerolog.

## Setup

1. **Clone the repository**
2. **Create a `.env` file** in the project root (or set environment variables directly). This file is ignored by Git.

   ```dotenv
   # Required: Your Torn API Key
   TORN_API_KEY=your_api_key_here

   # Optional: Set logging level (debug, info, warn, error, fatal, panic, disabled)
   # LOGLEVEL=debug

   # Optional: Set environment for logging format (production for JSON)
   # ENV=production
   ```

3. **Install Go dependencies:**

   ```sh
   go mod tidy
   ```

## Local Usage (Without Docker/Kubernetes)

Run the program directly using Go:

```sh
go run income_calculator.go
```

If you are not using a `.env` file, you must provide the API key as an environment variable:

```sh
TORN_API_KEY=your_api_key_here go run income_calculator.go
```

## Kubernetes Deployment (CronJob)

1. **Build the Docker image:**
   From the project root:

   ```sh
   docker build -t your-registry/torn-income-calculator:latest -f build/Dockerfile .
   ```

   *(Replace `your-registry/torn-income-calculator:latest` with your actual image name/tag)*

2. **Push the Docker image:**

   ```sh
   docker push your-registry/torn-income-calculator:latest
   ```

3. **Prepare Kubernetes Secret:**
   *   Encode your Torn API key: `echo -n 'YOUR_API_KEY' | base64`
   *   Edit `deploy/torn-secret.yaml` and replace `your_torn_api_key_base64_encoded` with the output from the command above.

4. **Deploy to Kubernetes:**
   From the project root:

   ```sh
   # Apply the Secret (only needed once or when the key changes)
   kubectl apply -f deploy/torn-secret.yaml

   # Apply the CronJob
   kubectl apply -f deploy/income-cronjob.yaml
   ```

5. **Check Status:**

   ```sh
   kubectl get cronjob torn-income-calculator
   kubectl get jobs --watch # Watch for job creation
   kubectl logs job/<job-name> # Check logs of a completed job
   ```

## Notes

- Requires Go 1.18 or newer.
- The program uses `github.com/joho/godotenv` to load environment variables, `github.com/rs/zerolog` for logging, and `golang.org/x/text/message` for number formatting.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo represents a GitHub repository
type Repo struct {
	FullName string `json:"full_name"`
}

// WorkflowRun represents a workflow run in a GitHub repository
type WorkflowRun struct {
	Status string `json:"status"`
}

var (
	cacheLock      sync.Mutex // To handle concurrency
	cachedJobs     int
	lastUpdateTime time.Time
	cacheTimeout   time.Duration
)

// Detect whether the API is public GitHub or GitHub Enterprise, and adjust the endpoint accordingly
func buildAPIURL(baseURL, endpoint string) string {
	// Check if we're using public GitHub (https://api.github.com)
	if strings.HasPrefix(baseURL, "https://api.github.com") {
		// Public GitHub case (no need for /api/v3)
		return fmt.Sprintf("%s/%s", strings.TrimSuffix(baseURL, "/"), endpoint)
	}

	// GitHub Enterprise case (use /api/v3)
	return fmt.Sprintf("%s/api/v3/%s", strings.TrimSuffix(baseURL, "/"), endpoint)
}

// GetRepos fetches the repositories for the given organization
func GetRepos(githubURL, org, token string) ([]Repo, error) {
	url := buildAPIURL(githubURL, fmt.Sprintf("orgs/%s/repos", org))

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error fetching repos: %s", resp.Status)
	}

	var repos []Repo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, err
	}

	return repos, nil
}

// GetWorkflowRuns fetches the workflow runs for a specific repository
func GetWorkflowRuns(githubURL, repo, token string) ([]WorkflowRun, error) {
	url := buildAPIURL(githubURL, fmt.Sprintf("repos/%s/actions/runs", repo))

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error fetching workflow runs: %s", resp.Status)
	}

	var workflowRuns struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&workflowRuns); err != nil {
		return nil, err
	}

	return workflowRuns.WorkflowRuns, nil
}

// CountQueuedJobs counts the total number of queued jobs across all repositories
func CountQueuedJobs(githubURL, org, token string) (int, error) {
	repos, err := GetRepos(githubURL, org, token)
	if err != nil {
		return 0, err
	}

	totalQueuedJobs := 0

	for _, repo := range repos {
		workflowRuns, err := GetWorkflowRuns(githubURL, repo.FullName, token)
		if err != nil {
			return 0, err
		}

		for _, run := range workflowRuns {
			if run.Status == "queued" {
				totalQueuedJobs++
			}
		}
	}

	return totalQueuedJobs, nil
}

// API handler to expose the queued jobs count with caching
func QueuedJobsHandler(w http.ResponseWriter, r *http.Request) {
	githubURL := os.Getenv("GITHUB_URL")
	org := os.Getenv("GITHUB_ORGANIZATION")
	token := os.Getenv("GITHUB_TOKEN")

	if githubURL == "" {
		githubURL = "https://api.github.com"
	}

	cacheLock.Lock()
	defer cacheLock.Unlock()

	// Check if the cache is still valid
	if time.Since(lastUpdateTime) < cacheTimeout {
		log.Println("Returning cached result")
		response := map[string]int{
			"queued_jobs": cachedJobs,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Otherwise, compute the queued jobs and update the cache
	queuedJobs, err := CountQueuedJobs(githubURL, org, token)
	if err != nil {
		http.Error(w, fmt.Sprintf("error counting queued jobs: %v", err), http.StatusInternalServerError)
		return
	}

	// Update cache
	cachedJobs = queuedJobs
	lastUpdateTime = time.Now()

	// Respond with the updated count in JSON format
	response := map[string]int{
		"queued_jobs": queuedJobs,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	// Set cache timeout from the environment variable
	timeoutStr := os.Getenv("GITHUB_RUNNER_SCALER_CACHE_TIMEOUT")
	if timeoutStr == "" {
		timeoutStr = "60" // Default cache timeout is 60 seconds
	}

	timeout, err := strconv.Atoi(timeoutStr)
	if err != nil {
		log.Printf("Invalid GITHUB_RUNNER_SCALER_CACHE_TIMEOUT: %v - using default", err)
		timeout = 60
	}

	cacheTimeout = time.Duration(timeout) * time.Second

	// Set up the HTTP server and route
	http.HandleFunc("/queued_jobs", QueuedJobsHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s with cache timeout of %d seconds...", port, timeout)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

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

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// disregards repos updated more than a year ago
const archiveThreshold = 1 * 365 * 24 * time.Hour // 1 years

// Repo represents a GitHub repository
type Repo struct {
	FullName string `json:"full_name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkflowRun represents a workflow run in a GitHub repository
type WorkflowRun struct {
	Status string `json:"status"`
	ID     int64  `json:"id"`
}

var (
	cacheLock      sync.Mutex // To handle concurrency
	cachedJobs     int
	lastUpdateTime time.Time
	cacheTimeout   time.Duration
)

var (
	repoCacheLock     sync.Mutex
	cachedRepos       []Repo
	repoLastUpdate    time.Time
	repoCacheTimeout  = 10 * time.Minute
)

type Job struct {
	Status string   `json:"status"`
	Labels []string `json:"labels"`
}


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
	var allRepos []Repo
	page := 1

	for {
		url := buildAPIURL(
			githubURL,
			fmt.Sprintf("orgs/%s/repos?per_page=100&page=%d", org, page),
		)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := httpClient.Do(req)
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

		if len(repos) == 0 {
			break
		}

		allRepos = append(allRepos, repos...)
		page++
	}

	return allRepos, nil
}

func GetReposCached(githubURL, org, token string) ([]Repo, error) {
	repoCacheLock.Lock()
	defer repoCacheLock.Unlock()

	if time.Since(repoLastUpdate) < repoCacheTimeout && cachedRepos != nil {
		log.Println("Returning cached repos")
		return cachedRepos, nil
	}

	log.Println("Refreshing repo list from GitHub")

	repos, err := GetRepos(githubURL, org, token)
	if err != nil {
		return nil, err
	}

	var activeRepos []Repo
	cutoff := time.Now().Add(-archiveThreshold)
	for _, r := range repos {
		if r.UpdatedAt.After(cutoff) {
			activeRepos = append(activeRepos, r)
		}
	}
	cachedRepos = activeRepos

	repoLastUpdate = time.Now()

	return cachedRepos, nil
}

// GetWorkflowRuns fetches the workflow runs for a specific repository
func GetWorkflowRuns(githubURL, repo, token string) ([]WorkflowRun, error) {
	var allRuns []WorkflowRun
	page := 1

	for {
		url := buildAPIURL(
			githubURL,
			fmt.Sprintf("repos/%s/actions/runs?per_page=100&page=%d", repo, page),
		)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("error fetching workflow runs: %s", resp.Status)
		}

		var response struct {
			WorkflowRuns []WorkflowRun `json:"workflow_runs"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, err
		}

		if len(response.WorkflowRuns) == 0 {
			break
		}

		allRuns = append(allRuns, response.WorkflowRuns...)
		page++
	}

	return allRuns, nil
}

func GetJobsForRun(githubURL, repo string, runID int64, token string) ([]Job, error) {
	var allJobs []Job
	page := 1

	for {
		url := buildAPIURL(
			githubURL,
			fmt.Sprintf(
				"repos/%s/actions/runs/%d/jobs?per_page=100&page=%d",
				repo, runID, page,
			),
		)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("error fetching jobs: %s", resp.Status)
		}

		var response struct {
			Jobs []Job `json:"jobs"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, err
		}

		if len(response.Jobs) == 0 {
			break
		}

		allJobs = append(allJobs, response.Jobs...)
		page++
	}

	return allJobs, nil
}

func matchesRunnerLabel(labels []string, runnerLabel string) bool {
	if runnerLabel == "" {
		return true // no filtering → all jobs count
	}

	for _, l := range labels {
		if l == runnerLabel {
			return true
		}
	}

	return false
}


func CountQueuedJobs(githubURL, org, token, runnerLabel string) (int, error) {
	repos, err := GetReposCached(githubURL, org, token)
	if err != nil {
		return 0, err
	}

	total := 0

	for _, repo := range repos {
		runs, err := GetWorkflowRuns(githubURL, repo.FullName, token)
		if err != nil {
			return 0, err
		}

		for _, run := range runs {
			if run.Status != "queued" && run.Status != "in_progress" {
				continue
			}

			jobs, err := GetJobsForRun(githubURL, repo.FullName, run.ID, token)
			if err != nil {
				return 0, err
			}

			for _, job := range jobs {
				if job.Status == "queued" && matchesRunnerLabel(job.Labels, runnerLabel) {
					total++
				}
			}
		}
	}

	return total, nil
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
	runnerLabel := os.Getenv("GITHUB_RUNNER_LABEL") // optional env variable
	if runnerLabel == "" {
    runnerLabel = "" // optional: default is empty = count all jobs
	}
	queuedJobs, err := CountQueuedJobs(githubURL, org, token, runnerLabel)
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

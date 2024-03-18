package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/fatih/color"
)

// cnameResult stores the result of a CNAME query for a domain
type cnameResult struct {
	domain string
	cname  string
	err    error
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <domain>")
		os.Exit(1)
	}

	printIntro()

	// check if the AppVersion was already set during compilation - otherwise manually get it from `./VERSION`
	CheckAppVersion()
	color.Yellow("Current version: %s\n\n", AppVersion)

	domain := os.Args[1]
	log.Info("Checking subdomains for: ", domain)

	queryCRTSH(domain)
}

func printIntro() {
	color.Green("##################################\n")
	color.Green("#                                #\n")
	color.Green("#           SubSnipe             #\n")
	color.Green("#                                #\n")
	color.Green("#       By dub-flow with ❤️       #\n")
	color.Green("#                                #\n")
	color.Green("##################################\n\n")
}

// Queries crt.sh for subdomains of the given domain and writes unique common names to a file
func queryCRTSH(domain string) {
	log.Info("Querying crt.sh for subdomains... (may take a moment)")

	url := fmt.Sprintf("https://crt.sh/?q=%s&output=json", domain)
	resp, err := http.Get(url)
	if err != nil {
		log.Error("Error querying crt.sh: ", err)
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Error("Error reading response body: ", err)
		return
	}

	var data []map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		log.Error("Error unmarshaling JSON: ", err)
		return
	}

	uniqueCommonNames := extractUniqueCommonNames(data)

	outputFile := "crt-subdomains.txt"
	if err := writeSubdomainsToFile(uniqueCommonNames, outputFile); err != nil {
		log.Error("Error writing to file: ", err)
		return
	}

	log.Info("Unique common names have been extracted to ", outputFile)
	checkCNAMEs(outputFile)
}

// Extracts unique common names from the JSON data returned by crt.sh
func extractUniqueCommonNames(data []map[string]interface{}) map[string]bool {
	uniqueCommonNames := make(map[string]bool)
	for _, entry := range data {
		if cn, ok := entry["common_name"].(string); ok {
			uniqueCommonNames[cn] = true
		}
	}
	return uniqueCommonNames
}

// Writes the extracted subdomains to the specified file
func writeSubdomainsToFile(subdomains map[string]bool, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	for cn := range subdomains {
		file.WriteString(cn + "\n")
	}
	return nil
}

// Reads subdomains from a file and queries for their CNAME records concurrently
func checkCNAMEs(fileName string) {
	log.Info("Querying CNAME records for subdomains...")

	file, err := os.Open(fileName)
	if err != nil {
		log.Error("Error opening file: ", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var wg sync.WaitGroup
	results := make(chan cnameResult, 100) // Buffer may be adjusted based on expected concurrency

	// Process results concurrently to sort and write to output.txt
	go processResults(results)

	maxConcurrency := 20
	sem := make(chan struct{}, maxConcurrency) // Control concurrency with a semaphore

	for scanner.Scan() {
		domain := scanner.Text()
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		// Launch a goroutine for each CNAME query
		go func(domain string) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore
			queryAndSendCNAME(domain, results)
		}(domain)
	}

	wg.Wait() // Wait for all queries to complete
	close(results) // Signal completion of queries

	if err := scanner.Err(); err != nil {
		log.Error("Error reading from file: ", err)
	}
}

// Performs a CNAME query for a given domain and sends the result to the results channel
func queryAndSendCNAME(domain string, results chan<- cnameResult) {
	cname, err := exec.Command("dig", "+short", "CNAME", domain).Output()
	if err != nil || len(cname) == 0 {
		results <- cnameResult{domain: domain, err: fmt.Errorf("no CNAME record found or dig command failed")}
		return
	}
	results <- cnameResult{domain: domain, cname: strings.TrimSpace(string(cname))}
}

// Processes CNAME query results from the results channel, sorting them into found and not found
func processResults(results <-chan cnameResult) {
	var found []string
	var notFound []string

	for result := range results {
		if result.err != nil || result.cname == "" {
			log.Warnf("Processing no CNAME found for: %s", result.domain)
			notFound = append(notFound, fmt.Sprintf("No CNAME record found for: %s", result.domain))
		} else {
			log.Infof("Processing CNAME found for: %s, CNAME: %s", result.domain, result.cname)
			found = append(found, fmt.Sprintf("CNAME for %s is: %s", result.domain, result.cname))
		}
	}

	writeResults(found, notFound)
}

// Writes the sorted CNAME query results to an output file
func writeResults(found, notFound []string) {
	log.Info("writeResults started!")

	file, err := os.Create("output.txt")
	if err != nil {
		log.Error("Error creating output file: ", err)
		return
	}
	defer file.Close()

	if len(found) > 0 {
		file.WriteString("CNAMEs Found:\n\n")
		for _, f := range found {
			file.WriteString("- " + f + "\n")
		}
		file.WriteString("\n")
	}

	if len(notFound) > 0 {
		file.WriteString("No CNAMEs Found:\n\n")
		for _, nf := range notFound {
			file.WriteString("- " + nf + "\n")
		}
	}

	log.Info("Results have been written to output.txt")
}

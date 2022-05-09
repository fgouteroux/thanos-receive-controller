/*
Copyright © 2022 François Gouteroux <francois.gouteroux@gmail.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const AppVersion = "0.0.1"

var (
	DebugLogger *log.Logger
	InfoLogger  *log.Logger
	ErrorLogger *log.Logger
)

// HashringConfig represents the configuration for a hashring
// a receive node knows about.
type HashringConfig struct {
	Hashring  string   `json:"hashring,omitempty"`
	Tenants   []string `json:"tenants,omitempty"`
	Endpoints []string `json:"endpoints"`
}

func httpClient(timeout int) *http.Client {
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	return client
}

func saveHashringFile(file, owner string, content []byte) {
	g, err := user.Lookup(owner)
	if err != nil {
		ErrorLogger.Printf("Cannot save file %s, error: %+v", file, err)
		return
	}

	err = ioutil.WriteFile(file, content, 0644)
	if err != nil {
		ErrorLogger.Printf("Cannot save file %s, error %+v", file, err)
		return
	}

	uid, _ := strconv.Atoi(g.Uid)
	gid, _ := strconv.Atoi(g.Gid)
	err = os.Chown(file, uid, gid)
	if err != nil {
		ErrorLogger.Printf("Cannot set %s owner on file %s. %+v", owner, file, err)
		return
	}
	InfoLogger.Printf("File %s saved.", file)
}

func healthyEndpoint(ch chan string, wg *sync.WaitGroup, scheme, endpoint string, timeout, portOffset int, verbose bool) {
	defer wg.Done()
	endpointSplit := strings.Split(endpoint, ":")
	host := endpointSplit[0]
	port, _ := strconv.Atoi(endpointSplit[1])
	url := fmt.Sprintf("%s://%s:%d/-/ready", scheme, host, port+portOffset)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		ErrorLogger.Printf("Error Occurred. %+v", err)
		return
	}

	response, err := httpClient(timeout).Do(req)
	if err != nil {
		ErrorLogger.Printf("Error sending request to endpoint: %+v", err)
		return
	}

	bodyBytes, _ := ioutil.ReadAll(response.Body)
	// Close the connection to reuse it
	defer response.Body.Close()
	if response.StatusCode == 200 && string(bodyBytes) == "OK" {
		if verbose {
			DebugLogger.Printf("Endpoint %s is ready.", endpoint)
		}
	} else {
		ErrorLogger.Printf("Endpoint is not ready: Getting %d from %s: %s", response.StatusCode, url, string(bodyBytes))
		return
	}
	ch <- endpoint
}

func checkHashringFile(file, owner string, scheme string, endpointTimeout, portOffset int, wg *sync.WaitGroup, verbose bool) {
	defer wg.Done()
	// Read trusted source file to perform healthy request on expected endpoints
	body, err := ioutil.ReadFile(file)
	if err != nil {
		ErrorLogger.Printf("Unable to read file %s: %v", file, err)
		return
	}

	// Decode json hashring format into struct
	var hashrings []HashringConfig
	if err := json.Unmarshal([]byte(body), &hashrings); err != nil {
		ErrorLogger.Printf("Unable to json decode file %s: %v", file, err)
		return
	}

	// Set soncurrency http request for each endpoint
	for pos, hashring := range hashrings {
		var wgEndpoint sync.WaitGroup
		var endpoints []string
		queue := make(chan string, len(hashring.Endpoints))
		for _, endpoint := range hashring.Endpoints {
			wgEndpoint.Add(1)
			go healthyEndpoint(queue, &wgEndpoint, scheme, endpoint, endpointTimeout, portOffset, verbose)
		}

		go func() {
			wgEndpoint.Wait()
			close(queue)
		}()

		for result := range queue {
			endpoints = append(endpoints, result)
		}
		// Sort endpoints list to avoid diff changes when comparing sha256sum
		sort.Strings(endpoints)
		hashrings[pos].Endpoints = endpoints
	}

	//Encode content struct to json hashring format
	content, err := json.Marshal(hashrings)
	if err != nil {
		ErrorLogger.Printf("Error Occurred. %+v", err)
		return
	}

	// Get sha256 checksum from content
	contentSha256Sum := sha256.Sum256([]byte(content))

	// Create new filename for the generated file from trusted source filename
	generatedFile := fmt.Sprintf("%s_generated.json", strings.Split(file, ".json")[0])

	save := true
	// Check if generated file already exists
	if _, err := os.Stat(generatedFile); err == nil {
		body, err := ioutil.ReadFile(generatedFile)
		if err != nil {
			ErrorLogger.Printf("Unable to read file %s: %v", file, err)
			return
		}

		// Get sha256 checksum from generated file content
		gFileSha256Sum := sha256.Sum256([]byte(body))

		// Check if current content is different than existing generated file content
		if string(gFileSha256Sum[:]) == string(contentSha256Sum[:]) {
			save = false
			if verbose {
				DebugLogger.Printf("Hashring file %s is OK, no update needed", generatedFile)
			}
		}
	}

	// Save/Overwrite generated file content
	if save {
		saveHashringFile(generatedFile, owner, content)
	}
}

func listHashringFiles(directory string) []string {
	var files []string
	err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err

		}
		if !info.IsDir() && !strings.HasSuffix(path, "_generated.json") && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		ErrorLogger.Println(err)
	}
	return files
}

func run(files []string, owner, scheme string, timeout, portOffset int, verbose bool) {
	// Set concurrency watcher for each hashring file
	var wg sync.WaitGroup
	for _, file := range files {
		wg.Add(1)
		go checkHashringFile(file, owner, scheme, timeout, portOffset, &wg, verbose)
	}
	wg.Wait()
}

func buildFilesList(directory, file string, verbose bool) []string {
	var hashringFiles []string
	if file != "" {
		hashringFiles = append(hashringFiles, file)
	} else {
		hashringFiles = listHashringFiles(directory)
	}
	if verbose {
		DebugLogger.Printf("Watching files: %v", hashringFiles)
	}
	return hashringFiles
}

func main() {
	InfoLogger = log.New(os.Stdout, "INFO ", log.Ldate|log.Ltime|log.Lshortfile)
	DebugLogger = log.New(os.Stdout, "DEBUG ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(os.Stderr, "ERROR ", log.Ldate|log.Ltime|log.Lshortfile)

	file := flag.String("file", "", "Hashring filepath to watch. (mutually exclusive with '--directory')")
	directory := flag.String("directory", "", "Directory path to watch hashring files. (mutually exclusive with '--file')")
	owner := flag.String("owner", "thanos", "Set owner on generated hashring files.")
	endpointScheme := flag.String("endpoint-scheme", "http", "Endpoint scheme to perform readiness requests.")
	endpointTimeout := flag.Int("endpoint-timeout", 5, "Endpoint timeout to perform readiness requests.")
	endpointPortOffset := flag.Int("endpoint-port-offset", 1, "Endpoint port offset to perform readiness requests.")
	interval := flag.Int("interval", 10, "Watcher Scheduler interval in seconds.")
	schedule := flag.Bool("schedule", false, "Enable hashring files watcher scheduler.")
	verbose := flag.Bool("verbose", false, "Enabled verbose mode.")
	version := flag.Bool("version", false, "Show version.")

	flag.Parse()

	if *version {
		fmt.Println(AppVersion)
		os.Exit(0)
	}

	if (*directory == "" && *file == "") || (*directory != "" && *file != "") {
		log.Fatal("FATAL: Either '--directory' or '--file' argument should be set. (mutually exclusive)")
	}

	if *interval <= *endpointTimeout {
		log.Fatalf("FATAL: '--interval %d'  must be greater than '--timeout %d'", *interval, *endpointTimeout)
	}

	if !*schedule {
		hashringFiles := buildFilesList(*directory, *file, *verbose)
		run(hashringFiles, *owner, *endpointScheme, *endpointTimeout, *endpointPortOffset, *verbose)
	} else {

		// Create and run the scheduler based on given interval
		ticker := time.NewTicker(time.Duration(*interval) * time.Second)
		scheduleDone := make(chan bool)

		go func() {
			for {
				select {
				case <-scheduleDone:
					return
				case t := <-ticker.C:
					InfoLogger.Printf("Tick at %s", t)
					hashringFiles := buildFilesList(*directory, *file, *verbose)
					run(hashringFiles, *owner, *endpointScheme, *endpointTimeout, *endpointPortOffset, *verbose)
				}
			}
		}()

		// Handle signals to stop the scheduler
		sigs := make(chan os.Signal, 1)
		signalDone := make(chan bool, 1)

		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			sig := <-sigs
			fmt.Println()
			InfoLogger.Printf("Caught SIGTERM %v", sig)
			signalDone <- true
		}()

		InfoLogger.Printf("Scheduler Started (run every %d seconds)", *interval)
		<-signalDone
		InfoLogger.Println("Scheduler Stopped...")
	}
}

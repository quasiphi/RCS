package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
)

type Config struct {
	SourceEndpoint string   `json:"SourceEndpoint"`
	PingTime       int      `json:"PingTime"`
	CacheBypass    []string `json:"CacheBypass"`
}

var c = cache.New(cache.NoExpiration, 60*time.Minute)
var sourceEndpoint = ""
var PingTime = 1
var cacheBypass = []string{}

func main() {
	log.Println("Starting RCS Server")

	// * Read config file
	config, err := readConfig("config.json")
	if err != nil {
		log.Fatal(err)
	} else {
		sourceEndpoint = config.SourceEndpoint
		cacheBypass = config.CacheBypass
	}

	http.HandleFunc("/", handler)

	log.Fatal(http.ListenAndServeTLS(":5000", "cert.pem", "cert.key", nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	// Check if the request path starts with /api/models/
	bypassCache := false
	for _, bypass := range cacheBypass {
		if strings.Contains(r.URL.Path, bypass) {
			bypassCache = true
			break
		}
	}

	if bypassCache {
		// Read the request body
		requestBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body", http.StatusInternalServerError)
			return
		}

		// Create a new reader for the request body because it has been read
		r.Body = ioutil.NopCloser(bytes.NewBuffer(requestBody))

		// Create a SHA256 hash of the request body
		hash := sha256.Sum256(requestBody)
		requestBodyHash := hex.EncodeToString(hash[:])

		// Create a unique key for the request
		key := r.URL.Path + requestBodyHash

		// Check if the request is in the cache
		item, found := c.Get(key)
		if found {
			w.Write(item.([]byte))
			return
		}

		// Create a new request with the same method, headers, and body
		req, err := http.NewRequest(r.Method, sourceEndpoint+r.URL.Path, bytes.NewBuffer(requestBody))
		if err != nil {
			http.Error(w, "Error creating new request", http.StatusInternalServerError)
			return
		}
		req.Header = r.Header

		// Send the request to the main server
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "Error from main endpoint", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Error reading main endpoint response", http.StatusInternalServerError)
			return
		}

		// Cache the response with a 1 minute expiration
		c.Set(key, body, time.Duration(PingTime)*time.Minute)

		// Copy the headers from the main server's response
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		w.Write(body)
		return
	} else {

		// Check if the main server is up
		_, err := http.Get(sourceEndpoint + "test")
		serverIsDown := err != nil

		// If the server is up, get the response from the main server
		if !serverIsDown {
			// Create a new request with the same method, headers, and body
			req, err := http.NewRequest(r.Method, sourceEndpoint+r.URL.Path, r.Body)
			if err != nil {
				http.Error(w, "Error creating new request", http.StatusInternalServerError)
				return
			}
			req.Header = r.Header

			// Send the request to the main server
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				http.Error(w, "Error from main endpoint", http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()

			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				http.Error(w, "Error reading main endpoint response", http.StatusInternalServerError)
				return
			}

			// Cache the response with no expiration
			c.Set(r.URL.Path, body, cache.NoExpiration)

			// Copy the headers from the main server's response
			for key, values := range resp.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}

			w.Write(body)
			return
		}

		// If the server is down and the item is in the cache, serve the cached item
		item, found := c.Get(r.URL.Path)
		if found {
			w.Write(item.([]byte))
		} else {
			http.Error(w, "Server is down", http.StatusInternalServerError)
		}

	}
}

func readConfig(filename string) (Config, error) {
	var config Config

	file, err := os.Open(filename)
	if err != nil {
		return config, err
	}
	defer file.Close()

	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return config, err
	}

	err = json.Unmarshal(bytes, &config)
	if err != nil {
		return config, err
	}

	return config, nil
}

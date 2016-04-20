package main

import (
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"github.com/valyala/fasthttp"
	"log"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"
)

// header field type
type hfield struct {
	name  string
	value string
}

// header type: a slice of strings implementing the flag.Value interface.
type header []hfield

// String is the method to format the flag's value, part of the flag.Value interface.
func (h *header) String() string {
	return fmt.Sprint(*h)
}

type errorString string

func (e errorString) Error() string {
	return string(e)
}

// Set is the method to set the flag value, part of the flag.Value interface.
func (h *header) Set(value string) error {
	i := strings.IndexRune(value, ':')
	if i < 0 {
		return errorString("Header field format must be `name: value'")
	}
	hf := hfield{value[0:i], value[i+1:]}
	*h = append(*h, hf)
	return nil
}

var ready_ch = make(chan bool)
var start_ch = make(chan bool)
var done_ch = make(chan bool)

func sendRequests(client *fasthttp.HostClient, iter int, template *fasthttp.Request) {
	var resp fasthttp.Response
	var req fasthttp.Request 
	template.CopyTo(&req)

	// Tell main thread we are ready
	ready_ch <- true

	// Wait for main thread to start the injection
	<-start_ch

	// Perform injection
	for i := 0; i < iter; i++ {
		err := client.Do(&req, &resp)
		if err != nil {
			log.Println(err)
			break
		}
	}
	done_ch <- true
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func main() {
	// Command line parameters
	var conc, reqs, cpus int
	var ka, comp bool
	var method, uri, body, user, pass, cpuprof /*, memprof*/ string
	var hdr header

	flag.StringVar(&body, "body", "", "Request body")
	flag.IntVar(&conc, "concurrency", 100, "Number of concurrent connections")
	flag.IntVar(&cpus, "cpus", 2, "Number of CPUs/kernel threads used")
	flag.StringVar(&cpuprof, "cpu-prof", "", "CPU profile file name (pprof format)")
	flag.BoolVar(&comp, "compress", false, "Use HTTP compression")
	flag.Var(&hdr, "header", "Additional request header (can be set multiple time)")
	flag.BoolVar(&ka, "keep-alive", true, "Use HTTP keep-alive")
	flag.StringVar(&pass, "pass", "", "HTTP authentication password")
	//flag.StringVar(&memprof, "mem-prof", "", "Memory allocation profile file name (pprof format)")
	flag.StringVar(&method, "method", "GET", "HTTP method (GET, POST, PUT, DELETE...)")
	flag.IntVar(&reqs, "requests", 10000, "Total number of requests")
	flag.StringVar(&uri, "url", "http://127.0.0.1/", "URL")
	flag.StringVar(&user, "user", "", "HTTP authentication user name")
	flag.Parse()

	// Use cpus kernel threads
	runtime.GOMAXPROCS(cpus)

	// Parse URL
	parsed_uri, err := url.Parse(uri)
	if err != nil {
		log.Println(err)
		return
	}
	if parsed_uri.Scheme != "http" && parsed_uri.Scheme != "https" {
		log.Println("Unknown URI scheme: " + parsed_uri.Scheme)
		return
	}

	// Create HTTP client according to configuration
	client := &fasthttp.HostClient{
		Addr: parsed_uri.Host,
		MaxConns: conc,
		IsTLS: (parsed_uri.Scheme == "https"),
		TLSConfig: &tls.Config{InsecureSkipVerify: true, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA}},
	}

	// Build request template
	var req fasthttp.Request 
	req.Header.SetMethod(method)
	req.SetRequestURI(uri)
	req.SetBody([]byte(body))
	for _, hf := range hdr {
		req.Header.Add(hf.name, hf.value)
	}
	if user != "" {
		req.Header.Set("Authorization", "Basic " + basicAuth(user, pass))
	}
	if comp {
		req.Header.Set("Accept-Encoding", "gzip, deflate")
	}

	// Create goroutines
	remaining := reqs
	for i := 0; i < conc; i++ {
		n := remaining / (conc - i)
		go sendRequests(client, n, &req)
		remaining -= n
	}

	// Wait for worker goroutines to get ready
	for i := 0; i < conc; i++ {
		<-ready_ch
	}

	// Profiling
	if cpuprof != "" {
		f, err := os.Create(cpuprof)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	begin := time.Now()

	// Start sending requests
	for i := 0; i < conc; i++ {
		start_ch <- true
	}
	// Wait for jobs to complete
	for i := 0; i < conc; i++ {
		<-done_ch
	}

	end := time.Now()
	elapsed := float32(end.Sub(begin))
	throughput := float32(reqs) * 1000000000 / elapsed
	fmt.Printf("%d requests sent in %.2f seconds - average throughput %.2f tps\n", reqs, elapsed/1000000000, throughput)

	// Profiling
	//if memprof != "" {
	//f, err := os.Create(memprof)
	//if err != nil {
	//log.Fatal(err)
	//}
	//pprof.Lookup("heap").WriteTo(f, 0)
	//f.Close()
	//return
	//}
}

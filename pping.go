package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/net/icmp"
)

func main() {
	outFileName := flag.String("o", "",
		"Write the results to output_file if provided, in CSV format")
	maxProcs := flag.Int("p", 0,
		"Sets the value of runtime.GOMAXPROCS to max_procs. If max_procs is set to -1, pping will print the default value for runtime.GOMAXPROCS and quit.")

	flag.Parse()

	urls := flag.Args()

	if *maxProcs < 0 {
		fmt.Printf("runtime.GOMAXPROCS = %d\n", runtime.GOMAXPROCS(-1))
		os.Exit(0)
	}

	if *maxProcs >= 1 {
		runtime.GOMAXPROCS(*maxProcs)
	}

	if len(urls) == 0 {
		log.Fatalln("No urls provided.")
	}

	var outFile *os.File
	var writer *csv.Writer = nil
	var err error

	if len(*outFileName) > 0 {
		outFile, err = os.Create(*outFileName)
		if err != nil {
			log.Fatalf("Unable to create output file: %v.\n", err)
		}
		defer outFile.Close()

		writer = csv.NewWriter(outFile)
		defer writer.Flush()
	}

	// Set the cap of ipAddrs and matchedUrls to the length of the provided urls,
	// so that memory won't need to be reallocated later and because there cannot
	// be more than the provided urls.
	ipAddrs := make([]*net.IPAddr, 0, len(urls))
	matchedUrls := make([]string, 0, len(urls))

	// Create the channels.
	connReceiver := make(chan *icmp.PacketConn)
	requestReceiver := make(chan Request)
	resultReceiver := make(chan Result)
	errorReceiver := make(chan Error)
	quit := make(chan struct{})

	// Start the listener in a goroutine, and store the PacketConn.
	go Listener(connReceiver, resultReceiver, quit)
	conn := <-connReceiver

	// For each provided url, resolve its ip address and make sure it's pingable.
	for _, url := range urls {
		// Resolve the IPv4 address.
		ip, err := net.ResolveIPAddr("ip4", url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not get IP for %s: %v\n", url, err)
		}

		// Attempt to ping the resolved ip address.
		go Ping(conn, ip, 0, 0, requestReceiver, errorReceiver)
		<-requestReceiver

		select {
		case <-resultReceiver:
			// If the ping succeeds, add it to the list of ip addresses and urls.
			ipAddrs = append(ipAddrs, ip)
			matchedUrls = append(matchedUrls, url)
		case e := <-errorReceiver:
			fmt.Fprintf(os.Stderr, "Failed to ping IP %s for %s: %v\n",
				ip.String(), url, e.err)
		}
	}

	// Precompute the number of ip addresses, because this will be referenced many
	// times later.
	ipCount := len(ipAddrs)

	if ipCount == 0 {
		log.Fatalf("Unable to find ips for any of the provided urls.\n")
	}

	// Create the requests and results with the cap ipCount.
	requests := make([]map[int]time.Time, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		requests[i] = map[int]time.Time{}
	}

	results := make([][]string, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		results[i] = []string{matchedUrls[i], ipAddrs[i].String()}
	}

	fmt.Printf("Pinging %d URLs...\n", len(matchedUrls))

	// Start the looped pinger in a separate routine so that we can handle stuff
	// in the main routine.
	go Pinger(conn, ipAddrs, requestReceiver, errorReceiver, quit)

	// Read keyboard input for q.
	// From https://github.com/pantherman594/tunnel/blob/master/main.go#L165.
	go func() {
		// Disable input buffering
		exec.Command("stty", "-F", "/dev/tty", "cbreak", "min", "1").Run()
		// Do not display entered characters on the screen
		exec.Command("stty", "-F", "/dev/tty", "-echo").Run()
		var b []byte = make([]byte, 1)

		for {
			os.Stdin.Read(b)
			if b[0] == 'q' {
				quit <- struct{}{}
				quit <- struct{}{}
				quit <- struct{}{}
				return
			}
		}
	}()

	startTime := time.Now()

	// Listen for requests, results, and errors.
	for {
		select {
		case req := <-requestReceiver:
			requests[req.id][req.seq] = time.Now()
		case res := <-resultReceiver:
			if res.id >= ipCount {
				fmt.Fprintln(os.Stderr, "Received invalid id.")
				continue
			}

			start, ok := requests[res.id][res.seq]
			if !ok {
				fmt.Fprintln(os.Stderr,
					"Response received without a corresponding request.")
				continue
			}
			delete(requests[res.id], res.seq)

			// Calculate the total duration, log it, and store it in results.
			dur := res.endTime.Sub(start)
			durMs := float64(dur.Nanoseconds()) / float64(time.Millisecond)
			durMsStr := strconv.FormatFloat(durMs, 'f', 4, 64)

			fmt.Printf("Pinged %s in %sms.\n", ipAddrs[res.id], durMsStr)
			results[res.id] = append(results[res.id], durMsStr)
		case e := <-errorReceiver:
			if e.id < ipCount {
				delete(requests[e.id], e.seq)
			}

			fmt.Fprintf(os.Stderr, "Failed to ping IP %s: %v\n",
				e.ipAddr.String(), e.err)
		case <-quit:
			dur := time.Since(startTime)
			durSec := float64(dur.Nanoseconds()) / float64(time.Second)
			totalPings := 0

			for _, v := range results {
				totalPings += len(v)
			}

			pingsPerSec := float64(totalPings) / durSec

			fmt.Printf("Pinged %d times in %0.4f seconds (%0.2f pings/sec).\n", totalPings, durSec, pingsPerSec)

			if writer != nil {
				for _, v := range results {
					err := writer.Write(v)
					if err != nil {
						fmt.Printf("Error writing to file: %v.\n", err)
					}
				}
			}
			return
		default:
		}
	}
}

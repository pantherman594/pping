package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
)

var (
	urls = []string{"google.com", "facebook.com"}
)

func main() {
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

	// Start the listener in a goroutine, and store the PacketConn.
	go listener(connReceiver, resultReceiver)
	conn := <-connReceiver

	// For each provided url, resolve its ip address and make sure it's pingable.
	for _, url := range urls {
		// Resolve the IPv4 address.
		ip, err := net.ResolveIPAddr("ip4", url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not get IP for %s: %v\n", url, err)
		}

		// Attempt to ping the resolved ip address.
		go ping(conn, ip, 0, 0, requestReceiver, errorReceiver)
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

	results := make([][]time.Duration, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		results[i] = []time.Duration{}
	}

	fmt.Printf("Pinging %d URLs...\n", len(matchedUrls))

	// Start the looped pinger in a separate routine so that we can handle stuff
	// in the main routine.
	go pinger(conn, ipAddrs, requestReceiver, errorReceiver)

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

			fmt.Printf("Pinged %s in %0.2fms.\n", ipAddrs[res.id], durMs)
			results[res.id] = append(results[res.id], dur)
		case e := <-errorReceiver:
			if e.id < ipCount {
				delete(requests[e.id], e.seq)
			}

			fmt.Fprintf(os.Stderr, "Failed to ping IP %s: %v\n",
				e.ipAddr.String(), e.err)
		}
	}
}

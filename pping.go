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

// printError prints an error message, while preserving the proper output
// format.
// ANSI sequences from https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
func printError(msg string, ipCount int) {
	// Move terminal cursor up to overwrite the line for url.
	fmt.Printf("\r\033[%dA\033[K", ipCount+2)

	// Print the error.
	fmt.Printf("- %s", msg)

	// Clear the next line.
	fmt.Printf("\r\033[1B\033[K")

	// Move back down and print the quit message again.
	fmt.Printf("\r\033[%dB\033[K", ipCount+2)
	fmt.Printf("\nPress q to quit.")
}

// durToString converts a duration to a string of the specified time unit and
// decimal places.
func durToString(dur, unit time.Duration, decimals int) string {
	durUnit := float64(dur) / float64(unit)
	return strconv.FormatFloat(durUnit, 'f', decimals, 64)
}

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
	go Listener(connReceiver, resultReceiver, errorReceiver, quit)
	conn := <-connReceiver

	// For each provided url, resolve its ip address and make sure it's pingable.
	for _, url := range urls {
		// Resolve the IPv4 address.
		ip, err := net.ResolveIPAddr("ip4", url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not get IP for %s: %v\n", url, err)
		} else {
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
	}

	// Precompute the number of ip addresses, because this will be referenced many
	// times later.
	ipCount := len(ipAddrs)

	if ipCount != len(urls) {
		if ipCount == 0 {
			log.Fatalf("Unable to find ips for any of the provided urls.\n")
		} else {
			fmt.Println()
		}
	}

	// Create the requests and results with the cap ipCount.
	requests := make([]map[int]time.Time, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		requests[i] = map[int]time.Time{}
	}

	results := make([][]string, ipCount)
	mins := make([]time.Duration, ipCount)
	maxs := make([]time.Duration, ipCount)
	tots := make([]time.Duration, ipCount)
	cnts := make([]int64, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		results[i] = []string{matchedUrls[i], ipAddrs[i].String()}

		// min and max duration from time package constants.
		mins[i] = 1<<63 - 1
		maxs[i] = -1 << 63
		tots[i] = 0
		cnts[i] = 0
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
	fmt.Printf("\nErrors:\n\n")
	for i := 0; i < ipCount; i++ {
		fmt.Println()
	}
	fmt.Printf("\nPress q to quit.")

	// Listen for requests, results, and errors.
	for {
		select {
		case req := <-requestReceiver:
			requests[req.id][req.seq] = time.Now()
		case res := <-resultReceiver:
			if res.id < 0 || res.id >= ipCount {
				printError(fmt.Sprintf("Received invalid id: %d", res.id), ipCount)
				continue
			}

			start, ok := requests[res.id][res.seq]
			if !ok {
				printError(fmt.Sprintf("[%s] Response received without a corresponding request.",
					matchedUrls[res.id]), ipCount)
				continue
			}
			delete(requests[res.id], res.seq)

			// Calculate the total duration, log it, and store it in results.
			dur := res.endTime.Sub(start)
			durMsStr := durToString(dur, time.Millisecond, 4)

			results[res.id] = append(results[res.id], durMsStr)

			if dur < mins[res.id] {
				mins[res.id] = dur
			}
			if dur > maxs[res.id] {
				maxs[res.id] = dur
			}
			tots[res.id] += dur
			cnts[res.id] += 1

			count := cnts[res.id]

			// Only update status every 100 pings.
			if count == 1 || count%100 == 0 {
				min := durToString(mins[res.id], time.Millisecond, 2)
				max := durToString(maxs[res.id], time.Millisecond, 2)
				avg := durToString(tots[res.id]/time.Duration(count), time.Millisecond, 2)

				// Move terminal cursor up to overwrite the line for url.
				fmt.Printf("\r\033[%dA\033[K", (ipCount-res.id)+1)
				fmt.Printf("[%d %s] Pinged %s in %sms. %d pings. min/max/avg: %s/%s/%sms",
					res.id, matchedUrls[res.id], ipAddrs[res.id], durMsStr, count, min,
					max, avg)

				// Move cursor back down.
				fmt.Printf("\r\033[%dB", (ipCount-res.id)+1)
			}
		case e := <-errorReceiver:
			if e.id >= 0 && e.id < ipCount {
				delete(requests[e.id], e.seq)
			}

			printError(fmt.Sprintf("[%d]: %v", e.id, e.err), ipCount)
		case <-quit:
			dur := time.Since(startTime)
			durSec := float64(dur) / float64(time.Second)
			totalPings := 0

			for _, v := range results {
				totalPings += len(v)
			}

			pingsPerSec := float64(totalPings) / durSec

			fmt.Printf("Pinged %d times in %0.4f seconds (%0.2f pings/sec).\n",
				totalPings, durSec, pingsPerSec)

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
